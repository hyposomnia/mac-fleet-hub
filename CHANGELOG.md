# Changelog

## 2026-08-27

- 新增真正的 shared WebSocket 路径：Mac 默认由 `com.macfleet.codex-app-server` 启动唯一一份 `features.code_mode_host=true` app-server，只监听 `ws://127.0.0.1:47682`；fleet-agent 与 Desktop 显式连接 `ws://127.0.0.1:47682/rpc`，不再依赖会被 Desktop config overrides 绕开的 `CODEX_APP_SERVER_USE_LOCAL_DAEMON` 分支，也不再为 shared bootstrap 官方 control-socket daemon。
- shared 安装通过 Aqua one-shot LaunchAgent 写入 `CODEX_APP_SERVER_WS_URL` 并取消旧 local-daemon 开关，但绝不打断活动 turn；旧 agent/plist/keeper/helper 纳入可恢复迁移，loopback readyz、监听地址和 agent health 全部通过后才提交。正式 release 会从同一不可变 bundle 执行现有节点迁移，用 `open --env` 重开 Desktop，并逐台验证 Desktop/Fleet 两条连接落在同一 server PID。监听地址仅允许 `127.0.0.1`；`isolated` 保留为显式回退。
- shared LaunchAgent 用 ChatGPT 自带的 OpenAI 签名 Node keeper 启动 app-server，维持 `node keeper → codex server → node codex_app MCP` 的受信父子链；keeper 读取目标 Desktop 的 `desktop-mcp.json`，只认 ChatGPT 主进程持有且权限为 `0600` 的 App Tools pipe，并通过稳定软链承接重启后的 UUID 轮换。Desktop 必须用 macOS 原生 `open --env CODEX_APP_SERVER_WS_URL=...` 确定性重开；仅设置 launchctl 环境后从旧 Dock/Finder 进程启动不算完成。
- 真实 Desktop UAT 证明 ChatGPT/Codex 当前版本会为本地 host 注入 `features.code_mode_host` / App MCP 配置，因此不满足 `CODEX_APP_SERVER_USE_LOCAL_DAEMON` 的共享分支条件；即使 GUI 环境变量为 `1`，Desktop 仍启动独立 app-server。Fleet 打开同一 thread 后会让 Desktop 报“已在另一个应用中打开”。
- Mac 安装与 fleet-agent 无显式环境变量时恢复默认 `isolated` / Desktop share `0`；`shared` 代码保留为实验选项。4 台生产节点已回滚到独立 sidecar，并停止不再使用的 managed daemon，避免残留 idle writer lock。
- 修复 `setup-mac.sh` 在 `set -u` 下把紧邻全角中文标点的未加花括号变量解析成错误变量名，导致 shared 迁移版本校验异常；新增静态回归测试禁止同类写法。

## 2026-08-26

- fleet-agent 自更新下载总超时由 60 秒提高到 5 分钟，允许家庭网络公网回环偶发低吞吐时完整下载约 8 MB 的统一签名产物，避免传输接近完成时被客户端主动取消。
- 恢复 Codex shared daemon 为 Mac 安装与 fleet-agent 的默认连接方式：Fleet 和完全退出后重开的 Codex.app 复用官方 managed daemon；`isolated` 保留为旧版 Desktop 与特殊环境的显式兼容回退。
- shared 安装在未显式指定 Codex 路径时优先使用 ChatGPT.app bundled CLI，以独立 release 目录和原子 `current` symlink 安全更新完整 standalone package（含 code-mode host、rg、zsh 与 package manifest；同版本残缺目录也会重建，旧式 current 目录保留可恢复备份），再执行官方 `daemon bootstrap --remote-control`；package 升级时会预告 bootstrap 将重启 daemon，bootstrap 后仅当 `managedCodexVersion` 与运行中的 `appServerVersion` 仍不一致时额外 restart，避免 Desktop 新版连接旧 daemon，也避免同版本任务被无谓打断。
- 从 isolated 迁移时会 unload 旧 `com.macfleet.codex-app-server.plist` 并移到 LaunchAgents 外的可恢复备份；managed release、bootstrap 或版本校验失败时，迁移会原子恢复原 `current`（兼容 symlink、旧式目录和原先不存在三种状态）、还原 plist 并重新 load sidecar。成功后备份保留；安装只设置 `CODEX_APP_SERVER_USE_LOCAL_DAEMON=1`，不会自动重启 Codex.app，并明确要求用户 Cmd+Q 后重新打开。
- 收紧 shared 的逻辑 turn 所有权：同一 daemon 的未知 turn 一律视为 Desktop 发起，只有 Fleet 自己成功 `turn/start` 返回的同 turn 才能 steer、停止或处理审批；权限预设仅缓存并随下一次 Fleet `turn/start` 生效，避免无 expected-turn 的 `thread/settings/update` 跨到 Desktop 新 turn。提前到达的通知会保守等待响应纠正，shared 空闲 resume 不再伪造 Fleet writer。
- shared 模式不再提供或接受进程级“强制接管”，避免把双方共用的 managed daemon 当外部 holder 杀掉；真正的外部 turn 只安全排队。isolated 接管同时修复 terminal/idle 后残留 `writerOwner=desktop` 的竞态，并在投递失败时保留真实错误原因。

## 2026-08-22

- fleet-agent 双架构发布产物统一使用固定 `com.macfleet.fleet-agent` identifier 和 Developer ID Application 证书签名，启用 hardened runtime 与可信时间戳；构建会自动选择唯一发布 identity 并严格验签，且拒绝误用 Apple Development，避免自更新后因代码身份变化重复申请 macOS 隐私权限。
- 新增签名构建机专用的一键发布入口：从干净且同步的 master 开始，自动完成测试、签名公证、发布提交、网关分发备份替换、公网 SHA 校验及全部 Mac 滚动更新和健康验收；真实基础设施清单仅保存在构建机私有配置中。

