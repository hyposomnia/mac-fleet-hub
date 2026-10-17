# Fleet Hub iOS

最新 Web 交互差异与共用边界：[交互对齐记录](../docs/ios-web-interaction-parity.md)。
本轮 SaaS 与真机安装证据：[2026-10-15 UAT 记录](../docs/ios-saas-uat-2026-10-15.md)。
Duo 双状态与软键盘验证：[2026-10-16 适配记录](../docs/ios-duo-uat-2026-10-16.md)。

原生 SwiftUI 设备栏、会话栏与现有 dashboard 会话窗口组合。最低 iOS 17；普通 iPhone、宽屏展开状态和 iPad 共用一套实现。iPhone Duo 不依赖未经验证的机型名称、屏幕尺寸或铰链 API，按窗口实际可用宽度适配。

## 当前实现

| 可用宽度（pt） | 布局 | 设备栏 | 会话栏 |
| --- | --- | --- | --- |
| 至多 860 | H5 单栏 | 顶部设备选择 → 原生选择面板 | 原生列表与 Web 会话切换；列表页无多余会话栏按钮或图钉 |
| 861–1180 | Web 中等宽度三栏 | 220pt 原生侧栏，可收为 72pt 图标栏 | 310pt 原生侧栏，可独立取消固定、临时呼出 |
| 超过 1180 | Web 三栏 | 260pt 原生侧栏，可收为 72pt 图标栏 | 330pt 原生侧栏，可独立取消固定、临时呼出 |

布局变化只调整已有视图的尺寸。同一 WKWebView 保留 SSE、聊天草稿和 ttyd iframe；返回列表不结束 Mac 上的进程。单栏会话详情支持左边缘右滑返回：详情跟随手指移动并露出列表，距离不足时回弹，达到距离或速度阈值后完成返回；返回按钮沿用同一过渡。展开后沿用用户保存的两栏显隐偏好。键盘和动态字体使用系统布局，Web 继续处理输入与 VisualViewport；App 隐藏 PWA 安装提示和重复的 Web 导航。

设备在线状态、助手筛选（ChatGPT / DeepSeek）、搜索、项目分组、归档筛选、分页、新建会话、置顶和归档均由原生列表提供。Claude 暂不在 App 提供，长按菜单不再提供主动切换 ttyd。DeepSeek 鲸鱼图标打开系统浏览器中的 DSH；文字选项仍用于筛选会话。鲸鱼沿用 dashboard 中的官方 SVG 路径，授权记录见 `server/dashboard/icons/deepseek/LICENSE.md`。

设置分类在导航栏下使用轻量文字 Tab，支持横向滚动扩展；密钥列表只显示名称和最近使用时间，进入详情后提供范围、复制与编辑操作。会话设置在点击“完成”时保存，切换分类保留修改，保存失败时留在页面重试；设备编辑标题区域显示只读状态标签与 IP。设置、设备选择、设备编辑、密钥编辑、账号安全和 Sub Agent 使用系统 sheet；密钥详情通过原生导航进入，模型和权限使用系统 alert。编辑详情从父层叠加呈现，系统处理后退、下滑关闭、键盘和安全区域。文件操作与查看方式使用系统菜单；文件命名、会话重命名和删除确认使用系统 alert。设备列表支持拖动排序并保存到账号偏好，App 只提示在 Mac 浏览器打开当前服务地址新增设备。配色同时支持系统调色盘和十六进制输入。聊天正文、附件、文件预览保留 Web；系统文件选择、分享及确认仍使用原生系统界面。Skills 输入联想是行内建议，不作为模态抽屉。

## 构建

原生待机组件与跨 Mac 当日模型用量见 [WidgetKit 安装与统计说明](../docs/features/duo-standby/WIDGETKIT.md)。需安装包含 FleetWidgets 扩展的 App，为 App/扩展启用同一 App Group，并更新网关和正式签名公证 agent；旧客户端不会自动具备统计接口。网页原型不等于原生组件。小组件使用专用可撤销只读凭据，不提取 WebKit Cookie。

打开已经生成并入库的 `FleetHub.xcodeproj`，选择 FleetHub scheme 和 iPhone / iPad 模拟器。真机安装前在 Xcode 配置自己的开发 Team。

Duo 必须使用 Xcode 27.1 SDK 构建并选择 iOS 27.1 的 iPhone Duo。仅安装新 Xcode 而仍由旧的 `xcode-select` 路径构建，会继续使用旧 SDK 的兼容布局。可用 `DEVELOPER_DIR=/path/to/Xcode.app/Contents/Developer` 为构建和 `xcrun` 单独选用新工具链，不必修改系统默认值。Duo 的设备界面由新版 Device Hub 提供，不是旧 Simulator.app。

若修改项目定义，使用 XcodeGen 重新生成：

