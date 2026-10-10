#!/usr/bin/env bash
# R2 回归测试：codex 路径解析器必须在 ChatGPT.app 布局变化后仍能解析出可执行文件，
# 并且对「断链软链 / 全部候选失效」明确失败（不再静默写死旧路径）。
set -euo pipefail

# 测试只使用下面的临时 App，避免继承 Desktop/keeper 的真实显式路径。
# 各用例仍可通过 env 单独传入自己的 FLEET_CODEX_BIN。
unset FLEET_CODEX_BIN FLEET_CODEX_KEEPER_NODE

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RESOLVER="$ROOT/mac/codex-bin-resolve.sh"

fail() {
  echo "codex-bin-resolve test failed: $*" >&2
  exit 1
}

tmp="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/macfleet-resolve.XXXXXX")" && pwd -P)"
trap 'rm -rf "$tmp"' EXIT
empty_home="$tmp/empty-codex-home"
mkdir -p "$empty_home"

make_app() { # <app-dir> <layout: manifest|new|legacy|dangling|none>
  local app="$1" layout="$2"
  mkdir -p "$app/Contents/Resources"
  case "$layout" in
    manifest)
      mkdir -p "$app/Contents/Resources/codex-cli/bin"
      printf '#!/bin/bash\necho codex-cli 9.9.9\n' > "$app/Contents/Resources/codex-cli/bin/codex"
      chmod +x "$app/Contents/Resources/codex-cli/bin/codex"
      printf '{"layoutVersion":1,"version":"9.9.9","entrypoint":"bin/codex"}\n' \
        > "$app/Contents/Resources/codex-cli/codex-package.json"
      ;;
    new)
      mkdir -p "$app/Contents/Resources/codex-cli/bin"
      printf '#!/bin/bash\necho codex-cli 9.9.8\n' > "$app/Contents/Resources/codex-cli/bin/codex"
      chmod +x "$app/Contents/Resources/codex-cli/bin/codex"
      ;;
    legacy)
      printf '#!/bin/bash\necho codex 0.0.1\n' > "$app/Contents/Resources/codex"
      chmod +x "$app/Contents/Resources/codex"
      ;;
    dangling)
      mkdir -p "$app/Contents/Resources/codex-cli/bin"
      ln -s "$tmp/does-not-exist" "$app/Contents/Resources/codex-cli/bin/codex"
      ;;
    none) : ;;
  esac
}

resolve_in() { # <app> [extra env assignments...]
  local app="$1"
  shift
  env PATH="/usr/bin:/bin" FLEET_CODEX_DESKTOP_APP_PATH="$app" FLEET_CODEX_HOME="$empty_home" "$@" bash "$RESOLVER" 2>/dev/null
}

resolve_json_in() { # <app> [extra env assignments...]
  local app="$1"
  shift
  env PATH="/usr/bin:/bin" FLEET_CODEX_DESKTOP_APP_PATH="$app" FLEET_CODEX_HOME="$empty_home" "$@" bash "$RESOLVER" --json 2>/dev/null
}

# 1) 新布局 + App 自带布局自述文件（本次事故的真实场景）
app="$tmp/manifest.app"
make_app "$app" manifest
line="$(resolve_in "$app")"
[[ "$line" == "$app/Contents/Resources/codex-cli/bin/codex" ]] \
  || fail "manifest 布局解析错误：$line"
json="$(resolve_json_in "$app")"
[[ "$json" == *'"source":"chatgpt-layout-manifest"'* ]] || fail "manifest 来源标识错误：$json"

# 2) 新布局但没有自述文件 → 仍要命中固定位置
app="$tmp/new.app"
make_app "$app" new
line="$(resolve_in "$app")"
[[ "$line" == "$app/Contents/Resources/codex-cli/bin/codex" ]] \
  || fail "新布局（无 manifest）解析错误：$line"
json="$(resolve_json_in "$app")"
[[ "$json" == *'"source":"chatgpt-codex-cli"'* ]] || fail "新布局来源标识错误：$json"

