# Fleet Hub iOS

最新 Web 交互差异与共用边界：[交互对齐记录](../docs/ios-web-interaction-parity.md)。

原生 SwiftUI 设备栏、会话栏与现有 dashboard 会话窗口组合。最低 iOS 17；普通 iPhone、宽屏展开状态和 iPad 共用一套实现。iPhone Duo 不依赖未经验证的机型名称、屏幕尺寸或铰链 API，按窗口实际可用宽度适配。

## 当前实现

| 可用宽度（pt） | 布局 | 设备栏 | 会话栏 |
| --- | --- | --- | --- |
| 至多 860 | H5 单栏 | 顶部设备选择 → 原生选择面板 | 原生列表与 Web 会话切换；列表页无多余会话栏按钮或图钉 |
| 861–1180 | Web 中等宽度三栏 | 220pt 原生侧栏，可收为 72pt 图标栏 | 310pt 原生侧栏，可独立取消固定、临时呼出 |
| 超过 1180 | Web 三栏 | 260pt 原生侧栏，可收为 72pt 图标栏 | 330pt 原生侧栏，可独立取消固定、临时呼出 |

布局变化只调整已有视图的尺寸。同一 WKWebView 保留 SSE、聊天草稿和 ttyd iframe；返回列表不结束 Mac 上的进程。展开后沿用用户保存的两栏显隐偏好。键盘和动态字体使用系统布局，Web 继续处理输入与 VisualViewport；App 隐藏 PWA 安装提示和重复的 Web 导航。

设备在线状态、助手筛选（ChatGPT / Claude / DeepSeek）、搜索、项目分组、归档筛选、分页、新建会话、置顶和归档均由原生列表提供。聊天、审批、附件、子任务和文件预览复用 Web。Claude 默认走 ttyd；ChatGPT 行的长按菜单也可使用 ttyd。终端始终使用默认权限，不隐式启用 bypass。

## 构建

打开已经生成并入库的 `FleetHub.xcodeproj`，选择 FleetHub scheme 和 iPhone / iPad 模拟器。真机安装前在 Xcode 配置自己的开发 Team。

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
- `server/dashboard/native.css`：仅嵌入模式的布局覆盖；不改变网页正常布局。
- `FleetHub/Models.swift`、`FleetStore.swift`：原生快照模型、命令确认和生命周期。
- `FleetHub/SidebarViews.swift`：原生设备与会话组件。
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

再选择宽屏模拟器（例如 iPad Air 11-inch），验证三栏和独立收起。设备名称和 runtime 可按 `xcrun simctl list devices available` 调整。UI 测试的本地 HTTP 网关参数仅在 Debug 接受，Release 仍只接受 HTTPS。测试数据不包含真实账号或节点。

额外浏览器回归需安装 Playwright 并使用本机 Chrome：

```sh
PLAYWRIGHT_MODULE=/path/to/node_modules/playwright node iOS/Tests/web-smoke.mjs
```

该回归验证桌面 Web、手机 Web、嵌入模式、Claude ttyd 输入和 iframe 缓存复用。

模拟测试不代替真机网关 UAT。真实 iPhone Duo 的折叠/展开动画、实际铰链安全区域、真实 FleetAuth 登录、真实 ttyd WebSocket、Wi-Fi/蜂窝切换和长时间后台恢复须在目标硬件与网关验证。商店素材、Push 和 TestFlight 发布不包含在本轮。
