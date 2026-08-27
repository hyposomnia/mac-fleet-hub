#!/usr/bin/env bash
# 在唯一签名构建机上发布 fleet-agent：测试 → 签名/公证 → commit/push → 网关 → 全 Fleet。
set -euo pipefail

# Remote SSH sessions on the signing Mac do not necessarily source Homebrew's
# shell initialization. Use deterministic tool paths for verify/build/release.
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="${FLEET_RELEASE_CONFIG:-$HOME/.config/mac-fleet-hub/release.env}"
EXPECTED_BRANCH="${FLEET_RELEASE_BRANCH:-master}"
MODE="release"
case "${1:-}" in
  "") ;;
  --check) MODE="check" ;;
  -h|--help)
    cat <<'EOF'
用法：
  bash scripts/release-fleet-agent.sh --check  # 只检查签名机、配置、SSH 与当前服务
  bash scripts/release-fleet-agent.sh          # 完整发布

完整发布：pull --ff-only → verify → build/sign/notarize → commit/push → gateway → Macs。
EOF
    exit 0
    ;;
  *) echo "未知参数：$1（用 --help 查看）" >&2; exit 2 ;;
esac
DIST_DIR="$ROOT/mac/fleet-agent/dist"
ARM_ASSET="$DIST_DIR/fleet-agent-darwin-arm64"
AMD_ASSET="$DIST_DIR/fleet-agent-darwin-amd64"

die() { echo "✗ $*" >&2; exit 1; }
step() { echo; echo "==> $*"; }
ssh_note() { echo ">>> ssh $1 — $2"; }
ssh_retry() { # port target description command
  local port="$1" target="$2" description="$3" command="$4" attempt
  for attempt in 1 2 3; do
    ssh_note "$target:$port" "$description (attempt $attempt/3)"
    if ssh -o BatchMode=yes -o ConnectTimeout=8 -p "$port" "$target" "$command"; then
      return 0
    fi
  done
  die "SSH 连续三次失败：$target:$port"
}

[[ "$(uname -s)" == "Darwin" ]] || die "发布必须在持有 Developer ID 私钥的 macOS 构建机运行。"
[[ -r "$CONFIG_FILE" ]] || die "缺少私有配置：$CONFIG_FILE（参考 scripts/release-fleet-agent.env.example）。"
# shellcheck disable=SC1090
source "$CONFIG_FILE"

: "${FLEET_RELEASE_BUILDER_IP:?配置 FLEET_RELEASE_BUILDER_IP}"
: "${FLEET_RELEASE_WEB_BASE:?配置 FLEET_RELEASE_WEB_BASE}"
: "${FLEET_RELEASE_GATEWAY_SSH:?配置 FLEET_RELEASE_GATEWAY_SSH}"
: "${FLEET_RELEASE_GATEWAY_PORT:?配置 FLEET_RELEASE_GATEWAY_PORT}"
: "${FLEET_RELEASE_MAC_TARGETS:?配置 FLEET_RELEASE_MAC_TARGETS}"

command -v git >/dev/null || die "未找到 git。"
TAILSCALE_BIN="${FLEET_RELEASE_TAILSCALE_BIN:-$(command -v tailscale 2>/dev/null || true)}"
if [[ -z "$TAILSCALE_BIN" ]]; then
  for candidate in /opt/homebrew/bin/tailscale /usr/local/bin/tailscale /Applications/Tailscale.app/Contents/MacOS/Tailscale; do
    if [[ -x "$candidate" ]]; then TAILSCALE_BIN="$candidate"; break; fi
  done
fi
[[ -x "$TAILSCALE_BIN" ]] || die "未找到 tailscale。"
command -v ssh >/dev/null || die "未找到 ssh。"
command -v scp >/dev/null || die "未找到 scp。"
[[ "$("$TAILSCALE_BIN" ip -4 2>/dev/null | head -n1)" == "$FLEET_RELEASE_BUILDER_IP" ]] \
  || die "当前机器不是签名构建机 $FLEET_RELEASE_BUILDER_IP。"

if [[ "$MODE" == "check" ]]; then
  step "检查签名构建机与 Developer ID"
  security find-identity -v -p codesigning | grep 'Developer ID Application:' \
    || die "钥匙串中没有有效 Developer ID Application identity。"
  step "检查网关 SSH 与服务"
  ssh_retry "$FLEET_RELEASE_GATEWAY_PORT" "$FLEET_RELEASE_GATEWAY_SSH" \
    "hostname + service status" \
    'hostname; systemctl is-active fleet-enroll nginx headscale authelia'
  step "检查所有 Mac SSH 与 fleet-agent health"
  for target in $FLEET_RELEASE_MAC_TARGETS; do
    ssh_retry 22 "$target" "hostname + PID + mesh health" \
      'set -e; ip=$(/opt/homebrew/bin/tailscale ip -4 | head -n1); hostname; launchctl print gui/$(id -u)/com.macfleet.fleet-agent | awk "/pid =/{print \"pid=\" \$3; exit}"; printf "health="; curl -fsS --max-time 3 http://$ip:7682/api/health; echo'
  done
  echo "✅ 发布环境检查通过"
  exit 0
