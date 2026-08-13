# Codex 服务端权威聊天与 Writer 租约设计

## 背景

Fleet 目前同时存在三套会影响真实行为的状态：fleet-agent 内存、目标 Mac 上的持久队列、浏览器 `localStorage` / JavaScript 状态。浏览器会根据自己的 `turnOwner` / `writerOwner` 投影决定调用 `turn/steer`、`turn/start` 或排队，并负责 follow-up 自动续投。浏览器退出、网络中断、多浏览器并行或 fleet-agent 重启时，这些状态会分叉；writer 释放失败也可能只写日志却对外报告成功。

本设计把 fleet-agent 设为唯一控制面。浏览器只提交用户意图、展示服务端状态、调用服务端允许的动作，不再保存或推进消息状态机。

## 目标

1. 每条用户消息在任何 Codex 写 RPC 前先持久化到目标 Mac。
2. steer、start、等待当前 Fleet turn、等待外部 writer 全部由 fleet-agent 判断。
3. writer 的取得、turn 完成、显式释放、异常恢复和孤儿回收全部由服务端生命周期驱动，不依赖 SSE 观察者数量。
4. `read_only` 是服务端持久 session 状态；刷新、换浏览器和 agent 重启后保持一致。
5. 队列状态转换使用原子 compare-and-swap，不允许取消与发送竞态。
6. 非幂等 RPC 结果未知时不盲目重放；服务端先用 `clientMessageId` 对账，无法确认则进入 `uncertain`。
7. 每个服务端状态只对应一种明确、可测试的浏览器表现和操作集合。

## 非目标

- 不修改 Codex app-server 协议。
- 不让网关保存目标 Mac 的消息或附件；权威数据仍位于目标 Mac 的 fleet-agent。
- 不跨 app-server 伪造 `thread/unsubscribe`。旧连接留下的孤儿锁通过重启 Fleet 专属 isolated sidecar 回收，不影响默认 Codex Desktop daemon。
- `localStorage` 仍可保存主题、布局等 UI 偏好，但不得保存待发送消息、writer、turn、access mode 或队列状态。

## 持久模型

`~/.macfleet/chat-queue.json` 升级为 version 2：

```json
{
  "version": 2,
  "sessions": {
    "codex\u0000<session-id>": {
      "accessMode": "read_write | read_only",
      "updatedAt": 0
    }
  },
  "items": []
}
```

队列项新增：

- `deliveryMode`: `auto | next`
  - `auto`：有 Fleet turn 时尝试 steer；否则 start。
  - `next`：不 steer，等待当前 turn 完成后 start。
- `delivery`: `steer | start`，成功后记录实际投递方式。
- `stateVersion`：每次状态转换递增，供 API CAS 与多浏览器对账。

浏览器不得提交 `writerOwner` 或 `status`。这两个值只能由服务端产生。

## 统一控制快照

`chat/start`、`chat/resume`、`GET /api/chat/queue` 和成功的 access 变更都返回完整的 `SessionControlSnapshot`，字段固定为：

- `serverEpoch`：fleet-agent 本次进程生命周期的随机标识；agent 重启后变化。
- `snapshotVersion`：同一进程内单调递增；浏览器不得用更旧版本覆盖新版本。
- `accessMode`：持久化的 `read_write | read_only`。
- `writerOwner`：从真实 writer lock 持锁进程判定的 `fleet | desktop | 空`；Fleet 私有 socket 之外的任何活持锁进程都视为外部客户端，不能依赖某个固定 Desktop 命令行形态。
- `turnPhase`、`activeTurnId`、`turnOwner`：由 rollout、真实锁和服务端运行态共同核对；terminal rollout 必须清除陈旧内存 turn。在 isolated 模式下，unfinished rollout 只表示历史中缺少 terminal 记录；真实 writer lock 无持有者时必须判 idle，不能反推为外部 writer。
- `approvalMode`：服务端最后确认的线程审批模式；浏览器可以暂存尚未提交的选择，但不得把乐观值当作已生效状态。
- `items`：该 session 的完整可见队列投影；每项带服务端派生的 `allowedActions`，浏览器不得按 `status` 维护第二套合法动作表。

