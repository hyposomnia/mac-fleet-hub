# Codex Desktop / app-server 重构基线

日期：2026-07-24

状态：Codex 自绘会话的 app-server 事件基线。消息投递、writer、access 与队列状态机以后续的 `../superpowers/specs/2026-08-13-codex-server-authoritative-chat-design.md` 为准。

## 目标

Fleet 的 Codex 会话不是独立发明一套聊天协议，而是复刻 Codex Desktop 的基础状态机和交互，再叠加 Fleet 的远程、多 Mac 与移动端能力。

数据源优先级固定为：

1. Codex app-server 官方文档。
2. 当前安装版本生成的 app-server schema。
3. 当前 Codex Desktop bundle 的实际调用和状态投影。
4. Fleet 扩展层。

Codex 的 session、turn、item 和交互请求不再以 SQLite、JSONL 或 TUI 文本猜测为主。JSONL/tmux 只保留为 ttyd fallback 和附加运行信号。

## 调研证据

- 官方文档：<https://learn.chatgpt.com/docs/app-server>
- 本机 Codex CLI：`codex-cli 0.144.6`
- 本机生成的 v2 schema：
  - `ThreadListParams`
  - `ThreadReadResponse`
  - `ThreadResumeResponse`
  - `TurnStartParams`
  - `TurnSteerParams`
  - server request / notification schemas
- 当前 Codex Desktop bundle：
  - thread list 参数、过滤和分页
  - follow-up mode 默认值与快捷键
  - optimistic `steeringUserMessage` 生命周期
  - `clientUserMessageId` 对账和去重
  - steer active-turn 竞态重试

每次升级 Codex CLI 后，应重新生成 schema 并运行真实 app-server probe。Desktop 私有实现只用于复刻产品行为，不替代公开协议。

## Session 目录

Desktop 的普通会话列表使用：

```json
{
  "limit": 50,
  "cursor": null,
  "sortKey": "recency_at",
  "modelProviders": null,
  "sourceKinds": [],
  "archived": false,
  "useStateDbOnly": true
}
```

Fleet 使用同一组默认参数，并把搜索映射到 `searchTerm`、归档页映射到 `archived:true`、加载更多映射到 app-server opaque cursor。

列表先排除 Desktop 同样隐藏的：

- `ephemeral === true`
- `threadSource === "ambient_suggestions"`

随后读取 `~/.codex/.codex-global-state.json` 的 `local-projects`、`thread-project-assignments` 与
`projectless-thread-ids`，对齐 Desktop 的项目归属：同一项目的主仓与 worktree 归入一个项目，快速会话统一归入
“无项目”。会话自身 `cwd` 保持不变，只额外投影项目根目录供列表归组与项目内新建使用。

有 `parentThreadId` 或对象型 `source.subAgent` / `source.subagent`、且没有明确本地项目绑定的内部子 Agent
线程不进入普通会话列表。不能只凭 `threadSource === "subagent"` 过滤，因为 Desktop 已绑定项目的 worktree
会话和部分历史迁移会话也可能保留这个值。

除此之外不按 CLI/app-server/source 名称自创白名单。`recency_at` 不受旧版本支持时，只窄回退到
`updated_at`，不回退本地数据库扫描。

会话重命名、归档、取消归档和删除分别使用：

- `thread/name/set`
- `thread/archive`
- `thread/unarchive`
- `thread/delete`

置顶是 Fleet 扩展，单独存于 `~/.codex/fleet-thread-pins.json`，不伪装成 app-server 字段。

## 打开与恢复

打开 Codex 会话的标准链路为：

```text
thread/read(includeTurns:false)
  -> thread/resume
  -> thread/items/list 或 thread/turns/list 分页
  -> 合并 hydration 期间到达的通知
  -> SSE backlog
```

`thread/read(includeTurns:false)` 负责读取 cwd 和轻量元数据；`thread/resume` 负责加载并订阅 thread；分页接口负责历史。恢复期间的实时 notification 必须缓存，不能在历史 hydration 与事件订阅之间丢失。

app-server notification 是状态转换，不是可丢弃的 telemetry。RPC reader 对 notification 使用背压；SSE 重连会回放 backlog 和未决 server request。backlog channel 容量按实际回放量分配，不能固定为 64 后在返回前阻塞。

## Follow-up / steer

Desktop 默认 `followUpQueueMode = "steer"`，支持：

