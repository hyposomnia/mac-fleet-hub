import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync, existsSync, symlinkSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';

const source = readFileSync(new URL('../server/enroll/multiuser/native_install.sh', import.meta.url), 'utf8');
const origin = 'https://fleet.example.test:19443';
// Only OS/network boundaries are replaced. The shipped shell control flow, validation and SHA check run unchanged.
function run({ changeManifest = () => {}, changeApp = () => {}, failure = '', machine = 'arm64', system = '14.7', http = '200', uid = '501' } = {}) {
  const root = mkdtempSync(join(tmpdir(), 'fleet-install-shell-'));
  try {
    const tools = join(root, 'tools'); mkdirSync(tools);
    const zip = Buffer.from('immutable package test bytes');
    const manifest = { schema: 1, installation_protocol: 1, bundle_id: 'com.macfleet.fleet-hub', notarization: 'Accepted', version: '2.3.4', build: 19,
      minimum_macos: '13.0', architectures: ['arm64', 'x86_64'], assets: { update: { path: '/enroll/clients/19/Fleet-Hub-update.zip', size: zip.length, sha256: createHash('sha256').update(zip).digest('hex') } } };
    const app = { CFBundleIdentifier: 'com.macfleet.fleet-hub', CFBundleExecutable: 'Fleet Agent Settings', FleetInstallProtocol: 1, CFBundleVersion: '19', CFBundleShortVersionString: '2.3.4' };
    changeManifest(manifest); changeApp(app);
    writeFileSync(join(root, 'manifest.json'), JSON.stringify(manifest));
    writeFileSync(join(root, 'app.json'), JSON.stringify(app));
    writeFileSync(join(root, 'update.zip'), zip);
    const dispatcher = `#!${process.execPath}
const fs = require('node:fs'), path = require('node:path');
const root = process.env.INSTALL_FIXTURE;
const name = path.basename(process.argv[1]), args = process.argv.slice(2);
fs.appendFileSync(path.join(root, 'calls'), JSON.stringify({ name, args }) + '\\n');
if (process.env.INSTALL_FAILURE === name) process.exit(1);
switch(name) {
case 'uname': console.log(args[0] === '-s' ? 'Darwin' : process.env.INSTALL_MACHINE); break;
case 'id': console.log(process.env.INSTALL_UID); break;
case 'sw_vers': console.log(process.env.INSTALL_SYSTEM); break;
case 'curl': {
  if (args.includes('-L') || args.includes('--location')) process.exit(30);
  const url = args.at(-1), out = args[args.indexOf('--output') + 1];
  const file = url === '${origin}/enroll/client-release.json' ? 'manifest.json' : url === '${origin}/enroll/clients/19/Fleet-Hub-update.zip' ? 'update.zip' : null;
  if (!file) process.exit(31);
  fs.copyFileSync(path.join(root, file), out); process.stdout.write(process.env.INSTALL_HTTP); break;
}
case 'plutil': {
  let value = JSON.parse(fs.readFileSync(args.at(-1), 'utf8'));
  for (const key of args[1].split('.')) value = value?.[key];
  if (value === undefined) process.exit(1);
  console.log(value); break;
}
case 'ditto': {
  const app = path.join(args.at(-1), 'Fleet Agent Settings.app');
  fs.mkdirSync(path.join(app, 'Contents/MacOS'), { recursive: true });
  fs.copyFileSync(path.join(root, 'app.json'), path.join(app, 'Contents/Info.plist'));
  fs.writeFileSync(path.join(app, 'Contents/MacOS/Fleet Agent Settings'), '#!/bin/bash\\nprintf \\"%s\\\\n\\" \\"$@\\" > \\"$INSTALL_FIXTURE/executed\\"\\nexit 0\\n', { mode: 0o755 }); break;
}
case 'codesign': case 'spctl': case 'lipo': break;
default: process.exit(99);
}
`;
    writeFileSync(join(tools, 'dispatch'), dispatcher, { mode: 0o755 });
    for (const tool of ['uname', 'id', 'sw_vers', 'curl', 'plutil', 'ditto', 'codesign', 'spctl', 'lipo']) symlinkSync('dispatch', join(tools, tool));
    let script = source.replace('/private/tmp/fleet-native-install.XXXXXX', join(root, 'download.XXXXXX')).replace('__FLEET_INSTALL_ORIGIN__', "'" + origin + "'");
    for (const tool of ['uname', 'id', 'sw_vers', 'curl', 'plutil', 'ditto', 'codesign', 'spctl', 'lipo']) {
      script = script.replaceAll('/usr/bin/' + tool, join(tools, tool)).replaceAll('/usr/sbin/' + tool, join(tools, tool));
    }
    const file = join(root, 'install.sh'); writeFileSync(file, script);
    const result = spawnSync('bash', [file], { encoding: 'utf8', timeout: 30000, env: { ...process.env, INSTALL_FIXTURE: root, INSTALL_FAILURE: failure,
      INSTALL_MACHINE: machine, INSTALL_SYSTEM: system, INSTALL_HTTP: http, INSTALL_UID: uid } });
    const executed = existsSync(join(root, 'executed')) ? readFileSync(join(root, 'executed'), 'utf8') : '';
    const calls = existsSync(join(root, 'calls')) ? readFileSync(join(root, 'calls'), 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse) : [];
    return { ...result, executed, calls };
  } finally { rmSync(root, { recursive: true, force: true }); }
}

