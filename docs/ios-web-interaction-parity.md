# iOS / Web 交互对齐

## 对照基线

2026-10-04 对照当前开发仓库 `55b673e` 的 dashboard（Web v170），在 `codex/ios-native-workspace` 独立工作树继续实现。没有覆盖主工作树、部署服务器或更换正式 Mac 客户端。此分支 dashboard 外壳升级至 v171。

原生设备与会话导航保留；聊天、终端、文件浏览、文件标签、设备设置与账号页面复用最新 Web。不是将旧网页简单套壳，也没有将业务判断复制成第二套 Swift 实现。

## 差异与实现

| Web 交互 | 原 iOS 差异 | 当前实现 |
| --- | --- | --- |
| 设备栏收起后保留图标与内容切换 | 整栏消失 | 原生 68pt 图标栏，设备仍可选，标识展开 |
| 会话列表固定 / 取消固定 | 只有显示 / 隐藏 | 原生图钉、悬浮呼出、外部点击与 Escape 关闭、选中后收起；固定偏好保留 |
| 会话 / 文件切换 | 文件藏在菜单且列表仍占位 | 原生内容切换；文件模式隐藏会话栏，仅允许具体设备 |
| 新建无项目会话位于搜索旁 | 通用路径表单位于标题 | 原生搜索右侧入口；全部设备时选择在线设备，具体设备时直接新建 |
| 项目内新建与空项目 | 没有项目创建入口，空项目丢失 | 从 Web 投影全部项目，路径与设备校验后新建 |
| 按项目 / 最近排序 | 独立 Swift 状态与不同排序 | 共用 Web 偏好、分组与排序，含置顶组优先及运行会话次序 |
| 项目展开 / 收起 | 独立 Swift 集合 | 共用 Web 折叠状态 |
| 搜索、归档、增量分页 | 文案、空态与分页不同 | 范围相关提示、同源搜索、自动加载、错误与离线空态 |
| 置顶、重命名、归档、恢复、删除 | 仅 Codex 置顶与归档 | 能力驱动的原生行操作与菜单；重命名表单、删除确认；调用 Web 同一 mutation |
| DeepSeek 入口与原生 UI | 无条件显示，未检查能力 | 从 agent 能力投影；不可用隐藏，降级提示、具体设备 native UI 入口与权限校验 |
| 图标、颜色、名称和代理设置 | 没有设备设置入口 | 原生设备菜单进入同一 Web 设置；离线外观编辑、原字段和保存行为保留 |
| 账户、管理、添加设备、自动化、设置、外观 | 缺少入口 | 原生菜单；管理员入口按身份显示，其他页面及弹层复用 Web；辅助页面共用原生弹窗 / 下载，返回 Fleet 回到已有工作区 |
| 会话内文件标签 | 文件打开在另一个 sheet，标签被隐藏 | WKWebView 中显示最新标签栏；同文件复用、关闭回聊天、原输入器与草稿保留 |
| PWA 安装提示 | 已安装 App 中出现网页安装提示 | 原生模式不显示 PWA 安装提示，普通浏览器行为不变 |
| 单栏返回与横竖屏变化 | 仅 toolbar 返回 | 原生返回、边缘手势；改变窗口宽度关闭临时列表，不销毁聊天或 ttyd |
| 逐用户鉴权与注销 | 旧 dashboard 与旧退出端点 | 最新 auth-client / CSRF / 用户存储键；身份变化清理原生搜索、弹层、临时下载文件与未完成命令，旧身份的进行中下载不会被分享 |
| 最新统一画布 | 三栏仍使用早期不同底色 | JSON 共用令牌同步最新白色 / 深色统一画布及设备颜色 |

Claude 在 iOS 继续使用 ttyd，这是已要求保留的客户端兼容行为；普通 Web 不因此新增 Claude 终端入口。

## 共用边界

