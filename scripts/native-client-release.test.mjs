import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createNativeRelease } from './native-client-release.mjs';

function fixture() {
  const directory = mkdtempSync(join(tmpdir(), 'fleet-release-'));
  const dmg = Buffer.alloc(1024);
  dmg.write('koly', dmg.length - 512);
  writeFileSync(join(directory, 'Fleet-Hub.dmg'), dmg);
  writeFileSync(join(directory, 'Fleet-Hub-update.zip'), Buffer.from([0x50, 0x4b, 3, 4, 1]));
  return { directory, metadata: { origin: 'https://fleet.example.test:9443', version: '1.2.3', build: 12,
    revision: 'a'.repeat(40), signature: Buffer.alloc(64, 1).toString('base64'),
    appNotary: { id: 'app-receipt', status: 'Accepted' }, dmgNotary: { id: 'dmg-receipt', status: 'Accepted' } } };
}

test('native release hashes actual app packages and derives feed from configured origin', () => {
  const { directory, metadata } = fixture();
  try {
    const { manifest, appcast } = createNativeRelease(directory, metadata);
    assert.equal(manifest.bundle_id, 'com.macfleet.fleet-hub');
    assert.equal(manifest.assets.dmg.path, '/enroll/clients/12/Fleet-Hub.dmg');
    assert.equal(manifest.assets.dmg.size, 1024);
    assert.match(manifest.assets.update.sha256, /^[a-f0-9]{64}$/);
    assert.ok(appcast.includes('https://fleet.example.test:9443/enroll/clients/12/Fleet-Hub-update.zip'));
    assert.ok(appcast.includes('sparkle:edSignature="' + metadata.signature + '"'));
    assert.ok(appcast.includes('<sparkle:version>12</sparkle:version>'));
    assert.deepEqual(manifest.architectures, ['arm64', 'x86_64']);
  } finally { rmSync(directory, { recursive: true }); }
});

test('native release rejects source archives, missing signatures and unfinished notarization', () => {
  const { directory, metadata } = fixture();
  try {
    for (const changes of [
      { origin: 'http://fleet.example.test' }, { origin: 'https://fleet.example.test/path' },
      { version: '<script>' }, { build: 0 }, { signature: 'fake' }, { revision: 'main' },
      { appNotary: { status: 'In Progress', id: 'pending' } }, { dmgNotary: { status: 'Accepted' } },
    ]) assert.throws(() => createNativeRelease(directory, { ...metadata, ...changes }));
    writeFileSync(join(directory, 'Fleet-Hub.dmg'), 'source archive');
    assert.throws(() => createNativeRelease(directory, metadata));
  } finally { rmSync(directory, { recursive: true }); }
});
