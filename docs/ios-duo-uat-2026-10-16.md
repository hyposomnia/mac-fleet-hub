# iPhone Duo 与输入体验回归（2026-10-16）

## 环境与范围

独立 worktree 分支 `codex/ios-duo-adaptation`，基于 SaaS `9c5bb4f`。使用 Xcode 27.1（27A9275）和 iOS 27.1 的 iPhone Duo 模拟器，不使用 iPad 冒充折叠设备。构建显式设置 `DEVELOPER_DIR`，不修改系统全局 Xcode 路径。

普通 iPhone 及 iOS 17 最低版本保持原有支持；Duo 必须使用 27.1 SDK 编译，旧 SDK 的设备兼容布局不能作为 Duo 验收。布局仍以实际窗口的安全可用宽度为准，不硬编码机型或铰链角度。

## 修正

- 原生工作区的空白占位层不再截获 Web 聊天区域的触摸，返回按钮和会话浮层仍单独响应。
- 统一抽屉背景保持全宽、贴底；内容避让动态横向安全区，避免 Duo 右侧系统区域挡住关闭按钮。折叠前后继续使用同一个设置页和会话 WebView。
- 搜索支持键盘搜索键收起，打开会话及离开列表释放焦点。异步搜索只禁用操作按钮，不再禁用输入框，避免键盘与中文输入组合在刷新时丢焦点。
- 会话重命名从系统输入 alert 改为共用原生抽屉，捕获原会话目标、支持取消/关闭/完成输入；保存重新检查当前会话仍存在且支持重命名。切换账号清除原生重命名状态。
- 原生设置与 Web 投影表单共用 `NativeFieldLabel` 的完成输入操作，普通文本、URL、数字和密码都可退出输入。文件命名同样支持完成按钮与系统 Done，不再缺少统一收键盘入口。
- 服务地址表单也使用同一完成输入入口，URL 键盘 Go 与连接按钮共用校验；非法地址不发起网络连接。
- 首次配置页改为 App 根节点与工作区互斥呈现，不再覆盖在空白工作区/WebView 上；修复普通 iPhone 首次配置画面空白且输入框无法点击的问题。
- 新增严格软键盘测试：必须有实际按键及足够的窗口内高度，输入框须位于键盘上方。区分硬件键盘工具条、完整软键盘与模拟按键输入；不以文字成功写入代替软键盘可见性。

## 已取得的证据

- `/private/tmp/fleet-ios-duo-soft-transitions1.log`：`TEST SUCCEEDED`。从外屏单栏进入聊天并显示完整软键盘，展开、收起两侧栏、打开设置、带设置折叠、关闭设置、重新展开并恢复侧栏偏好，再旋转，草稿始终一致。该轮只对外屏键盘做完整断言，不将其算作展开态软键盘验收。
- 同一轮 fixture 记录为 `rootLoads=1`、`chatResumes=1`、`messagesSent=0`，未因尺寸变化重新加载页面或重开会话，未发送 AI 消息。截图与层级在 `/private/tmp/fleet-ios-duo-soft-transitions1-evidence/`。
- 新版 Device Hub 已直接核对外屏完整 QWERTY 按键可见。此前只有硬件键盘工具条的截图和 `/private/tmp/fleet-ios-duo-transitions5.log` 只证明硬件输入与状态保留，不计作完整软键盘验收。
- `/private/tmp/fleet-ios-duo-expanded-keyboard1.log`：20 个单测及展开态几何、侧栏独立收起、重命名保存三项 UI 通过；五项软键盘测试未通过。当时键盘节点坐标在窗口外（窗口高 669，键盘起点 676），不能把节点存在算作唤起成功。
- `/private/tmp/fleet-ios-duo-expanded-diagnostic.log`：通过 Device Hub 的 Toggle Software Keyboard 恢复展开表面的完整 QWERTY 后，键盘坐标变为 `(75,449,801,219)`，搜索、原生重命名和聊天输入可见。该轮含人工切换，只作为诊断，不计无干预回归。
- `/private/tmp/fleet-ios-duo-expanded-keyboard-final.log`：恢复模拟器配置后无人工干预，20 个单测通过；设备名/URL、文件命名、首次配置地址、搜索/重命名取消/聊天四项 UI 通过。数字键盘已实际显示，但后续测试滚动起点落在系统底栏，未触达取消按钮。修正测试使用内容坐标和窗口底部避让后，`/private/tmp/fleet-ios-duo-expanded-numeric-final.log` 为 `TEST SUCCEEDED`，数字键盘、密码输入、完成收键盘和不提交密码均通过。不是将前一轮失败算作整套通过。
- `/private/tmp/fleet-ios-duo-fold-keyboard-final.log`：加强后的连续折叠用例为 `TEST SUCCEEDED`，外屏、展开、重新展开、旋转后均要求完整软键盘可见、输入框可点且在键盘上方。外屏键盘 `(0,418,466,245)`、35 键；展开键盘 `(75,449,801,219)`、38 键。Device Hub 画面也核对到完整 QWERTY，不是仅有工具条。最终 `stage=complete, rootLoads=1, chatResumes=1, messagesSent=0`，草稿和侧栏偏好保持；证据在 `/private/tmp/fleet-ios-duo-fold-keyboard-final-evidence/`。
- `/private/tmp/fleet-ios-iphone-keyboard-regression1.log`：普通 iPhone 原始回归 19 个单测通过、UI 8/9 通过；失败是首次配置白屏。修复根页面后 `/private/tmp/fleet-ios-iphone-gateway-keyboard3.log` 为 `TEST SUCCEEDED`，20 个单测及首次配置键盘回归通过。其余八项已覆盖聊天、列表切换/旋转、重命名保存/归档等操作、设置及设备名/URL/数字/密码/文件命名/搜索键盘。
- `/private/tmp/fleet-ios-iphone-keyboard-final.log`：最后补充的普通 iPhone 聊天单行/两行输入与草稿保留、数字/密码及完成收键盘两项 UI 均通过，`TEST SUCCEEDED`。数字键盘 12 键、密码键盘 35 键，窗口 `402×874`；新滚动辅助逻辑未破坏普通手机输入回归。
- `/private/tmp/fleet-ios-duo-delivery-verify.log`：最后一轮项目入口输出 `==> 全部验证通过 ✓`，Go、Dashboard JS、共享 Swift 包及 shell 回归均无失败。

所有账号、设备、URL、密码、密钥和文件写入测试只使用 loopback fixture 的合成数据，不操作真实账号凭据、不发送真实会话消息，不修改在线 dashboard、Mac Agent、launchd 或组网。模拟器不等于 Duo 真机：实际铰链、网络切换、长时间后台恢复仍须硬件 UAT。