## 2026-08-21

- 修复 HTML 预览内相对图片、视频和音频因沙箱为不透明源而无法携带同站鉴权的问题；预览仍禁用脚本、表单、连接和嵌入内容，同时支持 `srcset`、`poster`、`source`、字幕轨、`file://` 本地路径及 CSS 资源改写。独立媒体预览补充 SVG/HEIC/HEIF、Ogg/MPEG 视频、Opus/AIFF 音频，并继续用 Range 请求流式播放；Dashboard PWA 缓存升级到 v121。

## 2026-08-20

- 修复 Fleet turn 在审批或工具链断线后因 rollout 永远没有 terminal 记录而永久占用 Codex writer：isolated sidecar 现在把 rollout 最后进展作为 30 分钟租约，超时后先中断 turn、再 `thread/unsubscribe`；若旧连接无法释放，仅在没有仍有进展的其他 Fleet turn 时重启 sidecar。控制快照同步携带待处理请求数量，Dashboard 在标题与输入区上方持续显示“等待你审批或回答”，避免任务暂停被误画成普通“正在进行”；PWA 缓存升级到 v120。
- 修复 isolated Fleet sidecar 浏览空闲 Codex 会话时，权限切换对尚未加载的 thread 调用 `thread/settings/update` 并报 `thread not found`：空闲会话现在缓存审批模式，真正发送并恢复 thread 后随 `turn/start` 生效；同步重建双架构 fleet-agent 分发产物。

## 2026-08-19

- 修复 iPhone 从后台回到 Fleet 会话时，同一轮实时消息与历史补偿因服务端 `itemId` 不同而重复显示：前端现在按客户端消息 ID，并在同一 turn 内按内容与出现顺序对账；同步升级 Dashboard PWA 外壳缓存到 v113。

## 2026-08-13

- 修复 isolated 模式“强制接管”重启 Fleet sidecar、却没有释放 Desktop/CLI 实际 writer 的方向性错误：接管审计现在只列出真实外部持锁或持有 rollout 的 Codex 进程，确认后向这些外部 holder 发送 TERM、超时才升级 KILL，并等待目标会话的物理 writer 信号消失后才重新投递；Fleet 自己的 sidecar 与任务不再被误杀。直接 `codex exec resume` 没有 lock 文件时同样可被审计和接管。
- 修复外部 Codex writer 异常退出后 unfinished rollout 被长期误判为仍在运行的问题：isolated 模式以目标 Mac 的真实 writer lock、非 Fleet rollout 文件持有进程，或本 fleet-agent 进程已确认的同 turn Fleet 租约作为占用证据；详情控制快照、只读恢复、实时对账、会话列表、队列投递与 30 秒服务端回收使用同一判断。外部/未知 owner 无活进程时清除幽灵占用并允许 Fleet 重新取得 writer；直接 `codex exec resume` 不建 lock 文件仍能识别为外部 writer；新建 Fleet thread 首轮没有 lock 文件时仍保持真实运行态；进程探测异常则保守保持只读。结构化 app-server 错误不再显示 `[object Object]`，成功重试完成后清除瞬态错误；Dashboard PWA 缓存升级到 v117。
- 继续收紧 Codex 服务端权威控制面：控制快照新增审批模式与逐队列项合法动作，队列 worker 在 steer/start 前应用消息绑定的审批设置；浏览器仅渲染服务端动作、按状态版本解除按钮锁，并在控制快照超过 10 秒未刷新时进入不可写重连态。补正 `read_only + Fleet writer` 的“尚未释放”界面和释放重试，避免误报 Codex 已可接管；Dashboard PWA 缓存升级到 v116。
- 补齐 Codex 会话控制面的统一版本快照：`resume`、queue 与 access 响应现在一次返回 access、真实 writer、turn phase/owner 和完整可见队列，并用进程 epoch + 单调版本拒绝乱序浏览器响应。浏览器不再根据 SSE、history、submitting 或队列项推断/清除 writer 和运行态；实际 ChatGPT Desktop 持锁进程、terminal rollout 陈旧缓存、只读附件上传也纳入服务端校验。Dashboard PWA 缓存升级到 v115。
- 将 Codex Fleet 写入权、消息投递与 steer/next 队列收口为服务端持久化状态机：浏览器只提交消息意图并投影服务端状态，不再用 localStorage、SSE 断线或本地 writer 判断驱动投递；队列支持 CAS、崩溃恢复与不确定态，15 种状态都有明确界面和合法动作。Fleet 持有 writer 时标题栏提供“释放会话”，服务端先持久化只读、停止当前 turn、再 `thread/unsubscribe`；后台每 30 秒扫描真实锁与 rollout 回收残留 writer，必要时由 launchd 重启隔离 sidecar。Dashboard PWA 缓存升级到 v114。
- 修复 Codex 消息中的音频、视频 Markdown 媒体预览被错误渲染成破损图片、播放器请求被 fleet-agent 拒绝的问题；现在按文件类型显示原生播放控件，媒体端点支持白名单内的音频/视频与 Range 请求，图片预览行为保持不变。Dashboard PWA 缓存升级到 v113。
- 修复 Fleet 自己持有 writer 时把下一条消息误画成右侧“接管/排队”状态卡：当前 turn 的后续消息恢复到输入框上方的持久化 follow-up 队列，继续支持 steer、排队和取消；只有 Desktop/VS Code 等外部 writer 才在用户消息下显示接管操作，刷新页面后仍按服务器保存的 writer 归属恢复正确样式。
- 修复 Codex 会话列表误报“等待回复”以及进入会话后看不到确认内容：该状态现在只由 Fleet 实际持有、可在页面响应的未决请求触发，`chat/resume` 会同步重放请求详情；审批自动完成或 SSE 重连时不会因陈旧/重复事件留下假状态。
- 修复 Codex 工具活动组在实时输出刷新 DOM 时自动收起的问题；用户手动展开后会按会话和活动组保持展开，直到用户主动收起。

