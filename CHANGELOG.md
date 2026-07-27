# Changelog

mac-fleet-hub 变更记录（日期为本地时间）。

## 2026-07-27

### 修复大型 Codex 会话无法打开
- **传输层解除假上限**：app-server 单行 JSON 改用无固定 token 上限的换行读取，迁移长会话不再触发 `bufio.Scanner: token too long`。
- **旧版回退减载**：`thread/turns/list(itemsView=full)` 兼容路径从每页 12 个 turn 收敛到 1 个，避免多个完整 turn 在同一响应内叠加。
- **回归与发布**：新增超过 16 MiB 单行响应和回退分页契约测试，重建 Darwin 双架构产物并部署四台 Mac；真实故障会话恢复接口验证为 HTTP 200。

### 吸顶用户消息改为灰色底
- **视觉一致**：滚过会话顶部后显示的用户消息气泡复用普通用户消息的灰色背景，避免吸顶状态下误呈白底；明暗主题同步生效。

### 收敛 Codex 思考摘要样式
- **常规字重**：思考摘要中的 Markdown 强调文本不再加粗，统一使用轻量的正文层级。
- **稳定箭头**：展开或收起思考摘要时，右侧箭头保持原方向，不再旋转。
- **静态圆环**：Thought 左侧状态圆环不再播放旋转动画。

## 2026-07-26

### Codex app-server 请求触发式自恢复
- **统一故障检测**：所有 Codex RPC 都有内部 deadline；stdout EOF、broken pipe、connection reset 会立即唤醒挂起请求，不再依赖浏览器断开或永久等待。
- **安全恢复语义**：失联后清理旧子进程并单路重连；列表、读取、历史、skills 和模型等幂等请求自动重试一次，创建会话、发消息、归档等写请求只恢复连接而不重放，避免重复副作用。
- **最终自救**：新 app-server 仍无法初始化或幂等重试再次失联时，先返回明确错误，再由 fleet-agent 主动退出，交给 launchd `KeepAlive` 自动拉起；同一进程只安排一次重启。
- **取消请求隔离**：浏览器切页或主动取消不会触发连接重置和 agent 重启。
- **回归保护与产物**：覆盖超时重连、EOF 唤醒、写请求不重放、初始化失败自重启、重启防抖与客户端取消；fleet-agent Darwin amd64/arm64 产物同步重建。

## 2026-07-25

### 修复完全访问权限同步与继承
- **继承本机会话权限**：Codex app-server 的恢复元数据可能滞后；Web 现在优先读取 rollout 最新的 `turn_context` / `thread_settings_applied` 结构化权限，正确继承本机的 `never + danger-full-access`，无记录时才回退 app-server。
- **线程即时同步**：切换 Codex 审批模式时立即调用 `thread/settings/update`，不再等到下一轮发送才把选择传给 app-server。
- **每轮权限兜底**：每次 `turn/start` 都显式携带当前审批模式；“完全访问权限”稳定映射为 `approvalPolicy=never` 与 `dangerFullAccess`。
- **失败与运行中语义**：设置失败会回滚到服务端最后确认值；运行中的任务明确提示从下一轮生效，已产生的审批请求不会被静默放行。
- **静态缓存与产物**：dashboard 外壳缓存升级到 v42；fleet-agent Darwin amd64/arm64 产物同步重建并发布。

### Codex 会话活动折叠与原生交互对齐
- **原生分组边界**：连续命令、文件读取/搜索、文件改动、网页搜索、普通 MCP 与动态工具合并为一个可展开活动组；图片查看/生成、Computer Use 与子代理活动保持独立，避免不同交互被错误吞进命令组。
- **原生摘要顺序**：按 Codex Desktop 的来源集成、Skill 加载、普通工具、文件改动、读取/搜索文件、命令、网页搜索顺序生成组头；支持“运行了多个命令”“已使用 Chrome 集成”“已加载一个工具”等原生文案和图标优先级。
- **展开信息保真**：折叠只改变组头，展开后仍显示每条命令、输出、MCP 详情与逐文件增删行数。

### 自绘会话原生 Skill 输入与可靠发送
- **结构化 Skill**：通过 app-server `skills/list` 获取当前项目 Skill，输入 `$name` 或兼容 `/name` 后在 `turn/start` / `turn/steer` 发送原生 `type: skill`；支持带命名空间的插件 Skill、同名 Skill 稳定选择和键盘菜单操作。
- **失败不误发**：Skill 列表加载失败时保留输入并阻止消息退化成普通文本，避免未激活 Skill 却消耗一次模型调用；首轮请求失败会恢复正文与已上传图片。
- **历史图片恢复**：Codex `localImage` 历史保留安全媒体路径，乐观预览被持久化消息替换后仍能正常显示。
- **静态缓存与产物**：dashboard 外壳缓存升级到 v41；fleet-agent Darwin amd64/arm64 产物同步重建。

## 2026-07-24