test('one-line installer verifies the immutable package before invoking only the no-window entry', () => {
  for (const machine of ['arm64', 'x86_64']) {
    const result = run({ machine });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.executed, `--fleet-install-cli\n--fleet-origin=${origin}\n`);
    const signatureCheck = result.calls.find(call => call.name === 'codesign');
    assert.ok(signatureCheck);
    const requirement = signatureCheck.args[signatureCheck.args.indexOf('-R') + 1];
    assert.match(requirement, /^=identifier /, 'codesign inline requirements must start with =');
    if (process.platform === 'darwin') {
      const parsed = spawnSync('/usr/bin/csreq', ['-r', requirement, '-t'], { encoding: 'utf8' });
      assert.equal(parsed.status, 0, parsed.stderr);
    }
    assert.ok(result.calls.find(call => call.name === 'spctl'));
    const downloads = result.calls.filter(call => call.name === 'curl');
    assert.equal(downloads.length, 2);
    assert.ok(downloads.every(call => call.args.includes('--proto') && !call.args.includes('--location')));
  }
});

test('installer never executes incompatible, corrupt, redirected or untrusted downloads', () => {
  const cases = [
    { changeManifest: m => { delete m.installation_protocol; } },
    { changeManifest: m => { m.installation_protocol = 2; } },
    { changeManifest: m => { m.schema = 2; } },
    { changeManifest: m => { m.notarization = 'In Progress'; } },
    { changeManifest: m => { m.bundle_id = 'other.app'; } },
    { changeManifest: m => { m.assets.update.path = 'https://evil.test/update.zip'; } },
    { changeManifest: m => { m.assets.update.sha256 = 'a'.repeat(64); } },
    { changeManifest: m => { m.assets.update.size++; } },
    { changeManifest: m => { m.minimum_macos = '99.0'; } },
    { changeManifest: m => { m.architectures = ['x86_64']; } },
    { changeApp: app => { app.FleetInstallProtocol = 0; } },
    { changeApp: app => { app.CFBundleIdentifier = 'other.app'; } },
    { changeApp: app => { app.CFBundleVersion = '18'; } },
    { changeApp: app => { app.CFBundleExecutable = '../untrusted'; } },
    ...['curl', 'ditto', 'codesign', 'spctl', 'lipo'].map(failure => ({ failure })),
    { machine: 'ppc' }, { system: '12.7' }, { http: '302' }, { uid: '0' },
  ];
  for (const options of cases) {
    const result = run(options);
    assert.notEqual(result.status, 0, JSON.stringify(options));
    assert.equal(result.executed, '', JSON.stringify(options));
  }
});