## 2026-08-12

- 队列状态拆分为等待当前 Fleet turn 的 `waiting_turn` 与等待外部进程 writer 的 `waiting_writer`：前者只提供取消排队并在当前任务结束后续投，只有后者显示强制接管。
- 修复页面刷新后把服务器审计用的 `sent` / `cancelled` 队列项重新生成用户消息和状态卡；终态记录不再参与 UI 恢复，成功消息只由 Codex history 展示一次。
- 已成功发送的服务器队列项不再在普通对话下方长期显示“已发送”状态框；记录仍保留在目标 Mac 用于幂等和审计。
- 修复 fleet-agent 更新或重启后把 Fleet sidecar 正在执行的 turn 误报成“目标 Mac 本地 Codex 占用”：现在按 `thread-writer-locks` 的实际持锁进程和 Unix socket 恢复 writer 归属，Fleet 自己的任务继续支持 steer/停止，只有默认 daemon 持锁才进入外部占用队列。
- 修复强制接管后被陈旧 rollout 运行态挡住、又静默退回等待的问题：已授权接管的队列可直接使用 Fleet sidecar 已取得的 writer；再次冲突则显示明确失败并允许重新尝试。处理期间立即禁用全部操作按钮，状态文案明确说明会话在目标 Mac 本地被占用。
- Codex 追问与 writer 冲突消息改由目标 Mac 的 fleet-agent 持久化排队和后台投递；关闭浏览器、切换终端或刷新页面不会丢失或中断队列，旧 localStorage 队列会幂等迁移。
- 用户消息下方新增服务器状态卡，可继续排队、取消、重试或请求强制接管。若目标机器存在活跃任务，首次操作只展示影响清单，二次确认后才允许中断该机器默认 daemon 上全部受影响任务。
- 强制接管在执行前重新核对审计版本，串行重启默认 app-server，并持久记录失败状态；Dashboard PWA 缓存升级到 v105。

## 2026-08-11

- 修复从会话内打开文件预览后，顶部返回按钮错误跳到会话列表的问题；同站内打开时现在返回原会话，直接访问预览链接时仍回退到 Fleet 首页。Dashboard PWA 缓存升级到 v104。
- 将 Codex Desktop 设为同一 thread 的最高优先入口：Fleet 默认改用独立常驻 app-server sidecar，浏览与实时同步仅读取 history / rollout，不再通过 `thread/resume` 抢占 Desktop writer。
- Desktop 持有 writer 时，Fleet 明确显示只读状态；用户输入需二次确认并持久化排队，等 Desktop 切换或关闭会话后自动重试。Fleet 不跨 app-server steer、停止或审批 Desktop turn。
- Fleet 仅在真正发送时尝试取得 writer；自己的 turn 结束后立即 `thread/unsubscribe` 释放。安装脚本同步部署私有 Unix socket sidecar，并关闭 Desktop 共享 daemon 环境开关。
- Dashboard PWA 缓存升级到 v103；补充隔离连接、writer 冲突、只读恢复、队列交互和完成即释放的回归测试，并重建 Darwin amd64/arm64 agent。

## 2026-08-10

- 修复网关静态文件被旧版 `app.js` 混版覆盖后仍沿用 v103 缓存键的问题：PWA 外壳升级到 v104，确保重新加载外部 writer 只读界面与“尝试获取控制权”操作。
- 增加 Codex 外部 writer 只读模式：当 Desktop、VS Code 或另一 app-server 正在控制会话时，Fleet 不再显示原始 resume 错误，而是继续展示并同步历史与运行输出；输入、附件、审批和模型设置明确禁用，并提供“尝试获取控制权”操作，writer 释放后可原地恢复读写。
- 修复 Fleet 与本机 Codex.app 通过两个独立 app-server 抢占同一 thread writer 的问题：安装和 fleet-agent 启动会启用 Codex Desktop 内置的本机 daemon 连接开关，App 重启后与 Fleet 共享默认 Unix control socket；已用两个独立 WebSocket 客户端验证同一 daemon 可同时 resume 同一会话。自定义 Codex home/socket 与显式 `stdio` 模式保持隔离，并可通过 `FLEET_CODEX_DESKTOP_SHARED_DAEMON=0` 关闭。
- 修复 Codex 会话在最后一个网页观察者离开后仍被 fleet-agent 永久订阅的问题：现在会调用 app-server `thread/unsubscribe` 释放该连接的占用，正在执行的 turn 不受影响；网页与 Codex Desktop 同时打开时仍保持共同订阅，可继续协同操作同一会话。
- 修复聊天「跳到底部」悬浮球只有半透明、没有真实毛玻璃的问题：解除输入区对它的 backdrop 隔离，让球体能实际模糊背后的聊天内容；桌面 hover 仅轻微上浮 1px 并略微加深。
- 修复会话按最近排序时后台轮询整列重建导致的持续闪烁；现在按会话 ID 对比，仅更新、增删或移动发生变化的项目，并保持列表滚动位置。

mac-fleet-hub 变更记录（日期为本地时间）。

## 2026-08-09

### 会话列表时间改为输出结束时间
- **时间语义**：会话列表右侧相对时间现在显示「会话输出结束时间」（rollout 末条 `task_complete` 的时间戳），不再用 Codex state DB 里 turn 结束后不更新的 recency；无 rollout 的会话（首轮前/已归档）回退原时间。
- **双链路一致**：app-server 主链路（`thread/list`）与 SQLite/rollout 回退链路都返回 `outputEndedAt`；dashboard 列表渲染与软刷新优先读取。
- **回归保护**：新增 rollout 输出结束时间尾解析与 ListThreads 上报测试；dashboard 会话行测试断言「20 秒前优先于 11 分钟前」。
- **连接态收敛**：保持连接的自绘会话不再画灰色空心圈（省 25px 占位），改为标题加粗表示。

