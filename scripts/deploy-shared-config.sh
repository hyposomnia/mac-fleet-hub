#!/usr/bin/env bash
# Deploy the shared app-server/client configuration while retaining the already
# signed and notarized fleet-agent assets currently committed in dist/.
set -euo pipefail
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="${FLEET_RELEASE_CONFIG:-$HOME/.config/mac-fleet-hub/release.env}"
EXPECTED_BRANCH="${FLEET_RELEASE_BRANCH:-master}"
die() { echo "✗ $*" >&2; exit 1; }
step() { echo; echo "==> $*"; }
ssh_retry() {
  local port="$1" target="$2" description="$3" command="$4" attempt
  for attempt in 1 2 3; do
    echo ">>> ssh $target:$port — $description (attempt $attempt/3)"
    if ssh -o BatchMode=yes -o ConnectTimeout=8 -p "$port" "$target" "$command"; then return 0; fi
  done
  die "SSH 连续三次失败：$target:$port"
}

[[ "$(uname -s)" == Darwin ]] || die "配置发布必须从签名构建 Mac 运行。"
[[ -r "$CONFIG_FILE" ]] || die "缺少私有配置：$CONFIG_FILE"
# shellcheck disable=SC1090
source "$CONFIG_FILE"
: "${FLEET_RELEASE_BUILDER_IP:?配置 FLEET_RELEASE_BUILDER_IP}"
: "${FLEET_RELEASE_WEB_BASE:?配置 FLEET_RELEASE_WEB_BASE}"
: "${FLEET_RELEASE_GATEWAY_SSH:?配置 FLEET_RELEASE_GATEWAY_SSH}"
: "${FLEET_RELEASE_GATEWAY_PORT:?配置 FLEET_RELEASE_GATEWAY_PORT}"
: "${FLEET_RELEASE_MAC_TARGETS:?配置 FLEET_RELEASE_MAC_TARGETS}"
TARGETS="${FLEET_CONFIG_TARGETS:-$FLEET_RELEASE_MAC_TARGETS}"

TAILSCALE_BIN="$(command -v tailscale 2>/dev/null || true)"
if [[ -z "$TAILSCALE_BIN" ]]; then
  for candidate in /opt/homebrew/bin/tailscale /usr/local/bin/tailscale /Applications/Tailscale.app/Contents/MacOS/Tailscale; do
    if [[ -x "$candidate" ]]; then TAILSCALE_BIN="$candidate"; break; fi
  done
fi
[[ -x "$TAILSCALE_BIN" ]] || die "未找到 tailscale。"
[[ "$("$TAILSCALE_BIN" ip -4 | head -n1)" == "$FLEET_RELEASE_BUILDER_IP" ]] || die "当前机器不是配置的构建机。"

cd "$ROOT"
[[ "$(git branch --show-current)" == "$EXPECTED_BRANCH" ]] || die "只能从 $EXPECTED_BRANCH 发布。"
[[ -z "$(git status --porcelain)" ]] || die "工作树不干净。"
git pull --ff-only origin "$EXPECTED_BRANCH"
bash scripts/verify.sh

ARM_ASSET="$ROOT/mac/fleet-agent/dist/fleet-agent-darwin-arm64"
AMD_ASSET="$ROOT/mac/fleet-agent/dist/fleet-agent-darwin-amd64"
codesign --verify --deep --strict "$ARM_ASSET"
codesign --verify --deep --strict "$AMD_ASSET"
arm_sha="$(shasum -a 256 "$ARM_ASSET" | awk '{print $1}')"

release_commit="$(git rev-parse HEAD)"
release_short="$(git rev-parse --short HEAD)"
release_tmp="$(mktemp -d)"
trap 'rm -rf "$release_tmp"' EXIT
git archive "$release_commit" mac | gzip -9 > "$release_tmp/mac-bundle.tar.gz"
bundle_sha="$(shasum -a 256 "$release_tmp/mac-bundle.tar.gz" | awk '{print $1}')"

step "发布提交 $release_short 的配置 bundle 到网关"
remote_prefix="fleet-shared-config-$release_short"
ssh_retry "$FLEET_RELEASE_GATEWAY_PORT" "$FLEET_RELEASE_GATEWAY_SSH" "创建配置暂存目录" \
  "install -d -m 0700 /tmp/$remote_prefix-input"
