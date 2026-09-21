# Sub Agent 状态条与执行抽屉设计

日期：2026-09-22

## 目标

Fleet 的 ChatGPT 自绘会话要能在父会话内观察 Codex Sub Agent，而不把子线程重新混入普通会话列表，也不取得子线程写入权。

- 有 Sub Agent 时，在输入框上方显示一行可横向滚动的名称与状态点。
- 点击名称展示该 Sub Agent 的执行记录；桌面约半屏，移动端直接使用大半屏。
- 点击状态条右侧箭头展开全部 Sub Agent 列表，再次点击收起。
- 状态与现有设备/Agent 状态点保持一致：进行中、完成、失败、中断、未知。

## 数据来源

fleet-agent 新增只读 `GET /api/chat/subagents?assistant=codex&sessionId=<parent>`。它调用 app-server `thread/list` 的 `ancestorThreadId` 查询父线程的全部后代，并从 `source.subAgent.thread_spawn` 投影任务路径、父线程、层级、角色和昵称。

app-server 的 `thread.status` 在进程重启后可能只有 `notLoaded`，因此状态优先读取子线程 rollout 的生命周期：`task_started` 为进行中，`task_complete` 为完成，`turn_aborted` 按原因区分失败或中断；缺少 rollout 时才回退 thread status。

子线程的执行内容复用现有只读历史接口 `GET /api/chat/history`。详情打开后，进行中的 Sub Agent 每 2.5 秒增量刷新；从进行中进入终态时强制再补拉一次，避免漏掉最终回复。子线程不执行 `resume`、`turn/start`、审批或中断操作。

## 前端交互

状态条只在当前 Codex 会话存在 Sub Agent 时出现。名称使用相对 `/root/` 的 agent path，既保留创建任务时的名称，也能区分嵌套 Sub Agent；缺失时依次回退 nickname、role 和线程短 ID。

面板有三种显式状态：

- `closed`：只显示可横滑状态条。
- `list`：显示全部 Sub Agent 的名称、元信息和状态。
- `detail`：显示选中 Sub Agent 的历史、工具执行和最终回复。

桌面详情高度为 `50dvh`，列表为 `34dvh`；窄屏下两者统一为 `68dvh`。面板位于 composer 内部，展开时压缩上方聊天滚动区，输入区仍保持在窗口底部。Escape 优先收起 Sub Agent 面板。

父会话中的 `collabAgentToolCall` 和 `subAgentActivity` 仍保留在浏览器模型中，用于触发即时刷新，但标记为 internal，不再作为普通工具卡重复展示。

## 故障与兼容

- 旧 fleet-agent 没有新接口时，dashboard 静默隐藏附加信息，主聊天不受影响。
- 单次子线程查询或历史读取失败只影响抽屉；主会话不写入错误事件。
- 页面关闭、切换会话或缓存淘汰时停止轮询；重新打开或从后台恢复时重新同步。
- DeepSeek Harness 暂不声明该能力，接口返回结构化 `unsupported_assistant`。

## 验证

- Go 单测覆盖后代线程投影、嵌套命名、rollout 状态映射、只读 HTTP 接口及内部事件投影。
- Dashboard 单测覆盖 DOM 位置、横向滚动、桌面/移动端高度、状态点映射和主对话隐藏规则。
- 发布前运行项目统一入口 `bash scripts/verify.sh`；fleet-agent 必须走 Developer ID 签名、公证和正式滚动发布流程。
