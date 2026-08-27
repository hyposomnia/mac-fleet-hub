#!/usr/bin/env bash
# 在每台 Mac 上运行：入网 Headscale + 起 ttyd / filebrowser / fleet-agent（仅绑 mesh 内网 IP）。
#
# 用法：
#   MAC_INDEX=1 \
#   LOGIN_SERVER=https://fleet.example.com:8443 AUTHKEY=<preauthkey> \
#   bash mac/setup-mac.sh
#
#   - MAC_INDEX 必填(1/2/3/…)：决定终端/文件路径 /m{idx}/...，且要与网关 .env 的 MAC_IPS 第几个对应一致
#   - LOGIN_SERVER/AUTHKEY 选填：给出则自动 tailscale up 入网（Headscale）；
#     省略则假设你已手动入网。
#   - FLEET_UPDATE_BASE 选填：写进 ~/.zshrc，使 `fleet-agent update` 自更新开箱即用。
#   - 不修改系统「远程登录/屏幕共享」开关（mac↔mac 的 SSH/VNC 请自行在系统设置开启）。
set -euo pipefail

MAC_INDEX="${MAC_INDEX:?请设置 MAC_INDEX=1|2|3}"
TTYD_PORT="${TTYD_PORT:-7681}"
FB_PORT="${FB_PORT:-8080}"
AGENT_PORT="${AGENT_PORT:-7682}"
FB_ROOT="${FB_ROOT:-$HOME}"                       # 文件管理根目录 = 整个 home（用户决定）
FB_DB="$HOME/.macfleet-filebrowser.db"
TTYD_BASE="/m${MAC_INDEX}/term"
FB_BASE="/m${MAC_INDEX}/files"
BIN_DIR="$HOME/.local/bin"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- 0. Homebrew ---
command -v brew >/dev/null 2>&1 || { echo "未找到 Homebrew，请先装：https://brew.sh" >&2; exit 1; }
BREW_PREFIX="$(brew --prefix)"
CLAUDE_BIN="$(command -v claude || echo "$BREW_PREFIX/bin/claude")"
CODEX_HOME_DIR="${FLEET_CODEX_HOME:-$HOME/.codex}"
CODEX_APPSERVER_MODE="${FLEET_CODEX_APPSERVER_MODE:-shared}"
CODEX_APPSERVER_SOCK="${FLEET_CODEX_APPSERVER_SOCK:-}"
CODEX_DESKTOP_SHARED_DAEMON="${FLEET_CODEX_DESKTOP_SHARED_DAEMON:-1}"
case "$CODEX_APPSERVER_MODE" in
  isolated|shared|auto|daemon|stdio) ;;
  *) echo "非法 FLEET_CODEX_APPSERVER_MODE=${CODEX_APPSERVER_MODE}（应为 isolated/shared/auto/daemon/stdio）" >&2; exit 1 ;;
esac
if [[ "$CODEX_APPSERVER_MODE" == "isolated" && -z "$CODEX_APPSERVER_SOCK" ]]; then
  CODEX_APPSERVER_SOCK="$HOME/.macfleet/codex-app-server.sock"
fi
case "$CODEX_DESKTOP_SHARED_DAEMON" in
  0|1) ;;
  *) echo "非法 FLEET_CODEX_DESKTOP_SHARED_DAEMON=${CODEX_DESKTOP_SHARED_DAEMON}（应为 0/1）" >&2; exit 1 ;;
esac

# 显式指定的 Codex 路径始终优先。默认 shared 模式必须优先使用当前
# ChatGPT.app 自带的 Codex，避免新版 Desktop 连接到旧 PATH CLI 刷新的 daemon。
if [[ -n "${FLEET_CODEX_BIN:-}" ]]; then
  CODEX_BIN="$FLEET_CODEX_BIN"
elif [[ -n "${CODEX_BIN:-}" ]]; then
  CODEX_BIN="$CODEX_BIN"
elif [[ ( "$CODEX_APPSERVER_MODE" == "shared" || "$CODEX_APPSERVER_MODE" == "daemon" ) && \
        -x "/Applications/ChatGPT.app/Contents/Resources/codex" ]]; then
  CODEX_BIN="/Applications/ChatGPT.app/Contents/Resources/codex"
elif command -v codex >/dev/null 2>&1; then
  CODEX_BIN="$(command -v codex)"
elif [[ -x "/Applications/ChatGPT.app/Contents/Resources/codex" ]]; then
  CODEX_BIN="/Applications/ChatGPT.app/Contents/Resources/codex"
else
  CODEX_BIN="$BREW_PREFIX/bin/codex"
fi

# --- 1. Tailscale 客户端 + （可选）入网 Headscale ---
TS_BIN="$(command -v tailscale || echo /Applications/Tailscale.app/Contents/MacOS/Tailscale)"
if [[ -n "${LOGIN_SERVER:-}" && -n "${AUTHKEY:-}" ]]; then
  echo "入网 Headscale: $LOGIN_SERVER ..."
  "$TS_BIN" up --login-server="$LOGIN_SERVER" --authkey="$AUTHKEY" --hostname="mac${MAC_INDEX}" --accept-dns=false
fi
TS_IP="$("$TS_BIN" ip -4 2>/dev/null | head -n1 || true)"
[[ -n "${TS_IP}" ]] || { echo "拿不到 Tailscale/Headscale IP，请确认已入网。" >&2; exit 1; }
echo "本机 mesh IP: $TS_IP  (mac${MAC_INDEX})"