```sh
cd iOS
xcodegen generate
xcodebuild -project FleetHub.xcodeproj -scheme FleetHub \
  -sdk iphonesimulator -derivedDataPath /tmp/fleet-ios-build \
  CODE_SIGNING_ALLOWED=NO build
```

首次运行填写 HTTPS 网关根地址，可以带 NAT 端口。网关须先部署本分支的 `server/dashboard/` 全部资源，旧版本会明确提示尚未提供 App 接入组件。基础原生工作区无需为 UI 单独更新 agent；启用待机模型统计则需发布支持统计接口的新版 agent。

FleetAuth 登录、注册、两步验证、验证器绑定与恢复码流程使用 SwiftUI 原生控件。认证请求沿用网关 `/api/auth/*` 协议，通过 WebKit 隔离脚本环境提交；认证页的网页脚本禁用，网页不可见且不接受输入。密码、验证码和首次恢复码只在当前流程的内存中使用；原生层持久化网关地址和侧栏偏好，不提取网页登录 token、CSRF 或 Cookie。待机组件另存专用可撤销只读凭据与最小展示快照，不复用网页登录凭据。Cookie 留在 WKWebsiteDataStore，完成验证后同一 WebView 进入工作区；HTTPS 使用系统证书验证。原生桥仅接受已配置网关同源主文档的消息，终端地址还必须匹配选定设备路径。

## 共享代码

视觉基础见 `../docs/titanium-design.md`：原生栏与 Web 共用 `server/dashboard/titanium.json` 色彩 / 圆角，主题沿用 Web 偏好并经快照同步，App 图标使用同一 SVG 母版。

- `server/dashboard/fleet_core.js`：无 DOM 的节点解析、项目分组、会话查询、JSON transport 和 ttyd attach。
- `server/dashboard/workspace.js`：统一会话控制接口与版本化快照，复用 dashboard 的聊天及终端运行时。
- `server/dashboard/native_bridge.js`：WKWebView 消息传输；普通浏览器和子 frame 中不启用。
- `server/dashboard/native_dialogs.js`：将既有业务弹层投影为原生控件模型，复用输入、确认、选择与关闭处理；实时验证控件可见性、登录身份和控件 ID，普通 Web 不启用。
- `server/dashboard/native.css`：仅嵌入模式的布局覆盖；不改变网页正常布局。
- `FleetHub/Models.swift`、`FleetStore.swift`：原生快照模型、命令确认和生命周期。
- `FleetHub/SidebarViews.swift`：原生设备与会话组件。
- `FleetHub/FleetService.swift`：通过既有 WebView 内的 FleetAuth 复用 Cookie、CSRF 和账号代际检查；只允许明确的同源业务接口，不另存认证信息。
- `FleetHub/NativeAuthentication.swift`：原生认证表单、输入焦点与系统自动填充；使用现有服务的注册、登录、恢复和验证码接口，首次恢复码确认保存后才进入工作区。
- `FleetHub/NativeWebPresentation.swift`：原生文件菜单与系统输入、确认弹窗，操作仍交由当前 Web 业务运行时校验和执行。
- `FleetHub/NativeSettings.swift`、`NativeEditors.swift`、`NativeWebDialogs.swift`：原生设置、密钥和设备表单及系统 sheet 内容；密钥仅显示掩码，敏感复制限本机并设置两分钟过期。
- `FleetHub/FleetWebView.swift`：长期持有的 WebView、同源导航、预览；主窗口与辅助页面共用弹窗、下载和系统分享。

网页保留自己的导航和 DOM 渲染，App 保留 SwiftUI 渲染。两端使用同一业务逻辑和 Mac API。继续维护 dashboard 时，不要在 Swift 中复制聊天协议或 session 控制状态机。共享协议变更须保持快照 version=1 的兼容性，破坏性变更另起版本。

会话菜单的“标为未读”由网关 SQLite 按用户、设备、AI 客户端和会话保存，Web 与 iOS 共用 `/mN/api/sessions/read`。手动未读优先于旧的已读游标，列表轮询或已打开聊天中的自动阅读不会清除，重新打开会话才清除。Codex 通过 Desktop 已公开的 `set_thread_read_state` 工具同步；原生能力不可用或 DeepSeek 未提供对应接口时，网关仍保留标记。已确认同步到 Codex 的未读也允许后续 Desktop 阅读反向清除，旧快照不能清除新标记。该原生同步需要新版 agent；网关和两端 UI 是持久化功能的必要更新，不需要修改浏览器本地存储。

## 验证

```sh
bash scripts/verify.sh
node iOS/Tests/fixture-server.mjs
```

测试网关监听 `127.0.0.1:18765`，提供模拟 API 和模拟 ttyd，但直接运行真实 dashboard 资源。保持该服务运行，在另一个终端执行：