### Codex Desktop / app-server 基础状态机重构
- **会话目录标准化**：Codex session、搜索、归档和分页改用 Desktop 同参数的 `thread/list`；会话操作改用 `thread/name/set`、archive/unarchive/delete，不再以本地 SQLite/JSONL 扫描作为主数据源。
- **恢复链路标准化**：按 `thread/read -> thread/resume -> items/turns 分页` hydration，缓存恢复期间 notification，并在 SSE 重连时回放 backlog 和未决交互请求。
- **原生 follow-up**：Desktop 默认行为改为运行中 Enter 调 `turn/steer`，Cmd/Ctrl+Shift+Enter 显式排下一轮；`clientUserMessageId` 贯穿 optimistic 消息、app-server 与持久化 user item，steer 竞态会重试，失败则撤回正文并恢复 queue。
- **完整交互请求**：统一保存 app-server request id/method/params，新增 `/api/chat/respond`，覆盖 command/file/permission approval、request user input 与 MCP elicitation。
- **结构化投影**：补齐 reasoning、plan/todo、context compaction、review、dynamic/MCP/image/subagent 等 item，并支持本地图片安全渲染。
- **会话管理与扩展**：增加当前/已归档、搜索、加载更多、重命名、归档、删除和 Fleet 本地置顶；保留模型、effort、service tier、token usage、用户消息吸顶与 ttyd fallback。
- **静态缓存**：dashboard 外壳缓存升级到 v40。

### AI 回复占满会话正文列
- **全宽正文**：移除 AI 回复额外的 `82% / 760px` 桌面限宽和 `88%` 移动端限宽，使回复与输入框使用同一正文列宽；用户气泡和工具卡片保持原有紧凑宽度。

### 用户消息置顶跟随当前滚动轮次
- **逐轮置顶**：所有本地与同步用户消息都作为轮次锚点；消息滚过聊天区顶部后显示其首行，下一轮滚过时自动切换，不再只认整段会话最后一条用户消息。
- **静态缓存**：dashboard 外壳缓存升级到 v39。

### 修复自绘模式新建会话仍打开终端
- **新建链路对齐**：开启 Codex 自绘界面后，新建会话改走 app-server `thread/start`，创建成功后直接进入自绘聊天，不再启动 ttyd。
- **会话持续可见**：会话扫描显式接纳 `mac_fleet_hub` 创建的 `source=vscode` rollout；首次发送后立即进入活跃列表。
- **回归保护**：补充 agent 路由、app-server 参数、来源筛选和 dashboard 分流测试；静态缓存升级到 v38，fleet-agent Darwin 双架构产物同步重建。

### 模型选单随页面高度展开
- **桌面完整展示**：模型、推理强度与速度选单取消固定像素高度，在页面可用高度内按内容自然展开；只有内容确实超过视口时才滚动。
- **移动端统一滚动**：底部选单改由整张面板约束页面高度，移除选项列表的二次滚动区域。

### 统一 dashboard 字体系统
- **字体边界**：普通界面统一使用系统 UI 字体；仅代码、命令、路径、IP 与 URL 保留等宽字体，时间、token、延迟和缓存统计改用 UI 字体的等宽数字。
- **排版收敛**：全站字号归并为 11 / 12 / 13 / 15 / 17px 五档，字重归并为 400 / 500 / 600 / 700 四档，并增加契约测试防止再次分叉。
- **静态缓存**：dashboard 外壳缓存升级到 v37。

### 完全访问状态使用 Codex 警示红
- **选中态同步**：审批模式选择“完全访问权限”后，输入框工具栏中的盾牌、文字和下拉箭头统一显示为 Codex 同款警示橙红；切回其他模式恢复默认灰色。
- **静态缓存**：dashboard 外壳缓存升级到 v35。

### 修复运行中「引导」看似未生效
- **稳定确认**：追问队列 ID 通过 `clientUserMessageId` 传给 Codex，并在历史恢复、上翻分页和实时事件中的 `user_done` 到达时精确移除对应队列项。
- **防止重复**：引导请求期间按钮进入禁用状态；即使 HTTP 响应异常，只要 SSE 已确认接收，也不再误报失败或留下可重复点击的卡片。
- **静态缓存**：dashboard 外壳缓存升级到 v34；fleet-agent Darwin 双架构产物同步重建。

### 自绘会话活动折叠对齐 Codex
- **组合摘要**：连续工具活动按 Codex activity header 合并，显示“已读取文件运行了多个命令已搜索网页”等轻量摘要。
- **展开保真**：组头只做摘要，展开后仍保留每条命令、MCP、diff 的原始详情和输出。
- **静态缓存**：dashboard 外壳缓存升级到 v33。

## 2026-07-23

### 精简自绘会话头部
- **去除冗余标识**：自绘会话标题旁不再显示“自绘”徽标。
- **去除重复上下文**：标题下方不再重复显示主机、项目和 `Codex Desktop-backed` 来源说明。
- **静态缓存**：dashboard 外壳缓存升级到 v32。