快照在 per-session operation lock 内生成，确保 access、运行态和队列来自同一串行观察点。浏览器只应用带 epoch/version 的完整快照；同一 epoch 按 `snapshotVersion` 拒绝旧响应，跨 agent epoch 则再用浏览器请求序号阻止旧进程的在途响应覆盖新进程状态。SSE 仅作为“状态可能变化”的唤醒信号，收到 `turn_started`、`turn_done`、`thread_status`、`user_done` 或 `control_changed` 后重新取快照，不直接推进 writer/turn/queue。

## 消息状态机

```text
POST submit
  -> queued（消息已落盘）
     -> access=read_only -> waiting_access
     -> delivery=auto 且 Fleet turn active -> steering -> sent
     -> delivery=next 且 Fleet turn active -> waiting_turn
     -> idle -> sending -> sent
     -> external writer，未确认 -> writer_confirmation_required
          -> wait -> waiting_writer -> sending -> sent
          -> force -> takeover_check / takeover_confirmation_required
     -> 非幂等 RPC 结果未知 -> uncertain
     -> 明确错误 -> failed

waiting_turn -- steer action --> queued(delivery=auto)
waiting_access -- enable-write --> queued
failed/uncertain -- retry --> queued
任一未开始投递状态 -- cancel --> cancelled
```

`steering` / `sending` 是 worker 已原子 claim 的在途状态。此时不允许 cancel、retry、wait 或 force。所有更新必须验证 `expected status + stateVersion`。

## 统一提交接口

`POST /api/chat/queue` 是唯一普通消息写入口：

```json
{
  "assistant": "codex",
  "sessionId": "...",
  "clientMessageId": "...",
  "deliveryMode": "auto | next",
  "text": "...",
  "images": [],
  "skills": [],
  "options": {}
}
```

接口只做校验、幂等去重、原子落盘并唤醒 worker，不直接调用 Codex。`/api/chat/input` 和 `/api/chat/steer` 保留为内部兼容端点，但 Dashboard 不得调用；它们同样必须拒绝 `read_only` session。

worker 对 `auto` 项先调用服务端 `Steer`：

- 队列项携带 `approvalMode` 时，worker 在同一个 per-session operation lock 内先应用 `thread/settings/update`，再 steer/start；浏览器 settings 请求与队列请求的到达顺序不得改变本条消息的权限语义。
- 成功：记录 `delivery=steer`、`turnId`、`sent`。
- `no_active_turn`：在同一次 worker 流程中改走 `Input`。
- 外部 writer：进入确认/等待状态。
- 其它错误：进入 `failed` 或 `uncertain`，不由浏览器补投。

## Access mode

- 默认 `read_write`。
- `POST /api/chat/access {action:"release"}`：先持久化 `read_only`，再停止 Fleet turn并释放 writer。即使释放失败，也保持只读并返回错误，防止再次写入。
- `POST /api/chat/access {action:"enable-write"}`：持久化 `read_write`，把该 session 的 `waiting_access` 重新置为 `queued` 并唤醒 worker；不预先 claim writer。
- `chat/start`、`chat/resume`、`GET chat/queue` 和 access 变更都返回上述完整控制快照。
- `/chat/input`、`/chat/steer`、`/chat/settings`、附件上传等写接口在服务端再次校验 access mode。

## Writer 租约与孤儿恢复

### 正常释放

1. `turn_done` 仅当事件 turn ID 等于当前 Fleet turn ID 时释放。
2. turn 完成释放、显式释放、worker start 和定时回收共用 `writerLeaseMu`。
3. `thread/unsubscribe` 成功后才清除 `loadedThreads/writerOwners`。
4. RPC 失败时保留状态和租约，返回错误或等待定时重试。

### 浏览器生命周期

SSE 最后观察者离开只停止 rollout 增量同步并删除 channel，不调用 `thread/unsubscribe`。浏览器连接数不是 writer 租约条件。

### 定时巡检

每 30 秒扫描真实 `thread-writer-locks/*.lock`，用 `lsof + command line/socket` 区分 Fleet isolated sidecar 和外部 daemon；不以 fleet-agent 内存 map 作为候选来源。

