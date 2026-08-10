# Codex 外部 Writer 会话只读同步设计

## 目标

当 Codex Desktop、VS Code 或另一个 app-server 进程已经持有某个 thread 的 writer 时，Fleet 不再把 `thread/resume ... already has an active writer` 显示成致命错误，而是：

1. 立即展示该会话的持久化历史；
2. 通过 rollout 变更和只读历史接口持续同步运行进度；
3. 明确标记“其他 Codex 客户端正在控制，当前只读”；
4. 禁用发送、引导、停止、审批和设置等写操作；
5. 外部 writer 释放后自动取得 writer，并原地升级为可写模式。

## 不做什么

- 不终止 Codex Desktop、VS Code 或其 app-server 进程。
- 不通过私有 IPC、匿名 Unix socket 或进程注入强制夺取 writer。
- 不把 `thread/unsubscribe` 当作跨连接释放机制；它只能取消当前连接自己的订阅。
- 不在同一 rollout 上制造第二个 writer。
- 不把“创建分叉会话”冒充 writer 交接；分叉会产生独立历史线，另作功能评估。

## 协议契约

`POST /api/chat/resume` 成功响应增加：

```json
{
  "accessMode": "read-write | read-only",
  "accessReason": "external_writer | 空字符串"
}
```

- 普通 resume 成功：`read-write`。
- 仅当 app-server 错误精确匹配 active-writer 冲突时，降级为 `read-only`。
- 其他错误仍按现有错误路径返回，不能把损坏、超时或权限错误伪装成只读。

SSE 增加事件：

```json
{
  "type": "access_mode",
  "data": {
    "mode": "read-write | read-only",
    "reason": "external_writer | 空字符串"
  }
}
```

写端点在只读状态下必须防御性返回 HTTP 409：

```json
{
  "code": "thread_read_only",
  "message": "该会话正由其他 Codex 客户端控制，Fleet 当前为只读同步。"
}
```

覆盖 `/api/chat/input`、`steer`、`interrupt`、`respond` 和 `settings`。前端禁用不是安全边界，后端仍须拒绝。

## 后端数据流

### 初次打开

1. Fleet 按现有流程调用 `thread/read(includeTurns:false)`。
2. 调用 `thread/resume`。
3. 若成功，沿用现有读写流程。
4. 若命中 active-writer 冲突：
   - 不返回错误；
   - 调用 `thread/items/list`，不支持时回退 `thread/turns/list`；
   - 读取 rollout lifecycle，取得最近 turn id 与 `inProgress/completed/interrupted`；
   - 返回历史、运行态以及 `accessMode=read-only`；
   - 不把 thread 写入 `loadedThreads`，避免误以为当前 RPC 已持有 writer。

### 实时只读同步

复用现有 `runConnectedSync`、rollout stamp、history projection 与 stable sync id：

- rollout 文件大小或 mtime 变化时，增量解析 lifecycle；
- 通过只读 `thread/items/list`/`thread/turns/list` 拉取最新项目；
- 复用现有去重逻辑发布 `turn_started`、item 更新和 `turn_done`；
- 没有 rollout 变化时不重复拉取大历史。

只读 watcher 不能在当前 turn 完成后退出，因为 writer 可能仍被 Desktop 保留；只要仍有 Fleet SSE 订阅者，就继续等待 writer 释放。

### 自动升级

- 每个只读 thread 仅允许一个后台 acquisition loop。
- 使用有上限的退避重试 `thread/resume`，建议 3s、5s、10s，之后固定 10s。
- active-writer 仍存在：保持只读，不写错误消息。
- resume 成功：
  1. 重新同步一次历史，补齐竞争窗口；
  2. 调用现有 `rememberResumedThread`；
  3. 清除只读标记；
  4. 发布 `access_mode=read-write` 和最新 `thread_status`；
  5. 前端原地启用 composer，不刷新页面、不丢草稿。
