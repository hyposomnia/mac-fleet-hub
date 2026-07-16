# Codex 自绘会话 UI POC — SDD / TDD Slice

日期：2026-07-16
分支：`codex-desktop-backed-chat-ui`
状态：draft PR 起点；本文件限定第一阶段实现边界。

## 用户目标

在 dashboard 的个人菜单里新增「实验」二级菜单，提供「自绘界面」开关。开启后，**只在 Codex 会话页**把右侧 ttyd 终端区域替换为自绘聊天 UI；关闭后完全回到现有 ttyd 行为。

## 强约束

1. 不与现有会话 UI / ttyd 池实现重叠：新增代码走独立 chat path；现有 `connect()`、iframe pool、mobile ttyd input 不删除、不改语义。
2. 先只实现 Codex；Claude 一律保持现状，必要时显示「Claude 自绘暂未支持，可用终端打开」。
3. app-server 仅由本机 `fleet-agent` 连接；浏览器不直连 Codex app-server，不新增 nginx 暴露面。
4. 开关必须方便调试：默认关闭，存本地浏览器；打开后可随时退回 ttyd。
5. UI 只替换右侧窗口，左侧主机/会话列表、项目分组、权限模式按钮保持当前结构。

## 第一阶段验收切片

Codex Desktop 的对话交互复刻细则见：

- `docs/desktop-backed-agent-chat-ui/codex-desktop-parity-matrix.md`

### 后端（fleet-agent）

- 新增 Codex app-server JSON-RPC client：可初始化、请求/响应匹配、接收 notification。
- 新增稳定 chat event 投影类型，不把 app-server 原始事件直接泄漏给前端。
- 新增 `/api/chat/*` POC 端点（只支持 `assistant=codex`）：
  - `POST /api/chat/resume`
  - `POST /api/chat/input`
  - `GET /api/chat/events`（SSE）
  - `POST /api/chat/interrupt`（可先 no-op/501，但 API 形状固定）
- app-server 不可用时返回结构化错误 `appserver_unavailable`，前端显示终端 fallback。
- 当前本机 Codex 0.142.4 实测：`codex app-server --stdio` 可完成 initialize/thread-list；
  `codex app-server proxy` 连接 daemon 后无回包。POC 先用 stdio 直连本机 app-server，仍不暴露浏览器/nginx。

### 前端（dashboard）

- 个人菜单新增「实验」二级菜单和「自绘界面」开关。
- 开关关闭：现有行为 100% 不变。
- 开关开启 + assistant=codex：右侧显示 `chat-pane`，渲染：
  - 历史/加载态（POC 可先空历史 + 事件流）
  - 用户消息气泡
  - assistant 流式文本
  - 命令执行卡片 / 输出 delta
  - 审批卡片（命令审批优先；文件审批可先显示 unsupported）
  - 错误卡片 +「用终端打开」fallback
- 开关开启 + assistant=claude：仍走现有 ttyd 或显示 unsupported fallback，不做 Claude 自绘。

## 不做

- 不解析 ttyd/TUI 文本。
- 不写 JSONL 会话文件。
- 不实现 Claude 自绘。
- 不改变现有 `/api/open` / `/api/new` / `/api/sessions` 契约。
- 不在第一阶段追求 100% 长输出虚拟化；先做折叠/截断。

## TDD 顺序

1. `codex_appserver_test.go`：JSON-RPC client 红绿。
2. `chat_test.go`：ChatEvent JSON shape + Codex notification mapping 红绿。
3. `chat_handlers_test.go`：chat HTTP handler 的 unsupported/appserver_unavailable/SSE 基本行为红绿。
4. dashboard 侧先抽纯函数（feature flag、event reducer、render model），用 Node/jsdom 或无 DOM reducer 测试；再接 DOM。
5. 最后真实 Codex app-server probe + 真实 dashboard 手测。

## 手测脚本

- `codex app-server daemon version`
- `codex app-server generate-json-schema --out /tmp/...`
- dashboard 开启「实验 → 自绘界面」后，打开 Codex 会话，发送「只回复 OK」。
- 验证失败时「用终端打开」仍可回到 ttyd。