## 2026-08-07

### 明确项目与会话的列表层级
- **会话内收**：项目分组下的会话在桌面端和移动端统一向内缩进，选中态背景随条目一起收拢，与一级项目标题形成清晰层级。
- **缓存升级**：dashboard 外壳缓存升级到 v100，确保已安装 PWA 获取新的列表样式。

### 修复 iOS 自绘会话恢复与首次发送
- **前台恢复补偿**：H5 从其他 App 返回时主动替换可能假存活的 SSE，并通过 `chat/resume` 合并后台期间遗漏的输出与当前 turn 状态；同时覆盖普通可见性恢复和 BFCache `pageshow`。
- **首次轻点发送**：键盘展开时保留聊天输入框焦点，避免 iOS 因 VisualViewport 位移吞掉发送按钮的第一次点击。
- **回归保护**：新增前台恢复、历史补偿、实时流重建和移动端发送焦点测试。
- **缓存升级**：dashboard 外壳缓存升级到 v99，确保已安装 PWA 获取修复。

## 2026-08-05

### 对齐 Codex Desktop 的项目与会话归属
- **合并主仓与 worktree**：fleet-agent 读取 Codex Desktop 的本地项目和线程绑定状态，用稳定项目根目录归组；会话仍保留真实执行目录，恢复与继续执行不受影响。
- **收拢无项目会话**：Desktop 标记为无项目的快速会话统一显示在“无项目”分组，不再把临时工作目录伪装成多个项目，也不提供错误的项目内新建入口。
- **隐藏内部子 Agent**：未绑定本地项目、带父线程或对象型子 Agent 来源的内部线程不再混入普通列表；明确绑定项目的 worktree 与历史迁移会话继续保留。
- **回归保护**：新增 fleet-agent 与 dashboard 归组测试，覆盖主仓/worktree 合并、无项目聚合和内部子线程过滤。
- **缓存升级**：dashboard 外壳缓存升级到 v98，确保已安装 PWA 获取新的会话分组逻辑。

## 2026-08-03

### 修复 Codex control socket 被旧 CLI 路径遮蔽
- **根因修复**：fleet-agent 在配置的 Codex 可执行文件已失效时，不再直接返回空会话；只要当前用户的 Unix control socket 仍可用，就继续复用现有 Codex app-server。
- **无安装兼容**：Codex 可执行文件与 control socket 都不存在时仍返回空列表，不会误触发 app-server 恢复或 fleet-agent 重启。
- **回归保护**：新增“旧 CLI 路径失效但 control socket 可用”的接口测试，确保本地历史线程继续显示。

### 放大移动端会话设置操作按钮
- **整行双列操作**：移动端会话设置抽屉的取消与保存按钮铺满内容宽度，各占一半并提升到 48px 触控高度；其他弹窗保持原样。
- **缓存升级**：dashboard 外壳缓存升级到 v96，确保已安装 PWA 获取新的按钮布局。

### 移除 iOS 独立模式的底部裁剪
- **修正根画布高度**：`html` 与 `body` 使用完整的 large viewport 高度，避免百分比高度把会话列表、聊天输入区和全屏图片查看器裁在 iOS WebClip 的 894pt 布局视口内。
- **安全区自然留白**：页面内容自然铺满 956pt 屏幕，不添加遮罩、伪元素或底部定位色块；输入区通过透明的布局 padding 避开设备圆角，键盘打开时继续由 VisualViewport 只移动会话窗口。
- **全屏查看图片**：大图查看器占满整个屏幕，图片以 `contain` 完整显示，关闭按钮悬浮在右上角安全区内。
- **缓存升级**：dashboard 外壳缓存升级到 v97，确保已安装 PWA 获取根布局修复。

### 修复 iOS 聊天底部与键盘错位
- **移除错误延伸**：删除把聊天窗口推到根视口之外的负 `bottom`，输入框不再被 iOS 裁掉；`html`、`body` 与会话窗口的真实画布连续使用聊天背景，不增加遮罩、伪元素或定位补色层。
- **跟随真实可视区**：兼容 iOS 26 键盘同时缩小 `innerHeight` 与 VisualViewport、并产生 `offsetTop` 的行为；键盘打开时整段会话窗口按 VisualViewport 的位置和高度固定，头部不再钻进状态栏，输入框完整停在键盘上方。
- **缓存升级**：dashboard 外壳缓存升级到 v94，确保已安装 PWA 立即获取修复。

### 恢复按视口宽度切换响应式布局
- **根因纠正**：桌面端异常来自 Chrome DevTools 设备模拟固定在 `401px`，不是浏览器恢复窗口时报告了旧宽度。
- **撤销错误判定**：响应式布局重新只依据 `860px` 视口断点；移除指针类型条件，避免桌面设备模拟时把三栏桌面外壳挤进窄视口。
- **缓存升级**：dashboard 外壳缓存升级到 v95，确保已安装 PWA 获取纠正后的断点规则，同时保留 v94 的 iOS 聊天画布修复。

### 整理全局设置入口
- **菜单收口**：Web 侧栏入口改为“设置”，桌面与移动端一级菜单统一为主题、归档会话、会话设置和退出登录；文件页自动禁用归档入口。
- **会话设置分层**：归档开关移出弹窗，弹窗只保留终端与自绘页签；Codex 自绘界面对新用户默认开启，明确关闭的本机偏好继续保留。
- **安装入口独立**：PWA 安装动作移到独立提示条，不再占用常驻设置菜单。
- **缓存升级**：dashboard 外壳缓存升级到 v91，确保已安装 PWA 获取新的菜单结构与偏好逻辑。

