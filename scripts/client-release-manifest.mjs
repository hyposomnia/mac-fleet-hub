import { readFileSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

export function createClientRelease(directory, revision, receipt) {
  if (!/^[a-f0-9]{40}$/.test(revision) || receipt.status !== 'Accepted' || !receipt.id) throw new Error('An immutable revision and Accepted notarization receipt are required');
  const assets = Object.fromEntries(['bootstrap.sh', 'mac-bundle.tar.gz', 'dist/fleet-agent-darwin-arm64', 'dist/fleet-agent-darwin-amd64'].map((name) => {
    const bytes = readFileSync(join(directory, name));
    if (!bytes.length) throw new Error(`Empty release asset: ${name}`);
    return [name, { sha256: createHash('sha256').update(bytes).digest('hex'), size: bytes.length }];
  }));
  return { schema: 1, revision, notarization: receipt.status, device_authorization: 1, assets };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [directory, revision, receiptFile] = process.argv.slice(2);
  const manifest = createClientRelease(directory, revision, JSON.parse(readFileSync(receiptFile, 'utf8')));
  writeFileSync(join(directory, 'release.json'), JSON.stringify(manifest, null, 2) + '\n');
}
