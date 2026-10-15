# iOS SaaS 对齐与真机 UAT（2026-10-15）

## 当前交付：原生抽屉统一（0.2.0 build 6）

- 统一 `FullWidthBottomDrawer`：全宽、仅顶部圆角、背景延伸至屏幕底部，包含关闭、遮罩、拖动、展开和 Escape；设备/会话侧栏仍可独立收起。
- 原生重写设备编辑、五个设置页、访问密钥与账号安全表单；普通宽度采用横向设置导航，展开宽度采用左侧导航。电脑端安装入口依据服务器 manifest 显示，不在 iPhone 安装 Agent。
- 一并检查并接入设备/项目选择、文件设置、动态文件操作菜单、文件命名/删除确认、终止进程、模型/推理/速度、权限以及 Sub Agent 面板。图片完整预览、聊天正文、附件和行内 Skills 联想不是模态抽屉，继续使用 Web；系统分享及系统确认保持系统原生。
- `FleetService` 在持久 WebView 内复用 FleetAuth，发出请求之前和返回之后均检查账号身份，不建立第二份 Cookie/CSRF 存储。模型选项、权限、候选分页、配色、设备外观和请求示例继续共用 Web 业务处理。
- 投影后的 Web 弹层设置 inert 和 aria-hidden，既不显示 Web 表单，也不能抢键盘或重复出现在 VoiceOver 中；每次原生操作重新校验身份、可见性、控件归属和有效选项。账号切换隐藏旧弹层并使旧控件 ID 失效。
- 提交中的安全表单禁止关闭，恢复码必须确认保存才可离开；敏感信息仅保留在当前表单内。原生输入提供“完成输入”，数字键盘下可显式收起再保存；键盘通知按主 RunLoop 更新并忽略重复值，避免反复触发布局与 Web 脚本更新。
- 会话详情移除设置入口，Claude 暂停展示且拒绝原生命令调用；DeepSeek 使用原有官方鲸鱼图形，图标在系统浏览器打开 DSH，文字仍筛选会话。长按菜单不提供主动 ttyd 切换。
- 密钥卡片第一行名称/编辑，第二行掩码/复制/复制请求示例；复制仍取得完整值，敏感剪贴板仅本机可用且两分钟过期。未限定的设备、客户端、项目、会话范围明确显示“全部”，避免误读权限范围。H5 同步同样的卡片布局与掩码，普通 Web 不启用原生适配器。

### 验证边界

