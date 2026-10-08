import { readFileSync, mkdtempSync, mkdirSync, copyFileSync, writeFileSync, rmSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import assert from 'node:assert/strict';

const read = (relative) => readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8');

test('disk permissions always offer authorization and prepare the standalone drag target', () => {
  const view = read('../mac/settings-app/Sources/FleetHub/SettingsView.swift');
  const privacy = view.slice(view.indexOf('private var privacy:'), view.indexOf('private var about:'));
  assert.match(privacy, /Button\("授权磁盘访问"\)/);
  assert.doesNotMatch(privacy, /Button\("安装并启动"\)/);
  assert.match(privacy, /diskGuide\.authorize\(applicationURL: management\.layout\.backgroundApplication\)/);
  assert.match(privacy, /management\.start\(\)/);
});

test('runtime details show version and process without a disclosure control', () => {
  const view = read('../mac/settings-app/Sources/FleetHub/SettingsView.swift');
  const overview = view.slice(view.indexOf('private var overview:'), view.indexOf('private var connection:'));
  assert.doesNotMatch(overview, /DisclosureGroup\("运行详情"\)/);
  assert.match(overview, /Text\("运行详情"\)/);
  assert.match(overview, /row\("版本", current\.version\)/);
  assert.match(overview, /row\("进程", String\(current\.pid\)\)/);
});

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

test('both native app bundles include the adopted brand icon', () => {
  const script = read('./build-settings-app.sh');
  assert.match(script, /render-settings-icon\.swift/);
  assert.match(script, /iconutil.*-c icns/);
  assert.equal((script.match(/CFBundleIconFile/g) || []).length, 2);
  assert.match(script, /\$AGENT\/Contents\/Resources\/AppIcon\.icns/);
});

test('app icon artwork fills the canvas without clipping or changing the brand mark', { skip: process.platform !== 'darwin' }, () => {
  const root = mkdtempSync(join(tmpdir(), 'fleet-icon-test-'));
  try {
    const renderer = fileURLToPath(new URL('./render-settings-icon.swift', import.meta.url));
    const brand = fileURLToPath(new URL('../mac/settings-app/Resources/BrandMark.svg', import.meta.url));
    const render = spawnSync('/usr/bin/swift', [renderer, brand, root], { encoding: 'utf8', timeout: 60000 });
    assert.equal(render.status, 0, render.stderr);
    const probe = join(root, 'probe.swift');
    writeFileSync(probe, `import AppKit
let bitmap = NSBitmapImageRep(data: try Data(contentsOf: URL(fileURLWithPath: CommandLine.arguments[1])))!
var minimumX = bitmap.pixelsWide, minimumY = bitmap.pixelsHigh, maximumX = 0, maximumY = 0
for row in 0..<bitmap.pixelsHigh {
    for column in 0..<bitmap.pixelsWide {
        guard let color = bitmap.colorAt(x: column, y: row)?.usingColorSpace(.deviceRGB) else { continue }
        if color.redComponent < 0.6 && color.blueComponent > color.redComponent * 1.2 {
            minimumX = min(minimumX, column); minimumY = min(minimumY, row)
            maximumX = max(maximumX, column); maximumY = max(maximumY, row)
        }
    }
}
print("\\(minimumX) \\(minimumY) \\(maximumX) \\(maximumY) \\(bitmap.pixelsWide) \\(bitmap.pixelsHigh)")
`);
    const measured = spawnSync('/usr/bin/swift', [probe, join(root, 'AppIcon.png')], { encoding: 'utf8', timeout: 60000 });
    assert.equal(measured.status, 0, measured.stderr);
    const [minimumX, minimumY, maximumX, maximumY, width, height] = measured.stdout.trim().split(/\s+/).map(Number);
    assert.equal(width, 1024); assert.equal(height, 1024);
    assert.ok((maximumY - minimumY + 1) / height >= 0.76, 'logo is too small inside the app icon');
    assert.ok(minimumX >= width * 0.08 && minimumY >= height * 0.08);
    assert.ok(maximumX < width * 0.92 && maximumY < height * 0.92);
    assert.ok(Math.abs((minimumX + maximumX) / 2 - (width - 1) / 2) <= 1);
    assert.ok(Math.abs((minimumY + maximumY) / 2 - (height - 1) / 2) <= 1);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('login helper only starts the independently managed daemon', () => {
  const plist = read('../mac/settings-app/Resources/com.macfleet.desktop-login.plist');
  assert.match(plist, /com\.macfleet\.desktop-login/);
  assert.match(plist, /autostart-start/);
  assert.match(plist, /Contents\/Library\/Helpers\/fleet-login-launcher/);
  assert.doesNotMatch(plist, /Fleet Agent\.app|LoginItems/);
  assert.doesNotMatch(plist, /KeepAlive|sudo|bash|device_token|proxy_token/);
});

test('standalone agent is separately notarized before host signing', () => {
  const native = read('./lib/native-candidate-release.sh');
  assert.match(native, /com\.macfleet\.desktop-login/);
  assert.match(native, /notarize-agent\.zip/);
  assert.match(native, /agent-notary\.json/);
  assert.match(native, /stapler validate "\$agent"/);
  assert.ok(native.indexOf('stapler validate "$agent"') < native.indexOf('--identifier com.macfleet.fleet-hub'));
});

test('upgrade recovery synchronizes the standalone runtime even when an old daemon responds', () => {
  const updater = read('../mac/settings-app/Sources/FleetHub/AppUpdater.swift');
  const recovery = updater.slice(updater.indexOf('func recoverAfterLaunch'));
  assert.doesNotMatch(recovery, /if \(try\? await management\.status\(\)\) == nil/);
  assert.match(recovery, /try\? await management\.start\(\)/);
  const view = read('../mac/settings-app/Sources/FleetHub/SettingsView.swift');
  assert.match(view, /management\.prepareRuntime\(\)/);
  assert.match(view, /updater\.recoveryPending/);
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
  const guide = read('../mac/settings-app/Sources/FleetHub/DiskAccessGuide.swift');
  assert.match(guide, /Privacy_AllFiles/);
  assert.match(guide, /Fleet Agent\.app/);
  assert.match(view, /applicationURL: management\.layout\.backgroundApplication/);
  assert.match(guide, /NSDraggingItem\(pasteboardWriter: url as NSURL\)/);
  assert.match(guide, /activateFileViewerSelecting\(\[application\.url\]\)/);
  assert.match(guide, /无需授权 Fleet Hub/);
  assert.match(guide, /打开开关/);
  assert.match(view, /accessibilityLabel\("重新检查后台权限"\)/);
  assert.match(view, /ACL/);
  assert.doesNotMatch(view + guide, /tccutil|TCC\.db|sudo|mac-bundle\.tar/);
});

test('native sidebar and shared buttons hit the whole padded area, with first-run installation on the overview', () => {
  const navigation = read('../mac/settings-app/Sources/FleetHub/FleetNavigationButton.swift');
  assert.match(navigation, /frame\(maxWidth: \.infinity, alignment: \.leading\)/);
  assert.match(navigation, /frame\(height: FleetTheme\.controlHeight\)/);
  assert.match(navigation, /contentShape\(Rectangle\(\)\)/);
  const theme = read('../mac/settings-app/Sources/FleetHub/FleetTheme.swift');
  assert.match(theme, /frame\(minHeight:[\s\S]*?contentShape\(Rectangle\(\)\)/);
  const view = read('../mac/settings-app/Sources/FleetHub/SettingsView.swift');
  assert.match(view, /Button\(setupAction == \.installApplication \? installationTitle : setupAction\.title\)/);
  assert.match(view, /--fleet-install-and-start/);
  assert.match(view, /configuration\.createsNewApplicationInstance = true/);
  assert.doesNotMatch(view, /page = \.about; return/);
});

test('native account authorization stays actionable with automatic settings and startup on overview', () => {
  const view = read('../mac/settings-app/Sources/FleetHub/SettingsView.swift');
  const overview = view.slice(view.indexOf('private var overview:'), view.indexOf('private var connection:'));
  const connection = view.slice(view.indexOf('private var connection:'), view.indexOf('private var privacy:'));
  assert.match(overview, /Toggle\("登录后启动后台"/);
  assert.doesNotMatch(connection, /登录后启动后台|保存设置/);
  assert.match(connection, /if management\.layout\.requiresInstallation \{ installApplication\(\) \}/);
  assert.match(connection, /disabled\(!validOrigin \|\| model\.isSaving/);
  assert.match(view, /onChange\(of: model\.origin\)/);
  assert.match(view, /600_000_000/);
  assert.match(view, /guard await model\.save\(\) else \{ return \}/);
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

test('native notarization uses standard S3 uploads for each complete signed archive', () => {
  const submissions = read('./lib/native-candidate-release.sh').split('\n').filter(line => line.includes('xcrun notarytool submit'));
  assert.equal(submissions.length, 3);
  for (const submission of submissions) assert.match(submission, /--no-s3-acceleration/);
});
