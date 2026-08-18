# Codex 服务端消息队列与强制接管设计

## 目标

当 Codex thread 的 writer 被目标 Mac 上另一 app-server 占用时，Fleet 仍立即显示用户消息，并把消息持久保存到目标 Mac 的 fleet-agent。关闭浏览器、切换设备或 fleet-agent 重启后，消息及处理状态必须保留。用户可以选择继续等待 writer 自然释放，或通过二次确认强制接管。

## 非目标

- 不修改 Codex app-server 的 writer 协议。
- 不伪造跨 app-server 的 `thread/unsubscribe`；该 RPC 只能释放调用连接自己的订阅。
- 不在第一次点击“强制接管”时中断任务。
- 不把队列放到公网网关；队列归目标 Mac 管理，与该机的本地图片和 Codex runtime 同生命周期。

## 权威状态

fleet-agent 使用 `~/.macfleet/chat-queue.json` 作为持久队列。写入使用同目录临时文件、`fsync`、原子 rename，权限 `0600`。内存状态只是磁盘状态的缓存。

浏览器 `localStorage` 不再是队列权威来源。升级时可读取旧队列并逐项提交给 agent，服务端按 `clientMessageId` 幂等去重，成功后删除旧键。

## 队列项

每项至少保存：

- `id`、`clientMessageId`
- `assistant`、`sessionId`
- `text`、已上传图片 ID、skills
- turn options：model、effort、service tier、approval mode
- `status`、`decision`
- `createdAt`、`updatedAt`、`attemptedAt`、`sentAt`
- `turnId`、`error`
- 强制接管审计结果：受影响 thread ID、标题、turn ID、是否 active

状态机：

```text
queued
  ├─ writer 可用 → sending → sent
  └─ writer 冲突 → waiting_writer
         ├─ decision=wait → 周期重试
         └─ 请求强制接管 → takeover_check
                ├─ 无 active 任务 → taking_over → sending → sent
                └─ 有 active 任务 → takeover_confirmation_required
                       ├─ 取消 → waiting_writer
                       └─ 二次确认 → taking_over → sending → sent

任一执行错误 → failed（保留消息，可重试或取消）
```

## API

### 列出

`GET /api/chat/queue?assistant=codex&sessionId=<id>`

返回该 session 的未取消队列项，包括 sent/failed 状态，供页面恢复和消息卡片对账。

### 入队

`POST /api/chat/queue`

请求包含消息和 turn options。以 `clientMessageId` 幂等；相同 ID 重试返回原项，不重复发送。

### 决策

`POST /api/chat/queue/decision`

请求：`{id, action}`，action 为：

- `wait`：保持等待并由后台 worker重试。
- `force`：执行第一次强制接管审计；无 active任务时可直接执行。
- `confirm-force`：仅当状态为 `takeover_confirmation_required` 时有效；授权中断目标机器默认 daemon上的所有受影响任务。
- `retry`：failed 项回到 queued。
- `cancel`：未 sent项标记 cancelled。

非法状态转换返回 `409 queue_state_conflict`。

## 后台 worker

fleet-agent启动时加载队列并启动单 worker。每个 session严格 FIFO；不同 session可轮转，但强制接管全机串行。worker不依赖 SSE 或浏览器连接。

自然投递流程：

1. isolated app-server `thread/resume`。
2. 成功获得 writer后调用 `turn/start`。
3. 只在 `turn/start` 返回成功后标记 sent并记录 `turnId`。
4. active writer冲突则标记 `waiting_writer`；退避重试，不忙轮询。

agent在 `turn/start` 响应未知（超时/断线）时不自动重放，标记 failed并要求人工重试，避免重复消息。

## 强制接管

强制接管只作用于目标 Mac。

1. 定位目标会话 `thread-writer-locks/*.lock` 或 rollout 的真实外部持有进程；Fleet isolated sidecar 不属于接管目标。
2. 对外部进程持有的每个 thread 读取 rollout 生命周期，判断 active/idle，并返回受影响清单；直接 `codex exec resume` 没有 lock 文件时使用 rollout holder 作为物理证据。
3. 无 active任务：第一次 `force`即可进入执行。
4. 存在 active任务：进入 `takeover_confirmation_required`，第一下不做破坏性操作。
5. `confirm-force` 后：
   - 向真实外部 holder 发送 TERM；3 秒内未释放才升级 KILL。
   - 等目标会话的物理 writer 信号确认消失；不能把“已发信号”当作接管成功。
   - 保持 Fleet isolated sidecar 存活，由它立即对目标 thread `resume`并投递队首消息。
6. 同一外部 holder 上的 active 任务都可能中断；确认卡必须展示数量和会话名称/ID。

全机强制接管使用互斥锁。执行中其它 force请求串行等待并重新审计，不并发重启 runtime。

## 事件与 UI

页面恢复时先 GET 队列，随后每 3 秒从 agent 拉取一次完整状态。投递和接管均由 agent 后台执行，不依赖轮询或浏览器存活；轮询只负责刷新界面。

入队消息立即以普通右侧用户消息显示；消息气泡下方追加状态卡，样式复用授权卡的边框、间距和按钮层级：

- `waiting_writer`：`等待会话控制权`，按钮“强制接管”“继续排队”“取消”。
- `takeover_check`：`正在检查受影响任务…`。
- `takeover_confirmation_required`：显示受影响任务列表，按钮“中断全部并接管”“继续排队”。
- `taking_over`：显示接管步骤。
- `sending`：`正在发送…`。
- `sent`：短暂显示“已发送”，随后保留低调完成状态。
- `failed`：显示失败原因，按钮“重试”“取消”。

多浏览器看到同一状态；按钮调用服务端决策API，不直接操作本地数组。

## 安全与恢复

- 强制接管第一次点击永不终止进程。
- `confirm-force`必须匹配最近一次审计版本；受影响集合变化时返回新的审计结果，要求再次确认。
- agent崩溃在 taking_over状态时，重启后重新审计；不根据旧状态直接杀进程。
- sent项按容量和时间清理；未发送项不自动丢弃。
- 图片必须已经上传到 agent；队列只保存稳定图片ID，不保存浏览器 blob URL。

## 验收

- 入队后关闭全部浏览器，agent仍可在 writer释放后发送。
- agent重启后队列恢复且不重复发送。
- 换浏览器打开同一 session能看到队列消息和状态卡。
- 第一次 force遇到 active任务只返回影响清单，不中断。
- 二次确认后允许中断目标机器所有受影响任务并完成接管投递。
- 无 active任务时 force无需二次确认即可执行。
- 所有写路径以 `clientMessageId`幂等。