scp -q -o BatchMode=yes -o ConnectTimeout=8 -P "$FLEET_RELEASE_GATEWAY_PORT" \
  "$release_tmp/mac-bundle.tar.gz" "$FLEET_RELEASE_GATEWAY_SSH:/tmp/$remote_prefix-input/"
ssh_retry "$FLEET_RELEASE_GATEWAY_PORT" "$FLEET_RELEASE_GATEWAY_SSH" "备份并替换客户端 bundle" \
  "set -e
   input=/tmp/$remote_prefix-input/mac-bundle.tar.gz
   stamp=\$(date +%Y%m%d%H%M%S)
   sudo cp -p /var/www/fleet-enroll/mac-bundle.tar.gz /var/www/fleet-enroll/mac-bundle.tar.gz.bak.\$stamp
   sudo install -m 0644 \$input /var/www/fleet-enroll/mac-bundle.tar.gz
   sudo chown www-data:www-data /var/www/fleet-enroll/mac-bundle.tar.gz
   test \"\$(sha256sum /var/www/fleet-enroll/mac-bundle.tar.gz | awk '{print \$1}')\" = '$bundle_sha'
   systemctl is-active --quiet fleet-enroll nginx
   echo bundle_ok backup=\$stamp"
curl -fsSL "${FLEET_RELEASE_WEB_BASE%/}/enroll/mac-bundle.tar.gz" -o "$release_tmp/public-bundle.tar.gz"
[[ "$(shasum -a 256 "$release_tmp/public-bundle.tar.gz" | awk '{print $1}')" == "$bundle_sha" ]] \
  || die "公网 bundle SHA 不一致。"

step "逐台迁移 shared app-server（保留现有正式 agent 二进制）"
for target in $TARGETS; do
  ssh_retry 22 "$target" "空闲守卫、shared 迁移与同 PID UAT" \
    "set -e
     export PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
     work=\$(mktemp -d /tmp/macfleet-shared-$release_short.XXXXXX)
     trap 'rm -rf \"\$work\"' EXIT
     curl -fsSL '${FLEET_RELEASE_WEB_BASE%/}/enroll/mac-bundle.tar.gz' -o \"\$work/mac-bundle.tar.gz\"
     test \"\$(shasum -a 256 \"\$work/mac-bundle.tar.gz\" | awk '{print \$1}')\" = '$bundle_sha'
     tar -xzf \"\$work/mac-bundle.tar.gz\" -C \"\$work\"
     FLEET_CODEX_HOME=\"\$(/usr/bin/plutil -extract EnvironmentVariables.FLEET_CODEX_HOME raw -o - \"\$HOME/Library/LaunchAgents/com.macfleet.fleet-agent.plist\" 2>/dev/null || printf '%s' \"\$HOME/.codex\")\" bash \"\$work/mac/check-codex-idle.sh\"
     sleep 2
     FLEET_CODEX_HOME=\"\$(/usr/bin/plutil -extract EnvironmentVariables.FLEET_CODEX_HOME raw -o - \"\$HOME/Library/LaunchAgents/com.macfleet.fleet-agent.plist\" 2>/dev/null || printf '%s' \"\$HOME/.codex\")\" bash \"\$work/mac/check-codex-idle.sh\"
     FLEET_UPDATE_BASE='${FLEET_RELEASE_WEB_BASE%/}/enroll/dist' \
       FLEET_CODEX_APPSERVER_MODE=shared \
       FLEET_CODEX_APPSERVER_SOCK=\"\$HOME/.macfleet/codex-app-server.sock\" \
       FLEET_CODEX_DESKTOP_WS_URL=ws://127.0.0.1:47682/rpc \
       FLEET_CODEX_DESKTOP_SHARED_DAEMON=1 \
       bash \"\$work/mac/migrate-existing-client-to-shared.sh\"
     bin=\"\$HOME/.local/bin/fleet-agent\"
     codesign --verify --deep --strict \"\$bin\"
     test \"\$(shasum -a 256 \"\$bin\" | awk '{print \$1}')\" = '$arm_sha'"
done

echo
echo "✅ shared 配置发布完成：commit=$release_short bundle_sha=$bundle_sha targets=$TARGETS"