### Codex 工具活动行继续贴近原生
- **命令摘要**：运行中的命令改为 `正在运行命令，已持续 ...`，完成/停止后才在单行里显示命令文本，降低长命令滚动噪音。
- **MCP 归一**：`node_repl · js` 按 Codex 原生汇总显示为“已运行 1 条命令”，内部浏览器调用显示为“已使用浏览器”，不再暴露传输层工具名。
- **交互降权**：读取/搜索等只读活动保持不可展开的轻量摘要；修复 `.chat-tool-label` 样式冲突，让工具行回到 Codex activity-header 的低权重视觉。
- **静态缓存**：dashboard 外壳缓存升级到 v31。

### 审批说明链接
- **了解更多**：审批菜单里的“了解更多”跳转到 Codex sandboxing 文档的控制说明章节。
- **静态缓存**：dashboard 外壳缓存升级到 v30。

### 工具调用行复刻 Codex Desktop
- **内联摘要**：工具调用从“标题 + subtitle + 状态 pill”改为 Codex Desktop 同款 activity row，动词、命令/路径、耗时都在同一行内联显示。
- **命令/文件语义**：命令显示为 `已运行 <command>，耗时 ...`，读文件/图片、搜索、MCP 调用分别使用对应的 Codex 式动词和单行截断。
- **静态缓存**：dashboard 外壳缓存升级到 v29。

### 自绘会话尾部时间更紧凑
- **用户气泡时间**：用户消息的发送时间移到气泡边框外侧，避免像正文一样被包进消息框。
- **日期缩写规则**：用户与 AI 尾部时间按自然日压缩为 `22:45:55`、`昨天 22:45:55`、`前天 22:45:55`、`7-23 22:46:47` 或 `2025-7-23 22:46:58`。
- **静态缓存**：dashboard 外壳缓存升级到 v27。

### 恢复中的活动轮次不再提前显示统计
- **真实完成信号**：从历史恢复时，仅把 `final_answer` 或带明确完成时间的 assistant item 视为整轮完成；仍在写入的 `commentary` 不再提前显示模型、强度、token 与日期。
- **独立 app-server 兼容**：即使 fleet-agent 的 app-server 对 Codex Desktop 正在执行的会话返回 `idle`，也能依赖持久化 item 阶段正确隐藏当前轮统计。
- **静态缓存**：dashboard 外壳缓存升级到 v25。

### 自绘会话尾部与工具行对齐 Codex Desktop
- **真实 token 用量**：接入 app-server 的 `thread/tokenUsage/updated` 通知，取 `tokenUsage.last` 回填当前轮 `in / out`，并继续只在整轮完成后显示。
- **指令状态块**：识别 `::git-commit{...}` 等 Codex UI 指令，以安全、紧凑的状态块显示，不再把协议文本原样露在回复中。
- **隐藏内部工具**：过滤 `node_repl · js` 内部传输调用，保留命令、文件、搜索及真实 MCP 工具，视觉行为与 Codex Desktop 一致。
- **静态缓存**：dashboard 外壳缓存升级到 v24。

### 会话统计信息仅在轮次完成后显示
- **完成后显示**：实时 assistant 分段完成时不再提前显示模型、思考强度、token 与日期；只有收到整轮完成事件后才出现。
- **固定轮末**：统计信息锚定在该轮最后一个消息、工具调用或文件改动之后，避免后续工具行把统计信息留在回复中间。
- **历史兼容**：已持久化的历史轮次仍显示统计信息；dashboard 外壳缓存升级到 v23。

### 放大 Codex 停止生成图标
- **停止方块**：自绘聊天运行态按钮内的停止方块从 SVG `8×8` 放大到 `12×12`，外部圆形按钮尺寸保持不变。
- **原生图形**：停止标识继续使用内联 SVG `<rect>`，不依赖字体或 Unicode 字形，跨平台渲染一致。

### 移动端聊天输入框适当下移
- **安全区间距**：输入框底部间距从“固定 10px + iPhone 安全区”改为取两者较大值，保留 Home Indicator 避让，同时避免重复叠加造成输入框明显偏高。
- **静态缓存一致性**：dashboard 外壳缓存升级到 v22，确保移动端刷新后获取新布局。

### 修复 Codex 迁移会话数量再次缩少
- **根因**：后续聊天功能合并时，SQLite 查询意外恢复了旧的 `thread_source != subagent` 条件，把 Codex Desktop 仍可见的迁移任务再次过滤掉；MacBook Air M5 的 `aibaji_work_assistant` 因此从 20 条缩成 6 条。
- **修复**：SQLite 初筛不再使用不可靠的 `thread_source`，继续以 `archived=0`、`source=vscode` 和 rollout `originator=Codex Desktop` 为准；真实子代理仍由对象型 `source` 排除。
- **回归保护**：新增真实 SQLite 扫描测试，同时写入普通任务和 `thread_source=subagent` 的迁移任务，确保两者都出现在 fleet 会话列表。