- 最终 build 6：`/private/tmp/fleet-ios-native-drawers-regression6.log` 为 `TEST SUCCEEDED`，18 项 Swift 单测及 5 项专项 UI 全通过，覆盖密钥新建/修改/取消删除/删除、RPM 校验、数字键盘完成输入、设备编辑、文件命名、抽屉关闭及聊天草稿。
- 最终展开宽度：`/private/tmp/fleet-ios-native-drawers-wide6.log` 为 `TEST SUCCEEDED`，两个宽屏 UI 用例全通过。
- 最终真机：`/private/tmp/fleet-ios-native-drawers-release6-device.log` 为 `TEST EXECUTE SUCCEEDED`，四项只读 UAT 全通过，截图位于同名前缀的 `-evidence` 目录。`devicectl` 确认所选 iPhone 上 Fleet AI 为 `0.2.0 / 6`，已清理测试 runner 并重新启动客户端，未卸载客户端或清理登录数据。
- 最终项目入口：`/private/tmp/fleet-ios-native-drawers-release6-verify.log` 末行 `==> 全部验证通过 ✓`；签名严格校验与 `git diff --check` 通过。H5 与原生适配器的可选 Chrome / 桌面 WebKit 测试已分别通过 42/42。
- 所有写操作自动化只使用隔离 fixture 的合成密钥、设备、文件及会话；真机仅打开、查看、聚焦和关闭，不发送消息、不修改真实密钥、设备设置或认证凭据。
- 首轮整套手机回归日志 `/private/tmp/fleet-ios-native-drawers-full.log`：18 项 Swift 单测通过，21 项有效手机 UI 用例通过、1 项宽屏专用跳过；新增密钥用例暴露数字键盘后的测试命中与保存流程问题，后续专项回归独立记录，不把首轮日志宣称为全绿。
- 宽屏替代测试 `/private/tmp/fleet-ios-native-drawers-wide-final.log`：两栏独立收起和原生设置/设备编辑抽屉两个用例通过。设备是 iPad Air 模拟器的展开宽度，不是 iPhone Duo 真机。
- Build 5 与第一轮 build 6 均已在选定的 iPhone 12 上完成四项只读 UAT；第一轮 build 6 日志 `/private/tmp/fleet-ios-native-drawers-build6-device-final.log` 为 `TEST EXECUTE SUCCEEDED`。冷启动测试已补充等待原生详情真正打开，避免读取隐藏的旧 Web 输入框。
- 普通 Web 与桌面 WebKit 的 inert 控件兼容性由 `native_dialogs.test.mjs` 的可选浏览器测试验证；它不替代 iOS WKWebView 和实际网关验证。
- H5 修改仍为工作树源码，未部署在线服务器；未提交、推送或修改 Mac Agent、launchd、网关及组网配置。真实 Duo 硬件、Wi-Fi/蜂窝切换、长时间后台恢复及真实账号安全变更仍需专项 UAT。

## 范围

最初基于 SaaS 提交 `fd942a7`，开发期间再次 fast-forward 到 `edd42a6` 并同步新增的会话设备图标。独立 worktree 当前分支为 `codex/ios-saas-native-drawers`，不覆盖其他 SaaS 工作；已核对后续 SaaS 提交 `6f6836d` 的 dashboard 无新增差异。手机客户端访问电脑 AI 会话，不在手机运行电脑端 Agent。显示名 Fleet AI，bundle identifier 保留 `com.macfleet.FleetHub`。

保持 Web 视觉及业务语义：设备选择、会话列表、设置与所有抽屉控件为原生；聊天正文、附件及文件预览复用最新 Web。模型与权限、项目选择、Sub Agent 和文件操作的原生表现复用现有 Web 业务处理。不修改在线服务器、电脑端安装、launchd、Desktop、组网或正式 Mac 发布产物。

## 改动

- `native_appearance.js` 将 Web 实际语义色（包括自定义配色、OKLCH 混色）解析为 sRGB。模块作为 App 资源注入主页面，旧版在线桥也可工作，不在 Swift 复制配色算法。
- `FleetStyle` 经 SwiftUI environment 同步颜色，只接受已知 token 和六位十六进制值；身份变化和退出清理，未收到同步时回退共用 JSON。
- `FleetDeviceIcon` 支持服务器保存的 1–4 位字母/数字图标和设备色，离线去饱和。模式选择使用弱选中背景；已读无状态点，未读/运行/等待状态位于标题行末尾。
- 新建按当前设备范围判定在线状态。确认弹窗捕获呈现时的目标会话，保存明确为默认键盘操作。
- `FLEET_DEFAULT_GATEWAY` 可在构建时预填服务地址，不覆盖用户保存的地址；源码不写实际地址、开发 Team 或凭据，不放松 HTTPS/同源限制。
- 测试 target 自动生成 Info.plist，支持真机签名。真机专用用例不使用 loopback fixture、不输入密码、不发送消息、不修改真实会话。

## 已有证据