# 3) 旧布局 → 兼容
app="$tmp/legacy.app"
make_app "$app" legacy
line="$(resolve_in "$app")"
[[ "$line" == "$app/Contents/Resources/codex" ]] || fail "旧布局解析错误：$line"
json="$(resolve_json_in "$app")"
[[ "$json" == *'"source":"chatgpt-legacy"'* ]] || fail "旧布局来源标识错误：$json"

# 4) 写死的旧路径（事故现场）必须自愈到新位置，而不是失败
app="$tmp/stale.app"
make_app "$app" manifest
line="$(resolve_in "$app" FLEET_CODEX_BIN="$app/Contents/Resources/codex")"
[[ "$line" == "$app/Contents/Resources/codex-cli/bin/codex" ]] \
  || fail "失效的显式路径没有自愈：$line"
json="$(resolve_json_in "$app" FLEET_CODEX_BIN="$app/Contents/Resources/codex")"
[[ "$json" == *'"source":"chatgpt-layout-manifest"'* ]] || fail "自愈后的来源标识错误：$json"

# 5) 断链软链不能被当成可用二进制
app="$tmp/dangling.app"
make_app "$app" dangling
if out="$(resolve_in "$app")"; then
  fail "断链软链被误判为可用：$out"
fi

# 6) PATH 上的断链 codex 也要被拒绝（这次事故的第二个同类隐患）
mkdir -p "$tmp/pathbin"
ln -s "$tmp/does-not-exist" "$tmp/pathbin/codex"
app="$tmp/empty.app"
make_app "$app" none
if out="$(env PATH="$tmp/pathbin:/usr/bin:/bin" FLEET_CODEX_DESKTOP_APP_PATH="$app" \
          FLEET_CODEX_HOME="$empty_home" bash "$RESOLVER" 2>/dev/null)"; then
  fail "PATH 上的断链 codex 被误判为可用：$out"
fi

# 7) 全部候选失效 → 明确失败，且诊断里列出候选与修复提示
if out="$(resolve_in "$app" 2>&1)"; then
  fail "空布局不应解析成功：$out"
fi
explain="$(env PATH="/usr/bin:/bin" FLEET_CODEX_DESKTOP_APP_PATH="$app" FLEET_CODEX_HOME="$empty_home" \
  bash "$RESOLVER" --explain 2>&1 || true)"
[[ "$explain" == *"已尝试的候选"* ]] || fail "--explain 未列出候选：$explain"
[[ "$explain" == *"chatgpt-codex-cli"* ]] || fail "--explain 未列出新版候选：$explain"
[[ "$explain" == *"setup-mac.sh"* ]] || fail "--explain 未给出修复提示：$explain"

# 8) --json 契约（供 mjs / Go / doctor 使用）
app="$tmp/json.app"
make_app "$app" manifest
json="$(resolve_json_in "$app")"
[[ "$json" == *'"path":"'"$app"'/Contents/Resources/codex-cli/bin/codex"'* ]] \
  || fail "--json path 字段错误：$json"
[[ "$json" == *'"source":"chatgpt-layout-manifest"'* ]] || fail "--json source 字段错误：$json"
[[ "$json" == *'"version":"9.9.9"'* ]] || fail "--json version 字段错误：$json"

# 9) keeper node：App 自带签名 node 的解析（含布局兜底扫描）
app="$tmp/node.app"
mkdir -p "$app/Contents/Resources/other_layout/bin"
printf '#!/bin/bash\necho v20\n' > "$app/Contents/Resources/other_layout/bin/node"
chmod +x "$app/Contents/Resources/other_layout/bin/node"
node_bin="$(env FLEET_CODEX_DESKTOP_APP_PATH="$app" bash "$RESOLVER" --keeper-node)"
[[ "$node_bin" == "$app/Contents/Resources/other_layout/bin/node" ]] \
  || fail "keeper node 兜底扫描失败：$node_bin"

echo "codex-bin-resolve tests passed"
