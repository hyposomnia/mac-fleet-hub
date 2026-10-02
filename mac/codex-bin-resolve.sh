#!/usr/bin/env bash
# mac/codex-bin-resolve.sh —— Codex 可执行文件解析的单一事实来源（风险点 R2）。
#
# 背景（2026-09-30 事故）：ChatGPT.app 自动更新把内部布局从
#   Contents/Resources/codex           （旧）
# 改成
#   Contents/Resources/codex-cli/bin/codex  + codex-cli/codex-package.json（新）
# 任何写死的路径都会在 App 更新后静默失效：shared app-server 启动即退出、
# launchd KeepAlive 无限重启、端口永不监听；而 GUI 域里被注入的
# CODEX_APP_SERVER_WS_URL 又会让 ChatGPT 自己启动失败（ECONNREFUSED）。
#
# 本脚本把「App 内部布局」的知识收敛到一处：优先读 App 自己的布局自述文件
# （codex-cli/codex-package.json 的 layoutVersion/entrypoint），再退回已知候选，
# 每一项都校验「真实存在 + 可执行 + 软链不悬空」。shell / mjs / Go 都调用它。
#
# 用法：
#   source mac/codex-bin-resolve.sh
#   if line="$(fleet_resolve_codex_bin)"; then
#     bin="${line%%$'\t'*}"; source="${line##*$'\t'}"
#   else
#     fleet_codex_bin_explain_failure >&2
#   fi
#
#   bash mac/codex-bin-resolve.sh              # 打印解析到的路径
#   bash mac/codex-bin-resolve.sh --json       # {"path":...,"source":...,"version":...}
#   bash mac/codex-bin-resolve.sh --list       # 按序打印候选与可用性（排查用）
#   bash mac/codex-bin-resolve.sh --explain    # 成功/失败说明 + 候选清单
#   bash mac/codex-bin-resolve.sh --keeper-node # ChatGPT 自带签名 node（keeper 用）
#
# 注意：解析函数把结果写成 "<真实路径>\t<来源>"。调用方若用命令替换（子 shell）
# 取值，必须按这个契约解析——不要依赖函数内设置的全局变量。
#
# 可覆盖的 env：
#   FLEET_CODEX_BIN                 显式指定的 codex（有效则最高优先；失效则继续自愈）
#   FLEET_CODEX_DESKTOP_APP_PATH    目标 App 路径（默认 /Applications/ChatGPT.app）
#   FLEET_CODEX_HOME                Codex home（默认 ~/.codex），用于 managed standalone 候选
#   FLEET_CODEX_KEEPER_NODE         显式指定的 keeper node

# 说明：本文件会被 source，刻意不设置 set -e/-u，避免污染调用方。

fleet_codex_app_path() {
  printf '%s\n' "${FLEET_CODEX_DESKTOP_APP_PATH:-/Applications/ChatGPT.app}"
}

fleet_codex_home_path() {
  printf '%s\n' "${FLEET_CODEX_HOME:-${HOME:-}/.codex}"
}