- `fleet_core.js`：项目身份、分组排序、设备与协议基础、终端地址校验。
- `workspace.js`：从实际 Web 状态投影版本 1 的可选字段；执行命令前重新验证会话身份、设备、项目与 agent 能力。
- `native_bridge.js`：只在原生顶级根页面启用；普通 Web 与预览 iframe 不启用。
- `titanium.json`：Web CSS / iOS 动态色与设备颜色唯一来源。
- `FleetStore`：导航与命令生命周期；不自行请求另一套业务 API。
- `FleetWebView`：常驻会话内容；新窗口仅承载辅助账号页面，同源导航约束不放宽。

## 本地验证入口

```sh
bash scripts/verify.sh
node iOS/Tests/fixture-server.mjs
xcodebuild -project iOS/FleetHub.xcodeproj -scheme FleetHub \
  -destination 'platform=iOS Simulator,name=iPhone 17' \
  -parallel-testing-enabled NO CODE_SIGNING_ALLOWED=NO test
```

fixture 只绑定 loopback；会话、身份、CSRF、终端与文件都为模拟数据。UI 测试覆盖空项目、分组 / 最近 / 搜索、行操作、归档恢复、删除确认、项目 / 无项目 Claude 创建、文件标签草稿、外观与文件模式、主题和终端旋转。宽屏测试验证缩略设备栏、图钉、临时列表、外部关闭与重新固定。

这些结果不等于真实网关、真实终端 WebSocket、iPhone Duo 硬件或 App Store 发布验收；发布 App 前必须先让对应网关提供此版本 dashboard 接入组件。

## 2026-10-04 实际验证

- `bash scripts/verify.sh`：Go agent / enroll、317 项 JavaScript 与全部 Shell 层通过，输出 `==> 全部验证通过 ✓`。
- iPhone 17 / iOS 27：8 项 XCTest 与 9 项有效手机 UI 用例通过。此前 runner 记录的第 10 项宽屏用例在手机上未执行交互，不计入手机覆盖；现已显式标记跳过。
- iPad Air 11-inch (M4) / iOS 27：8 项 XCTest、1 项宽屏 UI 用例通过，包含横屏断言、设备图标栏、会话取消固定 / 临时呼出 / 外部关闭 / 重新固定及聊天显示。
- 缓存归属修复后追加 3 项原生回归通过：Claude 终端与旋转、项目 / 无项目创建、文件标签与草稿。请求记录明确断言缓存 Codex 会话不向 Claude API 投递。
- 辅助账户页的确认 / 取消与返回已有工作区、能力启用后的 DSH 页面入口均已在手机 UI 用例中实际执行。
- `git diff --check` 与 `node scripts/titanium-design.mjs --check` 通过。Xcode 构建、安装与测试均为模拟器 Debug 产物，未签名发布。

本机日志位于 `/private/tmp/fleet-ios-parity-verify-final-evidence.log`、`/private/tmp/fleet-ios-parity-phone-final2.log`、`/private/tmp/fleet-ios-parity-wide-screen.log`、`/private/tmp/fleet-ios-parity-cache-native.log`，对应 `.xcresult` 保留完整测试附件。文件标签和行操作最终回归分别见 `/private/tmp/fleet-ios-parity-tabs-final.log`、`/private/tmp/fleet-ios-parity-actions-final.log`，均为 `TEST SUCCEEDED`。这些临时路径不是生产部署或仓库内的永久测试产物。

## 2026-10-05 视觉复查与修正

上一轮交互通过不代表视觉一致。按用户三张标注图重新核对真实 dashboard 的 402px H5 和 1180px 桌面布局，而非照搬旧移动页标记或 SwiftUI 默认控件。

