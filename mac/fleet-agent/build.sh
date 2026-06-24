#!/usr/bin/env bash
# 重建 fleet-agent 双架构产物到 dist/，版本号注入 git short-sha + 日期。
# 改 main.go / selfcmd.go 后跑此脚本；产物入库，各机用 `fleet-agent update` 拉取生效。
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

VER="$(git rev-parse --short HEAD)-$(date +%Y%m%d)"
for arch in amd64 arm64; do
  GOOS=darwin GOARCH="$arch" go build -trimpath \
    -ldflags="-s -w -X main.version=${VER}" \
    -o "dist/fleet-agent-darwin-${arch}" .
  echo "built dist/fleet-agent-darwin-${arch}  (${VER})"
done
