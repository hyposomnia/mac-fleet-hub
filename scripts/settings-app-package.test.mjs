import { readFileSync, mkdtempSync, mkdirSync, copyFileSync, writeFileSync, rmSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import assert from 'node:assert/strict';

const read = (relative) => readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8');

test('SDK preparation replaces a tampered extracted cache from the verified archive', {
  skip: process.platform !== 'darwin' || !process.env.FLEET_SPARKLE_ARCHIVE,
}, () => {
  const root = mkdtempSync(join(tmpdir(), 'fleet-sdk-test-'));
  try {
    mkdirSync(join(root, 'scripts'));
    copyFileSync(fileURLToPath(new URL('./prepare-settings-sdk.sh', import.meta.url)), join(root, 'scripts/prepare-settings-sdk.sh'));
    const vendor = join(root, 'mac/settings-app/Vendor');
    mkdirSync(join(vendor, 'Sparkle.xcframework'), { recursive: true });
    writeFileSync(join(vendor, 'Sparkle.xcframework/Info.plist'), 'tampered');
    writeFileSync(join(vendor, 'unexpected-file'), 'must not survive');
    const result = spawnSync('bash', [join(root, 'scripts/prepare-settings-sdk.sh')], { encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    assert.notEqual(readFileSync(join(vendor, 'Sparkle.xcframework/Info.plist'), 'utf8'), 'tampered');
    assert.equal(existsSync(join(vendor, 'unexpected-file')), false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('development packaging is isolated and cannot publish an installer', () => {
  const script = read('./build-settings-app.sh');
  assert.match(script, /--development/);
  assert.match(script, /Fleet Hub\.app/);
  assert.match(script, /Fleet Agent\.app/);
  assert.match(script, /com\.macfleet\.fleet-hub/);
  assert.match(script, /com\.macfleet\.fleet-agent/);
  assert.match(script, /release_ready.*false/);
  assert.doesNotMatch(script, /ssh |scp |codesign --sign|hdiutil create|launchctl (bootstrap|kickstart|bootout)/);
});

test('login helper only starts the independently managed daemon', () => {
  const plist = read('../mac/settings-app/Resources/com.macfleet.desktop-login.plist');
  assert.match(plist, /com\.macfleet\.desktop-login/);
  assert.match(plist, /autostart-start/);
  assert.doesNotMatch(plist, /KeepAlive|sudo|bash|device_token|proxy_token/);
});

test('native packaging requires verified bundled runtime instead of developer Homebrew', () => {
  const script = read('./build-settings-app.sh');
  assert.match(script, /FLEET_SETTINGS_RUNTIME/);
  assert.match(script, /check-settings-runtime\.sh/);
  assert.match(script, /Resources\/bin/);
  assert.match(script, /Resources\/Licenses/);
  assert.match(script, /--arch arm64 --arch x86_64/);
  assert.match(script, /GOARCH/);
});

test('native privacy instructions identify the background bundle and do not promise universal permission', () => {
  const view = read('../mac/settings-app/Sources/FleetHub/SettingsView.swift');
  assert.match(view, /Privacy_AllFiles/);
  assert.match(view, /Fleet Agent\.app/);
  assert.match(view, /目标不存在、后台未运行或证据不足/);
  assert.match(view, /ACL/);
  assert.doesNotMatch(view, /tccutil|TCC\.db|sudo|mac-bundle\.tar/);
});

test('native candidate publication stays behind the unique signing entry and Accepted receipts', () => {
  const entry = read('./release-fleet-agent.sh');
  const candidate = read('./lib/candidate-release.sh');
  const native = read('./lib/native-candidate-release.sh');
  assert.match(entry, /--native-candidate/);
  assert.match(candidate, /native_candidate_preflight/);
  assert.match(candidate, /run_native_candidate_release/);
  assert.match(native, /Developer ID Application/);
  assert.match(native, /notarytool submit/);
  assert.match(native, /Accepted/);
  assert.match(native, /stapler validate/);
  assert.match(native, /hdiutil create/);
  assert.match(native, /client-native-releases/);
  assert.match(native, /native-client-release\.mjs/);
  assert.ok(native.indexOf('stapler validate') < native.indexOf('scp '));
  assert.doesNotMatch(native, /fleet-agent update|launchctl|git commit/);
});
