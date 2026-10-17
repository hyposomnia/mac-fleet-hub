# 原生待机小组件

2026-10-17。实现位于 `iOS/FleetWidgets`、`iOS/Shared`、`mac/fleet-agent/daily_usage.go` 和 `server/enroll/multiuser/standby.go`。不依赖 cccost 的安装或在线服务。

## 使用

1. 更新网关 Go 服务，并通过项目唯一签名构建机的正式流程发布支持 `/api/usage/daily` 的 fleet-agent。旧 agent 会显示需升级，不能把旧版本的 404 当作今日零用量。仅更新网页不足以启用统计。
2. 在 Xcode 的 FleetHub 与 FleetWidgets 两个 target 选择同一开发 Team，为二者启用同一 App Group：`group.com.macfleet.FleetHub`。如果修改应用 bundle ID，也要同步修改 entitlements 和 `StandbyStorage.group`。安装包含 Widget 扩展的新 iOS App。
3. 在 App 中连接 HTTPS 网关并登录。App 自动申请专用只读凭据并同步，不让用户另填访问密钥或复制 Cookie。当前实现不为 HTTP 开发网关启用组件联网。
4. iPhone 打开系统“设置 → 待机显示”，充电横放后长按小组件区域，添加 Fleet AI 的“活动与未读会话”和“时钟与模型用量”，左右各一个。也可添加到主屏幕。系统支持、常亮能力和唤醒行为以设备实际能力为准。
5. 点击整个钟面切换横排 24 小时 `HH:mm`／指针；无“数字／指针”文字按钮。两个组件均支持 small、medium，StandBy 使用 small。

待机由两个系统原生组件组成，而不是接管整个待机屏。small 会话组件最多同时显示三条，轮换查看其余；medium 增加项目名，small 优先保障标题与设备名可读。用量组件显示总数、模型明细、设备覆盖数及数据时间。没有模型记录时不编造模型；没有任何电脑成功上报时显示 `—`，而不是把离线当零。部分可用时显示“完整上报台数 / 总台数”。

## 当天模型用量

手机传入 IANA 时区；所有宿主按同一时区的自然日 `[00:00, 次日00:00)` 统计，而不是各自按本地时间直接相加。DST 用自然日边界，不固定加 24 小时。跨日不继续展示昨天的数值为“今日”。

| 来源 | 读取位置与去重 | token 口径 |
| --- | --- | --- |
| Claude | `FLEET_CLAUDE_HOME/projects` 内 JSONL，包含子代理；message ID + request ID 去重，流式块取完整用量而非重复相加 | 未缓存输入 + cache read + cache creation + 输出 |
| Codex | `FLEET_CODEX_HOME/sessions` 与 `archived_sessions` 的 rollout，含 Desktop、CLI 和子代理；session + 累计计数去重 | 优先累计计数增量，首条/兼容记录使用 last usage；缓存输入已包含在输入，reasoning 已包含在输出 |
| DeepSeek Desktop | `FLEET_DSH_HOME/sessions` 的 JSONL / `session.v4.jsonl.zstd`，包括子代理；session + turn + step 去重 | 未缓存输入 + cache read + 输出；chunk/message 对同一请求不重复计数 |

按 assistant + 原始 model ID 聚合；未知模型归入 `unknown`，不猜测名称或单价。统计所有已记录模型，不只是 Fleet 当前选中的客户端、项目或会话。不同 assistant 的同名模型保留来源边界，避免把不同口径的提供商错误合并。此处是实际记录的 token，不是费用、套餐余额或账单；日志已删除、供应商未落盘的使用无法还原，不把它解释为提供商全账号账单。

同一 Mac 的 active/archive、重复消息与 shared 多观察端不会重复累加；不同 Mac 视为独立执行来源。不要把同一套会话日志复制到多台 Mac 后再作为独立用量来源统计——跨宿主的复制日志目前不做全局去重。读取权限遵循 agent 运行账号；不越权扫描 Mac 上其他系统用户的 home。其他客户端及自定义日志布局需增加明确适配器，不宣称已覆盖任何任意程序。