### 会话标题栏与空态使用白色背景
- **标题层级**：右侧会话标题栏明确使用白色表面色，与聊天内容区形成清晰分层，同时保留原有底部分隔线。
- **空态一致性**：未选择会话时，右侧空态内容区使用相同的白色表面色，不再透出浅灰工作区底色。
- **静态缓存一致性**：dashboard 外壳缓存升级到 v21，确保已安装 PWA 刷新后获取完整背景样式。

## 2026-07-22

### 修复 Codex 自绘发送按钮叠加停止图标
- **默认状态**：提高停止图标隐藏规则的选择器优先级，避免被全局 SVG 显示规则覆盖；空闲时只显示发送箭头，运行时才切换为停止方块。
- **回归保护**：补充样式契约测试，覆盖默认隐藏和运行态显示两种状态。

### Codex 自绘聊天支持运行态停止与追问队列
- **真实运行态**：消费 thread/turn 事件并在恢复会话时读取 Codex thread 状态；生成期间发送键切换为 Codex 风格圆形停止按钮。
- **追问队列**：运行中发送的文字和图片按会话 FIFO 排队，当前轮完成后自动发送；支持直接引导当前轮、编辑回填和删除，失败不丢队列项。

### 调小 Codex 工具行图标
- **视觉协调**：自绘聊天里的工具调用图标从 18px 调整为 14px，并略降线宽；保留原 20px 对齐盒子，避免影响工具行文本基线和行高。

### Codex 自绘聊天显示每轮消息元信息
- **用户消息时间**：本地新发送消息立即记录发送时间；历史用户消息保留 app-server item 原始时间字段，气泡底部显示 `用户：YYYY-MM-DD HH:mm:ss`。
- **AI 回复统计**：助手消息从实时 delta / completed / turn_done 事件中归一化模型、思考强度、token in/out 与完成时间，按 `AI：model, effort  |  in N / out N  |  YYYY-MM-DD HH:mm:ss` 展示。
- **历史字段保留**：fleet-agent 的 Codex 历史投影不再只保留 text/images，而是保留原始 item 字段，避免历史消息的时间、usage、model 等元信息丢失；已重建 darwin amd64/arm64 agent 产物。

### 会话工具默认切换为 Codex
- **Codex 优先**：会话工具切换项改为 Codex 在前、Claude 在后；首次打开或没有当前窗口可恢复时默认加载 Codex 会话。
- **保留恢复语义**：已有当前终端快照时仍恢复该窗口实际使用的 Claude / Codex，不强行覆盖用户正在使用的工具。
- **静态缓存一致性**：dashboard 外壳缓存升级到 v14，避免旧页面继续使用 Claude 默认值。

### Codex 自绘聊天展示完整工具调用
- **实时与历史一致**：命令执行、MCP / 动态工具、网页搜索、图片查看与生成、等待及多代理调用均进入自绘消息流；开始时显示运行中，完成后更新成功或失败状态。
- **紧凑工具卡片**：默认展示工具图标、名称、调用摘要、耗时和状态；参数、进度与输出按需展开，长内容限制在卡片内滚动。
- **静态缓存一致性**：dashboard 外壳缓存升级到 v13，避免新工具事件模型与旧渲染脚本混用。

### 修复 Codex 活跃列表混入 340 条 CLI 历史记录
- **来源过滤**：读取 `state_5.sqlite` 时仅保留 `source=vscode` 且 rollout `originator=Codex Desktop` 的未归档线程，排除 `cli`、`exec`、`codex_vscode` 与子代理记录。
- **异常分组消失**：Mac Mini M4 上路径为 `/`、标题类似 `status/status` 的 340 条五月 CLI 记录不再出现在 Codex「活跃」列表。

### Codex 自绘聊天吸顶显示当前轮输入
- **当前轮提示**：最后一条用户输入完全滚出会话区上沿后，在顶部靠右显示其第一行；滚回原消息时自动隐藏，长文本保持单行省略。
- **跳底对齐**：「跳到底部」按钮改为跟随输入框顶部和右边缘，不再贴住页面右侧；输入框增高及桌面 / 移动布局下均保持同一对齐基准。
- **聚焦样式**：消息输入框获得焦点时不再显示内层矩形描边，由外层输入坞统一提供聚焦反馈。
- **静态缓存一致性**：dashboard 外壳缓存升级到 v12，四个自有静态资源使用同步版本化 URL，避免新 DOM 与旧静态资源混用。

### Codex 文件改动显示文件清单与增删行数
- **文件清单**：自绘聊天中的“文件改动”卡片不再显示占位文案，改为逐行列出实际文件路径。
- **行数统计**：根据 app-server 返回的逐文件 unified diff 统计新增、删除行数，分别用绿色 `+N` 与红色 `-N` 展示；不展开具体代码改动。

