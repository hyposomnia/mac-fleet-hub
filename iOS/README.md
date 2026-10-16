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

布局变化只调整已有视图的尺寸。同一 WKWebView 保留 SSE、聊天草稿和 ttyd iframe；返回列表不结束 Mac 上的进程。展开后沿用用户保存的两栏显隐偏好。键盘和动态字体使用系统布局，Web 继续处理输入与 VisualViewport；App 隐藏 PWA 安装提示和重复的 Web 导航。

设备在线状态、助手筛选（ChatGPT / DeepSeek）、搜索、项目分组、归档筛选、分页、新建会话、置顶和归档均由原生列表提供。Claude 暂不在 App 提供，长按菜单不再提供主动切换 ttyd。DeepSeek 鲸鱼图标打开系统浏览器中的 DSH；文字选项仍用于筛选会话。鲸鱼沿用 dashboard 中的官方 SVG 路径，授权记录见 `server/dashboard/icons/deepseek/LICENSE.md`。

设置、设备编辑、访问密钥、设备和项目选择、文件命名与删除确认、文件操作、模型权限和 Sub Agent 面板使用原生控件及统一全宽底部抽屉，仅顶部圆角，底部延伸至安全区域。普通宽度使用横向设置导航，展开宽度使用左侧导航。聊天正文、附件、文件预览保留 Web；系统文件选择、分享及确认仍使用原生系统界面。Skills 输入联想是行内建议，不作为模态抽屉。

## 构建

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

首次运行填写 HTTPS 网关根地址，可以带 NAT 端口。网关须先部署本分支的 `server/dashboard/` 全部资源，旧版本会明确提示尚未提供 App 接入组件。无需改 fleet-agent 或重新发布 agent 二进制。

认证沿用网关现有页面，在 WKWebView 完成 Authelia 或 FleetAuth 登录及两步验证。原生层只持久化网关地址和侧栏偏好，不提取密码、token、CSRF 或 Cookie。Cookie 留在 WKWebsiteDataStore；HTTPS 使用系统证书验证。原生桥仅接受已配置网关同源主文档的消息，终端地址还必须匹配选定设备路径。

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
- `FleetHub/NativeSettings.swift`、`NativeEditors.swift`、`NativeWebDialogs.swift`：原生设置、密钥和设备表单及共享抽屉内容；密钥仅显示掩码，敏感复制限本机并设置两分钟过期。
- `FleetHub/FleetWebView.swift`：长期持有的 WebView、同源导航、预览；主窗口与辅助页面共用弹窗、下载和系统分享。

网页保留自己的导航和 DOM 渲染，App 保留 SwiftUI 渲染。两端使用同一业务逻辑和 Mac API。继续维护 dashboard 时，不要在 Swift 中复制聊天协议或 session 控制状态机。共享协议变更须保持快照 version=1 的兼容性，破坏性变更另起版本。

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

模拟测试不代替真机网关 UAT。真实 iPhone Duo 的折叠/展开动画、实际铰链安全区域、真实 FleetAuth 登录、真实 ttyd WebSocket、Wi-Fi/蜂窝切换和长时间后台恢复须在目标硬件与网关验证。商店素材、Push 和 TestFlight 发布不包含在本轮。
