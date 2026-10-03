#!/usr/bin/env bash
set -euo pipefail
DIRECTORY="${1:?提供运行组件目录}"
for name in ttyd tmux filebrowser; do
  binary="$DIRECTORY/bin/$name"
  [[ -x "$binary" && ! -L "$binary" ]] || { echo "缺少运行组件 $name" >&2; exit 1; }
  architectures="$(lipo -archs "$binary")"
  [[ "$architectures" == 'x86_64 arm64' || "$architectures" == 'arm64 x86_64' ]] || { echo "$name 不是双架构组件" >&2; exit 1; }
  for arch in arm64 x86_64; do
    minimum="$(xcrun vtool -arch "$arch" -show-build "$binary" | awk '/minos/{print $2}')"
    [[ -n "$minimum" ]] || { echo "$name 缺少最低系统版本" >&2; exit 1; }
    awk -v version="$minimum" 'BEGIN {split(version,parts,"."); exit !(parts[1] < 13 || parts[1] == 13 && parts[2] == 0)}' || { echo "$name 要求 macOS ${minimum}，不能宣称支持 13.0" >&2; exit 1; }
  done
  dependencies="$(otool -L "$binary" | awk '/compatibility version/{print $1}')"
  if nm -m "$binary" | grep 'weak external _pipe2' >/dev/null; then echo "$name 包含低版本系统不可用的 pipe2 调用" >&2; exit 1; fi
  while IFS= read -r dependency; do
    case "$dependency" in /usr/lib/*|/System/Library/*) ;; *) echo "$name 依赖外部库 $dependency" >&2; exit 1;; esac
  done <<< "$dependencies"
done
[[ -x "$DIRECTORY/bin/fleet-attach" && ! -L "$DIRECTORY/bin/fleet-attach" ]]
printf '运行组件：双架构、macOS 13 兼容声明、无 Homebrew 动态依赖检查通过\n'