### Codex 自绘聊天加载历史并支持模型 / 审批选择
- **历史恢复与上翻分页**：打开自绘 Codex 会话后恢复最近 40 条可显示消息并自动定位到底部；滚动到顶部时继续拉取更早记录，前端按 item ID 去重并保持当前阅读位置，不因 prepend 跳动。
- **滚动竞态修复**：初始定位与 prepend 锚点使用即时滚动，恢复完成后才连接 SSE，避免平滑滚动从顶部经过加载阈值时误触连续上翻、自动灌入整段历史并停在中间。
- **兼容迁移长会话**：优先使用 app-server 原生 `thread/items/list`；当前 Codex 版本尚未实现该方法时，自动回退 `thread/turns/list`，把单个迁移 turn 内的 item 切成不透明 cursor 分页。回退页使用 32 页有界快照缓存，重新打开会话即刷新，避免上翻时反复展开完整长 turn。
- **输入选项**：输入框新增审批模式与模型选择；模型列表来自 app-server `model/list`，不支持时隐藏。保持当前会话默认值，只有用户主动更改后才把 `model` / `effort` / `approvalPolicy` / `sandboxPolicy` 覆盖传给 `turn/start`。
- **历史投影**：持久化的用户消息、助手消息、命令执行与文件变更统一投影为 dashboard 事件；过滤环境/权限等注入消息，并保留带文件前言的真实用户请求。
- **静态缓存一致性**：Service Worker 外壳缓存升级到 v7，把 `chat_model.js` 纳入 network-first 强制重新校验，并让 `chat_model.js` / `app.js` 使用同一版本化 URL；即使旧 Worker 尚未更新，也不会出现新 `app.js` 搭配浏览器旧 reducer 导致自绘聊天启动失败。

## 2026-07-21

### 修复 MacBook Air M5 的 Codex 迁移会话被错误隐藏
- **问题**：Codex Desktop 已迁移的顶层会话会保留 `thread_source=subagent`，但 `source` 仍是普通字符串 `vscode`。fleet-agent 的 SQLite 适配同时按 `thread_source=subagent` 和“创建后活动超过 1 分钟”过滤，导致 Desktop 侧可见的 `Emoji / Deploy / TTS / API / Upload / Ranking` 等会话在 dashboard「活跃」列表中消失，只剩 3 条。
- **修复**：恢复与 Codex Desktop 一致的判定：`archived=0` 即活跃；字符串 `source` 的迁移会话保留，只有对象型 `source` 的真实 guardian/worker 子代理被排除。补充回归测试覆盖短时迁移会话与无后续活动的未归档会话。
- **来源隔离**：字符串 `source=vscode` 也可能来自 VS Code Codex 插件，不能单独视为 Desktop 会话。SQLite 扫描现在读取 rollout 首条 `session_meta.originator`，排除明确的 `codex_vscode`，同时保留 `Codex Desktop` 迁移会话与已在 Desktop 中可见的 `codex-tui` 会话。

## 2026-06-30

### dashboard 主机设置：Mesh IP 复制按钮改为 Ping 延时探测
- **去掉复制按钮**：主机设置弹窗里 Mesh IP 右侧不再显示复制图标，改为一个可点击的 Ping 延时 chip。
- **打开即探测**：弹窗首次打开时先拉 `/mN/api/info` 显示 Mesh IP，随后对同一台 Mac 的 `/mN/api/health` 做一次真实控制链路 RTT 探测（浏览器 → 网关 → mesh → fleet-agent），结果显示为 `xxms`。点击数值会立即变成 `...`，直到新探测完成再回填。
- **软底颜色**：`≤80ms` 绿色，`≤200ms` 黄色，`>200ms / 失败 / 超时` 红色；失败显示「失败」，5 秒未返回显示「超时」。纯 dashboard 静态改动，已部署网关。

### 修复「重载会话」横幅在纯 CLI 会话上误报
- **问题**：网页终端打开的会话（只有自己这个 CLI 在写）也时不时弹「桌面端有新内容 / 重载会话」。根因——`evalWatch` 用 DAG 父子拓扑判「Desktop 外部写入」：新追加行的 `parentUuid` 不等于当前跟踪的叶子 `tip` 就判外部。但 claude CLI 自身就会因 **api_error 重试 / 消息编辑 / 上下文压缩** 产生合法分叉（同一父节点多个子节点、文件里多叶子），拓扑法把这些当成 Desktop 写入。实测本地 40 个纯 CLI 会话有 **24 个被误判**；截图里出问题的 `0b50106f` 会话近 40 行全是 `cli` 写入、零 `claude-desktop`，仍弹横幅。
- **修复**：改用 jsonl 行的 `entrypoint` 字段直接区分写入端——`cli`＝本网页终端，`claude-desktop`＝Claude Desktop 等外部客户端。`evalWatch` 仅当新追加行 `entrypoint` 非 `cli` 时才判外部写入。缺省 / 空 / 其它格式（Codex rollout 无此字段）一律不误判，保持 Codex「从不弹」的原行为。顺手移除只服务于旧拓扑法的 `watcher.tip`。新增 `watch_test.go` 覆盖：纯 cli 分叉不报、出现 claude-desktop 行才报、external 粘滞。改 main.go → 已重建 dist 双架构并部署三台 Mac。

## 2026-06-29

