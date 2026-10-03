#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARCHIVE="${FLEET_SPARKLE_ARCHIVE:-$HOME/Library/Caches/fleet-hub/Sparkle-2.10.0.zip}"
EXPECTED=17e28312b8e18ab7cdbbe09a6fb28cc55a5479ec6c371dbc07cdecd2a14fd959
if [[ ! -f "$ARCHIVE" ]]; then
  mkdir -p "$(dirname "$ARCHIVE")"
  curl --proto '=https' --tlsv1.2 -fL --max-time 600 https://github.com/sparkle-project/Sparkle/releases/download/2.10.0/Sparkle-for-Swift-Package-Manager.zip -o "$ARCHIVE"
fi
[[ "$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')" == "$EXPECTED" ]] || { echo 'Sparkle SDK 校验失败。' >&2; exit 1; }
DESTINATION="$ROOT/mac/settings-app/Vendor"
mkdir -p "$(dirname "$DESTINATION")"
LOCK="${DESTINATION}.prepare-lock"
mkdir "$LOCK" 2>/dev/null || { echo '另一个 Sparkle SDK 准备操作正在运行。' >&2; exit 1; }
WORK=""
cleanup() {
  [[ -z "$WORK" ]] || rm -rf "$WORK"
  rmdir "$LOCK"
}
trap cleanup EXIT
WORK="$(mktemp -d "${DESTINATION}.prepare.XXXXXX")"
ditto -x -k "$ARCHIVE" "$WORK/new"
test -f "$WORK/new/Sparkle.xcframework/Info.plist"
if [[ -e "$DESTINATION" || -L "$DESTINATION" ]]; then
  mv "$DESTINATION" "$WORK/previous"
fi
if ! mv "$WORK/new" "$DESTINATION"; then
  [[ ! -e "$WORK/previous" && ! -L "$WORK/previous" ]] || mv "$WORK/previous" "$DESTINATION"
  exit 1
fi
printf '%s\n' "$DESTINATION"