- `steer`：把输入插入当前 in-flight turn。
- `queue`：排到下一轮。
- `interrupt`：中断当前轮后处理新输入。

VS Code 默认是 `queue`，但 Desktop 默认是 `steer`。Fleet 保留 Desktop 的交互语义，但 writer 选择只在服务端完成：

- 运行中按 Enter：浏览器提交 `deliveryMode=auto`，fleet-agent 判定并调用 `turn/steer`
- 运行中按 Cmd/Ctrl+Shift+Enter：浏览器提交 `deliveryMode=next`，服务端显式排下一轮
- idle 时按 Enter：同一 `auto` 提交由 fleet-agent 调用 `turn/start`

原生 steer 状态机：

```text
optimistic steering message (pending)
  -> turn/steer(expectedTurnId, clientUserMessageId)
  -> accepted
  -> persisted userMessage.clientId arrives
  -> optimistic item is reconciled with the server item
```

实现约束：

- `expectedTurnId` 必须来自 fleet-agent 当前 active turn，浏览器不得自行提供。
- 每条 optimistic 消息生成稳定的 `clientUserMessageId`。
- 收到持久化 `userMessage.clientId` 时按该 ID 对账，不能按正文猜测。
- app-server 返回 actual turn mismatch 时，按 Desktop 逻辑更新 active turn 并重试一次。
- steer 失败、turn 已结束和下一轮续投都由服务端持久化状态机处理，不能静默丢失或重复投递。
- 从 queue 点“引导”只提交 decision，失败时服务端保留原 item，不能生成副本。
- `turn/steer` 不产生新的 `turn/started`，也不携带 model/cwd/sandbox 等 turn override。

Fleet 的持久 queue 是所有普通输入的唯一浏览器写入口；`deliveryMode=auto` 仍保持 Desktop 默认 steer 的用户语义。

## Server Request

app-server 发给客户端的 request 必须保存：

- 原始 JSON-RPC request id
- method
- params
- thread/session id

统一通过 `POST /api/chat/respond` 响应；成功写回 app-server 后才从 pending map 移除。SSE 重连时重放未决请求。

当前标准映射：

| request | response |
|---|---|
| command/file approval | `{"decision":"accept|acceptForSession|decline|cancel"}` |
| permission approval | `{"permissions":{...},"scope":"turn|session"}` |
| `item/tool/requestUserInput` | `{"answers":{...}}` |
| `mcpServer/elicitation/request` | `{"action":"accept|decline|cancel","content":...}` |

旧 `/api/chat/approve` 只用于旧 dashboard 兼容，不再作为新 UI 的协议入口。

## Item 投影

Fleet 前端只消费稳定 `ChatEvent`，不直接认识 app-server wire。投影至少覆盖：

- user / assistant streaming 与完成态
- reasoning summary 与耗时
- plan / todo
- context compaction
- command、file change、MCP、dynamic tool
- web/search/read/image
- review 状态
- subagent / collaboration activity
- token usage、model、effort、service tier
- approval、request user input、MCP elicitation

未知 notification 可以记录并忽略；已知状态转换不能静默丢弃。工具卡的 read/search/list 判断优先使用 app-server `commandActions`，正文启发式仅作旧协议兼容。

## Fleet 扩展层

以下能力有意超出 Desktop 基础复刻：

- 多 Mac 和远程网关
- 移动端布局
- model / effort / service tier 显示
- token usage 与 cached input 比例
- 用户消息吸顶
- Fleet 本地 pin
- ttyd fallback

扩展不能改变 Thread/Turn/Item 的基础语义。若扩展与 app-server 状态冲突，以 app-server 为主。

## 验证门槛

每次变更至少运行：

```bash
cd mac/fleet-agent
go test ./...
go test -race ./...

cd ../../server/dashboard
node --check app.js
node --check chat_model.js
node --check markdown.js
node --test chat_model.test.mjs

git diff --check
```

同时执行：

1. 真实 app-server `initialize -> thread/list -> model/list` probe。
2. Desktop 宽屏回归。
3. `390x844` 移动端回归。
4. 运行中 Enter steer、显式 queue、steer 失败回落、queue 引导失败保留。
5. SSE 重连后历史、未决 request 和 optimistic user message 不重复、不丢失。

不能声称覆盖尚未验证的 Desktop 私有功能。新的基础能力先按官方协议与 Desktop 行为补矩阵和测试，再进入 Fleet 扩展设计。