### 修复 Codex 会话列表：只列 desktop app 活跃会话、消除「未知项目」
- **不再列归档 / 命令行 / 子代理会话**：之前以 `session_index.jsonl` 全量（含已归档）为源，且无 cwd 的会话堆成「未知项目」。改为扫 `~/.codex/sessions/`，只保留同时满足以下条件的会话——精确对应 Codex desktop app 的活跃集：
  - `originator == "Codex Desktop"`（排除命令行 `codex-tui` / `codex_exec` 运行——mac1 实测有 340 条 CLI 运行）；
  - `source` 为字符串（如 `"vscode"`；subagent 子代理线程的 `source` 是对象，如并行子任务「重做大对话图」的 worker，排除）；
  - rollout 不在 `~/.codex/archived_sessions/`（即未归档）。
- **标题/项目**：标题优先 `session_index` 的 `thread_name`，否则取首条「非注入」user 文本首行（跳过 `<environment_context>`/`# AGENTS.md`/`# Files mentioned by the user`/权限说明等注入消息）；cwd 取自 `session_meta`，故不再出现空 cwd 的「未知项目」分组。实测会话数：mac2 48→5（与 desktop 活跃集一一对应）、mac1 342→7。已重建 dist 双架构并部署三台 Mac。

### 修复 Codex auto 权限模式参数
- **auto 补 `--sandbox workspace-write`**：之前 codex auto 只给 `--ask-for-approval never`，但不设沙箱时默认 `read-only` → 模型既不能写文件、又不弹审批，实际跑不动。改为 `--ask-for-approval never --sandbox workspace-write`（自动批准 + 工作区可写、越界/网络受限）。`bypass` 不变（`--dangerously-bypass-approvals-and-sandbox`）。dashboard auto 按钮 title 同步。改 main.go → 已重建 dist 双架构（待部署三台 Mac）。

### 修复 Codex 会话列表（以 desktop app 为权威源）
- **不再把每个原始 rollout 当会话列出**：`scanCodexSessions` 之前 glob `~/.codex/sessions/**/*.jsonl` 把数百个一次性 CLI 运行全列成会话，标题还错取注入的 `# AGENTS.md`/env 文本（如「zsh 2026-05-14 Asia/Shanghai」）。改为以 Codex desktop app 的 `~/.codex/session_index.jsonl` 为权威列表（真实 `thread_name` 标题），用本地 rollout 的 `session_meta` 补 `cwd`（无则退 `.codex-global-state.json` 的 workspace hint）。实测会话数从 349→48、标题恢复为真实线程名。
- **顺手清理**：rollout 文件名即含完整 uuid，`jsonlPathFor/jsonlPathsFor` 从「逐个读文件取 id」改为文件名解析；`codexFileMeta` 精简为只取 `cwd+mtime` 且 cwd 到手即早停。改 main.go → 已重建 dist 双架构（待部署三台 Mac）。

## 2026-06-28

### 棕点（等待你回复 / 选择）退出机制：会话列表轻量轮询
- **问题**：棕点（`s.waiting`）后端判定本是动态的——jsonl 末条 `assistant + stop_reason==tool_use` 即「等你答 AskUserQuestion / 授权」，你答完末条变 `user.tool_result`，`sessionWaiting` 立即重算为 `false`。但前端会话列表**从不定时刷新**：`loadSessions` 只在手动刷新 / 切台 / 切 mode / 关会话后才重拉，`watchTimer` 只管重载 banner。于是答复后棕点 DOM 停在旧 `wait`，一直亮、不退出。
- **修复**：新增 `refreshSessionsSoft()` 每 5s 静默拉一次会话，**只就地** toggle 各行棕点、更新相对时间与计数，不 `clear` 重建整列表（避免把 hover 的路径 tooltip、冷会话展开的「连接/Bypass/Auto」按钮周期性闪断）；会话**增删**（结构变化）时才回退全量 `loadSessions`。`init` 挂 `setInterval(refreshSessionsSoft, 5000)`，函数自带 `mode/macId` guard。答复 / 授权后最多 5s 棕点自动消失。纯前端改动，不动 fleet-agent / dist。

## 2026-06-26