# --- 2. 依赖 ---
echo "安装 ttyd tmux ..."
brew install ttyd tmux
[[ -x "$BREW_PREFIX/bin/ttyd" ]] || { echo "ttyd 安装失败：$BREW_PREFIX/bin/ttyd 不存在。" >&2; exit 1; }
[[ -x "$BREW_PREFIX/bin/tmux" ]] || { echo "tmux 安装失败：$BREW_PREFIX/bin/tmux 不存在。" >&2; exit 1; }

# shared（默认）让 Fleet 与 Codex Desktop 连接同一个官方 managed daemon。
# isolated 是兼容回退：Fleet sidecar 独立常驻，只连接专用 Unix WebSocket。
MANAGED_CODEX="$CODEX_HOME_DIR/packages/standalone/current/codex"
MANAGED_STANDALONE_DIR="$(dirname "$(dirname "$MANAGED_CODEX")")"
MANAGED_RELEASES_DIR="$MANAGED_STANDALONE_DIR/releases"
MANAGED_CODEX_CHANGED=0
MANAGED_MIGRATION_ACTIVE=0
SHARED_MIGRATION_COMMITTED=0
MANAGED_CURRENT_KIND="absent"
MANAGED_CURRENT_LINK_TARGET=""
MANAGED_CURRENT_LEGACY_BACKUP=""
MANAGED_CURRENT_TOUCHED=0
MANAGED_REPLACED_RELEASE_PATH=""
MANAGED_REPLACED_RELEASE_BACKUP=""
LA_EARLY="$HOME/Library/LaunchAgents"
OLD_ISOLATED_CODEX_PLIST="$LA_EARLY/com.macfleet.codex-app-server.plist"
OLD_ISOLATED_CODEX_PLIST_PRESENT=0
OLD_ISOLATED_CODEX_PLIST_BACKUP=""
daemon_version_field() {
  local version_json="$1" field="$2"
  printf '%s' "$version_json" | /usr/bin/plutil -extract "$field" raw -o - - 2>/dev/null
}
codex_binary_version() {
  "$1" --version 2>/dev/null | awk 'NR == 1 { print $NF }'
}
standalone_package_field() {
  local package_json="$1" field="$2"
  /usr/bin/plutil -extract "$field" raw -o - "$package_json" 2>/dev/null
}
standalone_release_complete() {
  local release_dir="$1" expected_version="$2" expected_target="$3"
  local package_json="$release_dir/codex-package.json"

  [[ -d "$release_dir" \
     && -L "$release_dir/codex" \
     && "$(readlink "$release_dir/codex" 2>/dev/null)" == "bin/codex" \
     && -x "$release_dir/bin/codex" \
     && -x "$release_dir/bin/codex-code-mode-host" \
     && -x "$release_dir/codex-path/rg" \
     && -x "$release_dir/codex-resources/zsh/bin/zsh" \
     && -f "$package_json" \
     && "$(codex_binary_version "$release_dir/bin/codex" || true)" == "$expected_version" \
     && "$(standalone_package_field "$package_json" layoutVersion || true)" == "1" \
     && "$(standalone_package_field "$package_json" version || true)" == "$expected_version" \
     && "$(standalone_package_field "$package_json" target || true)" == "$expected_target" \
     && "$(standalone_package_field "$package_json" variant || true)" == "codex" \
     && "$(standalone_package_field "$package_json" entrypoint || true)" == "bin/codex" \
     && "$(standalone_package_field "$package_json" resourcesDir || true)" == "codex-resources" \
     && "$(standalone_package_field "$package_json" pathDir || true)" == "codex-path" ]]
}
safe_system_zsh() {
  local candidate perms
  for candidate in /bin/zsh /usr/bin/zsh; do
    [[ -x "$candidate" && "$(/usr/bin/stat -f '%u' "$candidate" 2>/dev/null)" == "0" ]] || continue
    perms="$(/usr/bin/stat -f '%Sp' "$candidate" 2>/dev/null)"
    [[ ${#perms} -ge 9 && "${perms:5:1}" != "w" && "${perms:8:1}" != "w" ]] || continue
    printf '%s\n' "$candidate"
    return 0
  done
  return 1
}
resolve_standalone_resources() {
  local selected_version="$1" release_name="$2" selected_dir selected_real selected_parent inferred_root
  local candidate candidate_version

  STAGED_CODE_MODE_HOST_SOURCE=""
  STAGED_RG_SOURCE=""
  STAGED_ZSH_SOURCE=""
  selected_dir="$(cd "$(dirname "$CODEX_BIN")" 2>/dev/null && pwd -P)" || return 1

  # ChatGPT.app bundles the matching helpers next to Resources/codex.
  [[ -x "$selected_dir/codex-code-mode-host" ]] && STAGED_CODE_MODE_HOST_SOURCE="$selected_dir/codex-code-mode-host"
  [[ -x "$selected_dir/rg" ]] && STAGED_RG_SOURCE="$selected_dir/rg"

  selected_real="$CODEX_BIN"
  if command -v realpath >/dev/null 2>&1; then
    selected_real="$(realpath "$CODEX_BIN" 2>/dev/null || printf '%s' "$CODEX_BIN")"
  fi
  selected_parent="$(cd "$(dirname "$selected_real")" 2>/dev/null && pwd -P)" || selected_parent=""
  inferred_root=""
  if [[ "$(basename "$selected_parent")" == "bin" ]]; then
    inferred_root="$(dirname "$selected_parent")"
  elif [[ "$(basename "$selected_real")" == "codex" ]]; then
    inferred_root="$selected_parent"
  fi

  # Exact-version standalone packages are valid sources for version-coupled helpers.
  for candidate in "$MANAGED_RELEASES_DIR/$release_name" "$inferred_root" "$MANAGED_STANDALONE_DIR/current" "$MANAGED_RELEASES_DIR"/*; do
    [[ -n "$candidate" && -d "$candidate" ]] || continue
    candidate_version="$(codex_binary_version "$candidate/codex" 2>/dev/null || true)"
    [[ "$candidate_version" == "$selected_version" ]] || continue
    [[ -n "$STAGED_CODE_MODE_HOST_SOURCE" || ! -x "$candidate/bin/codex-code-mode-host" ]] || STAGED_CODE_MODE_HOST_SOURCE="$candidate/bin/codex-code-mode-host"
    [[ -n "$STAGED_RG_SOURCE" || ! -x "$candidate/codex-path/rg" ]] || STAGED_RG_SOURCE="$candidate/codex-path/rg"
    [[ -n "$STAGED_ZSH_SOURCE" || ! -x "$candidate/codex-resources/zsh/bin/zsh" ]] || STAGED_ZSH_SOURCE="$candidate/codex-resources/zsh/bin/zsh"
  done

  # rg/zsh are not protocol-coupled. Reuse a complete existing package before falling
  # back to the protected system zsh; code-mode-host must still match this Codex build.
  for candidate in "$MANAGED_STANDALONE_DIR/current" "$MANAGED_RELEASES_DIR"/*; do
    [[ -d "$candidate" ]] || continue
    [[ -n "$STAGED_RG_SOURCE" || ! -x "$candidate/codex-path/rg" ]] || STAGED_RG_SOURCE="$candidate/codex-path/rg"
    [[ -n "$STAGED_ZSH_SOURCE" || ! -x "$candidate/codex-resources/zsh/bin/zsh" ]] || STAGED_ZSH_SOURCE="$candidate/codex-resources/zsh/bin/zsh"
  done
  if [[ -z "$STAGED_ZSH_SOURCE" ]]; then
    STAGED_ZSH_SOURCE="$(safe_system_zsh)" || return 1
  fi

  [[ -x "$STAGED_CODE_MODE_HOST_SOURCE" && -x "$STAGED_RG_SOURCE" && -x "$STAGED_ZSH_SOURCE" ]]
}
begin_shared_migration() {
  local current_path="$MANAGED_STANDALONE_DIR/current" backup_dir

  MANAGED_MIGRATION_ACTIVE=1
  MANAGED_CURRENT_KIND="absent"
  MANAGED_CURRENT_LINK_TARGET=""
  MANAGED_CURRENT_LEGACY_BACKUP=""
  MANAGED_CURRENT_TOUCHED=0
  MANAGED_REPLACED_RELEASE_PATH=""
  MANAGED_REPLACED_RELEASE_BACKUP=""
  OLD_ISOLATED_CODEX_PLIST_PRESENT=0
  OLD_ISOLATED_CODEX_PLIST_BACKUP=""
  trap 'rollback_shared_migration >/dev/null 2>&1 || true' EXIT

  if [[ -L "$current_path" ]]; then
    MANAGED_CURRENT_KIND="symlink"
    MANAGED_CURRENT_LINK_TARGET="$(readlink "$current_path")" || return 1
  elif [[ -d "$current_path" ]]; then
    MANAGED_CURRENT_KIND="directory"
  elif [[ -e "$current_path" ]]; then
    echo "无法迁移：$current_path 既不是 symlink 也不是目录。" >&2
    return 1
  fi

  if [[ -e "$OLD_ISOLATED_CODEX_PLIST" || -L "$OLD_ISOLATED_CODEX_PLIST" ]]; then
    OLD_ISOLATED_CODEX_PLIST_PRESENT=1
    backup_dir="$HOME/.macfleet/migration-backups/isolated-to-shared.$(date +%Y%m%d%H%M%S).$$"
    install -d -m 0700 "$backup_dir" || return 1
    OLD_ISOLATED_CODEX_PLIST_BACKUP="$backup_dir/com.macfleet.codex-app-server.plist"
    echo "迁移到 shared daemon：先停止旧 Fleet isolated sidecar，并把 plist 备份到 ${OLD_ISOLATED_CODEX_PLIST_BACKUP}。"
    echo "若旧 sidecar 有正在运行的 Codex turn，该 turn 会被中断。"
    launchctl unload "$OLD_ISOLATED_CODEX_PLIST" 2>/dev/null || true
    mv "$OLD_ISOLATED_CODEX_PLIST" "$OLD_ISOLATED_CODEX_PLIST_BACKUP" || return 1
  fi
}
quarantine_path() {
  local path="$1" label="$2" quarantined
  [[ -e "$path" || -L "$path" ]] || return 0
  quarantined="${path}.${label}.$(date +%Y%m%d%H%M%S).$$.${RANDOM}"
  mv "$path" "$quarantined"
}
rollback_shared_migration() {
  local current_path="$MANAGED_STANDALONE_DIR/current" restore_link rollback_ok=0 step_ok=0
  [[ "$MANAGED_MIGRATION_ACTIVE" == "1" ]] || return 0
  MANAGED_MIGRATION_ACTIVE=0
  trap - EXIT
  echo "shared daemon 迁移失败，正在恢复原 Codex current 与 isolated sidecar ..." >&2

  # If an incomplete canonical release was replaced, restore its exact contents before
  # restoring a current symlink that may point at it.
  if [[ -n "$MANAGED_REPLACED_RELEASE_BACKUP" && ( -e "$MANAGED_REPLACED_RELEASE_BACKUP" || -L "$MANAGED_REPLACED_RELEASE_BACKUP" ) ]]; then
    step_ok=0
    quarantine_path "$MANAGED_REPLACED_RELEASE_PATH" "failed-shared-release" || step_ok=1
    if [[ "$step_ok" == "0" ]]; then
      mv "$MANAGED_REPLACED_RELEASE_BACKUP" "$MANAGED_REPLACED_RELEASE_PATH" || step_ok=1
    fi
    [[ "$step_ok" == "0" ]] || rollback_ok=1
  fi

  if [[ "$MANAGED_CURRENT_TOUCHED" == "1" ]]; then
    step_ok=0
    case "$MANAGED_CURRENT_KIND" in
      symlink)
        restore_link="$MANAGED_STANDALONE_DIR/.current.restore.$$.${RANDOM}"
        ln -s "$MANAGED_CURRENT_LINK_TARGET" "$restore_link" || step_ok=1
        if [[ "$step_ok" == "0" ]]; then
          if [[ -e "$current_path" && ! -L "$current_path" ]]; then
            quarantine_path "$current_path" "failed-shared-current" || step_ok=1
          fi
          [[ "$step_ok" != "0" ]] || mv -fh "$restore_link" "$current_path" || step_ok=1
        fi
        ;;
      directory)
        quarantine_path "$current_path" "failed-shared-current" || step_ok=1
        if [[ "$step_ok" == "0" && -n "$MANAGED_CURRENT_LEGACY_BACKUP" ]]; then
          mv "$MANAGED_CURRENT_LEGACY_BACKUP" "$current_path" || step_ok=1
        fi
        ;;
      absent)
        quarantine_path "$current_path" "failed-shared-current" || step_ok=1
        ;;
    esac
    [[ "$step_ok" == "0" ]] || rollback_ok=1
  fi

  if [[ "$OLD_ISOLATED_CODEX_PLIST_PRESENT" == "1" ]]; then
    step_ok=0
    install -d -m 0700 "$LA_EARLY" || step_ok=1
    if [[ -n "$OLD_ISOLATED_CODEX_PLIST_BACKUP" && ( -e "$OLD_ISOLATED_CODEX_PLIST_BACKUP" || -L "$OLD_ISOLATED_CODEX_PLIST_BACKUP" ) ]]; then
      quarantine_path "$OLD_ISOLATED_CODEX_PLIST" "failed-shared-plist" || step_ok=1
      [[ "$step_ok" != "0" ]] || mv "$OLD_ISOLATED_CODEX_PLIST_BACKUP" "$OLD_ISOLATED_CODEX_PLIST" || step_ok=1
    fi
    if [[ -e "$OLD_ISOLATED_CODEX_PLIST" || -L "$OLD_ISOLATED_CODEX_PLIST" ]]; then
      launchctl load "$OLD_ISOLATED_CODEX_PLIST" >/dev/null 2>&1 || step_ok=1
    else
      step_ok=1
    fi
    [[ "$step_ok" == "0" ]] || rollback_ok=1
  fi

  if [[ "$rollback_ok" == "0" ]]; then
    echo "已恢复迁移前的 Codex current 与 isolated sidecar。" >&2
    return 0
  fi
  echo "警告：自动回滚未完整成功，请检查 ${MANAGED_STANDALONE_DIR} 与 ${LA_EARLY}。" >&2
  return 1
}
commit_shared_migration() {
  MANAGED_MIGRATION_ACTIVE=0
  SHARED_MIGRATION_COMMITTED=1
  trap - EXIT
  if [[ -n "$OLD_ISOLATED_CODEX_PLIST_BACKUP" ]]; then
    echo "旧 isolated plist 的可恢复备份保留于：$OLD_ISOLATED_CODEX_PLIST_BACKUP"
  fi
  if [[ -n "$MANAGED_CURRENT_LEGACY_BACKUP" ]]; then
    echo "旧式 Codex current 目录的可恢复备份保留于：$MANAGED_CURRENT_LEGACY_BACKUP"
  fi
}
refresh_managed_codex_binary() {
  local selected_version managed_version standalone_dir releases_dir target_triple release_name release_dir
  local staged_release current_path current_link release_version replaced_release_backup

  MANAGED_CODEX_CHANGED=0
  selected_version="$(codex_binary_version "$CODEX_BIN")" || return 1
  [[ "$selected_version" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]*$ ]] || return 1
  standalone_dir="$MANAGED_STANDALONE_DIR"
  releases_dir="$MANAGED_RELEASES_DIR"
  case "$(uname -m)" in
    arm64) target_triple="aarch64-apple-darwin" ;;
    x86_64) target_triple="x86_64-apple-darwin" ;;
    *) target_triple="$(uname -m)-apple-darwin" ;;
  esac
  release_version="${selected_version//\//_}"
  release_name="${release_version}-${target_triple}"
  release_dir="$releases_dir/$release_name"
  managed_version=""
  if [[ -x "$MANAGED_CODEX" ]]; then
    managed_version="$(codex_binary_version "$MANAGED_CODEX")" || managed_version=""
  fi
  if [[ "$selected_version" == "$managed_version" ]] \
     && standalone_release_complete "$MANAGED_STANDALONE_DIR/current" "$selected_version" "$target_triple"; then
    echo "Codex managed binary 已是所选版本: $selected_version"
    return 0
  fi

  # 现代 Codex 的 current 是 release 目录 symlink，current/codex 又是 bin/codex
  # symlink。不能覆盖 current/codex，否则会破坏官方包布局。始终构造一个独立
  # release，再原子切换 current；旧式真实 current 目录先做可恢复重命名。
  install -d -m 0700 "$releases_dir"

  if standalone_release_complete "$release_dir" "$selected_version" "$target_triple"; then
    echo "复用已安装的 Fleet Codex release：$release_name"
  else
    resolve_standalone_resources "$selected_version" "$release_name" || {
      echo "无法为 Codex $selected_version 找到匹配的 codex-code-mode-host / rg / zsh 资源。" >&2
      return 1
    }
    staged_release="$(mktemp -d "$releases_dir/.macfleet-release.XXXXXX")"
    if ! install -d -m 0755 "$staged_release/bin" "$staged_release/codex-path" "$staged_release/codex-resources/zsh/bin" \
       || ! install -m 0755 "$CODEX_BIN" "$staged_release/bin/codex" \
       || ! install -m 0755 "$STAGED_CODE_MODE_HOST_SOURCE" "$staged_release/bin/codex-code-mode-host" \
       || ! install -m 0755 "$STAGED_RG_SOURCE" "$staged_release/codex-path/rg" \
       || ! install -m 0755 "$STAGED_ZSH_SOURCE" "$staged_release/codex-resources/zsh/bin/zsh" \
       || ! ln -s "bin/codex" "$staged_release/codex" \
       || ! printf '{\n  "layoutVersion": 1,\n  "version": "%s",\n  "target": "%s",\n  "variant": "codex",\n  "entrypoint": "bin/codex",\n  "resourcesDir": "codex-resources",\n  "pathDir": "codex-path"\n}\n' \
            "$selected_version" "$target_triple" > "$staged_release/codex-package.json" \
       || ! chmod 0644 "$staged_release/codex-package.json" \
       || ! standalone_release_complete "$staged_release" "$selected_version" "$target_triple"; then
      rm -rf "$staged_release"
      return 1
    fi
    replaced_release_backup=""
    if [[ -e "$release_dir" || -L "$release_dir" ]]; then
      replaced_release_backup="${release_dir}.incomplete-backup.$(date +%Y%m%d%H%M%S).$$"
      echo "同版本 Codex release 不完整，将可恢复地重建；旧目录保留为 $replaced_release_backup"
      mv "$release_dir" "$replaced_release_backup" || { rm -rf "$staged_release"; return 1; }
      MANAGED_REPLACED_RELEASE_PATH="$release_dir"
      MANAGED_REPLACED_RELEASE_BACKUP="$replaced_release_backup"
    fi
    if ! mv "$staged_release" "$release_dir"; then
      if [[ -n "$replaced_release_backup" && ! -e "$release_dir" && ! -L "$release_dir" ]]; then
        mv "$replaced_release_backup" "$release_dir" || true
        MANAGED_REPLACED_RELEASE_PATH=""
        MANAGED_REPLACED_RELEASE_BACKUP=""
      fi
      rm -rf "$staged_release"
      return 1
    fi
  fi

  current_path="$standalone_dir/current"
  current_link="$standalone_dir/.current.macfleet.$$.${RANDOM}"
  ln -s "$release_dir" "$current_link" || return 1
  if [[ ( -e "$current_path" || -L "$current_path" ) && ! -L "$current_path" ]]; then
    MANAGED_CURRENT_LEGACY_BACKUP="${current_path}.legacy-backup.$(date +%Y%m%d%H%M%S).$$"
    echo "检测到旧式 Codex current 目录，将可恢复地保留为 $MANAGED_CURRENT_LEGACY_BACKUP"
    if ! mv "$current_path" "$MANAGED_CURRENT_LEGACY_BACKUP"; then
      rm -f "$current_link"
      return 1
    fi
    MANAGED_CURRENT_TOUCHED=1
  fi
  if ! mv -fh "$current_link" "$current_path"; then
    rm -f "$current_link"
    return 1
  fi
  MANAGED_CURRENT_TOUCHED=1

  MANAGED_CODEX_CHANGED=1
  echo "Codex managed binary 已更新：${managed_version:-未安装} → $selected_version"
}
bootstrap_shared_codex_daemon() {
  local version_json managed_version app_server_version refreshed_json refreshed_app_server_version

  refresh_managed_codex_binary || return 1
  if [[ "$MANAGED_CODEX_CHANGED" == "1" ]]; then
    echo "注意：下一步官方 daemon bootstrap 将重启 Codex managed daemon；正在运行的 turn 需先结束。"
  fi
  CODEX_HOME="$CODEX_HOME_DIR" "$CODEX_BIN" app-server daemon bootstrap --remote-control >/dev/null 2>&1 || return 1
  version_json="$(CODEX_HOME="$CODEX_HOME_DIR" "$CODEX_BIN" app-server daemon version 2>/dev/null)" || return 1
  managed_version="$(daemon_version_field "$version_json" managedCodexVersion)" || return 1
  app_server_version="$(daemon_version_field "$version_json" appServerVersion)" || return 1
  [[ -n "$managed_version" && -n "$app_server_version" ]] || return 1

  if [[ "$managed_version" != "$app_server_version" ]]; then
    echo "检测到 Codex managed daemon 版本不一致（managed=${managed_version}, running=${app_server_version}），将重启 daemon ..."
    CODEX_HOME="$CODEX_HOME_DIR" "$CODEX_BIN" app-server daemon restart >/dev/null 2>&1 || return 1
    refreshed_json="$(CODEX_HOME="$CODEX_HOME_DIR" "$CODEX_BIN" app-server daemon version 2>/dev/null)" || return 1
    managed_version="$(daemon_version_field "$refreshed_json" managedCodexVersion)" || return 1
    refreshed_app_server_version="$(daemon_version_field "$refreshed_json" appServerVersion)" || return 1
    [[ -n "$managed_version" && "$managed_version" == "$refreshed_app_server_version" ]] || return 1
    echo "Codex managed daemon 已刷新到 $managed_version"
  else
    echo "Codex managed daemon 版本已一致（${managed_version}），无需重启"
  fi
}

if [[ "$CODEX_APPSERVER_MODE" != "stdio" && -x "$CODEX_BIN" ]]; then
  echo "配置 Codex app-server daemon ..."
  if [[ "$CODEX_APPSERVER_MODE" == "isolated" && ! -x "$MANAGED_CODEX" ]]; then
    # daemon 固定从 managed 路径启动。目标机已有 Codex CLI 时复用该签名二进制，
    # 后续 bootstrap updater 仍可按 Codex 官方安装器正常更新此路径。
    install -d -m 0700 "$(dirname "$MANAGED_CODEX")"
    install -m 0755 "$CODEX_BIN" "$MANAGED_CODEX"
  fi
  if [[ "$CODEX_APPSERVER_MODE" == "isolated" ]]; then
    install -d -m 0700 "$(dirname "$CODEX_APPSERVER_SOCK")"
    echo "Fleet 独立 Codex app-server 将使用 $CODEX_APPSERVER_SOCK"
  elif begin_shared_migration && bootstrap_shared_codex_daemon; then
    commit_shared_migration
    echo "Codex app-server daemon 已就绪"
  elif [[ "$CODEX_APPSERVER_MODE" == "shared" || "$CODEX_APPSERVER_MODE" == "daemon" ]]; then
    rollback_shared_migration || true
    echo "Codex app-server daemon 刷新或版本校验失败；shared 模式无法继续，请更新 Codex/ChatGPT 后重试。" >&2
    exit 1
  elif [[ -S "${CODEX_APPSERVER_SOCK:-$CODEX_HOME_DIR/app-server-control/app-server-control.sock}" ]]; then
    rollback_shared_migration || true
    echo "检测到现有 Codex control socket；fleet-agent 将先直接连接并验证，失败才回退 stdio。"
  else
    rollback_shared_migration || true
    echo "警告：Codex daemon 不可用，fleet-agent 将回退独立 stdio；更新 agent 仍可能中断活动 turn。" >&2
  fi
elif [[ "$CODEX_APPSERVER_MODE" == "shared" || "$CODEX_APPSERVER_MODE" == "daemon" ]]; then
  echo "Codex 可执行文件不可用：${CODEX_BIN}；shared 模式无法配置 managed daemon。" >&2
  exit 1
fi

# shared 默认让 Codex Desktop 与 Fleet 复用默认 Unix socket。这里只更新后续
# GUI 进程继承的 launchd 环境；脚本不会擅自退出或重启正在运行的 Codex.app。
DEFAULT_CODEX_HOME="$HOME/.codex"
DEFAULT_CODEX_SOCK="$DEFAULT_CODEX_HOME/app-server-control/app-server-control.sock"
CODEX_DESKTOP_REOPEN_REQUIRED=0
if [[ "$CODEX_DESKTOP_SHARED_DAEMON" == "1" && ( "$CODEX_APPSERVER_MODE" == "shared" || "$CODEX_APPSERVER_MODE" == "daemon" ) && \
      "$CODEX_HOME_DIR" == "$DEFAULT_CODEX_HOME" && \
      ( -z "$CODEX_APPSERVER_SOCK" || "$CODEX_APPSERVER_SOCK" == "$DEFAULT_CODEX_SOCK" ) ]]; then
  if launchctl setenv CODEX_APP_SERVER_USE_LOCAL_DAEMON 1; then
    CODEX_DESKTOP_REOPEN_REQUIRED=1
    echo "Codex.app 已配置为在下次启动时复用 managed daemon。"
    echo "注意：本脚本不会自动重启 Codex.app；安装完成后必须完全退出（Cmd+Q）再重新打开一次。"
  else
    echo "警告：无法写入 Codex.app GUI 环境；fleet-agent 启动后会重试。" >&2
  fi
else
  launchctl unsetenv CODEX_APP_SERVER_USE_LOCAL_DAEMON 2>/dev/null || true
fi

# --- 3. 安装 fleet-agent / filebrowser 二进制 + ttyd 附着脚本 ---
mkdir -p "$BIN_DIR"
ARCH="$(uname -m)"; [[ "$ARCH" == "arm64" ]] && AB="arm64" || AB="amd64"
install -m 0755 "$SCRIPT_DIR/fleet-agent/dist/fleet-agent-darwin-${AB}" "$BIN_DIR/fleet-agent"
install -m 0755 "$SCRIPT_DIR/fleet-agent/fleet-attach.sh" "$BIN_DIR/fleet-attach"
AGENT_BIN="$BIN_DIR/fleet-agent"
FLEET_ATTACH="$BIN_DIR/fleet-attach"

# filebrowser：装官方 release 二进制（Homebrew 的 bottle 缺内嵌前端，/files 会空白）。
# 优先级：FILEBROWSER_BIN 指定本地二进制 > 官方 release 下载(校验 sha256) > brew 兜底。
FB_BIN="$BIN_DIR/filebrowser"
FB_VER="${FB_VER:-v2.63.16}"                       # 想换版本：导出 FB_VER 覆盖
fb_brew_fallback() {
  echo "⚠️ $1，回退 brew（若 /files 空白请用 FILEBROWSER_BIN 指定官方二进制）" >&2
  brew install filebrowser 2>/dev/null || true; FB_BIN="$BREW_PREFIX/bin/filebrowser"
}
if [[ -n "${FILEBROWSER_BIN:-}" && -f "${FILEBROWSER_BIN}" ]]; then
  install -m 0755 "${FILEBROWSER_BIN}" "$FB_BIN"
  xattr -dr com.apple.quarantine "$FB_BIN" 2>/dev/null || true
elif command -v curl >/dev/null 2>&1; then
  echo "下载 filebrowser ${FB_VER}（官方 release，darwin-${AB}）..."
  FB_TMP="$(mktemp -d)"; FB_TGZ="darwin-${AB}-filebrowser.tar.gz"
  FB_REL="https://github.com/filebrowser/filebrowser/releases/download/${FB_VER}"
  if curl -fsSL "$FB_REL/$FB_TGZ" -o "$FB_TMP/$FB_TGZ" \
     && curl -fsSL "$FB_REL/filebrowser_${FB_VER#v}_checksums.txt" -o "$FB_TMP/sums.txt"; then
    WANT="$(grep " ${FB_TGZ}\$" "$FB_TMP/sums.txt" | awk '{print $1}')"
    GOT="$(shasum -a 256 "$FB_TMP/$FB_TGZ" | awk '{print $1}')"
    if [[ -n "$WANT" && "$WANT" == "$GOT" ]]; then
      tar -xzf "$FB_TMP/$FB_TGZ" -C "$FB_TMP" filebrowser
      install -m 0755 "$FB_TMP/filebrowser" "$FB_BIN"
      xattr -dr com.apple.quarantine "$FB_BIN" 2>/dev/null || true
    else
      fb_brew_fallback "filebrowser sha256 校验失败 (want=${WANT:-?} got=$GOT)"
    fi
  else
    fb_brew_fallback "filebrowser 下载失败"
  fi
  rm -rf "$FB_TMP"
else
  fb_brew_fallback "无 curl 可用"
fi
[[ -x "$FB_BIN" ]] || { echo "filebrowser 安装失败：$FB_BIN 不可执行。" >&2; exit 1; }

# --- 4. filebrowser DB：建用户 + noauth + baseURL（鉴权交给 Headscale ACL）---
# 重跑场景：先卸载已在运行的服务，否则 filebrowser config set 会因 DB 被占而超时。
for svc in com.macfleet.ttyd com.macfleet.filebrowser com.macfleet.fleet-agent; do
  launchctl unload "$LA_EARLY/$svc.plist" 2>/dev/null || true
done
if [[ "$CODEX_APPSERVER_MODE" == "isolated" ]]; then
  launchctl unload "$OLD_ISOLATED_CODEX_PLIST" 2>/dev/null || true
elif [[ "$SHARED_MIGRATION_COMMITTED" == "1" ]]; then
  # 防御性幂等清理：即使安装过程中旧 plist 被外部流程重新写入，也不能留到下次登录。
  launchctl unload "$OLD_ISOLATED_CODEX_PLIST" 2>/dev/null || true
  rm -f "$OLD_ISOLATED_CODEX_PLIST"
fi
if [[ ! -f "$FB_DB" ]]; then
  "$FB_BIN" -d "$FB_DB" config init >/dev/null
fi
# noauth 需要一个已存在的用户来自动登录（否则 /api/login 500）；密码随机、不用于登录
"$FB_BIN" -d "$FB_DB" users add admin "$(openssl rand -base64 12)" --perm.admin >/dev/null 2>&1 || true
"$FB_BIN" -d "$FB_DB" config set --auth.method=noauth --baseURL "$FB_BASE" --root "$FB_ROOT" >/dev/null

# --- 5. 渲染并安装 launchd 服务 ---
LA="$HOME/Library/LaunchAgents"; mkdir -p "$LA"
# fleet-agent daemon 拉空闲回收时长的网关地址：由 FLEET_UPDATE_BASE（.../enroll/dist）推导
# 为 .../enroll/agent-config；未给则留空 → agent 沿用本地 FLEET_IDLE_SEC 默认。
FLEET_CONFIG_URL=""
if [[ -n "${FLEET_UPDATE_BASE:-}" ]]; then
  FLEET_CONFIG_URL="${FLEET_UPDATE_BASE%/}"; FLEET_CONFIG_URL="${FLEET_CONFIG_URL%/dist}/agent-config"
fi
render() { # src dst
  sed -e "s#__BREW_PREFIX__#${BREW_PREFIX}#g" \
      -e "s#__FLEET_CONFIG_URL__#${FLEET_CONFIG_URL}#g" \
      -e "s#__TS_IP__#${TS_IP}#g" \
      -e "s#__PORT__#${PORT:-}#g" \
      -e "s#__ROOT__#${FB_ROOT}#g" \
      -e "s#__DB__#${FB_DB}#g" \
      -e "s#__TTYD_BASE__#${TTYD_BASE}#g" \
      -e "s#__FB_BASE__#${FB_BASE}#g" \
      -e "s#__FB_BIN__#${FB_BIN}#g" \
      -e "s#__FLEET_ATTACH__#${FLEET_ATTACH}#g" \
      -e "s#__AGENT_BIN__#${AGENT_BIN}#g" \
      -e "s#__AGENT_PORT__#${AGENT_PORT}#g" \
      -e "s#__MAC_INDEX__#${MAC_INDEX}#g" \
      -e "s#__CLAUDE_BIN__#${CLAUDE_BIN}#g" \
      -e "s#__CODEX_BIN__#${CODEX_BIN}#g" \
      -e "s#__MANAGED_CODEX_BIN__#${MANAGED_CODEX}#g" \
      -e "s#__CODEX_HOME__#${CODEX_HOME_DIR}#g" \
      -e "s#__CODEX_APPSERVER_MODE__#${CODEX_APPSERVER_MODE}#g" \
      -e "s#__CODEX_APPSERVER_SOCK__#${CODEX_APPSERVER_SOCK}#g" \
      -e "s#__CODEX_DESKTOP_SHARED_DAEMON__#${CODEX_DESKTOP_SHARED_DAEMON}#g" \
      "$1" > "$2"
}
PORT="$TTYD_PORT" render "$SCRIPT_DIR/com.macfleet.ttyd.plist"        "$LA/com.macfleet.ttyd.plist"
PORT="$FB_PORT"   render "$SCRIPT_DIR/com.macfleet.filebrowser.plist" "$LA/com.macfleet.filebrowser.plist"
                  render "$SCRIPT_DIR/com.macfleet.fleet-agent.plist" "$LA/com.macfleet.fleet-agent.plist"
if [[ "$CODEX_APPSERVER_MODE" == "isolated" ]]; then
                  render "$SCRIPT_DIR/com.macfleet.codex-app-server.plist" "$LA/com.macfleet.codex-app-server.plist"
fi

SERVICES=(com.macfleet.ttyd com.macfleet.filebrowser)
if [[ "$CODEX_APPSERVER_MODE" == "isolated" ]]; then SERVICES+=(com.macfleet.codex-app-server); fi
SERVICES+=(com.macfleet.fleet-agent)
for svc in "${SERVICES[@]}"; do
  launchctl unload "$LA/$svc.plist" 2>/dev/null || true
  launchctl load  "$LA/$svc.plist"
  echo "已加载服务: $svc"
done

# fleet-agent 自更新源：写进 ~/.zshrc 受管块，使交互式 `fleet-agent update` 开箱即用
# （update 是手动 CLI，读交互 shell 环境变量，不读 launchd plist）。幂等：先删旧块再追加。
if [[ -n "${FLEET_UPDATE_BASE:-}" ]]; then
  ZRC="$HOME/.zshrc"; MB="# >>> mac-fleet-hub >>>"; ME="# <<< mac-fleet-hub <<<"; touch "$ZRC"
  if grep -qF "$MB" "$ZRC"; then
    tmp="$(mktemp)"; awk -v b="$MB" -v e="$ME" '$0==b{skip=1} !skip{print} $0==e{skip=0}' "$ZRC" > "$tmp" && mv "$tmp" "$ZRC"
  fi
  { echo "$MB"; echo "export FLEET_UPDATE_BASE=\"$FLEET_UPDATE_BASE\""; echo "$ME"; } >> "$ZRC"
  echo "已写入 ~/.zshrc：FLEET_UPDATE_BASE=${FLEET_UPDATE_BASE}（新开终端后 'fleet-agent update' 即可用）"
fi

cat <<EOF

✅ 完成（mac${MAC_INDEX}，仅 mesh 内网可达）：
   网页终端    http://${TS_IP}:${TTYD_PORT}${TTYD_BASE}   (经 fleet-agent 选会话)
   文件管理    http://${TS_IP}:${FB_PORT}${FB_BASE}        (整个 home, noauth)
   会话服务    http://${TS_IP}:${AGENT_PORT}/api/health

下一步（在网关）：把本机 mesh IP ${TS_IP} 按顺序填到 server/.env 的 MAC_IPS（第 ${MAC_INDEX} 台 = 第 ${MAC_INDEX} 个），再跑 setup-server.sh。
提醒：mac↔mac 的 SSH/VNC 需你自行在「系统设置 > 通用 > 共享」开启（本脚本不动这些开关）。
日志：/tmp/macfleet-ttyd.* /tmp/macfleet-filebrowser.* /tmp/macfleet-agent.*
EOF

if [[ "$CODEX_DESKTOP_REOPEN_REQUIRED" == "1" ]]; then
  echo "Codex Desktop：请现在完全退出（Cmd+Q）并重新打开；脚本没有替你重启 App。"
fi
