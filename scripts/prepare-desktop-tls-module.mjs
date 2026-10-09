import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { chmodSync, cpSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// Tailscale v1.104 loses the expected IP in VerifyConnection because TLS
// omits IP literals from SNI. Use a pinned temporary module copy rather than edit
// the module cache or broaden the user's macOS certificate trust settings.
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const module = JSON.parse(execFileSync('go', ['list', '-m', '-json', 'tailscale.com'], {
  cwd: join(root, 'mac/fleet-agent'), encoding: 'utf8',
}));
if (module.Version !== 'v1.104.0' || module.Replace) {
  throw new Error('Desktop TLS patch requires the reviewed tailscale.com v1.104.0 dependency.');
}
const original = join(module.Dir, 'net/dnscache/dnscache.go');
const source = readFileSync(original, 'utf8');
const hash = createHash('sha256').update(source).digest('hex');
if (hash !== '94236cdfaf955f83cfb82ffaed8c913fa7d69b4b35c9549660fa600f5d4585df') {
  throw new Error('Tailscale TLS source changed; review the patch before building.');
}
const before = '\t\ttlsConn := tls.Client(tcpConn, cfg)';
const after = `\t\t// An IP literal has no SNI. Keep its expected identity available to
\t\t// the existing verifier, including hostname-scoped macOS SSL trust.
\t\tif net.ParseIP(cfg.ServerName) != nil && cfg.VerifyConnection != nil {
\t\t\texpectedHost, verify := cfg.ServerName, cfg.VerifyConnection
\t\t\tcfg.VerifyConnection = func(state tls.ConnectionState) error {
\t\t\t\tif state.ServerName == "" {
\t\t\t\t\tstate.ServerName = expectedHost
\t\t\t\t}
\t\t\t\treturn verify(state)
\t\t\t}
\t\t}
${before}`;
if (source.split(before).length !== 2) throw new Error('Unexpected Tailscale TLS patch location.');
if (process.argv.length !== 3) throw new Error('Usage: node prepare-desktop-tls-module.mjs OUTPUT_DIRECTORY');
const directory = resolve(process.argv[2]);
mkdirSync(directory, { recursive: true, mode: 0o700 });
const staged = join(directory, 'tailscale');
cpSync(module.Dir, staged, { recursive: true });
const patched = join(staged, 'net/dnscache/dnscache.go');
chmodSync(patched, 0o600);
writeFileSync(patched, source.replace(before, after));
const modfile = join(directory, 'desktop.mod');
writeFileSync(modfile, readFileSync(join(root, 'mac/fleet-agent/go.mod'), 'utf8') +
  `\nreplace tailscale.com => ${JSON.stringify(staged)}\n`, { mode: 0o600 });
cpSync(join(root, 'mac/fleet-agent/go.sum'), join(directory, 'desktop.sum'));
process.stdout.write(modfile + '\n');
