#!/usr/bin/env bash
# 重建并签名 fleet-agent 双架构产物到 dist/，版本号注入 git short-sha + 日期。
# 改 main.go / selfcmd.go 后跑此脚本；产物入库，各机用 `fleet-agent update` 拉取生效。
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

SIGN_IDENTITY="${FLEET_CODESIGN_IDENTITY:-}"
SIGN_IDENTIFIER="${FLEET_CODESIGN_IDENTIFIER:-com.macfleet.fleet-agent}"
NOTARY_PROFILE="${FLEET_NOTARY_PROFILE:-mac-fleet-hub-notary}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "fleet-agent 发布产物必须在 macOS 上使用 Developer ID Application 证书签名。" >&2
  exit 1
fi
command -v codesign >/dev/null 2>&1 || { echo "未找到 codesign。" >&2; exit 1; }

# 自动选择唯一的 Developer ID Application identity。Apple Development 明确不接受，
# 避免受控构建误产出会被其他 Mac 的 Gatekeeper/XProtect 拒绝的二进制。
if [[ -z "$SIGN_IDENTITY" ]]; then
  identities="$(security find-identity -v -p codesigning 2>/dev/null \
    | sed -n '/"Developer ID Application:/s/^ *[0-9]*) \([0-9A-F]\{40\}\) .*/\1/p')"
  identity_count="$(printf '%s\n' "$identities" | sed '/^$/d' | wc -l | tr -d ' ')"
  if [[ "$identity_count" != "1" ]]; then
    echo "需要唯一的 Developer ID Application 签名证书（当前找到 ${identity_count} 张）。" >&2
    echo "请设置 FLEET_CODESIGN_IDENTITY 为证书名称或 SHA-1 identity 后重试。" >&2
    exit 1
  fi
  SIGN_IDENTITY="$identities"
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

# 裸 Mach-O 不能 staple ticket，但 Apple 公证会按签名后的 code hash 登记结果。
# 两个架构放进同一 ZIP 一次提交；notarytool 非 Accepted 会以非零状态退出。
command -v xcrun >/dev/null 2>&1 || { echo "未找到 xcrun/notarytool。" >&2; exit 1; }
NOTARY_TMP="$(mktemp -d)"
cleanup_notary_tmp() { [[ -n "${NOTARY_TMP:-}" && -d "$NOTARY_TMP" ]] && rm -rf -- "$NOTARY_TMP"; }
trap cleanup_notary_tmp EXIT
install -d "$NOTARY_TMP/payload"
cp dist/fleet-agent-darwin-amd64 dist/fleet-agent-darwin-arm64 "$NOTARY_TMP/payload/"
/usr/bin/ditto -c -k --keepParent "$NOTARY_TMP/payload" "$NOTARY_TMP/fleet-agent-notary.zip"
echo "submitting signed fleet-agent binaries for Apple notarization (profile=$NOTARY_PROFILE)"
xcrun notarytool submit "$NOTARY_TMP/fleet-agent-notary.zip" \
  --keychain-profile "$NOTARY_PROFILE" --wait --timeout 60m
echo "built + signed + notarized fleet-agent darwin binaries"