### 设置弹窗分组化 + 终端自动关闭时长端到端可配 + 刷新后终端恢复
- **设置项包进「终端设置」分组**：弹窗 body 外包一层 `.set-group`（组标题「终端设置」），现有桌面 / 移动小节归入其下，为以后加别的设置组留位（`.set-group + .set-group` 自带分隔线）。
- **「自动关闭」设置真正驱动各 Mac 后台回收（默认 30 分钟，范围 1～1440）**：`fleet-enroll` `dashSettings.autoCloseMinutes`（`normalize` 钳 [1,1440]、缺省回退 30）→ 新增公开只读端点 `GET /enroll/agent-config` 投影 `idleSec`（nginx 加无 `auth_request` 的 location）→ 各 Mac `fleet-agent` 启动 + 每 5min 拉取（`configSync`，地址由 plist 注入的 `FLEET_CONFIG_URL` 给出，由 `FLEET_UPDATE_BASE` 推导）→ 运行时原子更新 `idleSec`，既有 `reaper()`（`attached==0 && 空闲>idleSec` → `kill-session` 释放 pty）读它。网关不可达 / 旧网关无端点 → 保留 `FLEET_IDLE_SEC` 本地默认，全局最终一致、故障不停摆。
- **撤销前一版无效的前端 `poolReapIdle`**：那只 detach 当前页 iframe、不 kill 后台、刷新即失效——真正的空闲回收一直是 agent `reaper()`，上面把它的时长接成网页可配。
- **刷新 / 崩溃后自动恢复终端**：池变化时把 `[{macId,sessionId,permMode,title,cwd}]` 写 `sessionStorage`（`poolAdd`/`poolDrop` 内），`init` 时逐条重连恢复（受 `poolMax` 约束，复位到刷新前的当前会话、不抢焦点）。用 `sessionStorage` 故关标签即清，不会哪天恢复一堆陈年旧窗口。与 agent `reaper()` 正交：恢复后会话重新 attach 不被回收；没恢复的孤儿无 attach 到点被回收，pool 不爆。
- **绿点会话点行直接进入**：有运行中进程（行尾绿点 `s.pty`）的会话点行即直接重连 / 瞬时切换，不再展开三权限模式按钮——`ensureTmux` 对已存在 tmux 是复用、权限模式启动时已固定，三按钮只对「冷会话」有意义。

### 连接权限模式：新增 Auto（三按钮 连接 / Bypass / Auto）
- **未在池的会话行展开三种权限模式**：`连接`（普通，逐项确认）/ `Bypass`（红，`--dangerously-skip-permissions`）/ `Auto`（琥珀，`--permission-mode auto`——自动批准 + 后台安全分类器，介于二者之间）。已在池的会话点行即瞬时切换，无按钮。
- **fleet-agent**：`/api/open|new` 的 `bypass` 布尔泛化为三态 `mode`（default/bypass/auto），`permFlag` 白名单映射（不放行任意 `--permission-mode`，`normMode` 把 plan 等收敛成 default），兼容旧前端 `bypass:true`。`watcher`/reload 沿用 mode。终端头按模式显 `⚠ 跳过权限` / `⚡ Auto` 徽标。改 main.go → 已重建 dist 双架构并升级三台 Mac。
- **rail 模式 tab 图标**：`Claude会话` 用 Claude 官方 logo（Bootstrap claude，描边填充），`文件` 用 file-text 文档图标。

### 工具栏图标 → 规整内联 SVG
- 刷新 / 重连 / 全屏 / ⓘ / ⋮ / 返回 / 设置 / 主题 / 退出 / 复制 / 发送 / 新建 / 关闭 / 空状态大图标 等 Unicode 字形换成 Feather 风格内联 SVG（统一 `.ic` 描边样式），放大到 17–18px，不再细小。品牌 logo 与 keybar 键标签保留文字。

### 终端 iframe 池（切换丝滑 + 上限可调）
- **单 iframe「导航式」终端 → iframe 池**：每个打开过的会话一个常驻 iframe，全尺寸叠放在 `#frames`，靠 `.show`（`visibility` 而非 `display:none`——后者会把 iframe 尺寸塌成 0、ttyd 把 pty resize 成 0×0、Claude TUI 排版炸掉）显隐切换。切换 = 改 class，瞬时；池内会话全程保持实时、不掉线。`#frame` 仅留给「文件」模式。
- **超上限按「最后收到输出时间」LRU 释放（排除当前窗口）**：包 `term.write` 记每个窗口的最后收到输出时刻；超上限时释放非当前窗口里最早的那个——关 iframe → WS 断 → tmux detach → 释放 1 个 pty，后台 Claude 进程不受影响，再点回来重新 attach。真实 Chrome 实测：单终端 iframe 边际成本 ~2.1MB JS 堆（+ 几 MB GPU 后备），浏览器内存非瓶颈，真正天花板是系统 pty 池。
- **选中已打开的会话即瞬时切换**：点会话行时若该会话已在池中，直接显示，不必再点「进入连接」。
- **设置弹窗（存网关、所有浏览器共享）**：user / ⋮ 菜单加「⚙ 设置」，4 项——桌面 / 移动 各设「最大终端窗口数」「每窗口最大回滚行数」（默认桌面 10、移动 4，回滚 5000）。回滚行数经 xterm `term.options.scrollback` 即时生效。存储走 `fleet-enroll` 新增 `/settings` 端点（原子写 `dashboard-settings.json`，服务端 `normalize` 钳制+缺省回退），nginx 加 `/api/settings`（Authelia 保护，镜像 `/api/names`）。
- 终止当前终端会话后从池移除并回空态（不再停在 `[exited]/Press ⏎ to Reconnect`）。

## 2026-06-25