```sh
cd iOS
xcodebuild -project FleetHub.xcodeproj -scheme FleetHub \
  -destination 'platform=iOS Simulator,name=iPhone 17,OS=27.0' \
  -derivedDataPath /tmp/fleet-ios-tests CODE_SIGNING_ALLOWED=NO test
```

再选择 iPhone Duo，使用 Device Hub 的 Closed / Open 验证同一进程的折叠往返；宽屏 iPad 只用于额外窗口尺寸回归，不能代替 Duo。设备名称和 runtime 可按 `xcrun simctl list devices available` 调整。UI 测试的本地 HTTP 网关参数仅在 Debug 接受，Release 仍只接受 HTTPS。测试数据不包含真实账号或节点。

键盘回归前，在 Device Hub 的 Device → Keyboard 中关闭 Simulate Hardware Keyboard。若该显示表面曾手动隐藏软键盘，点击输入框后用 Toggle Software Keyboard 恢复显示，再不干预地重新运行测试；手动切换中的诊断轮不计作通过。`testSoftwareKeyboard*` 必须能找到完整字母/数字按键，并验证键盘与当前窗口的实际交集高度及输入框不被遮挡；仅有键盘工具条或屏幕外的键盘节点不计通过。覆盖搜索、原生重命名、聊天草稿、设备名、URL、数字、密码和文件命名。

连续折叠用例默认跳过；先启动 fixture，再显式启用并单独运行：

```sh
curl -fsS -X POST -H 'Content-Type: application/json' \
  -d '{"enabled":true,"stage":"idle"}' http://localhost:18765/__duo
xcodebuild -project iOS/FleetHub.xcodeproj -scheme FleetHub \
  -destination 'platform=iOS Simulator,name=iPhone Duo,OS=27.1' \
  -parallel-testing-enabled NO -collect-test-diagnostics never \
  -only-testing:FleetHubUITests/FleetHubDuoUITests CODE_SIGNING_ALLOWED=NO test
```

用 `curl -fsS http://localhost:18765/__duo` 读取阶段，依次在 Device Hub 执行 `close-before-start` → Closed、`open-with-keyboard` → Open、`close-with-settings` → Closed、`reopen-preserving-sidebars` → Open。每阶段最多等待五分钟；完成后须看到 `complete` 及首页加载/会话恢复各一次、消息发送零次。恢复普通回归前 POST `{"enabled":false}`。Duo 自动旋转后使用实际 UIWindow 的边界做断言，不能使用仍指向外屏坐标的 XCUIApplication.frame。

额外浏览器回归需安装 Playwright 并使用本机 Chrome：

```sh
PLAYWRIGHT_MODULE=/path/to/node_modules/playwright node iOS/Tests/web-smoke.mjs
```

该回归验证桌面 Web、手机 Web、嵌入模式、Claude ttyd 输入和 iframe 缓存复用。

文件菜单回归：`PLAYWRIGHT_MODULE=/path/to/node_modules/playwright node iOS/Tests/file-menu-smoke.mjs`。验证桌面与 H5 的图标、列表、分栏视图中，文件名、创建时间、修改时间各自升降序，排序记忆、路径弹窗和触屏无 hover 残留。iOS 使用同一文件业务逻辑和系统原生菜单、alert；H5 使用对应的菜单分组与居中输入弹窗，桌面保留表头快捷排序。创建时间由新版 Mac agent 返回 macOS birth time；旧 agent 未返回的日期保持未知并排在同组末尾，不用修改时间代替，源码构建不代表现有设备已经更新。

模拟测试不代替真机网关 UAT。真实 iPhone Duo 的折叠/展开动画、实际铰链安全区域、真实 FleetAuth 登录、真实 ttyd WebSocket、Wi-Fi/蜂窝切换和长时间后台恢复须在目标硬件与网关验证。商店素材、Push 和 TestFlight 发布不包含在本轮。

### Build 11 原生交互整理

设置统一使用原生分组背景；账号安全移除管理后台入口，操作改为整行菜单，退出登录单独显示。自动化新建入口放入导航栏，复制和文档等操作分行排列。配色有效输入自动串行保存，连续修改合并，深浅主题显式传递至原生表单和嵌套 sheet。密钥范围只用项目名称和会话标题选择，保存实际路径与 ID，加载失败保留已有绑定。聊天权限和模型选项用原生 alert，保留 Web 业务状态及子菜单。手机分栏每栏比视口窄 64 点，露出相邻栏。

设置的五个页面及其编辑器统一使用系统分组表单，共用背景、正文和辅助文字、分组标题、行高、图标宽度与系统卡片圆角、分隔线。分组标题放在卡片外，Tab 直接占用导航标题位置，与完成按钮同一行，空间不足时横向滚动。设备选择器不再显示会话汇总说明。文字图标使用“1-4个英文字母或数字”占位提示；输入代理在修改文本前拒绝非 ASCII 字母/数字及超过四位的输入（包括粘贴），允许清空和选区替换，保存时仍要求一至四位有效字符。