- 活跃 Fleet rollout：保留。
- terminal / idle rollout：尝试当前连接 unsubscribe；失败或锁属于旧 connection epoch 时重启 Fleet isolated sidecar。
- rollout 未知：记录 `orphanFirstSeen`；宽限 2 分钟后再次核对，仍无服务端活跃 turn 证据则重启 Fleet sidecar。
- isolated sidecar 被多个 Fleet 会话共享；若另一个 Fleet writer 仍有真实活跃 rollout，自动/普通 release 不得通过重启 sidecar 打断它。此时延后孤儿恢复；只有列出全部受影响任务并获确认的强制接管可以重启。
- 每次动作后重新检查文件锁；未释放则保留告警状态并下轮重试。
- 外部 Desktop/CLI 已被服务端观察为 owner、但随后真实锁无持有者时，立即清理内存 owner 并广播 idle；详情快照、只读 resume、connected sync、会话列表和队列 worker 都必须独立执行同一核对，避免重启或重连再次从陈旧 rollout 复活幽灵 writer。

显式 release 已获得用户中断授权：当前连接无法释放或检测到 orphan 时，可立即重启 Fleet isolated sidecar。只操作 `com.macfleet.codex-app-server`，不操作默认 Desktop daemon。

## 崩溃恢复与幂等

启动加载时把 `steering/sending` 转成 `recovering`。worker 用 rollout/history 中的 `clientMessageId` 对账：

- 已存在：标记 `sent`，恢复 turn ID（若可得）。
- 不存在：标记 `uncertain`，等待用户从服务器状态卡选择重试或取消。

绝不在无法确认时自动重放非幂等写 RPC。

## 浏览器 UI 状态矩阵

### Session 顶部与 composer

| 服务端状态 | 标题栏 | Composer | 主按钮 |
|---|---|---|---|
| `read_write`, no writer, idle | 仅标题 | 可输入 | 有内容“发送” |
| Fleet writer + Fleet running | “会话已被 Fleet 占有，如果 Codex 操作受限，可先 释放会话” | 可输入 | 空输入“停止”；有内容默认 `auto` |
| 外部 writer | “其他 Codex 客户端正在使用 · Fleet 只读同步” | 可输入并允许提交到服务端确认队列 | 无内容禁用；有内容“提交” |
| `read_only`, writer 已释放 | “Fleet 只读，Codex 可接管 · 恢复 Fleet 写入” | 输入、附件、审批和模型设置全部禁用，placeholder“Fleet 已释放此会话” | 禁用 |
| `read_only`, Fleet writer 尚未释放 | “Fleet 已只读，writer 仍在释放或等待服务端回收 · 重试释放” | 全部禁用，不能恢复写入 | 禁用 |
| release 请求中 | 原占用提示，链接变“释放中…” | 暂时禁用 | 禁用 |

`read_only` 与 writer 必须联合展示；只有 `writerOwner` 为空或属于外部客户端时才能宣称 Codex 可接管。外部 writer 不冒充 Fleet 手动只读。

### 消息与队列项

| item status | 放置位置 | 文案 | 操作 |
|---|---|---|---|
| `queued` | 用户气泡下方短暂状态 | “消息已保存” | 取消（仅当服务端 `allowedActions` 返回） |
| `steering` | 用户气泡 meta | “正在插入当前任务…” | 无 |
| `waiting_turn` | composer 上方 follow-up tray | “服务器已保存 · 当前任务结束后发送” | “引导当前任务”、取消 |
| `sending` | 用户气泡 meta | “正在启动下一轮…” | 无 |
| `writer_confirmation_required` | 用户气泡下方状态卡 | “此会话正在其他 Codex 客户端中使用。等待释放，或确认强制接管。” | “等待并发送”、“强制接管”、“取消” |
| `waiting_writer` | 同一状态卡 | “已保存，等待其他客户端释放” | “强制接管”、“取消” |
| `waiting_access` | 用户气泡下方状态卡 | “Fleet 当前只读；恢复 Fleet 写入后才会发送。” | “恢复 Fleet 写入”、取消 |
| `takeover_confirmation_required` | 用户气泡下方危险确认卡 | 受影响任务清单 | “中断全部并接管”、“继续等待”、“取消” |
| `uncertain` | 用户气泡下方警告卡 | “上次投递结果未知；请先核对会话历史” | “确认重试”、取消 |
| `failed` | 用户气泡下方错误卡 | 服务端错误 | “重试”、取消 |
| `sent/cancelled` | 不显示队列卡 | 由真实 Codex history / 移除结果接管 | 无 |