- `/private/tmp/fleet-ios-saas-verify-final.log`：项目验证通过，末行 `==> 全部验证通过 ✓`，包含颜色同步两项 JS 测试。
- `/private/tmp/fleet-ios-saas-phone-focused2.log`：账户折叠区/原生确认/取消/返回，以及重命名/置顶/归档/恢复/删除通过。
- 自定义服务端配色、深浅模式与字母图标用例通过，附件在 `/private/tmp/fleet-ios-saas-phone-focused.xcresult`。
- 个人团队签名成功；选定真机实际安装 Fleet AI 0.2.0（build 2），安装清单核验在 `/private/tmp/fleet-uat-app-installed.json`。
- 首次真机运行被系统无线数据授权弹窗阻挡，截图在 `/private/tmp/fleet-ios-saas-real-device4.xcresult`。安装成功不等于联网、登录或真实 AI 会话 UAT 已通过；须由用户允许网络并自行登录后继续。

宽屏回归 `/private/tmp/fleet-ios-saas-wide.log` 为 `TEST SUCCEEDED`，验证独立收起、浮层关闭/固定、离线选中与聊天显示。宽屏模拟器不作为 iPhone Duo 真机证明。

完整手机回归 `/private/tmp/fleet-ios-saas-phone-final.log` 为 `TEST SUCCEEDED`：11 项 XCTest、11 项有效手机 UI 通过；宽屏与物理设备用例在手机模拟器明确跳过，不计作手机通过覆盖。

同步到 `edd42a6` 后，`/private/tmp/fleet-ios-saas-verify-latest.log` 再次完整通过，`/private/tmp/fleet-ios-saas-latest-phone.log` 的 11 项 XCTest、自定义配色/字母图标与 H5 几何两项 UI 回归通过。服务器 `style.css` 与该版本源码 SHA-256 同为 `ce4daca3df1af0a278d2da334d890ec575c4000e3c1b2818bfebc64b8b17a443`，公开登录页使用 v241 资源。

最终独立真机构建日志 `/private/tmp/fleet-ios-saas-uat-latest-build.log` 为 `BUILD SUCCEEDED`。真实账号登录、真实电脑聊天与蜂窝切换仍待用户完成网络授权和登录，不以模拟结果代替。

该最终构建已执行严格验签、覆盖安装和启动；安装清单 `/private/tmp/fleet-uat-final-apps.json` 核实显示名 Fleet AI、版本 0.2.0、build 2。仅卸载本轮创建的 `com.macfleet.FleetHubUITests.xctrunner`，不卸载客户端、不清除登录数据。

## 全宽设备抽屉与登录后真机验证

用户完成联网授权和 SaaS 登录后，按真机反馈将紧凑布局中的系统浮动 sheet 改为共享原生 `FullWidthBottomDrawer`：会话、文件设备选择均左右贴边、底部贴屏，只保留顶部圆角；内容避让 Home Indicator。支持拖动手柄展开/收起、下滑关闭、关闭按钮、背景点击、辅助功能退出，并尊重减少动态效果。宽屏继续使用原有呈现，不改设备栏与会话栏逻辑。

- `/private/tmp/fleet-ios-saas-drawer-verify.log`：`bash scripts/verify.sh` 输出 `==> 全部验证通过 ✓`。
- `/private/tmp/fleet-ios-saas-drawer-sim2.log`：`TEST SUCCEEDED`，会话/文件两种模式的横向边界、底部边界、展开、按钮关闭、背景点击关闭、下滑关闭均通过。
- 严格验签后使用 `devicectl device install app` 覆盖安装到用户选择的 iPhone 12，重新启动新二进制，保留用户登录及本地数据。测试启动不会自动替换已运行的旧进程，不能仅以 `activate()` 代表新构建已运行。
- `/private/tmp/fleet-ios-saas-drawer-device2.log`：真实已登录 SaaS 原生工作区及设备抽屉几何断言通过，`Executed 1 test, with 0 failures`、`TEST SUCCEEDED`。真机验证抽屉宽度与 App 宽度相等（容差 1pt）、左边界一致且底部贴屏，关闭后恢复工作区。
- 真机截图附件：`/private/tmp/fleet-ios-saas-drawer-device2.xcresult`。未输入凭据，未发送 AI 消息，未修改真实会话或远程文件；真实消息收发、ttyd 写入、蜂窝切换与 Duo 真机仍不计为通过。