### 移除搜索框内部的重复焦点圈
- **单层焦点反馈**：会话搜索与文件搜索不再继承全局输入焦点阴影，只保留搜索框外壳的蓝色焦点提示。
- **缓存升级**：dashboard 外壳缓存升级到 v90，确保已安装 PWA 获取新的搜索框样式。

## 2026-08-02

### Codex 执行生命周期脱离 fleet-agent
- **独立 daemon**：Mac 安装时通过 `codex app-server daemon bootstrap --remote-control` 配置 pid-backed 独立进程；fleet-agent 直接通过当前用户私有 Unix control socket 的 WebSocket transport 连接，不再把实际 app-server 作为自己的子进程。
- **更新不中止 turn**：fleet-agent 或其 socket 客户端更新、退出、重启时，正在运行的 Codex turn 继续留在 daemon；网页 SSE 重连后自动执行 `thread/resume`，重新挂载事件流和活动 turn。
- **兼容与可控回退**：`FLEET_CODEX_APPSERVER_MODE=auto` 默认优先 daemon、不可用时回退旧 stdio；`daemon` 可强制禁止回退，`stdio` 可显式保留旧行为，并支持自定义 control socket。
- **回归保护**：新增 daemon/socket 启动选择、Unix WebSocket framing、真实 managed socket、回退、严格模式、`CODEX_HOME` 环境覆盖和 RPC 重建后 thread 重载测试；同步重建 fleet-agent Darwin amd64/arm64 产物。

### 文本预览增加行号与语法高亮
- **只读代码视图**：文本类文件使用本地内置的 CodeMirror 渲染，每个逻辑行显示稳定行号，长文件按可视区域更新 DOM。
- **按格式高亮**：支持 JSON、JavaScript/JSX、TypeScript/TSX、Go、Python、Ruby、Shell、YAML、TOML、XML、CSS 与 INI/CONF/ENV；普通文本、日志和 CSV 保留无高亮行号视图。
- **换行兼容**：沿用原有自动换行开关与本机偏好，换行后的续行不重复编号，深浅主题均使用高对比度语法配色。
- **离线可用**：CodeMirror 核心与语言模式全部随 dashboard 部署并进入 PWA 外壳缓存，不依赖运行时 CDN。
- **缓存升级**：dashboard 外壳缓存升级到 v89，确保已安装 PWA 获取新的代码预览器。

### 修复移动端聊天安全区露底
- **真正根因**：iOS PWA 中固定聊天窗口停在底部安全区上沿，露出了页面根背景；此前只修 VisualViewport 键盘偏移并没有消除这段灰色“下巴”。
- **窗口自然延伸**：仅在自绘聊天打开且软键盘关闭时，让聊天窗口本身延伸至屏幕底边；不添加遮罩、伪元素或背景补色。
- **键盘仍可顶起**：检测到真实软键盘后立即取消安全区延伸，沿用现有 VisualViewport 位移将输入框顶到键盘上方。
- **缓存升级**：dashboard 外壳缓存升级到 v88，确保已安装 PWA 获取修正后的窗口边界。

### 文本预览支持自动换行
- **标题栏开关**：文本类文件在预览标题栏显示自动换行按钮，完整预览页也提供同一入口，Markdown、图片、音视频等类型不显示。
- **即时且持久**：切换时直接更新当前正文，不重新加载文件，并在本机记住选择供后续文本预览沿用。
- **缓存升级**：dashboard 外壳缓存升级到 v87，确保已安装 PWA 获取新的预览控件与样式。

### Codex 双源消息使用稳定逻辑身份
- **根因修复**：实时 / rollout 消息使用 `msg_…` 原生 ID，而 app-server 恢复历史会生成 `item-N` 合成 ID；主动对账不再把这两个不稳定 ID 当成两条消息。
- **合法重复保留**：稳定身份由回合、消息阶段、正文和同文案出现序号组成，同一回合中模型有意发送两条相同内容时仍会完整保留。
- **竞态收口**：历史恢复期间到达的完成事件会在进入 SSE backlog 前完成同一套对账，覆盖实时流、周期同步、断线恢复和历史水合交汇。

### 移除移动端聊天底部遮罩
- **不再补色遮盖**：删除会话窗口的 `chat-open` 背景补色与 composer 底部实色带，输入框直接贴近真实可视区底部。
- **只响应真实键盘**：VisualViewport 位移先扣除无键盘时的浏览器栏 / 安全区基线，并忽略不足 120px 的小幅变化；键盘收起后底部归零，弹起时仍完整顶起输入框。
- **位置下移**：移动端 composer 底边从 6px 收到 4px，不额外叠加安全区间距。
- **缓存升级**：dashboard 外壳缓存升级到 v86，确保已安装 PWA 获取修正后的布局与键盘逻辑。

### PWA 外壳更新自动接管
- **不再停留旧版本**：新版 Service Worker 完成外壳预缓存后立即激活，现有页面自动刷新；页面初始化也会主动检查更新，避免服务端已发布但客户端仍运行旧脚本。

### 会话列表直接同步 Codex 执行状态
- **无需进入详情**：fleet-agent 会从每个 Codex rollout 的 `task_started` / `task_complete` / `turn_aborted` 生命周期补齐列表状态，进行中的会话首屏即显示“正在进行”。
- **轮询自动收口**：rollout 解析按文件增量缓存，列表每 5 秒刷新时只读取新增内容，任务完成后会自动恢复为普通时间显示。

### 会话与文件共用设备选择入口
- **图标统一**：会话页不再显示层叠图标，与文件页一样显示 `M1/M2…` 设备头像，全部设备汇总态显示 `ALL`。
- **组件复用**：两个页签由同一个设备范围按钮组件生成，共用结构、样式和打开选择器的交互。
- **缓存升级**：dashboard 外壳缓存升级到 v85，确保已安装 PWA 同步获取新组件。