# fleet_codex_realpath <path>：跟随软链（最多 40 跳）后打印真实路径。
# 悬空软链 / 不存在的路径一律失败。
fleet_codex_realpath() {
  local target="$1" hops=0 dir base
  [[ -n "$target" ]] || return 1
  while [[ -L "$target" ]]; do
    hops=$((hops + 1))
    (( hops <= 40 )) || return 1
    dir="$(cd "$(dirname "$target")" >/dev/null 2>&1 && pwd -P)" || return 1
    base="$(readlink "$target")" || return 1
    case "$base" in
      /*) target="$base" ;;
      *) target="$dir/$base" ;;
    esac
  done
  [[ -e "$target" ]] || return 1
  dir="$(cd "$(dirname "$target")" >/dev/null 2>&1 && pwd -P)" || return 1
  printf '%s/%s\n' "${dir%/}" "$(basename "$target")"
}

# fleet_codex_bin_usable <path>：可执行 + 软链可解析 → 打印真实路径，否则失败。
fleet_codex_bin_usable() {
  local candidate="$1" real
  [[ -n "$candidate" && -x "$candidate" ]] || return 1
  real="$(fleet_codex_realpath "$candidate")" || return 1
  [[ -f "$real" && -x "$real" ]] || return 1
  printf '%s\n' "$real"
}

# fleet_codex_manifest_entrypoint <app>：读 App 自带布局自述文件的 entrypoint。
# 只接受非空相对路径（不得以 / 开头、不得含 ..）；读不到返回 1（属正常回退路径）。
fleet_codex_manifest_entrypoint() {
  local app="$1" manifest entry
  manifest="$app/Contents/Resources/codex-cli/codex-package.json"
  [[ -f "$manifest" ]] || return 1
  entry="$(/usr/bin/plutil -extract entrypoint raw -o - "$manifest" 2>/dev/null)" || return 1
  [[ -n "$entry" ]] || return 1
  case "$entry" in
    /*|*..*) return 1 ;;
  esac
  printf '%s\n' "$entry"
}

# fleet_codex_bin_candidate_list：按优先级打印 "<source>\t<path>"。
# 顺序即「App 自述 → 新版固定位置 → 旧版固定位置 → Fleet 托管版本 → PATH」。
fleet_codex_bin_candidate_list() {
  local app configured entry path_bin
  app="$(fleet_codex_app_path)"
  configured="${FLEET_CODEX_BIN:-}"
  if [[ -n "$configured" ]]; then
    printf 'configured\t%s\n' "$configured"
  fi
  if entry="$(fleet_codex_manifest_entrypoint "$app")"; then
    printf 'chatgpt-layout-manifest\t%s\n' "$app/Contents/Resources/codex-cli/$entry"
  fi
  printf 'chatgpt-codex-cli\t%s\n' "$app/Contents/Resources/codex-cli/bin/codex"
  printf 'chatgpt-legacy\t%s\n' "$app/Contents/Resources/codex"
  printf 'managed-standalone\t%s\n' "$(fleet_codex_home_path)/packages/standalone/current/codex"
  path_bin="$(command -v codex 2>/dev/null || true)"
  if [[ -n "$path_bin" ]]; then
    printf 'path\t%s\n' "$path_bin"
  fi
}

# fleet_resolve_codex_bin：按候选顺序解析，成功打印 "<真实路径>\t<来源>"，失败返回 1。
fleet_resolve_codex_bin() {
  local source candidate real
  while IFS=$'\t' read -r source candidate; do
    [[ -n "$candidate" ]] || continue
    if real="$(fleet_codex_bin_usable "$candidate")"; then
      printf '%s\t%s\n' "$real" "$source"
      return 0
    fi
  done < <(fleet_codex_bin_candidate_list)
  return 1
}

# fleet_codex_bin_explain_failure：失败时可直接使用的多行诊断（同一 shell 内完成，
# 因此能看到完整候选清单）。解析成功则打印一行成功说明并返回 0。
fleet_codex_bin_explain_failure() {
  local line source candidate
  if line="$(fleet_resolve_codex_bin)"; then
    printf '解析成功：%s（来源 %s）\n' "${line%%$'\t'*}" "${line##*$'\t'}"
    return 0
  fi
  printf '无法解析 Codex 可执行文件（App: %s）。\n' "$(fleet_codex_app_path)"
  printf '已尝试的候选：\n'
  while IFS=$'\t' read -r source candidate; do
    [[ -n "$candidate" ]] || continue
    printf '  - %s = %s\n' "$source" "$candidate"
  done < <(fleet_codex_bin_candidate_list)
  printf '常见原因：ChatGPT.app 更新后内部布局变化，或 App 未安装 / 被移动 / 不完整。\n'
  printf '修复：确认 App 完整后重跑安装（bash mac/setup-mac.sh），或显式指定 FLEET_CODEX_BIN=<codex 路径>。\n'
  return 1
}

# fleet_resolve_keeper_node：解析 ChatGPT 自带的签名 node（keeper 用）。
# 只认 App 自带的签名 node；找不到就失败（不允许静默退回系统 node）。
fleet_resolve_keeper_node() {
  local app candidate real
  app="$(fleet_codex_app_path)"
  for candidate in "${FLEET_CODEX_KEEPER_NODE:-}" "$app/Contents/Resources/cua_node/bin/node"; do
    [[ -n "$candidate" ]] || continue
    if real="$(fleet_codex_bin_usable "$candidate")"; then
      printf '%s\n' "$real"
      return 0
    fi
  done
  # 布局兜底：Resources 下任意一层 */bin/node（App 自带、由 App 签名覆盖）。
  while IFS= read -r candidate; do
    [[ -n "$candidate" ]] || continue
    if real="$(fleet_codex_bin_usable "$candidate")"; then
      printf '%s\n' "$real"
      return 0
    fi
  done < <(/usr/bin/find "$app/Contents/Resources" -maxdepth 3 -type f -name node 2>/dev/null | sort)
  return 1
}

fleet_codex_bin_version() {
  local bin="$1"
  [[ -x "$bin" ]] || return 1
  "$bin" --version 2>/dev/null | awk 'NR == 1 { print $NF }'
}

# ---------------- CLI ----------------
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  set -uo pipefail
  case "${1:-}" in
    --keeper-node)
      node_bin="$(fleet_resolve_keeper_node)" || {
        echo "无法解析 ChatGPT 自带 node（App: $(fleet_codex_app_path)）" >&2
        exit 1
      }
      printf '%s\n' "$node_bin"
      ;;
    --list)
      while IFS=$'\t' read -r source candidate; do
        if real="$(fleet_codex_bin_usable "$candidate")"; then
          printf '可用   %-24s %s -> %s\n' "$source" "$candidate" "$real"
        else
          printf '不可用 %-24s %s\n' "$source" "$candidate"
        fi
      done < <(fleet_codex_bin_candidate_list)
      ;;
    --json)
      if line="$(fleet_resolve_codex_bin)"; then
        bin="${line%%$'\t'*}"
        source="${line##*$'\t'}"
        version="$(fleet_codex_bin_version "$bin" || true)"
        printf '{"path":"%s","source":"%s","version":"%s"}\n' "$bin" "$source" "$version"
      else
        printf '{"path":"","source":"","version":"","error":"unresolved"}\n'
        exit 1
      fi
      ;;
    --explain)
      fleet_codex_bin_explain_failure || exit 1
      ;;
    ""|--print)
      if line="$(fleet_resolve_codex_bin)"; then
        printf '%s\n' "${line%%$'\t'*}"
      else
        fleet_codex_bin_explain_failure >&2
        exit 1
      fi
      ;;
    *)
      echo "用法: $0 [--print|--json|--list|--explain|--keeper-node]" >&2
      exit 64
      ;;
  esac
fi