fi

cd "$ROOT"
[[ "$(git branch --show-current)" == "$EXPECTED_BRANCH" ]] || die "只能从 $EXPECTED_BRANCH 分支发布。"
[[ -z "$(git status --porcelain)" ]] || die "工作树不干净；先提交或处理现有改动。"

step "拉取并快进到 origin/$EXPECTED_BRANCH"
echo ">>> git pull --ff-only origin $EXPECTED_BRANCH"
GIT_SSH_COMMAND="ssh -o BatchMode=yes" git pull --ff-only origin "$EXPECTED_BRANCH"
[[ "$(git rev-parse HEAD)" == "$(git rev-parse origin/$EXPECTED_BRANCH)" ]] \
  || die "pull 后本地 HEAD 仍与 origin/$EXPECTED_BRANCH 不一致。"

step "运行项目验证"
bash "$ROOT/scripts/verify.sh"

step "构建、Developer ID 签名并等待 Apple 公证 Accepted"
bash "$ROOT/mac/fleet-agent/build.sh"
codesign --verify --deep --strict "$AMD_ASSET"
codesign --verify --deep --strict "$ARM_ASSET"

step "提交并推送不可变发布产物"
git add "$AMD_ASSET" "$ARM_ASSET"
if git diff --cached --quiet; then
  echo "产物没有变化，无需新提交。"
else
  version="$($ARM_ASSET version)"
  git commit -m "build(agent): publish $version"
  git push origin "$EXPECTED_BRANCH"
fi
release_commit="$(git rev-parse HEAD)"
release_short="$(git rev-parse --short HEAD)"
arm_sha="$(shasum -a 256 "$ARM_ASSET" | awk '{print $1}')"
amd_sha="$(shasum -a 256 "$AMD_ASSET" | awk '{print $1}')"

step "生成提交 $release_short 的客户端包"
release_tmp="$(mktemp -d)"
cleanup_release_tmp() { [[ -n "${release_tmp:-}" && -d "$release_tmp" ]] && rm -rf -- "$release_tmp"; }
trap cleanup_release_tmp EXIT
git archive "$release_commit" mac | gzip -9 > "$release_tmp/mac-bundle.tar.gz"
bundle_sha="$(shasum -a 256 "$release_tmp/mac-bundle.tar.gz" | awk '{print $1}')"
cp "$AMD_ASSET" "$ARM_ASSET" "$release_tmp/"

remote_prefix="fleet-agent-release-$release_short"
ssh_retry "$FLEET_RELEASE_GATEWAY_PORT" "$FLEET_RELEASE_GATEWAY_SSH" \
  "创建远端暂存目录" "install -d -m 0700 /tmp/$remote_prefix-input"
echo ">>> scp signed assets → $FLEET_RELEASE_GATEWAY_SSH:/tmp/$remote_prefix-input/"
scp -o BatchMode=yes -o ConnectTimeout=8 -P "$FLEET_RELEASE_GATEWAY_PORT" \
  "$release_tmp/mac-bundle.tar.gz" "$AMD_ASSET" "$ARM_ASSET" \
  "$FLEET_RELEASE_GATEWAY_SSH:/tmp/$remote_prefix-input/"

step "备份并更新网关分发源"
ssh_retry "$FLEET_RELEASE_GATEWAY_PORT" "$FLEET_RELEASE_GATEWAY_SSH" "备份、替换、核 SHA、检查服务" \
  "set -e
   input=/tmp/$remote_prefix-input
   stamp=\$(date +%Y%m%d%H%M%S)
   sudo cp -p /var/www/fleet-enroll/mac-bundle.tar.gz /var/www/fleet-enroll/mac-bundle.tar.gz.bak.\$stamp
   for arch in amd64 arm64; do
     sudo cp -p /var/www/fleet-enroll/dist/fleet-agent-darwin-\$arch /var/www/fleet-enroll/dist/fleet-agent-darwin-\$arch.bak.\$stamp
     sudo install -m 0644 \$input/fleet-agent-darwin-\$arch /var/www/fleet-enroll/dist/fleet-agent-darwin-\$arch
   done
   sudo install -m 0644 \$input/mac-bundle.tar.gz /var/www/fleet-enroll/mac-bundle.tar.gz
   sudo chown www-data:www-data /var/www/fleet-enroll/mac-bundle.tar.gz /var/www/fleet-enroll/dist/fleet-agent-darwin-*
   test \"\$(sha256sum /var/www/fleet-enroll/dist/fleet-agent-darwin-arm64 | awk '{print \$1}')\" = '$arm_sha'
   test \"\$(sha256sum /var/www/fleet-enroll/dist/fleet-agent-darwin-amd64 | awk '{print \$1}')\" = '$amd_sha'
   systemctl is-active --quiet fleet-enroll nginx headscale authelia
   echo gateway_ok backup=\$stamp"

