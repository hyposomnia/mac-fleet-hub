#!/usr/bin/env bash
# mac-fleet-hub 卸载（风险点 R1/R3）：可本地运行，也可免 clone：
#   curl -fsSL https://<网关域名:端口>/enroll/uninstall.sh | bash
#
# 设计要点（2026-09-30 事故后重写）：
#   1. 按安装清单（~/Library/Application Support/macfleet/manifest.json）精确清理，
#      没有清单时退回已知路径清单；幂等，重复执行不报错。
#   2. **必须**还原被 fleet 改过的 GUI 域环境变量：launchctl bootout 不会清除
#      `launchctl setenv` 写入域环境的变量，漏掉这一步就会出现「卸载后 ChatGPT
#      仍连 127.0.0.1:47682 启动失败」。
#   3. 最后自检域环境（期望清空即必须 grep -c 为 0），失败以非 0 退出，避免假成功。
#
# 用法：
#   bash mac/uninstall.sh                    # 交互（TTY 下询问是否退出 mesh）
#   bash mac/uninstall.sh --keep-tailscale   # 非交互：保留 Tailscale/Headscale
#   bash mac/uninstall.sh --down-tailscale   # 非交互：退出 mesh 并卸载 tailscaled
#   bash mac/uninstall.sh --purge            # 连同日志与状态目录一起删除
set -uo pipefail

LA="$HOME/Library/LaunchAgents"
BIN_DIR="$HOME/.local/bin"
CODEX_KEEPER_DIR="$HOME/.local/lib/macfleet"
SUPPORT_DIR="$HOME/Library/Application Support/macfleet"
STATE_DIR="$SUPPORT_DIR/state"
LOG_DIR="$HOME/Library/Logs/macfleet"
MANIFEST="$SUPPORT_DIR/manifest.json"
FB_DB="$HOME/.macfleet-filebrowser.db"
LEGACY_TMP_DIR="${FLEET_LEGACY_TMP_DIR:-/tmp}"
PURGE=0
TAILSCALE_ACTION="ask"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --purge) PURGE=1 ;;
    --keep-tailscale) TAILSCALE_ACTION="keep" ;;
    --down-tailscale) TAILSCALE_ACTION="down" ;;
    -h|--help)
      sed -n '2,20p' "$0"
      exit 0
      ;;
    *) echo "未知参数：$1（见 --help）" >&2; exit 64 ;;
  esac
  shift
done

bold() { printf "\033[1m%s\033[0m\n" "$1"; }
info() { printf '  %s\n' "$1"; }
warn() { printf '  ⚠️  %s\n' "$1" >&2; }

STEP_FAILURES=0
REMOVED=0
DOMAIN="gui/$(id -u)"

bold "== mac-fleet-hub 卸载 =="

