#!/usr/bin/env bash
# 在唯一签名构建机上发布 fleet-agent：测试 → 签名/公证 → commit/push → 网关 dist → 各 Mac 自更新。
set -euo pipefail

# Remote SSH sessions on the signing Mac do not necessarily source Homebrew's
# shell initialization. Use deterministic tool paths for verify/build/release.
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="${FLEET_RELEASE_CONFIG:-$HOME/.config/mac-fleet-hub/release.env}"
EXPECTED_BRANCH="${FLEET_RELEASE_BRANCH:-main}"
MODE="release"
case "${1:-}" in
  "") ;;
  --check) MODE="check" ;;
  -h|--help)
    cat <<'EOF'
用法：
  bash scripts/release-fleet-agent.sh --check  # 只检查签名机、配置、SSH 与当前服务
  bash scripts/release-fleet-agent.sh          # 发布签名二进制并逐台自更新

正式发布：pull --ff-only → verify → build/sign/notarize → commit/push → gateway dist → Macs update。
EOF
    exit 0
    ;;
  *) echo "未知参数：$1（用 --help 查看）" >&2; exit 2 ;;
esac
DIST_DIR="$ROOT/mac/fleet-agent/dist"
ARM_ASSET="$DIST_DIR/fleet-agent-darwin-arm64"
AMD_ASSET="$DIST_DIR/fleet-agent-darwin-amd64"

die() { echo "✗ $*" >&2; exit 1; }
# 公证凭据的 keychain profile 名；与 mac/fleet-agent/build.sh 的默认值同源。
NOTARY_PROFILE="${FLEET_NOTARY_PROFILE:-mac-fleet-hub-notary}"
step() { echo; echo "==> $*"; }
ssh_note() { echo ">>> ssh $1 — $2"; }
# 公证凭据预检 —— 必须在"构建 + 签名"之前跑。
#
# 凭据缺失时 build.sh 的失败点在签名之后、公证那一刻，那时 dist/ 里已经躺了两个
# "已签名但未公证"的产物；它们不能被分发，只能人工 git checkout 还原。所以这里
# 先探一次，把失败提前到不产生任何副作用的位置。
#
# 两种失败要分开对待：钥匙串里确实没有该条目 → 明确失败；当前会话访问不到钥匙串
# （例如从 SSH 运行时）→ 无法判定，只告警不阻塞，否则会把正常的发布流程卡死。
require_notary_credentials() {
  local out
  out="$(xcrun notarytool history --keychain-profile "$NOTARY_PROFILE" 2>&1 || true)"
  if grep -q "No Keychain password item found" <<<"$out"; then
    die "钥匙串中没有公证凭据 profile「${NOTARY_PROFILE}」，无法提交 Apple 公证。先在图形会话的终端里执行：
    xcrun notarytool store-credentials $NOTARY_PROFILE --apple-id <Apple ID> --team-id <Team ID> --password <App 专用密码>"
  fi
  if grep -qi "keychainLocked\|User interaction is not allowed" <<<"$out"; then
    [[ "$MODE" == "check" ]] || die "当前会话访问不到公证钥匙串；请在签名机的图形终端执行正式发布。"
    echo "⚠️  当前会话访问不到钥匙串，跳过公证凭据预检；请在图形会话的终端里执行正式发布。"
    return 0
  fi
  echo "公证凭据 profile「${NOTARY_PROFILE}」可用。"
}

ssh_retry() { # port target description command
  local port="$1" target="$2" description="$3" command="$4" attempt status
  for attempt in 1 2 3; do
    ssh_note "$target:$port" "$description (attempt $attempt/3)"
    if ssh -o BatchMode=yes -o ConnectTimeout=8 -p "$port" "$target" "$command"; then
      return 0
    else
      status=$?
      [[ "$status" == 255 ]] || die "远端步骤失败：$target:$port (exit=$status)"
    fi
  done
  die "SSH 连续三次连接失败：$target:$port"
}

[[ "$(uname -s)" == "Darwin" ]] || die "发布必须在持有 Developer ID 私钥的 macOS 构建机运行。"
[[ -r "$CONFIG_FILE" ]] || die "缺少私有配置：${CONFIG_FILE}（参考 scripts/release-fleet-agent.env.example）。"
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
  || die "当前机器不是签名构建机 ${FLEET_RELEASE_BUILDER_IP}。"