### 文件页返回手势只移动列表区
- **表头以上保持固定**：移动端在文件目录内从左缘右滑返回时，目录标题、面包屑、搜索框和“名称”表头全部固定，只让文件行与底部计数跟随手指。
- **返回语义不变**：横纵轴判定、短滑回弹和 `history.back()` 目录历史保持原逻辑，纵向滚动不会误触返回。
- **缓存升级**：dashboard 外壳缓存升级到 v84，确保已安装 PWA 同步获取新的手势边界、脚本和样式。

### 移动端底部收口与附件大图预览
- **列表自然到底**：会话列表不再二次为系统安全区扩大内边距，去掉末尾多余空块，仅保留常规滚动间距。
- **键盘偏移跟随焦点**：只在聊天或终端输入框聚焦时应用 VisualViewport 键盘高度，失焦后立即归零，不再把 iOS 浏览器栏误认为键盘。
- **聊天底色统一**：自绘聊天打开时统一窗口、面板与输入区底色，输入框下移并贴近可视区底部。
- **附件大图**：用户附件、待发送缩略图、工具图片和 Markdown 图片共用全屏预览，支持遮罩、关闭按钮和 Esc 关闭。
- **缓存升级**：dashboard 外壳缓存升级到 v81，确保已安装 PWA 获取新的布局与图片预览行为。

### 修复手势返回后会话列表冻结
- **移除递归观察链**：不再通过 `MutationObserver` 监听 `term-open` 后反复改写同一个 class，避免返回列表后主线程陷入微任务循环。
- **统一关闭入口**：切换设备、切换文件页和终止当前会话时都走同一套手势状态清理，避免残留位移或动画状态。
- **缓存升级**：dashboard 外壳缓存升级到 v80，确保已安装 PWA 立即获取热修脚本。

### 移动端 Web 外壳强化原生手感
- **页面稳定**：固定根视口并禁止页面级回弹露底，统一浏览器文字调整，同时继续保留双指缩放能力。
- **关闭双击放大**：Dashboard 与独立文件预览统一使用 `touch-action: manipulation`，避免连续点击触发页面放大，不影响单指滚动和双指缩放。
- **主题色同步**：站内切换深浅主题时同步浏览器 `theme-color`，状态栏不再只跟随系统主题。
- **返回手势跟手**：移动端会话详情右滑返回会跟随手指移动，短滑回弹，完成滑出后再进入会话列表。
- **缓存升级**：dashboard 外壳缓存升级到 v79，确保已安装 PWA 获取新的视口与手势行为。

## 2026-08-01

### Thinking 改为回合级派生状态
- **事件与视图解耦**：reasoning item 继续作为内部事件保存，不再逐项映射为消息行；统一 reducer 按真实事件顺序维护当前回合阶段，界面只读取该派生状态。
- **天然保持单例**：无论一个回合产生多少 reasoning item，页面最多只渲染一个“正在思考”；正文、工具或计划成为最新阶段后自动清除。
- **请求没有重复**：改动不触碰 Codex 请求次数或 reasoning 事件协议，只修正前端状态建模。
- **缓存升级**：dashboard 外壳缓存升级到 v77，确保已安装 PWA 获取新的回合状态模型。

### Thinking 状态随 reasoning 事件即时显示
- **减少首字等待感**：收到 Codex reasoning 事件后立即在消息流显示动态“正在思考”状态，不再等到工具活动或回复正文出现。
- **不暴露内部推理**：界面只展示运行状态，不渲染 reasoning 摘要或内部思考内容；后续可见输出到达后状态自动让位。
- **避免状态闪断**：reasoning 已完成但当前回合仍在运行时继续保留提示，直到正文、工具或计划出现。
- **缓存升级**：dashboard 外壳缓存升级到 v76，确保已安装 PWA 获取新的即时反馈。

### 项目内新建入口改为加号
- **语义更直接**：项目标题右侧的新建入口由“/”改为“+”图标，项目归属继续由所在分组表达，点击后仍立即进入该项目的空白会话详情。
- **缓存升级**：dashboard 外壳缓存升级到 v75，确保已安装 PWA 获取新的入口图标。

### 新建会话改为项目就地入口与延迟连接
- **工具栏顺序调整**：移动端排序按钮移到搜索框左侧，排序与无项目新建按钮固定为同尺寸方形控件。
- **入口职责明确**：顶部“+”只新建无项目会话；每个项目标题右侧的独立入口直接在该项目内新建会话，不再弹出项目选择列表。
- **先进入再连接**：Codex 新建操作会立即打开空白会话详情，首次发送内容时才连接 app-server、创建真实线程并提交消息，减少点击后的等待感。
- **草稿附件兼容**：真实线程创建前选择的图片保留在本地，首次发送时绑定到新线程后再上传。
- **缓存升级**：dashboard 外壳缓存升级到 v74，确保已安装 PWA 获取新的结构与交互。

### 精简设备选择并修正移动端会话行对齐
- **设备列表直接展示**：移除设备选择弹窗中的“搜索设备”输入框及过滤逻辑，小规模设备列表打开后可直接选择，也不再自动唤起移动端键盘。
- **会话文字垂直居中**：紧凑的移动端会话行改为纵向居中，单行标题不再贴近行顶部。
- **弹窗进一步收窄**：去掉搜索框占位及设备列表前的额外间距，让设备选择弹窗更紧凑。
- **缓存升级**：dashboard 外壳缓存升级到 v73，确保已安装 PWA 同步获取结构与样式更新。

### 移动端设备选择器并入标题栏
- **入口上移**：会话页和文件页的设备选择器移到“会话 / 文件”标题与设置按钮之间，移除原先单独占用的一整行。
- **窄屏收缩**：选择器改为 38px 高的紧凑单行控件，隐藏次要状态文案；长设备名在控件内省略，标题和设置按钮保持不换行、不重叠。
- **缓存升级**：dashboard 外壳缓存升级到 v72，确保已安装 PWA 获取新的移动端标题布局。