# ---------------- 1. 停止并卸载 launchd 服务 ----------------
LABELS=(com.macfleet.ttyd com.macfleet.filebrowser com.macfleet.fleet-agent com.macfleet.codex-desktop-env com.macfleet.codex-app-server)
if [[ -f "$MANIFEST" ]]; then
  manifest_labels=()
  while IFS= read -r label; do
    [[ -n "$label" ]] && manifest_labels+=("$label")
  done < <(/usr/bin/plutil -extract labels json -o - "$MANIFEST" 2>/dev/null | tr ',' '\n' | tr -d ' "[]' | sed '/^$/d')
  # plutil 对数组的输出平台间有差异，取不到就沿用默认清单。
  if (( ${#manifest_labels[@]} > 0 )); then
    LABELS=("${manifest_labels[@]}")
  fi
fi
for label in "${LABELS[@]}"; do
  if launchctl print "${DOMAIN}/${label}" >/dev/null 2>&1; then
    if launchctl bootout "${DOMAIN}/${label}" >/dev/null 2>&1; then
      info "已停止服务 ${label}"
    else
      warn "停止服务 ${label} 失败"
      STEP_FAILURES=1
    fi
  fi
done
# 旧式安装：可能只存在于 plist 文件里
for plist in "$LA"/com.macfleet.*.plist; do
  [[ -e "$plist" ]] || continue
  launchctl unload "$plist" >/dev/null 2>&1 || true
done

# ---------------- 2. 还原 GUI 域环境变量（R1：最容易漏的一步） ----------------
bold "== 还原 GUI 域环境变量 =="
GUI_VARS=(CODEX_APP_SERVER_WS_URL CODEX_APP_SERVER_USE_LOCAL_DAEMON)
if [[ -f "$MANIFEST" ]]; then
  info "安装清单：${MANIFEST}（按快照还原）"
else
  info "未找到安装清单，按「安装前未设置」的保守假设处理（全部清除）"
fi
ENV_EXPECT_ABSENT=()
for var in "${GUI_VARS[@]}"; do
  was_set="false"
  value=""
  if [[ -f "$MANIFEST" ]]; then
    was_set="$(/usr/bin/plutil -extract "guiEnvPrevious.${var}.wasSet" raw -o - "$MANIFEST" 2>/dev/null || true)"
    value="$(/usr/bin/plutil -extract "guiEnvPrevious.${var}.value" raw -o - "$MANIFEST" 2>/dev/null || true)"
  fi
  if [[ "$was_set" == "true" && -n "$value" ]]; then
    if launchctl setenv "$var" "$value" >/dev/null 2>&1; then
      info "已还原 ${var}=${value}"
    else
      warn "还原 ${var} 失败（需要在 Aqua 图形会话内执行）"
      STEP_FAILURES=1
    fi
  else
    ENV_EXPECT_ABSENT+=("$var")
    if launchctl unsetenv "$var" >/dev/null 2>&1; then
      info "已清除 ${var}（安装前未设置）"
    else
      warn "清除 ${var} 失败（需要在 Aqua 图形会话内执行：launchctl unsetenv ${var}）"
      STEP_FAILURES=1
    fi
  fi
done

# ---------------- 3. 删除文件与目录（按清单，幂等） ----------------
bold "== 清理文件 =="
remove_path() {
  local path="$1"
  if [[ -e "$path" || -L "$path" ]]; then
    if rm -rf -- "$path" 2>/dev/null; then
      info "已删除 ${path}"
      REMOVED=$((REMOVED + 1))
    else
      warn "删除失败 ${path}"
      STEP_FAILURES=1
    fi
  fi
}
remove_glob() {
  local pattern="$1" match
  shopt -s nullglob
  for match in $pattern; do
    remove_path "$match"
  done
  shopt -u nullglob
}

manifest_list() { # <json key>：把清单里的字符串数组逐行打印
  local key="$1"
  [[ -f "$MANIFEST" ]] || return 0
  /usr/bin/python3 - "$MANIFEST" "$key" <<'PY' 2>/dev/null || true
import json, sys
try:
    data = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    sys.exit(0)
for item in data.get(sys.argv[2], []) or []:
    print(item)
PY
}

while IFS= read -r path; do
  [[ -n "$path" ]] && remove_path "$path"
done < <(manifest_list paths; manifest_list dirs)
while IFS= read -r pattern; do
  [[ -n "$pattern" ]] && remove_glob "$pattern"
done < <(manifest_list backupGlobs)

# 兜底清单（无 manifest / 老版本安装 / 手工装）
remove_glob "$LA/com.macfleet.*.plist"
remove_glob "$LA/com.macfleet.*.bak*"
remove_glob "$LA/com.macfleet.*.backup-*"
remove_path "$BIN_DIR/fleet-agent"
remove_path "$BIN_DIR/fleet-attach"
remove_path "$BIN_DIR/filebrowser"
remove_glob "$BIN_DIR/fleet-agent.bak*"
remove_glob "$BIN_DIR/.fleet-agent-release-*"
remove_glob "$BIN_DIR/fleet-agent.[0-9]*"
remove_path "$CODEX_KEEPER_DIR"
remove_path "$FB_DB"
remove_path "$HOME/.macfleet-proxy.json"
remove_path "$HOME/.macfleet-tmux.conf"
remove_path "$HOME/.macfleet"
if (( PURGE )); then
  remove_path "$STATE_DIR"
  remove_path "$SUPPORT_DIR"
  remove_path "$LOG_DIR"
else
  remove_path "$STATE_DIR"
  remove_path "$MANIFEST"
  if [[ -d "$LOG_DIR" ]]; then
    info "保留日志目录 ${LOG_DIR}（需要一并删除请加 --purge）"
  fi
fi

# 历史遗留：/tmp 下的日志与 socket（新版已迁到稳定路径）
remove_glob "${LEGACY_TMP_DIR%/}/macfleet-*"

# 收回残留的 fleet- tmux 会话
if command -v tmux >/dev/null 2>&1; then
  while IFS= read -r session; do
    [[ -n "$session" ]] || continue
    tmux kill-session -t "$session" 2>/dev/null && info "收回 tmux 会话 ${session}"
  done < <(tmux ls 2>/dev/null | sed -n 's/^\(fleet-[a-z0-9]*\):.*/\1/p')
fi

# ---------------- 4. 自检（R1 验收） ----------------
bold "== 自检 =="
CHECK_FAILED=0
if (( ${#ENV_EXPECT_ABSENT[@]} > 0 )); then
  for var in "${ENV_EXPECT_ABSENT[@]}"; do
    leaked="$(launchctl print "$DOMAIN" 2>/dev/null | grep -c "$var" || true)"
    value="$(launchctl getenv "$var" 2>/dev/null || true)"
    if [[ "$leaked" == "0" && -z "$value" ]]; then
      info "✓ GUI 域无残留：${var}"
    else
      warn "✗ GUI 域仍存在 ${var}（grep -c=${leaked}, value=${value}）"
      warn "  请在图形会话的终端里执行：launchctl unsetenv ${var}"
      CHECK_FAILED=1
    fi
  done
fi
for label in "${LABELS[@]}"; do
  if launchctl print "${DOMAIN}/${label}" >/dev/null 2>&1; then
    warn "✗ 服务仍被加载：${label}"
    CHECK_FAILED=1
  fi
done
if [[ -e "$HOME/.macfleet" || -e "$BIN_DIR/fleet-agent" || -e "$LA/com.macfleet.fleet-agent.plist" ]]; then
  warn "✗ 仍有 macfleet 残留文件"
  CHECK_FAILED=1
fi
info "已删除 ${REMOVED} 项"
(( CHECK_FAILED == 0 )) && info "✓ 卸载自检通过"

# ---------------- 5. 网络（mesh） ----------------
bold "== Tailscale / mesh =="
ts_answer="no"
case "$TAILSCALE_ACTION" in
  down) ts_answer="yes" ;;
  keep) ts_answer="no" ;;
  ask)
    if [[ -t 0 ]]; then
      read -r -p "是否退出 Headscale 网络并卸载 tailscaled?（需要 sudo）[y/N] > " ts_answer || ts_answer="no"
    else
      info "非交互环境：默认保留 Tailscale（需要退出请用 --down-tailscale）"
    fi
    ;;
esac
if [[ "$ts_answer" =~ ^[Yy]$ ]]; then
  TS="$(command -v tailscale || echo /opt/homebrew/bin/tailscale)"
  sudo "$TS" down 2>/dev/null || true
  sudo "$TS" logout 2>/dev/null || true
  sudo "${TS}d" uninstall-system-daemon 2>/dev/null || sudo tailscaled uninstall-system-daemon 2>/dev/null || true
  info "已退出网络并卸载 tailscaled"
else
  info "保留 tailscale（彻底移除：sudo tailscale down && sudo tailscaled uninstall-system-daemon）"
fi

bold "✓ 卸载流程结束"
cat <<'EOF'
说明：
  - Homebrew 装的 ttyd/tmux/filebrowser 未删除：brew uninstall ttyd tmux filebrowser
  - 「ChatGPT Desktop Fixer」是第三方工具产物，fleet 不碰：
    ~/Library/Application Support/ChatGPT Desktop Fixer/
  - 若要重新纳管本机，重跑安装即可（bash mac/setup-mac.sh 或网关 bootstrap）。
EOF

if (( CHECK_FAILED != 0 || STEP_FAILURES != 0 )); then
  echo "结果：卸载未完全成功，请按上面的 ⚠️ 处理。" >&2
  exit 1
fi
exit 0