step "从公网下载并核对发布 SHA"
curl -fsSL "${FLEET_RELEASE_WEB_BASE%/}/enroll/dist/fleet-agent-darwin-arm64" \
  -o "$release_tmp/public-arm64"
[[ "$(shasum -a 256 "$release_tmp/public-arm64" | awk '{print $1}')" == "$arm_sha" ]] \
  || die "公网分发包 SHA 不一致。"
codesign --verify --deep --strict "$release_tmp/public-arm64"
curl -fsSL "${FLEET_RELEASE_WEB_BASE%/}/enroll/mac-bundle.tar.gz" \
  -o "$release_tmp/public-mac-bundle.tar.gz"
[[ "$(shasum -a 256 "$release_tmp/public-mac-bundle.tar.gz" | awk '{print $1}')" == "$bundle_sha" ]] \
  || die "公网客户端 bundle SHA 不一致。"

step "逐台备份、更新、迁移 shared WebSocket 并完成 Desktop/Fleet UAT"
for target in $FLEET_RELEASE_MAC_TARGETS; do
  ssh_retry 22 "$target" "空闲守卫、更新、shared 迁移、Desktop/Fleet 同 PID UAT" \
    "set -e
     export PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
     bin=\"\$HOME/.local/bin/fleet-agent\"
     stamp=\$(date +%Y%m%d%H%M%S)
     work=\$(mktemp -d /tmp/macfleet-release-$release_short.XXXXXX)
     trap 'rm -rf \"\$work\"' EXIT
     curl -fsSL '${FLEET_RELEASE_WEB_BASE%/}/enroll/mac-bundle.tar.gz' -o \"\$work/mac-bundle.tar.gz\"
     test \"\$(shasum -a 256 \"\$work/mac-bundle.tar.gz\" | awk '{print \$1}')\" = '$bundle_sha'
     tar -xzf \"\$work/mac-bundle.tar.gz\" -C \"\$work\"
     FLEET_CODEX_HOME=\"\$(/usr/bin/plutil -extract EnvironmentVariables.FLEET_CODEX_HOME raw -o - \"\$HOME/Library/LaunchAgents/com.macfleet.fleet-agent.plist\" 2>/dev/null || printf '%s' \"\$HOME/.codex\")\" \
       bash \"\$work/mac/check-codex-idle.sh\"
     sleep 2
     FLEET_CODEX_HOME=\"\$(/usr/bin/plutil -extract EnvironmentVariables.FLEET_CODEX_HOME raw -o - \"\$HOME/Library/LaunchAgents/com.macfleet.fleet-agent.plist\" 2>/dev/null || printf '%s' \"\$HOME/.codex\")\" \
       bash \"\$work/mac/check-codex-idle.sh\"
     oldpid=\$(launchctl print gui/\$(id -u)/com.macfleet.fleet-agent | awk '/pid =/{print \$3; exit}')
     cp -p \"\$bin\" \"\$bin.bak.\$stamp\"
     FLEET_UPDATE_BASE='${FLEET_RELEASE_WEB_BASE%/}/enroll/dist' \"\$bin\" update
     FLEET_UPDATE_BASE='${FLEET_RELEASE_WEB_BASE%/}/enroll/dist' \
       FLEET_CODEX_APPSERVER_MODE=shared \
       FLEET_CODEX_APPSERVER_SOCK=ws://127.0.0.1:47682/rpc \
       FLEET_CODEX_DESKTOP_SHARED_DAEMON=1 \
       bash \"\$work/mac/migrate-existing-client-to-shared.sh\"
     ip=\$(/opt/homebrew/bin/tailscale ip -4 | head -n1)
     ok=0
     for n in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
       if curl -fsS --max-time 1 http://\$ip:7682/api/health 2>/dev/null | grep -qx ok; then ok=1; break; fi
       sleep 1
     done
     test \"\$ok\" = 1
     newpid=\$(launchctl print gui/\$(id -u)/com.macfleet.fleet-agent | awk '/pid =/{print \$3; exit}')
     test -n \"\$newpid\"
     codesign --verify --deep --strict \"\$bin\"
     test \"\$(shasum -a 256 \"\$bin\" | awk '{print \$1}')\" = '$arm_sha'
     info=\$(curl -fsS --max-time 3 http://\$ip:7682/api/info)
     test \"\$(printf '%s' \"\$info\" | /usr/bin/plutil -extract codexAppServerMode raw -o - -)\" = shared
     test \"\$(printf '%s' \"\$info\" | /usr/bin/plutil -extract codexAppServerConnected raw -o - -)\" = true
     echo node_ok host=\$(hostname) oldpid=\$oldpid newpid=\$newpid binary_backup=\$stamp"
done

echo
echo "✅ fleet-agent 发布完成：commit=$release_short arm64_sha=$arm_sha"
