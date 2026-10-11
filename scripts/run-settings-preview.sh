#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ "$(uname -s)" == Darwin ]] || { echo '界面预览需要 macOS。' >&2; exit 1; }
INSTALLED='/Applications/Fleet Hub.app'
[[ -d "$INSTALLED/Contents/Frameworks/Sparkle.framework" ]] || { echo '请先安装正式 Fleet Hub。' >&2; exit 1; }
(cd "$ROOT/mac/settings-app" && swift build -c debug)
BIN="$(cd "$ROOT/mac/settings-app" && swift build -c debug --show-bin-path)"
OUTPUT="$(mktemp -d /private/tmp/fleet-hub-preview.XXXXXX)"
APP="$OUTPUT/Fleet Hub Preview.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources/codex" "$APP/Contents/Frameworks"
install -m 0755 "$BIN/FleetHub" "$APP/Contents/MacOS/Fleet Hub Preview"
ditto "$INSTALLED/Contents/Frameworks/Sparkle.framework" "$APP/Contents/Frameworks/Sparkle.framework"
for file in AppIcon.icns BrandMark.png; do
  install -m 0644 "$INSTALLED/Contents/Resources/$file" "$APP/Contents/Resources/$file"
done
for file in codex-bin-resolve.sh codex-shared-app-server.mjs codex-desktop-env.sh check-codex-idle.sh com.macfleet.codex-shared-app-server.plist com.macfleet.codex-desktop-env.plist; do
  install -m 0644 "$ROOT/mac/$file" "$APP/Contents/Resources/codex/$file"
done
cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.macfleet.fleet-hub.preview</string>
<key>CFBundleName</key><string>Fleet Hub Preview</string>
<key>CFBundleExecutable</key><string>Fleet Hub Preview</string>
<key>CFBundleIconFile</key><string>AppIcon</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>NSHighResolutionCapable</key><true/>
<key>FleetUIPreview</key><true/>
</dict></plist>
PLIST
plutil -lint "$APP/Contents/Info.plist"
for key in CFBundleShortVersionString CFBundleVersion; do
  value="$(/usr/libexec/PlistBuddy -c "Print :$key" "$INSTALLED/Contents/Info.plist")"
  plutil -replace "$key" -string "$value" "$APP/Contents/Info.plist"
done
open -n "$APP"
printf '%s\n' "$APP"