agent 按文件 mtime/size 与统计日缓存。超过扫描上限、不可读、解压失败、超时都返回 partial；并发查询等待受请求截止时间约束。超大非计费正文记录可流式跳过，不因附件阻断后续 token 记录。每文件解压后上限 512 MiB、计费行上限 16 MiB、单文件当日事件上限 20 万；触限明确不完整。

## 接口与授权

- Mac：`GET /api/usage/daily?timezone=Asia%2FShanghai`，沿用逐设备代理认证。返回 date、timezone、generatedAt、models、totalTokens、partial、warnings；不返回聊天正文或本地日志路径。
- 网关：`GET /api/standby?timezone=Asia%2FShanghai`，查询当前 owner 的所有 active 设备。每次最多并发四台，总请求有截止时间。设备状态分别为 ok、partial、unavailable、upgrade-required；离线设备不从覆盖清单消失。会话查询复用原有列表、分页与网关共享未读状态，仅投影标题、设备、项目和状态，不返回正文或完整 cwd。最多返回 60 条用于显示的活动/未读会话。
- App：`POST /api/standby/token`，必须有完整登录、Origin 和 CSRF。只读能力凭据绑定签发它的浏览器会话；不能请求设备代理、文件、终端、账号管理，也不能写入或续期登录。最长 30 天且不超过签发时登录期限。
- Widget：`Authorization: Bearer <standby-token>` 仅能 GET `/api/standby`。每次查询前后检查会话、账号状态，设备响应也需通过归属/节点绑定复核。管理员仍只能看到自己的设备。退出、撤销登录、禁用账号后不能继续查询。

App Group 只保存专用能力凭据和最小展示快照，不复制 Cookie、密码、CSRF 或聊天正文；原子写入、排除备份、使用首次解锁后文件保护。跨进程锁与账号代际检查阻止旧请求在切换账号后回写数据。退出／换网关／换账号清除共享状态并请求 WidgetKit 重载。会话标题和模型用量标记 privacySensitive，由系统隐私显示策略决定锁定时可见性。

远程撤权立即使服务器查询失败，但系统已渲染画面的移除时机受 WidgetKit 控制；不承诺服务器能瞬时抹去屏幕上的旧快照。请求收到 401/403 时清除本地状态；离线时只保留当天且不超过 30 分钟的快照，并显示原更新时间，不把失败包装成实时数据。

## 系统刷新与设计差异

WidgetKit 不运行网页的永久定时器或滚动容器。组件请求约 15 分钟后的下一次联网刷新，最终由 iOS 调度；不能承诺每分钟联网。App 活跃时最多每分钟同步一次。缓存内容通过一分钟一个 timeline entry 整行轮换，不是网页 8 秒/420ms 的动画。指针按 timeline 时间绘制，无秒针；数字时间使用同一时间线。

系统 small 尺寸无法无损容纳网页完整双列大画布；会话与时钟/用量拆成两块，medium 用量组件内部左右排。待机背景、放大与低光着色由系统处理，遵循 containerBackground 和 privacySensitive，不套网页手机外框。

会话辅助时间当前表示最近活动距今，不冒充当前轮次持续时长；既有列表未暴露准确轮次开始时间。后续若要显示“已持续”，须从真实运行时补充 startedAt 并验证，不能用会话创建时间或 mtime 假代。

## 验证与发布边界

已在本地完成 App 与 Widget 扩展模拟器编译、iOS 单元测试，以及统计去重、缓存、压缩日志、跨日/DST、账号隔离、令牌范围、退出失效等自动测试。另使用本机真实日志做只读统计检查，不修改正在运行的 agent。

生产网关、所有宿主新版签名公证 agent、iOS 真机签名安装、App Groups 权限和真实锁屏 StandBy 的刷新/隐私/能耗还需要正式发布与设备验收。源码、模拟器构建与本地日志读取不等于这些已上线。

Apple 参考：[StandBy 支持](https://developer.apple.com/documentation/widgetkit/adding-standby-and-carplay-support-to-your-widget)、[时间线与刷新](https://developer.apple.com/documentation/widgetkit/keeping-a-widget-up-to-date)、[交互式组件](https://developer.apple.com/documentation/widgetkit/adding-interactivity-to-widgets-and-live-activities)。