## 2026-07-31

### 移除移动端列表的多余底部留白
- **列表自然收口**：会话列表没有底部输入框，不再预留固定的 18px 输入区间距；仅在带系统安全区的设备上保留必要空间。
- **缓存升级**：dashboard 外壳缓存升级到 v71，确保已安装 PWA 获取修正后的列表布局。

### 移动端会话详情支持右滑返回
- **边缘返回手势**：会话详情从屏幕左缘向右滑可返回会话列表，聊天和 ttyd 终端区域均可触发。
- **防止误触**：仅响应足够距离且横向明确的右滑，短滑、纵向滚动和反向手势不会返回。
- **历史一致**：右滑与左上角返回按钮统一使用页面历史记录，不结束后台 tmux 或 Codex 会话。
- **缓存升级**：dashboard 外壳缓存升级到 v70，确保已安装 PWA 获取移动端手势。

### 修正文件位置重复高亮
- **唯一选中项**：当前目录同时属于多个收藏位置时，只高亮路径最具体的一项；进入桌面及其子目录不再同时高亮用户主目录。
- **保留位置上下文**：进入收藏位置的下级目录后，该收藏仍保持选中，不退化为只能匹配目录根节点。
- **缓存升级**：dashboard 外壳缓存升级到 v69，确保已安装 PWA 获取修复后的位置状态。

### 修复 Fleet 输入框被后台终端抢焦点
- **阻止后台聚焦**：隐藏的 ttyd iframe 在初次连接或 WebSocket 重连时不再覆盖 Fleet 页面当前焦点。
- **保留终端体验**：当前可见终端仍可正常聚焦；用户正在编辑搜索、聊天或文件控件时，终端重连不会打断输入。
- **缓存升级**：dashboard 外壳缓存升级到 v68，确保已安装 PWA 获取修复后的脚本。

## 2026-07-30

### 精简会话工具栏与文件操作入口
- **去掉冗余统计**：会话页移除“未归档”汇总行、分组状态文字和项目级会话数量，仅保留项目名称与路径入口。
- **移动端操作下移**：新建会话按钮移到搜索与排序工具栏，减少顶部标题区拥挤；最近更新排序启用时显示明确的高亮选中态。
- **文件设置归位**：文件设置入口从左侧导航标题移到右上角操作区，与上传和新建文件夹放在一起。
- **缓存升级**：dashboard 外壳缓存升级到 v67，确保已安装 PWA 获取本次布局与交互更新。

### 完全访问即时接管当前任务审批
- **根因修复**：运行中的任务仍持有启动时的审批策略；切换“完全访问权限”后，fleet-agent 现在会立即记录会话意图，不再等下一轮才生效。
- **自动放行**：完全访问模式下，当前任务后续产生的命令、文件改动和权限扩展请求由服务端直接批准；切换前已经挂起的审批也会立即解除，普通问题与 MCP 表单仍交给用户回答。
- **缓存升级**：dashboard 外壳缓存升级到 v65，并明确提示当前任务后续审批会自动允许。

### 会话状态改用右侧文字提示
- **单一状态信号**：会话等待用户操作时在标题右侧显示“等待回复”，运行中显示“正在进行”，替代原来的右侧绿色圆点。
- **移除重复圆点**：删除标题前的棕色等待点及其占位缩进；状态文字会替代相对时间，避免同一会话同时出现点、文字和时间三种提示。
- **缓存升级**：dashboard 外壳缓存升级到 v64，避开并行发布曾复用的旧 URL，确保已安装 PWA 获取新的会话状态样式。


### 强化移动端顶部当前页标识
- **选中项更明确**：顶部“会话 / 文件”当前项改为蓝色粗体并增加蓝色下划线，未选中项保持普通文字色；预留透明边线避免切换时产生高度跳动。

### 恢复 Codex Desktop 历史工具调用
- **修复真实缺口**：旧版 app-server 不支持 `thread/items/list` 时，`thread/turns/list(itemsView=full)` 会保留消息、reasoning 和 diff，却省略 Desktop Code Mode 的命令与图片查看；Fleet 现在从对应 rollout 按 `turn_id` 补回这些活动。
- **顺序与去重**：从 `custom_tool_call` 中恢复单命令、并行命令、图片查看及其它工具摘要，按 assistant commentary 边界插回原顺序；若 app-server 已返回同一原生工具 item，则不会重复显示。
- **长会话增量读取**：首次索引后只解析 rollout 新增字节，不反复扫描完整历史，也不缓存工具输出中的图片 base64；真实 42 MB 会话恢复后可返回 `commandExecution` 与 `imageView`。

### 文件类型使用专属图标
- **按类型识别**：文件浏览器按文件名、扩展名与 MIME 识别常见编程语言、媒体、文档、表格、演示、压缩包、数据库、字体与配置文件，三种查看方式共用同一映射。
- **开源本地资源**：采用 Material Icon Theme 5.37.0 的 SVG 图标并随 dashboard 本地发布，保留 MIT 许可，不依赖 CDN 或图标字体。
- **状态一致**：隐藏文件的彩色图标会同步灰化，未知类型回退到通用文档图标；dashboard 外壳缓存升级到 v60。

### 文件列表支持按时间和大小排序
- **表头排序**：文件列表的名称、修改时间和大小表头可点击切换排序；切到时间或大小时默认优先显示最新或最大的项目，再次点击反转方向。
- **目录优先**：无论选择哪种排序，文件夹始终位于文件之前，同值项目按名称稳定排列；排序不会改写接口返回的数据。
- **状态延续**：排序字段与方向保存在本地，并同步作用于列表、图标和分栏视图；表头箭头和无障碍状态会标明当前方向。
- **缓存升级**：dashboard 外壳缓存升级到 v59，已安装 PWA 会获取新的排序交互。

