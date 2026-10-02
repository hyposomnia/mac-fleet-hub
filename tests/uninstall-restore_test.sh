#!/usr/bin/env bash
# R1/R3 回归测试：卸载必须按安装清单精确清理，并按快照还原 GUI 域环境变量
# （launchctl bootout 不会清除 launchctl setenv 写入域环境的变量——本次事故最容易漏的一步）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNINSTALL="$ROOT/mac/uninstall.sh"

fail() {
  echo "uninstall-restore test failed: $*" >&2
  exit 1
}

tmp="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/macfleet-uninstall.XXXXXX")" && pwd -P)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"

# 假 launchctl：维护一个 KEY=VALUE 状态文件，getenv/unsetenv/setenv/print 都基于它。
cat > "$tmp/bin/launchctl" <<'EOF'
#!/bin/bash
store="${FAKE_CTL_STORE:-/dev/null}"
echo "launchctl $*" >> "${FAKE_CTL_LOG:-/dev/null}"
cmd="${1:-}"; shift || true
case "$cmd" in
  print)
    target="${1:-}"
    case "$target" in
      gui/*/*) exit 1 ;;  # 服务：视为未加载
      *)
        echo "environment = {"
        [[ -f "$store" ]] && while IFS='=' read -r k v; do [[ -n "$k" ]] && echo "        $k => $v"; done < "$store"
        echo "}"
        exit 0
        ;;
    esac
    ;;
  getenv) [[ -f "$store" ]] && sed -n "s/^$1=//p" "$store" | head -1 ;;
  unsetenv)
    if [[ -f "$store" ]]; then grep -v "^$1=" "$store" > "$store.tmp" || true; mv "$store.tmp" "$store"; fi
    ;;
  setenv) printf '%s=%s\n' "$1" "$2" >> "$store" ;;
  bootout|unload|bootstrap|kickstart) exit 0 ;;
esac
exit 0
EOF
chmod +x "$tmp/bin/launchctl"

write_manifest() { # <home> <ws-was-set: true|false> <ws-value>
  local home="$1" was_set="$2" value="$3"
  mkdir -p "$home/Library/Application Support/macfleet/state"
  cat > "$home/Library/Application Support/macfleet/manifest.json" <<EOF
{
  "schema": 1,
  "macIndex": "2",
  "guiEnvPrevious": {
    "CODEX_APP_SERVER_WS_URL": {"wasSet": ${was_set}, "value": "${value}"},
    "CODEX_APP_SERVER_USE_LOCAL_DAEMON": {"wasSet": false, "value": ""}
  },
  "paths": ["${home}/.local/bin/fleet-agent", "${home}/Library/LaunchAgents/com.macfleet.fleet-agent.plist"],
  "dirs": ["${home}/.macfleet"],
  "backupGlobs": ["${home}/.local/bin/fleet-agent.bak*"],
  "labels": ["com.macfleet.fleet-agent", "com.macfleet.codex-desktop-env"]
}
EOF
}

run_uninstall() { # <home>
  local home="$1"
  HOME="$home" PATH="$tmp/bin:$PATH" \
    FAKE_CTL_STORE="$tmp/ctl.store" FAKE_CTL_LOG="$tmp/ctl.log" \
    FLEET_LEGACY_TMP_DIR="$tmp/legacy-tmp" \
    bash "$UNINSTALL" --keep-tailscale
}

# ---------------- 场景 1：安装前未设置 → 卸载后必须清空（本次事故现场） ----------------
home1="$tmp/home1"
mkdir -p "$home1/.local/bin" "$home1/Library/LaunchAgents" "$home1/.macfleet/migration-backups"
: > "$home1/.local/bin/fleet-agent"
: > "$home1/Library/LaunchAgents/com.macfleet.fleet-agent.plist"
: > "$home1/.local/bin/fleet-agent.bak.20260101"
write_manifest "$home1" false ""
# 事故现场：卸载前 GUI 域里残留着指向 shared app-server 的变量
printf 'CODEX_APP_SERVER_WS_URL=ws://127.0.0.1:47682/rpc\n' > "$tmp/ctl.store"
: > "$tmp/ctl.log"

set +e
run_uninstall "$home1" > "$tmp/out1.log" 2>&1
rc1=$?
set -e
[[ "$rc1" -eq 0 ]] || { cat "$tmp/out1.log" >&2; fail "场景 1 卸载退出码应为 0，实际 $rc1"; }
grep -q "launchctl unsetenv CODEX_APP_SERVER_WS_URL" "$tmp/ctl.log" || fail "未清除 CODEX_APP_SERVER_WS_URL"
grep -q "launchctl unsetenv CODEX_APP_SERVER_USE_LOCAL_DAEMON" "$tmp/ctl.log" || fail "未清除 CODEX_APP_SERVER_USE_LOCAL_DAEMON"
grep -q "GUI 域无残留：CODEX_APP_SERVER_WS_URL" "$tmp/out1.log" || fail "缺少 GUI 域自检通过输出"
! grep -q "CODEX_APP_SERVER_WS_URL=" "$tmp/ctl.store" || fail "假 launchctl 域环境里仍残留变量"
grep -q "✓ 卸载自检通过" "$tmp/out1.log" || fail "卸载自检未通过"
[[ ! -e "$home1/.local/bin/fleet-agent" ]] || fail "清单里的二进制未删除"
[[ ! -e "$home1/Library/LaunchAgents/com.macfleet.fleet-agent.plist" ]] || fail "清单里的 plist 未删除"
[[ ! -e "$home1/.macfleet" ]] || fail "清单里的目录未删除"
[[ ! -e "$home1/.local/bin/fleet-agent.bak.20260101" ]] || fail "备份 glob 未清理"
[[ ! -e "$home1/Library/Application Support/macfleet/manifest.json" ]] || fail "卸载后清单应被删除"

# ---------------- 场景 2：安装前就有值 → 必须精确还原（而不是无脑清空） ----------------
home2="$tmp/home2"
mkdir -p "$home2/Library/Application Support/macfleet/state"
write_manifest "$home2" true "ws://127.0.0.1:9999/rpc"
printf 'CODEX_APP_SERVER_WS_URL=ws://127.0.0.1:47682/rpc\n' > "$tmp/ctl.store"
: > "$tmp/ctl.log"

set +e
run_uninstall "$home2" > "$tmp/out2.log" 2>&1
rc2=$?
set -e
[[ "$rc2" -eq 0 ]] || { cat "$tmp/out2.log" >&2; fail "场景 2 卸载退出码应为 0，实际 $rc2"; }
grep -q "launchctl setenv CODEX_APP_SERVER_WS_URL ws://127.0.0.1:9999/rpc" "$tmp/ctl.log" \
  || fail "未按快照还原 CODEX_APP_SERVER_WS_URL"
grep -q "已还原 CODEX_APP_SERVER_WS_URL=ws://127.0.0.1:9999/rpc" "$tmp/out2.log" || fail "缺少还原输出"
grep -q "^CODEX_APP_SERVER_WS_URL=ws://127.0.0.1:9999/rpc$" "$tmp/ctl.store" \
  || fail "还原后的域环境值不正确：$(cat "$tmp/ctl.store")"

# ---------------- 场景 3：幂等（重复卸载不报错、不误判残留） ----------------
set +e
run_uninstall "$home1" > "$tmp/out3.log" 2>&1
rc3=$?
set -e
[[ "$rc3" -eq 0 ]] || { cat "$tmp/out3.log" >&2; fail "重复卸载应幂等成功，实际 $rc3"; }

echo "uninstall-restore tests passed"