### 网页终端体验
- **终端配色跟随 dashboard 深/浅色**：ttyd 把 xterm 实例挂在 iframe 的 `window.term`，按 `data-theme` 给 `term.options.theme` 套深/浅两套配色（取自 `style.css` 设计 token），切换实时生效、每次终端加载/重连后重套用。无需改 ttyd 启动参数。
- **网页终端可往上翻历史**：`fleet-agent` 经 `tmux -f ~/.macfleet-tmux.conf new-session` 加载 `history-limit 50000` + `mouse on`（原默认 mouse 关闭，网页里滚轮/上滑进不了 copy-mode，只能看一屏）。conf 由 agent 启动时自写。副作用：桌面端拖选复制改为 Shift+拖选；移动端无影响。改 `main.go` → 已重建 dist 双架构。
  - *修正*：初版用「建会话前 `set-option -g`」在**冷启动**（空闲会话全回收、tmux server 已退出）时哑火——无 server 可 set、空 server 又因 `exit-empty` 自杀；改用 `-f conf` 在 server 启动时加载，冷/温都生效（单测钉死 `-f` 须在 `new-session` 之前）。

### 品牌
- 新 app 图标（`icons/icon.svg`）：品牌渐变圆角砖（`#6e8bff→#9b7bff`）+ 两张等大会话窗叠层 + 品牌色提示符，体现「多 Mac 多终端会话」；矢量，各尺寸锐利。补 `<link rel="icon">` 浏览器标签页 favicon、manifest 加 `maskable`、`theme_color`/`background_color` 对齐 `#090c12`。

### dashboard
- **主机设置代理框填默认值**：未配置时 HTTP/HTTPS 代理框直接填入真实默认值 `http://127.0.0.1:7897`（而非仅 placeholder），避免「看着像填了、实际存成空 → 开关 on 但不注入代理 → claude 403」的陷阱。

### dashboard 移动端
- **终止按钮改 SVG**：原 Unicode `⏹` 在 iOS 渲染成彩色 emoji，换成内联 SVG 实心圆角方块（`currentColor`，跟随悬停变红）。
- **软键盘弹起顶起输入坞**：VisualViewport 算键盘高度 → `--kb`，移动端输入坞上移到键盘之上（修 iOS 输入框被键盘遮挡）。
- **禁页面缩放 + 全宽不溢出**：viewport 加 `maximum-scale=1, user-scalable=no, interactive-widget=resizes-content`；`html,body` 加 `overflow-x:hidden`。

### dashboard 交互优化
- **运行态会话行：静态绿点 + hover 才出停止按钮**：已起 fleet 进程的会话行，正常态显示一个**静态绿点**（`--online`，不脉冲，表示进程在跑）；鼠标 hover 时切换成**红色停止按钮**（露出 ⏹ SVG）。停止按钮边框由原圆角方块改为**圆形**（`border-radius: 50%`）。纯 CSS（`.stopbtn` 用 `::before` 画绿点、hover 互斥显隐绿点/图标）。
- **会话行去掉常驻状态点**：默认不显示行首点；仅「等待你回复 / 选择」的会话显示**棕色点**（由 fleet-agent `waiting` 信号驱动，见下）。行首点位**恒定留出**（无点时透明占位，标题统一对齐）；行距收紧；棕色点**静态不脉冲**。
- **分组折叠箭头**改用清晰的内联 SVG chevron（原 Unicode `▾` 太淡）。
- **「终止 ⏹」按钮**改由真实进程状态驱动（fleet-agent 新增 `pty` 字段）：仅对**已起 fleet 进程**的会话显示，且**不选中也显示**；未起进程的不显示。
- **已连接的会话**再选中时按钮显示「**进入连接**」，点击仅回到已有终端、**不重连、不重载**（tmux 进程持久）。
- 去掉「选择项目目录」（新建会话）弹窗里无意义的 `+` 图标。
- 会话列表刷新改为 stale-while-revalidate：仅空列表显示骨架，刷新已有内容不再闪。
- 修复「每次点击都闪一下」：`$$('[data-mode]')` 误把带 `data-mode`（CSS 切栅格用）的 `#app` 根容器选中并挂上 `setMode` onclick，导致点页面任意处都冒泡触发重渲染；收窄为 `button[data-mode]`。

### fleet-agent
- `GET /api/sessions` 每个会话新增 `pty` 字段：按 `tmux` 实况标记该会话是否已有 fleet 进程（供前端显示「终止 / 进入连接」）。
- `GET /api/sessions` 每个会话新增 `waiting` 字段：读 jsonl 尾部，最后一条是 assistant 且 `stop_reason==tool_use` → 卡在「等你回答/授权」（AskUserQuestion 待答或工具待授权）。供前端显示棕色点。按 mtime 缓存，仅瞬态（工具执行中/子 agent）可能短暂为真。

## 此前

- **dashboard 重构 + F1–F4**：连接 / Bypass连接（`--dangerously-skip-permissions`）、终止进程（`POST /api/close`，会话保留）、登录有效期 30 天（Authelia）、退出登录（`/auth/logout`）。
- **fleet-agent 自管理子命令**：`update / start / stop / restart / status`；pty 耗尽精确提示（503 + 可读 message）。
- Web 域名迁移到独立子域（`fleet.example.com`）；mesh 控制面与 web 子域解耦。
