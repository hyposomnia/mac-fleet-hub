#!/usr/bin/env bash
set -euo pipefail
OUTPUT="${1:?提供运行组件输出目录}"
CACHE="${FLEET_RUNTIME_CACHE:-$HOME/Library/Caches/fleet-hub/runtime}"
case "$OUTPUT" in /private/tmp/*|/tmp/*) ;; *) echo '只允许独立临时构建目录。' >&2; exit 2;; esac
mkdir -p "$OUTPUT/universal/bin" "$OUTPUT/licenses" "$CACHE"
for arch in arm64 amd64; do
  case "$arch" in
    arm64) expected=b7d451cb6e31497d649895d410df5e2786fe23c58b6c2341073c967388959f8e ;;
    amd64) expected=6117f440538d442e7ef831749f6ca085f9928c27fc4c62d9944c96b2d20333a1 ;;
  esac
  archive="$CACHE/filebrowser-$arch.tar.gz"
  if [[ ! -f "$archive" ]]; then
    curl --proto '=https' --tlsv1.2 -fL --max-time 600 "https://github.com/filebrowser/filebrowser/releases/download/v2.63.23/darwin-$arch-filebrowser.tar.gz" -o "$archive"
  fi
  [[ "$(shasum -a 256 "$archive" | awk '{print $1}')" == "$expected" ]] || { echo '文件组件校验失败。' >&2; exit 1; }
  mkdir -p "$OUTPUT/filebrowser-$arch"
  tar -xzf "$archive" -C "$OUTPUT/filebrowser-$arch" filebrowser LICENSE
done
lipo -create "$OUTPUT/filebrowser-arm64/filebrowser" "$OUTPUT/filebrowser-amd64/filebrowser" -output "$OUTPUT/universal/bin/filebrowser"
chmod 0755 "$OUTPUT/universal/bin/filebrowser"
cp "$OUTPUT/filebrowser-arm64/LICENSE" "$OUTPUT/licenses/filebrowser-LICENSE"