| 区域 | 原生偏差 | 修正 |
| --- | --- | --- |
| 手机顶部 | 设备栏按钮、额外会话栏按钮、填色模式控件 | 28pt 文字会话 / 文件切换、右侧设备范围选择与菜单；列表页没有返回按钮或图钉 |
| 助手 | 独立大块选中背景，裸按钮 | 4pt 内边距的分段底色，白色选中项；手机高 52，桌面高 39 |
| 搜索 / 新建 | 默认字体、额外 12pt 上下留白、裸加号 | 手机 44 / 桌面 36 高，7pt 间隔，主色新建按钮与相应圆角 |
| 项目 / 会话 | List / Section 的默认顶部空白、分隔线和段间距 | ScrollView / LazyVStack 明确控制间距；13pt 项目标题、15 / 13pt 会话标题；元信息位于行下方 |
| 宽屏顶部 | 设备 / 会话图标、重复标题、第二个菜单占满额外一行 | 删除全局导航，设备栏品牌与会话助手直接顶部对齐；唯一菜单位于设备栏底部 |
| 设备行 | 设置按钮在选中范围外，名称被额外状态点压缩 | 设置按钮纳入整行背景，状态点贴设备图标，13pt 标签和完整栏宽 |
| 详情头部 | 原生标题行叠加 Web 标签行 | 保留 Web 原有会话 / 文件标签，仅在同一行两侧叠加原生返回和菜单；不增加第二个标题行 |
| 断点 | 700 / 900 与 Web 860 不同，遗漏中等宽屏 | 按实际窗口可用宽度统一 860；861–1180 为 220 / 310，更宽为 260 / 330，72 图标栏均读取共用 JSON |

共用 JSON 新增字号与导航尺寸，Web 生成 CSS，原生直接读取。正文、文件标签、草稿与 ttyd 池继续使用原 WKWebView。辅助操作“刷新”收起键盘但保留输入草稿，避免原生菜单关闭后旧链接命中区域失效。

手机布局回归验证文字切换尺寸、项目和搜索间距，以及列表页没有冗余按钮、进入详情后出现返回按钮。主题与文件预览也重新实测，不能用上一轮功能日志代替本轮视觉证据。宽屏模拟器仅验证布局，不冒称 iPhone Duo 硬件；用户最终预览保留 iPhone 17。

宽屏会话浮层与遮罩只附着到右侧 Web 区域，不覆盖设备栏。回归通过真实点击与设备选中状态、离线提示、重新固定后的聊天验证闭环，而非仅依赖展开后的单个 accessibility 命中属性。

最终宽屏回归日志为 `/private/tmp/fleet-ios-style-wide-final11.log`，结果为 `TEST SUCCEEDED`。覆盖 220pt 设备栏、无重复全局工具条、离线设备整行选中、两栏独立收起、会话浮层外部关闭、设备栏展开后选择、浮层打开时切换设备、重新固定及聊天显示；附件位于同名 `.xcresult`。最终项目验证日志 `/private/tmp/fleet-ios-style-verify-final3.log` 记录 Go 两层、317 项 JavaScript 和全部 Shell 测试通过，输出 `==> 全部验证通过 ✓`；共用 token 同步检查和 `git diff --check` 均通过。

行操作整套回归的首次失败定位为模拟器键盘出现后，弹窗 accessibility 触摸坐标仍指向键盘前的位置；保存按钮实际未被点击，fixture 没有收到写请求。测试改为先结束编辑、等待键盘收起再保存，并断言弹窗关闭及名称更新。保留原有业务弹窗实现，撤回未解决该问题的试验改动。`/private/tmp/fleet-ios-style-actions-final7.log` 为 `TEST SUCCEEDED`；请求记录包含 rename、pin、unpin、archive、unarchive、delete 各一次，取消删除没有提交写请求。

最终 iPhone 17 / iOS 27 整套回归 `/private/tmp/fleet-ios-style-phone-final2.log` 为 `TEST SUCCEEDED`：9 项 XCTest 和 10 项有效手机 UI 用例全部通过，1 项仅供宽屏执行的用例明确跳过，不计入手机覆盖。同名 `.xcresult` 包含列表、深浅主题聊天、文件标签、Claude 终端、文件模式与最近列表截图。本轮仍仅使用本地 fixture 与模拟器 Debug 构建，没有修改生产服务或签名发布。

回归结束后重新安装并启动 iPhone 17 Debug App；`simctl list devices booted` 只列出该手机，宽屏测试设备已关闭。本机 Mac 仍锁屏，不能把 `simctl launch` 成功当作 Device Hub 窗口已经切换的证据；最终手机截图来自上述成功 UI 回归，窗口切换须在用户解锁后继续。