## build 3：统一设置、会话长按与紧凑输入框

- 原生齿轮不再打开旧系统菜单，直接进入与 H5 相同的统一设置抽屉，默认账号设置，沿用设备、外观、自动化、账号设置、会话设置五个页面，以及 H5 的关闭、取消、页面切换和未保存状态处理。不再另开账户 Web sheet，也不重复实现配色或账户业务。
- 主 WKWebView 保持唯一实例，在普通会话区域与全屏 Web 浮层之间调整布局，保留聊天、文件、草稿和终端状态；设置浮层覆盖原生导航时停用底层点击，关闭后恢复原视图。聊天内容延伸到屏幕底部，消除额外原生安全区白条。
- 会话行去掉归档及更多按钮，使用原生长按菜单执行打开、归档/恢复、置顶、重命名和删除；未读/运行状态保留。顶部提供与 H5 相同的归档范围按钮。移除主动“用 ttyd 打开”选项、Swift 参数和原生适配器中的强制切换能力；保留 Claude 已有自动 ttyd 展示兼容。
- 按用户要求委派子代理完成 H5 `mobile_composer.js/css`，再将同一份资源打包注入 iOS：默认单行，输入聚焦后两行，收起隐藏工具栏长文字，保留图标与发送/停止功能；失焦或键盘关闭后收起，草稿和附件不丢。支持重入，普通 H5 由 HTML 引用，桌面 Web 不改布局；iOS 通过键盘通知同步状态。
- 本轮 H5 源码已同步，但未提交、推送或部署线上 dashboard。iOS 使用 bundle 中相同组件，因此不依赖先更新服务器。

证据：

- `/private/tmp/fleet-ios-saas-composer-final-verify.log`：完整项目验证 `==> 全部验证通过 ✓`，包含新增 4 项原生工作区适配测试、11 项紧凑输入框测试。
- `/private/tmp/fleet-ios-saas-menu-final.log`：`TEST SUCCEEDED`；12 项单元测试、14 项有效手机 UI 测试通过，宽屏用例在手机上明确跳过。覆盖设置五页切换、账户原生确认取消、长按重命名/置顶/归档/恢复/删除、配色同步、输入框展开/收起及草稿保留、聊天/文件/Claude 与旋转。
- `/private/tmp/fleet-ios-saas-menu-wide.log`：宽屏独立收起/固定/浮层/聊天回归通过，`TEST SUCCEEDED`。仍为宽屏布局替代验证，不冒充 Duo 专用模拟器或真机。
- `/private/tmp/fleet-ios-saas-composer-device-build3.log`：`TEST BUILD SUCCEEDED`；严格验签后实际覆盖安装到选定 iPhone 12，保留登录及本地数据。`/private/tmp/fleet-ios-saas-build3-installed.json` 核实 Fleet AI 0.2.0、build 3。
- `/private/tmp/fleet-ios-saas-composer-device-uat.log`：`TEST EXECUTE SUCCEEDED`，真机 3 项测试、0 失败。验证已登录 SaaS、全宽设备抽屉、H5 设置抽屉、原生会话长按及无 ttyd 强制入口、真实会话输入框单行/聚焦两行与原草稿不变。只读查看，无输入、发送消息或执行会话修改。
- 真机附件 `/private/tmp/fleet-ios-saas-composer-device-uat.xcresult`，导出目录 `/private/tmp/fleet-ios-saas-composer-device-evidence`。Device Hub 连接期间键盘截图展示系统硬件键盘工具条；本轮验证焦点与键盘输入坞，不把工具条截图当成完整软键盘按键布局验收。

本轮测试 runner 已清理，客户端保留并重新启动；未修改服务器、Mac Agent、真实文件、网络设置或认证凭据。
