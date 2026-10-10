#!/usr/bin/env bash
# 项目自有验证入口：按序执行三个测试层（Go agent / dashboard JS / shell 工具）。
# 提交 / 部署前必须运行本脚本并贴出真实输出（见 AGENTS.md「给 AI 的收尾准则」）。
# 不依赖任何外部 CI 服务，本机 bash + go + node 即可运行。
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
ROOT="$(pwd)"

step() { printf '\n==> %s\n' "$*"; }

command -v go >/dev/null || { echo "缺少 go，请先安装 Go 工具链" >&2; exit 1; }
command -v node >/dev/null || { echo "缺少 node，请先安装 Node.js" >&2; exit 1; }

step "Go 测试：mac/fleet-agent (go test ./...)"
(cd "$ROOT/mac/fleet-agent" && go test ./...)

step "原生组网 IP TLS 验证：保留目标身份及证书拒绝条件"
DESKTOP_TLS_TEST_WORK="$(mktemp -d "${TMPDIR:-/tmp}/fleet-tls-test.XXXXXX")"
trap 'rm -rf "$DESKTOP_TLS_TEST_WORK"' EXIT
DESKTOP_TLS_MODFILE="$(node "$ROOT/scripts/prepare-desktop-tls-module.mjs" "$DESKTOP_TLS_TEST_WORK")"
(cd "$ROOT/mac/fleet-agent" && go test -modfile "$DESKTOP_TLS_MODFILE" -tags fleet_desktop -trimpath -run '^TestDesktopTLS(IP|DERPIP)KeepsVerificationHost$' -count=1 .)

step "Go 测试：server/enroll (go test ./...)"
(cd "$ROOT/server/enroll" && go test ./...)

step "Dashboard JS 测试：server/dashboard (node --test)"
node --test "$ROOT/server/dashboard/fleet_core.test.mjs" "$ROOT/server/dashboard/titanium.test.mjs"
node --test "$ROOT/server/dashboard/chat_model.test.mjs" "$ROOT/server/dashboard/upload_model.test.mjs" "$ROOT/server/dashboard/assistant_gate.test.mjs" "$ROOT/server/dashboard/gateway_down.test.mjs" "$ROOT/server/dashboard/auth.test.mjs" "$ROOT/server/dashboard/auth_integration.test.mjs" "$ROOT/server/dashboard/account_pages.test.mjs" "$ROOT/server/dashboard/theme.test.mjs" "$ROOT/server/dashboard/device_appearance.test.mjs" "$ROOT/server/dashboard/sidebar_layout.test.mjs" "$ROOT/server/dashboard/workspace_tabs.test.mjs" "$ROOT/server/dashboard/compact_composer.test.mjs"

step "候选安装包与独立网关配置测试"
node --test "$ROOT/server/dashboard/settings_dialog.test.mjs" "$ROOT/server/dashboard/auth_effects.test.mjs"
node --test "$ROOT/scripts/client-release-manifest.test.mjs" "$ROOT/scripts/acceptance-network-templates.test.mjs" "$ROOT/scripts/settings-app-package.test.mjs" "$ROOT/scripts/native-client-release.test.mjs"
node --test "$ROOT/scripts/notary-preflight.test.mjs"

if [[ "$(uname -s)" == Darwin ]]; then
  step "原生设置应用：校验 Sparkle SDK 与 Swift 测试"
  bash "$ROOT/scripts/prepare-settings-sdk.sh"
  (cd "$ROOT/mac/settings-app" && swift test)
fi

step "Shell 工具测试：tests/tailscale-utils_test.sh"
bash "$ROOT/tests/tailscale-utils_test.sh"
echo "tailscale-utils tests passed"

step "Mac shared 安装测试：tests/setup-mac-shared_test.sh"
bash "$ROOT/tests/setup-mac-shared_test.sh"

step "逐用户客户端授权：tests/client-authorization_test.sh"
bash "$ROOT/tests/client-authorization_test.sh"

step "Codex 路径解析回归（R2）：tests/codex-bin-resolve_test.sh"
bash "$ROOT/tests/codex-bin-resolve_test.sh"

step "app-server 熔断/退避回归（R4）：tests/codex-keeper-launch_test.sh"
bash "$ROOT/tests/codex-keeper-launch_test.sh"

step "卸载还原 GUI 域环境变量回归（R1/R3）：tests/uninstall-restore_test.sh"
bash "$ROOT/tests/uninstall-restore_test.sh"

step "Codex 空闲迁移守卫测试：tests/check-codex-idle_test.sh"
bash "$ROOT/tests/check-codex-idle_test.sh"

echo "
==> Fleet 自更新安全守卫测试：tests/check-fleet-update-safe_test.sh"
bash "$ROOT/tests/check-fleet-update-safe_test.sh"

step "nginx 站点配置测试：tests/nginx-config_test.sh"
bash "$ROOT/tests/nginx-config_test.sh"

step "统一多用户部署测试：tests/multiuser-server_test.sh"
bash "$ROOT/tests/multiuser-server_test.sh"

step "shell 变量花括号守卫：tests/bash-var-brace_test.sh"
bash "$ROOT/tests/bash-var-brace_test.sh"

step "shared 迁移 agent API 重试：tests/migrate-agent-retry_test.sh"
bash "$ROOT/tests/migrate-agent-retry_test.sh"

printf '\n==> 全部验证通过 ✓\n'