## 2026-07-29

### 修正移动端会话密度与输入框自动缩放
- **紧凑会话行**：移动端会话行最小高度由 68px 收至 44px，并压缩上下留白和元信息间距，避免列表显得过高。
- **输入不再触发页面放大**：移动端文本输入、文本域和选择框统一使用 16px 控件字号，避免 iOS Safari 聚焦时自动放大页面；viewport 继续允许用户双指缩放。

### 文件浏览增加图标、列表与分栏视图
- **三种查看方式**：文件工具栏新增图标、列表和 Finder 式分栏查看，默认保持列表，并记住用户上次选择。
- **分栏导航**：选择文件夹会在右侧载入下一栏，切换分支会截断旧栏；移动端使用整栏横向滑动并自动定位当前目录。
- **交互一致**：三种视图共用搜索、隐藏文件灰显、文件操作和预览逻辑；异步目录响应会校验当前设备与选中路径，避免旧请求覆盖新目录。
- **移动触控**：查看方式使用 44px 分段控件，文件上传、新建文件夹和设置同步扩大到 44px 触控区。
- **缓存升级**：dashboard 外壳缓存升级到 v56，已安装 PWA 会获取新的文件浏览界面。

### 隐藏文件使用灰色弱化显示
- **清晰区分**：开启“显示隐藏文件”后，隐藏文件与文件夹的名称、图标、修改时间和大小统一使用低对比度灰色，普通项目保持原样。
- **交互不变**：隐藏项目仍可正常打开、预览、重命名和删除，悬停及选中状态继续沿用文件列表交互。
- **缓存升级**：dashboard 外壳缓存升级到 v55，已安装 PWA 会获取新的隐藏文件样式。

### Codex 会话移除非原生 Thought 行
- **忠实显示**：内部 reasoning item 不再渲染为 `Thought`；用户可见的 commentary 正文、工具活动和文件编辑状态仍按原顺序显示，与 Codex Desktop 一致。
- **活动不丢失**：隐藏 reasoning 后，连续 tool / diff 仍参与原生活动摘要，不影响“已读取文件运行了多个命令”和“正在编辑文件”等实时状态。
- **缓存升级**：dashboard 外壳缓存升级到 v54，避免已安装 PWA 继续执行旧的 Thought 渲染逻辑。

### 文件预览增加信息区
- **上下分区**：文件预览栏改为上方显示内容、下方显示文件信息，保留完整预览与关闭动作。
- **原生式信息**：集中展示文件类型、大小、完整修改时间、所在位置和来源设备；主目录内路径以 `~` 简写，长路径可换行选择。
- **移动端适配**：全屏文件预览沿用同一结构，信息区限制高度并独立滚动，保留底部安全区。
- **缓存升级**：dashboard 外壳缓存升级到 v51，已安装的 PWA 会重新获取本次布局。

## 2026-07-28

### 新建 Codex 会话可立即选择模型
- **创建响应补全**：`chat/start` 直接返回新线程实际使用的模型、推理强度、速度和完整模型目录，不再依赖后续 `resume` 才能初始化选项。
- **首轮即可改选**：空白新会话进入后立即显示模型与推理强度；用户改选后，首条消息会携带所选参数。
- **缓存升级**：dashboard 外壳缓存升级到 v47，已安装的 iPhone PWA 会重新获取本次界面逻辑。

### 会话列表改为滚动自动加载
- **移除手动入口**：删除列表底部“加载更多”按钮，滚动距底部 240px 时自动请求下一页。
- **连续浏览**：首屏不足一屏时自动补页，追加期间显示轻量加载指示，并保持用户当前滚动位置。

## 2026-07-27

### 会话列表默认只显示活跃会话
- **入口收拢**：新建会话移到刷新按钮旁，列表头部不再展示活跃 / 已归档切换。
- **低频历史浏览**：设置新增“浏览历史 / 已归档会话”开关，默认关闭，并在本地记住用户选择。

### 聊天本地文件链接可安全预览
- **Fleet 预览页**：聊天回复中指向当前 Mac 的 Markdown、HTML、图片、视频和音频路径会改写为 `/view`，支持绝对路径、`~/`、相对会话 cwd 与文本行号后缀。
- **静态 HTML 隔离**：HTML 经 DOMPurify 清洗后放入无脚本 sandbox，本地 CSS/媒体资源通过受限接口加载，不把本地 HTML 作为 Fleet 同源页面执行。
- **文件边界与流式播放**：fleet-agent 新增预览/内容接口，解析符号链接后限制在 `FLEET_FILE_ROOT` 内；文本限 5 MB，音视频保留 HTTP Range 以支持进度拖动。

### Codex Desktop 长任务持续同步
- **跨进程增量对账**：SSE 保持连接时监控对应 rollout 的追加变化，只拉取最新结构化 item 并按内容指纹发布增量，Desktop 与 fleet 使用独立 app-server 时也能持续追上。
- **真实完成语义**：以 rollout 中同一 `turn_id` 的 `task_started`、`task_complete` 或 `turn_aborted` 判断生命周期，不再被独立 app-server 错报的 `idle/interrupted` 提前终止。
- **连接与去重**：多个订阅者共享同步 worker，最后一个连接断开或当前 turn 完成后停止；忽略 assistant 消息中持续变化的累计 usage，避免重放旧消息。

### 修正 iPhone 聊天输入框位置
- **去除重复安全区**：移动端全屏会话容器已由 iOS 避让底部系统区域，聊天输入框不再重复叠加 `safe-area-inset-bottom`，恢复为距内容区底边 10px。
- **缓存与回归**：dashboard 外壳缓存升级到 v43，并增加样式契约，防止输入框再次被底部安全区抬高。

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
