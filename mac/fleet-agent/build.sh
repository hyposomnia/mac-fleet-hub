#!/usr/bin/env bash
# 重建并签名 fleet-agent 双架构产物到 dist/，版本号注入 git short-sha + 日期。
# 改 main.go / selfcmd.go 后跑此脚本；产物入库，各机用 `fleet-agent update` 拉取生效。
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

SIGN_IDENTITY="${FLEET_CODESIGN_IDENTITY:-}"
SIGN_IDENTIFIER="${FLEET_CODESIGN_IDENTIFIER:-com.macfleet.fleet-agent}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "fleet-agent 发布产物必须在 macOS 上使用 Developer ID Application 证书签名。" >&2
  exit 1
fi
command -v codesign >/dev/null 2>&1 || { echo "未找到 codesign。" >&2; exit 1; }

# 未显式指定时优先选择 Developer ID Application；受控内网设备可回退到唯一的
# Apple Development 证书名称。同名的续期证书具有相同 Team/subject，codesign 会选择
# 当前有效项，designated requirement 仍保持稳定。
if [[ -z "$SIGN_IDENTITY" ]]; then
  mapfile_output="$(security find-identity -v -p codesigning 2>/dev/null \
    | sed -n 's/.*"\(Developer ID Application:.*\)".*/\1/p' | sort -u)"
  identity_count="$(printf '%s\n' "$mapfile_output" | sed '/^$/d' | wc -l | tr -d ' ')"
  if [[ "$identity_count" == "1" ]]; then
    SIGN_IDENTITY="$mapfile_output"
  else
    development_identities="$(security find-identity -v -p codesigning 2>/dev/null \
      | sed -n 's/.*"\(Apple Development:.*\)".*/\1/p' | sort -u)"
    development_count="$(printf '%s\n' "$development_identities" | sed '/^$/d' | wc -l | tr -d ' ')"
    if [[ "$identity_count" == "0" && "$development_count" == "1" ]]; then
      # 同一身份续期后钥匙串可能同时保留多个同名有效证书；按名称会 ambiguous。
      # 使用 security 列出的首个有效 identity 指纹签名，指纹不写入仓库。
      SIGN_IDENTITY="$(security find-identity -v -p codesigning 2>/dev/null \
        | sed -n '/"Apple Development:/s/^ *[0-9]*) \([0-9A-F]\{40\}\) .*/\1/p' | head -1)"
      [[ -n "$SIGN_IDENTITY" ]] || { echo "无法解析 Apple Development identity 指纹。" >&2; exit 1; }
      echo "警告：未找到 Developer ID Application；使用受控设备 Apple Development 签名。" >&2
    else
      echo "无法唯一确定签名身份（Developer ID Application=${identity_count}, Apple Development=${development_count}）。" >&2
      echo "请设置 FLEET_CODESIGN_IDENTITY 为证书名称或 SHA-1 指纹后重试。" >&2
      exit 1
    fi
  fi
fi

VER="$(git rev-parse --short HEAD)-$(date +%Y%m%d)"
for arch in amd64 arm64; do
  output="dist/fleet-agent-darwin-${arch}"
  GOOS=darwin GOARCH="$arch" go build -trimpath \
    -ldflags="-s -w -X main.version=${VER}" \
    -o "$output" .
  codesign --force --sign "$SIGN_IDENTITY" --identifier "$SIGN_IDENTIFIER" \
    --options runtime --timestamp "$output"
  codesign --verify --deep --strict --verbose=2 "$output"
  actual_identifier="$(codesign -d --verbose=4 "$output" 2>&1 | sed -n 's/^Identifier=//p')"
  [[ "$actual_identifier" == "$SIGN_IDENTIFIER" ]] || {
    echo "签名 identifier 异常：${actual_identifier:-<empty>}" >&2
    exit 1
  }
  echo "built + signed $output  (${VER}, identifier=$SIGN_IDENTIFIER)"
done