所有按钮只调用服务端 decision/access API，并严格按队列项的 `allowedActions` 渲染。浏览器可以做“按钮提交中”这种瞬时视觉反馈，但不得预先改写持久 status；响应或下一次服务端同步后再更新。按钮在途状态绑定发起时的 `stateVersion`，收到更高版本快照或请求结束后必须清除，不能跨服务端状态残留。

现有 session 在首个有效控制快照到达前显示“正在同步会话控制状态”，输入、附件、审批、设置与发送全部禁用；新建且尚未获得 session ID 的本地草稿例外为可写，因为此时服务端还不存在可竞争的 writer。

浏览器可以维护 `lastControlAt` 作为纯传输层新鲜度。控制轮询连续失败且最后一个成功快照超过 10 秒后，必须把控制面标记为“状态不可确认”、禁用所有 mutation，并继续重试；不得无限期信任旧快照，也不得在失联时自行推断新的 writer/turn/access 值。

## UI 不变量

1. Fleet 自己的 `waiting_turn` 永远不显示“强制接管”。
2. 外部 writer 队列卡永远不出现在 composer follow-up tray。
3. `read_only` 时不能通过键盘快捷键、附件或旧 DOM 状态触发提交。
4. `steering/sending` 不显示取消，避免虚假撤回。
5. 同一 `clientMessageId` 只能渲染一个用户气泡；history 到达后替换 optimistic 投影。
6. 刷新、换浏览器或关闭全部页面不改变任何 queue/access/writer 状态。

## TDD 验收

### Go

- 所有消息先入队，worker 才可调用 Steer/Input。
- `auto` 活跃 Fleet turn走 steer；`next`走 waiting_turn；steer no-active 原子回退 start。
- cancel 与 worker claim 并发时只有一个合法结果。
- 非法状态转换返回 `queue_state_conflict`。
- 重启恢复 steering/sending 并按 client ID 对账。
- access mode 持久化、resume/queue返回、所有写端点强制校验。
- unsubscribe 失败不清内存、不返回成功，下一轮可重试。
- SSE 断开不调用 unsubscribe。
- stale turn_done 不释放较新的 Fleet writer。
- map 为空但真实 Fleet lock存在时巡检仍能发现并回收。
- unknown rollout 超过宽限期后回收孤儿锁，活跃 turn 不回收。
- 实际 ChatGPT Desktop 命令行持锁识别为外部 writer；terminal rollout 清除陈旧内存 turn/writer。
- isolated 模式下 active rollout 无真实持锁进程时，Control、Resume、connected sync、会话列表均投影 idle，Input 可重新取得 writer；锁探测工具失败或持锁进程无法分类时保守判外部占用。
- `read_only` 时附件上传与其他写接口一致返回拒绝。

### Dashboard

- 源码不包含 `chat/input`、`chat/steer`、`flushChatFollowups` 或 Codex follow-up localStorage key。
- submit 只调用 queue API并携带 `deliveryMode`，不携带 `writerOwner/status`。
- UI 矩阵逐态断言标题、composer、卡片文案和允许按钮。
- 合法队列动作只来自服务端 `allowedActions`；未知或缺失动作默认不显示。
- access、writer、turn 和 queue 只由带 epoch/version 的完整控制快照覆盖；乱序旧响应与不完整响应不得落地。
- 控制快照超过新鲜度 TTL 后禁写，下一份有效快照恢复；失联不改写快照内的 writer/turn/access 值。
- 成功的队列决定在新 `stateVersion` 到达后解除本地按钮锁；失败也解除，不依赖刷新。
- `read_only + writerOwner=fleet` 显示等待回收/重试释放，不能宣称 Codex 已可接管或开放恢复写入。
- settings 与紧随其后的队列消息乱序到达时，worker 仍在 steer/start 前应用该消息携带的审批模式。
- SSE、historyReady、submitting 和本地 optimistic 消息不得直接改变 writer/turn/queue 状态。
- 多次队列同步不会重复用户气泡。
