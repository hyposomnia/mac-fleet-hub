# Codex Desktop 对话交互复刻验收矩阵

日期：2026-07-24
状态：基础交互验收矩阵。当前消息投递、writer、access 与 follow-up 控制协议以 `../superpowers/specs/2026-08-13-codex-server-authoritative-chat-design.md` 为准。

## 原则

这不是“把 app-server 事件渲染成聊天气泡”，而是在 mac-fleet-hub dashboard 中复刻 Codex Desktop 的会话交互。session 目录也必须使用 Desktop 的 `thread/list` 逻辑；不能只替换右侧渲染后继续扫描本地库猜测。

实验开关默认关闭。关闭时 `/api/open`、`/api/new`、iframe pool、移动端 ttyd 输入坞的行为必须与现有版本一致。

## 交互状态矩阵

| 状态 | Desktop 交互目标 | POC 验收 |
|---|---|---|
| 未打开 | 右侧为空状态，不抢占会话列表 | 仍显示当前空态；自绘只在 Codex + 开关打开 + 用户进入会话后出现 |
| 加载历史 | 会话进入后先有可感知加载，不闪烁成空白 | `chat-pane` 显示加载态；失败显示 fallback，不创建 ttyd |
| 历史回放 | 历史 item 按原时间/turn 顺序恢复 | POC 可先显示 app-server resume/read 返回的可用 turns；缺失类型用占位卡，不丢顺序 |
| 用户输入 | 用户消息靠近 composer，提交后立即落入流 | `Enter` 发送，`Shift+Enter` 换行；发送后本地追加 user message，composer 清空 |
| 运行中追问 | Desktop 默认 steer 当前 turn；可显式排下一轮 | 浏览器统一提交服务端 queue：`Enter` 使用 `deliveryMode=auto` 由 agent 判断 steer/start；Cmd/Ctrl+Shift+Enter 入下一轮；用 `clientUserMessageId` 对账 |
| Assistant streaming | 文本按 delta 合并为同一条 assistant message | 同一 `itemId` 的 `assistant_delta` 追加，不产生多条碎片 |
| Assistant 完成 | 完成态不再闪烁；最终文本替换/校准流式文本 | 只有 `item/completed.item.type=="agentMessage"` 才触发 `assistant_done` |
| Reasoning | 与 Codex Desktop 类似的折叠/轻量状态 | POC 若遇到 reasoning item，显示“思考中/摘要”折叠占位，不混进正文 |
| 命令执行 | 命令卡片显示命令、cwd、状态、输出流 | `tool_delta` 进入对应工具卡；stdout/stderr 有视觉区分，长输出可折叠 |
| 文件改动 | diff/update 作为文件变更卡，不混入 assistant 文本 | `diff_update` 渲染为文件变更占位卡；后续再展开 diff 细节 |
| 审批请求 | 审批卡片保留原 request id，按钮响应同一请求 | approval data 必含 `requestId`；approve/deny 不靠 `itemId` 猜 |
| 审批完成 | 卡片变为 resolved，不重复提交 | `serverRequest/resolved` 或 approve 响应后更新为已处理 |
| 错误 | 错误卡可读，提供回终端 fallback | `appserver_unavailable` 显示“用终端打开”按钮 |
| 中断 | 运行时可中断，状态回 idle/aborted | `interrupt` 保留端点；前端有中断入口，失败不影响 fallback |
| 滚动 | 用户在底部时自动跟随；上翻时不强拉回 | 新 delta 只在接近底部时 auto-scroll；显示“跳到底部”入口 |
| 移动端 | 不使用 ttyd 输入坞；使用自绘 composer | `#mobile-input` 仍只服务 ttyd；自绘 UI 有自己的底部 composer |
| 关闭开关 | 立即回到现有终端路径 | 不销毁旧 ttyd 池语义；已开的自绘状态可以丢弃 |

## 视觉与文案矩阵

| 元素 | Desktop 复刻方向 | POC 要求 |
|---|---|---|
| 整体布局 | 窗口内单列会话流 + 底部 composer | 不改左栏结构；`chat-pane` 占满右侧窗口主体 |
| 消息密度 | 接近桌面端：克制间距、低噪音 | 不做大号 AI 聊天气泡；正文宽度和行高偏阅读型 |
| User message | 明确但不夸张的用户块 | 与 assistant 区分，支持多行文本 |
| Assistant message | Markdown/纯文本阅读优先 | delta 合并后保持连续段落 |
| Tool card | 卡片化、可折叠、状态色克制 | 命令、cwd、状态、输出分区 |
| Approval card | 风险/动作/路径清楚 | 按 Codex 语义显示 command/file/permission，不用泛泛“确认？” |
| Error fallback | 明确说明“自绘不可用”，给终端出口 | 按钮文案“用终端打开” |

## 事件映射硬约束

| app-server 输入 | dashboard 事件 | 约束 |
|---|---|---|
| `item/agentMessage/delta` | `assistant_delta` | 按 `itemId` 合并 |
| `item/completed` + `agentMessage` | `assistant_done` | 非 agentMessage 不得映射为 assistant 完成 |
| `item/commandExecution/outputDelta` | `tool_delta` | 按 `itemId` 归属工具卡 |
| server request `item/commandExecution/requestApproval` | `approval_request` | 必须保留 JSON-RPC `id` 为 `requestId` |
| server request `item/fileChange/requestApproval` | `approval_request` | POC 可显示 unsupported，但不能丢 request id |
| `item/fileChange/patchUpdated` | `diff_update` | 独立 diff/file 卡 |
| `turn/completed` | `turn_done` | 只结束本 turn，不代表所有 item 都是正文 |
| `error` | `error` | 可读错误 + fallback |

## 回归清单

- 开关关闭时：Codex/Claude 的连接按钮、Bypass/Auto、iframe pool、重载、终止、文件模式行为不变。
- 开关开启时：Claude 仍走旧终端路径；Codex 不在自绘路径里创建 tmux/pty。
- 新 `/api/chat/*` 只在 fleet-agent 本机；不新增 gateway nginx 暴露面。
- 前端失败时可一键回到终端打开同一 Codex session。
