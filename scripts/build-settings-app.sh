#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ "$(uname -s)" == Darwin ]] || { echo '原生应用开发构建需要 macOS。' >&2; exit 1; }
[[ "${1:-}" == --development && $# == 2 ]] || { echo '用法：bash scripts/build-settings-app.sh --development /private/tmp/输出父目录' >&2; exit 2; }
PARENT="$2"
case "$PARENT" in
  /private/tmp/*|/tmp/*) ;;
  *) echo '开发构建只允许隔离临时目录，不覆盖 Applications 或正式 dist。' >&2; exit 2 ;;
esac
: "${FLEET_SETTINGS_RUNTIME:?提供已构建并校验的运行组件根目录}"
VERSION="${FLEET_SETTINGS_VERSION:-0.1.0}"
BUILD="${FLEET_SETTINGS_BUILD:-1}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && "$BUILD" =~ ^[1-9][0-9]*$ ]] || { echo '应用版本或构建号无效。' >&2; exit 1; }
bash "$ROOT/scripts/check-settings-runtime.sh" "$FLEET_SETTINGS_RUNTIME/universal"
[[ -d "$FLEET_SETTINGS_RUNTIME/licenses" ]] || { echo '缺少第三方许可证。' >&2; exit 1; }
mkdir -p "$PARENT"
OUTPUT="$(mktemp -d "$PARENT/fleet-settings.XXXXXX")"
APP="$OUTPUT/Fleet Hub.app"
AGENT="$APP/Contents/Library/LoginItems/Fleet Agent.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$APP/Contents/Library/LaunchAgents" "$AGENT/Contents/MacOS"
swift "$ROOT/scripts/render-settings-icon.swift" "$ROOT/mac/settings-app/Resources/BrandMark.svg" "$OUTPUT/artwork"
mkdir -p "$OUTPUT/AppIcon.iconset" "$AGENT/Contents/Resources"
for size in 16 32 128 256 512; do
  sips -z "$size" "$size" "$OUTPUT/artwork/AppIcon.png" --out "$OUTPUT/AppIcon.iconset/icon_${size}x${size}.png" >/dev/null
  retina=$((size * 2))
  sips -z "$retina" "$retina" "$OUTPUT/artwork/AppIcon.png" --out "$OUTPUT/AppIcon.iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$OUTPUT/AppIcon.iconset" -o "$APP/Contents/Resources/AppIcon.icns"
install -m 0644 "$APP/Contents/Resources/AppIcon.icns" "$AGENT/Contents/Resources/AppIcon.icns"
install -m 0644 "$OUTPUT/artwork/BrandMark.png" "$APP/Contents/Resources/BrandMark.png"
bash "$ROOT/scripts/prepare-settings-sdk.sh"
(cd "$ROOT/mac/settings-app" && MACOSX_DEPLOYMENT_TARGET=13.0 swift build -c release --arch arm64 --arch x86_64)
SWIFT_BIN="$(cd "$ROOT/mac/settings-app" && swift build -c release --arch arm64 --arch x86_64 --show-bin-path)"
install -m 0755 "$SWIFT_BIN/FleetHub" "$APP/Contents/MacOS/Fleet Hub"
mkdir -p "$APP/Contents/Library/Helpers"
install -m 0755 "$SWIFT_BIN/FleetLogin" "$APP/Contents/Library/Helpers/fleet-login-launcher"
mkdir -p "$APP/Contents/Frameworks"
ditto "$ROOT/mac/settings-app/Vendor/Sparkle.xcframework/macos-arm64_x86_64/Sparkle.framework" "$APP/Contents/Frameworks/Sparkle.framework"
for arch in arm64 amd64; do
  clang_arch="$arch"
  [[ "$arch" != amd64 ]] || clang_arch=x86_64
  (cd "$ROOT/mac/fleet-agent" && GOOS=darwin GOARCH="$arch" CGO_ENABLED=1 CGO_CFLAGS="-arch $clang_arch -mmacosx-version-min=13.0" CGO_LDFLAGS="-arch $clang_arch -mmacosx-version-min=13.0" go build -tags fleet_desktop -trimpath -ldflags="-s -w -X main.version=$VERSION+$BUILD" -o "$OUTPUT/agent-$arch" .)
done
lipo -create "$OUTPUT/agent-arm64" "$OUTPUT/agent-amd64" -output "$AGENT/Contents/MacOS/fleet-agent"
mkdir -p "$AGENT/Contents/Resources/bin" "$APP/Contents/Resources/Licenses"
ditto "$FLEET_SETTINGS_RUNTIME/universal/bin" "$AGENT/Contents/Resources/bin"
ditto "$FLEET_SETTINGS_RUNTIME/licenses" "$APP/Contents/Resources/Licenses"
install -m 0644 "$ROOT/mac/settings-app/Resources/com.macfleet.desktop-login.plist" "$APP/Contents/Library/LaunchAgents/com.macfleet.desktop-login.plist"
cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.macfleet.fleet-hub</string>
<key>CFBundleName</key><string>Fleet Hub</string>
<key>CFBundleDisplayName</key><string>Fleet Hub</string>
<key>CFBundleExecutable</key><string>Fleet Hub</string>
<key>CFBundleIconFile</key><string>AppIcon</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>0.1.0</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
cat > "$AGENT/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.macfleet.fleet-agent</string>
<key>CFBundleName</key><string>Fleet Agent</string>
<key>CFBundleExecutable</key><string>fleet-agent</string>
<key>CFBundleIconFile</key><string>AppIcon</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>0.1.0</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>LSUIElement</key><true/>
</dict></plist>
PLIST
printf '%s\n' '{"channel":"development","release_ready":false}' > "$APP/Contents/Resources/release-status.json"
for plist in "$APP/Contents/Info.plist" "$AGENT/Contents/Info.plist"; do
  plutil -replace CFBundleShortVersionString -string "$VERSION" "$plist"
  plutil -replace CFBundleVersion -string "$BUILD" "$plist"
done
plutil -lint "$APP/Contents/Info.plist" "$AGENT/Contents/Info.plist" "$APP/Contents/Library/LaunchAgents/com.macfleet.desktop-login.plist"
printf '\n仅开发预览：未签名、公证，不可发布下载或用于正式安装。\n%s\n' "$APP"
