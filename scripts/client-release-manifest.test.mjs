import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createClientRelease } from './client-release-manifest.mjs';

test('client downloads require every real file, immutable revision and Accepted receipt', () => {
  const directory = mkdtempSync(join(tmpdir(), 'fleet-client-manifest-'));
  try {
    const revision = 'a'.repeat(40);
    const receipt = { status: 'Accepted', id: 'test-submission' };
    assert.throws(() => createClientRelease(directory, revision, receipt));
    mkdirSync(join(directory, 'dist'));
    for (const asset of ['bootstrap.sh', 'mac-bundle.tar.gz', 'dist/fleet-agent-darwin-arm64', 'dist/fleet-agent-darwin-amd64']) writeFileSync(join(directory, asset), asset);
    assert.throws(() => createClientRelease(directory, 'main', receipt));
    assert.throws(() => createClientRelease(directory, revision, { status: 'Invalid', id: 'test-submission' }));
    assert.throws(() => createClientRelease(directory, revision, { status: 'Accepted' }));
    const release = createClientRelease(directory, revision, receipt);
    assert.equal(release.notarization, 'Accepted');
    assert.equal(release.device_authorization, 1);
    assert.equal(Object.keys(release.assets).length, 4);
    for (const asset of Object.values(release.assets)) assert.match(asset.sha256, /^[a-f0-9]{64}$/);
    writeFileSync(join(directory, 'mac-bundle.tar.gz'), '');
    assert.throws(() => createClientRelease(directory, revision, receipt));
  } finally { rmSync(directory, { recursive: true, force: true }); }
});
