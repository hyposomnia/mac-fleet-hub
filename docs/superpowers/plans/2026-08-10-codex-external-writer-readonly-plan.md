# Codex 外部 Writer 只读模式实施计划

规格：`docs/superpowers/specs/2026-08-10-codex-external-writer-readonly-design.md`

## 文件结构

- `mac/fleet-agent/chat.go`：扩展 resume 响应与结构化只读错误；新增显式获取 writer 的 HTTP 动作。
- `mac/fleet-agent/codex_chat.go`：识别 active-writer、只读恢复历史、维护每线程访问模式、只读 SSE 与安全重试。
- `mac/fleet-agent/chat_handlers_test.go`：HTTP 契约与状态码测试。
- `mac/fleet-agent/codex_chat_test.go`：backend 红绿测试。
- `server/dashboard/app.js`：消费访问模式、禁用写操作、触发 writer 重试。
- `server/dashboard/index.html`：输入区只读提示和获取控制权按钮。
- `server/dashboard/style.css`：只读状态样式。
- `server/dashboard/chat_model.test.mjs`：DOM、状态和源码契约测试。
- `CHANGELOG.md`：用户可见行为变更。
- `mac/fleet-agent/dist/*`：通过标准参数重建双架构二进制。

## 任务 1：后端 active-writer 降级

1. 在 `codex_chat_test.go` 写失败测试：`thread/resume` 返回 JSON-RPC `-32600` active-writer 时，`Resume` 返回历史和 `read-only/external_writer`，非同类错误仍失败。
2. 运行 `go test ./...`，确认因缺少访问模式字段/分支而失败。
3. 增加单一错误分类 helper、只读 thread 状态和 `ChatResumeResult` 字段。
4. 复用 `listHistory` 与 rollout lifecycle 生成只读初始状态；运行测试转绿。

## 任务 2：只读 SSE 与写保护

1. 写失败测试：只读 thread 建立 Events 时不得再次 resume，rollout 更新仍经现有 connected sync 发布。
2. 写失败测试：Input/Steer/Interrupt/Respond/Settings 在只读 thread 上返回统一 `errThreadReadOnly`。
3. 实现只读 Events 分支和 backend 写入口 guard。
4. 在 HTTP handler 测试中断言 `409 thread_read_only`。

## 任务 3：显式尝试获取 writer

1. 复用现有幂等 `POST /api/chat/resume`，不新增平行端点或 backend 接口。
2. 写失败测试覆盖：只读 thread 再次 Resume 时，仍冲突则返回只读；成功时清除只读标记、补齐历史并返回 `read-write`。
3. 将 Resume 的读写与只读 hydrate 收敛在同一实现中，不复制历史映射逻辑。
4. 本阶段不后台自动抢占；只由用户按钮触发，避免无意义轮询 Desktop writer。

## 任务 4：Dashboard 只读交互

1. 在 `chat_model.test.mjs` 写失败测试，要求存在明确只读栏、获取控制权按钮和 read-only composer guard。
2. Dashboard chat state 增加 `accessMode/accessReason/acquiringWriter`。
3. resume 返回只读时正常渲染历史并启动 SSE，不创建 error card。
4. 输入框、附件、发送、follow-up、停止、审批和设置入口禁用；草稿保持。
5. 点击“尝试获取控制权”再次调用 `/api/chat/resume`：成功恢复读写，冲突则保留只读并提示。

## 任务 5：验证与产物

1. 运行 `gofmt` 和 `git diff --check`。
2. 运行 `bash scripts/verify.sh`，预期 Go、Dashboard、shell 三层全部通过。
3. 用 `GOOS=darwin GOARCH=amd64/arm64 go build -trimpath -ldflags="-s -w"` 重建 `dist`。
4. 再运行 `bash scripts/verify.sh`，核对源码与产物均在 diff 中。
5. 更新 CHANGELOG，聚焦提交并推送；部署前按 runbook 告知服务重启，部署 `100.64.0.3` 并用真实 active-writer 会话验证只读展示和安全重试。