# 发布前先确认全 Fleet 空闲，避免签名和网关分发源更新后才在某台 Mac 上停住。
check_script_b64="$(base64 < "$ROOT/mac/check-codex-idle.sh" | tr -d '\n')"
check_target_idle() {
  local target="$1"
  ssh_retry 22 "$target" "发布前 Codex 空闲检查" \
    "set -e
     work=\$(mktemp /tmp/macfleet-idle-check.XXXXXX)
     trap 'rm -f \"\$work\"' EXIT
     printf '%s' '$check_script_b64' | base64 -D > \"\$work\"
     codex_home=\$(/usr/bin/plutil -extract EnvironmentVariables.FLEET_CODEX_HOME raw -o - \"\$HOME/Library/LaunchAgents/com.macfleet.fleet-agent.plist\" 2>/dev/null || printf '%s' \"\$HOME/.codex\")
     FLEET_CODEX_HOME=\"\$codex_home\" bash \"\$work\""
}

if [[ "$MODE" == "check" ]]; then
  step "检查签名构建机与 Developer ID"
  security find-identity -v -p codesigning | grep 'Developer ID Application:' \
    || die "钥匙串中没有有效 Developer ID Application identity。"
  step "检查公证凭据（notarytool keychain profile）"
  require_notary_credentials
  step "检查网关 SSH 与服务"
  ssh_retry "$FLEET_RELEASE_GATEWAY_PORT" "$FLEET_RELEASE_GATEWAY_SSH" \
    "hostname + service status" \
    'hostname; systemctl is-active fleet-enroll nginx headscale authelia'
  step "检查所有 Mac SSH 与 fleet-agent health"
  for target in $FLEET_RELEASE_MAC_TARGETS; do
    ssh_retry 22 "$target" "hostname + PID + mesh health" \
      'set -e; ip=$(/opt/homebrew/bin/tailscale ip -4 | head -n1); hostname; launchctl print gui/$(id -u)/com.macfleet.fleet-agent | awk "/pid =/{print \"pid=\" \$3; exit}"; printf "health="; curl -fsS --max-time 3 http://$ip:7682/api/health; echo'
  done
  step "检查所有 Mac 是否空闲"
  for target in $FLEET_RELEASE_MAC_TARGETS; do check_target_idle "$target"; done
  echo "✅ 发布环境及空闲检查通过"
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
check_script_b64="$(base64 < "$ROOT/mac/check-codex-idle.sh" | tr -d '\n')"

step "运行项目验证"
bash "$ROOT/scripts/verify.sh"

step "发布前确认所有 Mac 空闲"
for target in $FLEET_RELEASE_MAC_TARGETS; do check_target_idle "$target"; done

step "预检公证凭据（失败时不产生任何产物）"
require_notary_credentials

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

step "准备提交 $release_short 的空闲守卫与新安装入口"
release_tmp="$(mktemp -d)"
cleanup_release_tmp() { [[ -n "${release_tmp:-}" && -d "$release_tmp" ]] && rm -rf -- "$release_tmp"; }
trap cleanup_release_tmp EXIT
git show "$release_commit:mac/check-codex-idle.sh" > "$release_tmp/check-codex-idle.sh"
git show "$release_commit:server/enroll/bootstrap.sh" > "$release_tmp/bootstrap.sh"
bootstrap_sha="$(shasum -a 256 "$release_tmp/bootstrap.sh" | awk '{print $1}')"

remote_prefix="fleet-agent-release-$release_short"
ssh_retry "$FLEET_RELEASE_GATEWAY_PORT" "$FLEET_RELEASE_GATEWAY_SSH" \
  "创建远端暂存目录" "install -d -m 0700 /tmp/$remote_prefix-input"
echo ">>> scp signed assets + bootstrap → $FLEET_RELEASE_GATEWAY_SSH:/tmp/$remote_prefix-input/"
scp -o BatchMode=yes -o ConnectTimeout=8 -P "$FLEET_RELEASE_GATEWAY_PORT" \
  "$AMD_ASSET" "$ARM_ASSET" "$release_tmp/bootstrap.sh" \
  "$FLEET_RELEASE_GATEWAY_SSH:/tmp/$remote_prefix-input/"

step "备份并更新网关自更新源及新安装入口"
ssh_retry "$FLEET_RELEASE_GATEWAY_PORT" "$FLEET_RELEASE_GATEWAY_SSH" "备份、替换、核 SHA、检查服务" \
  "set -e
   input=/tmp/$remote_prefix-input
   stamp=\$(date +%Y%m%d%H%M%S)
   sudo cp -p /var/www/fleet-enroll/bootstrap.sh /var/www/fleet-enroll/bootstrap.sh.bak.\$stamp
   for arch in amd64 arm64; do
     sudo cp -p /var/www/fleet-enroll/dist/fleet-agent-darwin-\$arch /var/www/fleet-enroll/dist/fleet-agent-darwin-\$arch.bak.\$stamp
     sudo install -m 0644 \$input/fleet-agent-darwin-\$arch /var/www/fleet-enroll/dist/fleet-agent-darwin-\$arch
   done
   sudo install -m 0644 \$input/bootstrap.sh /var/www/fleet-enroll/bootstrap.sh
   sudo chown www-data:www-data /var/www/fleet-enroll/bootstrap.sh /var/www/fleet-enroll/dist/fleet-agent-darwin-*
   test \"\$(sha256sum /var/www/fleet-enroll/dist/fleet-agent-darwin-arm64 | awk '{print \$1}')\" = '$arm_sha'
   test \"\$(sha256sum /var/www/fleet-enroll/dist/fleet-agent-darwin-amd64 | awk '{print \$1}')\" = '$amd_sha'
   test \"\$(sha256sum /var/www/fleet-enroll/bootstrap.sh | awk '{print \$1}')\" = '$bootstrap_sha'
   systemctl is-active --quiet fleet-enroll nginx headscale authelia
   echo gateway_ok backup=\$stamp"

step "从公网下载并核对发布 SHA"
for arch in arm64 amd64; do
  curl -fsSL "${FLEET_RELEASE_WEB_BASE%/}/enroll/dist/fleet-agent-darwin-$arch" \
    -o "$release_tmp/public-$arch"
  expected_sha="$arm_sha"; [[ "$arch" == amd64 ]] && expected_sha="$amd_sha"
  [[ "$(shasum -a 256 "$release_tmp/public-$arch" | awk '{print $1}')" == "$expected_sha" ]] \
    || die "公网 $arch 产物 SHA 不一致。"
  codesign --verify --deep --strict "$release_tmp/public-$arch"
done
curl -fsSL "${FLEET_RELEASE_WEB_BASE%/}/enroll/bootstrap.sh" -o "$release_tmp/public-bootstrap.sh"
[[ "$(shasum -a 256 "$release_tmp/public-bootstrap.sh" | awk '{print $1}')" == "$bootstrap_sha" ]] \
  || die "公网 bootstrap.sh SHA 不一致。"

step "逐台空闲检查、备份并运行 fleet-agent update"
for target in $FLEET_RELEASE_MAC_TARGETS; do
  ssh_retry 22 "$target" "空闲守卫、自更新、签名与 mesh health" \
    "set -e
     export PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
     bin=\"\$HOME/.local/bin/fleet-agent\"
     stamp=\$(date +%Y%m%d%H%M%S)
     work=\$(mktemp -d /tmp/macfleet-release-$release_short.XXXXXX)
     trap 'rm -rf \"\$work\"' EXIT
     printf '%s' '$check_script_b64' | base64 -D > \"\$work/check-codex-idle.sh\"
     codex_home=\$(/usr/bin/plutil -extract EnvironmentVariables.FLEET_CODEX_HOME raw -o - \"\$HOME/Library/LaunchAgents/com.macfleet.fleet-agent.plist\" 2>/dev/null || printf '%s' \"\$HOME/.codex\")
     FLEET_CODEX_HOME=\"\$codex_home\" bash \"\$work/check-codex-idle.sh\"
     sleep 2
     FLEET_CODEX_HOME=\"\$codex_home\" bash \"\$work/check-codex-idle.sh\"
     oldpid=\$(launchctl print gui/\$(id -u)/com.macfleet.fleet-agent | awk '/pid =/{print \$3; exit}')
     cp -p \"\$bin\" \"\$bin.bak.\$stamp\"
     FLEET_UPDATE_BASE='${FLEET_RELEASE_WEB_BASE%/}/enroll/dist' \"\$bin\" update
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
     case \"\$(uname -m)\" in
       arm64) expected_sha='$arm_sha' ;;
       x86_64) expected_sha='$amd_sha' ;;
       *) echo 'unsupported architecture' >&2; exit 1 ;;
     esac
     test \"\$(shasum -a 256 \"\$bin\" | awk '{print \$1}')\" = \"\$expected_sha\"
     echo node_ok host=\$(hostname) oldpid=\$oldpid newpid=\$newpid binary_backup=\$stamp"
done

echo
echo "✅ fleet-agent 发布完成：commit=$release_short arm64_sha=$arm_sha amd64_sha=$amd_sha"