- 其他错误：保留只读展示；可恢复连接错误沿用现有 RPC recovery；不可恢复错误以非正文状态提示，不污染聊天历史。
- 最后一个 Fleet SSE 订阅者离开时停止同步和 acquisition loop。

## 前端交互

只读模式下：

- 会话正文正常显示并持续更新；
- 标题区域显示“其他 Codex 客户端正在控制 · 只读同步”；
- 输入框保留已有草稿但设为 disabled；
- 发送、停止、follow-up、审批响应和会话设置全部禁用；
- 不再显示红色 `chat-error` 和“用终端打开”；
- 提供“立即重试接管”次要按钮，只触发一次安全的 `thread/resume` 尝试，不杀进程；
- 自动升级成功后显示短暂 toast“已取得控制权”，恢复原交互状态。

文案使用“其他 Codex 客户端”，不能断言一定是 Desktop；实际 writer 也可能来自 VS Code、CLI 或另一个 daemon。

## 手动 Writer 交接研究结论

Codex 当前公开协议没有跨进程 writer takeover/release API：

- `thread/unsubscribe` 只移除当前连接的 subscription，Fleet 无法用它释放 Desktop 连接；
- writer 即使 turn 已完成也可能继续由原 app-server 持有；
- `thread/read` 和历史分页仍可只读使用；
- 第二个进程只有在原 writer 进程释放 thread 或退出后才能 `thread/resume` 成功。

因此分三级处理：

1. **安全默认**：自动退避重试 + “立即重试接管”。
2. **用户侧交接**：提示用户在原 Codex 客户端关闭/释放该会话，Fleet 随后自动升级。
3. **运维级强制交接（本功能不实现）**：退出目标 Mac 上整个 ChatGPT/Codex Desktop 或终止其 app-server，再由 Fleet resume。该动作影响其他会话和正在运行的 turns，必须作为独立高风险功能设计，不能放进普通会话按钮。

可另行验证 `thread/fork` 在外部 writer 存在时是否允许从持久化快照创建 Fleet 新 thread；即使可行，它也是“分叉继续”，不是 writer 交接，必须明确展示历史分叉语义。

## 失败语义与边界

- active-writer 字符串识别封装为单一 helper，并保留 JSON-RPC code 检查，避免散落字符串判断。
- rollout 暂时不可读：保留已加载历史并继续重试，不清空 UI。
- 只读历史接口暂时失败：SSE 保持连接，下一个 rollout tick 重试。
- 外部 writer 在 acquisition 竞争窗口重新出现：保持只读；不发送任何写请求。
- 自动升级与浏览器断开并发：以 subscription/acquisition 锁保证不会在最后订阅者离开后继续 resume。
- 多个浏览器打开同一只读 thread：共享一个 watcher 和一个 acquisition loop。

## 测试要求

### Go

1. active-writer resume 错误降级为成功的只读响应。
2. 非 active-writer 错误不降级。
3. 只读初始历史来自 items/turns list，运行态来自 rollout lifecycle。
4. 只读 SSE 不调用 `ensureThreadLoaded`，仍能收到 rollout 增量。
5. 多订阅者只创建一个 acquisition loop。
6. writer 释放后一次 resume 成功，发布 `access_mode=read-write` 并补齐历史。
7. 最后订阅者退出后 watcher 与 acquisition loop 停止。
8. 所有写端点在只读状态下返回 `thread_read_only`。

### Dashboard

1. 只读响应不渲染红色错误卡。
2. 历史正常显示，composer 与所有写控件禁用。
3. 草稿在只读与读写切换中保留。
4. `access_mode=read-write` 后原地恢复发送。
5. 多次只读同步不重复插入消息。

### 集成验证

在测试 CODEX_HOME 中启动两个 app-server：第一个持有 writer，第二个通过 Fleet 路径打开同一 thread，验证只读展示；关闭第一个后验证第二个自动升级并能启动新 turn。生产验证只在测试会话执行，不终止真实 Desktop 任务。
