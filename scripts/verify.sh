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

step "Dashboard JS 测试：server/dashboard (node --test)"
node --test "$ROOT/server/dashboard/chat_model.test.mjs"

step "Shell 工具测试：tests/tailscale-utils_test.sh"
bash "$ROOT/tests/tailscale-utils_test.sh"
echo "tailscale-utils tests passed"

step "Mac shared 安装测试：tests/setup-mac-shared_test.sh"
bash "$ROOT/tests/setup-mac-shared_test.sh"

step "Codex 空闲迁移守卫测试：tests/check-codex-idle_test.sh"
bash "$ROOT/tests/check-codex-idle_test.sh"

printf '\n==> 全部验证通过 ✓\n'
