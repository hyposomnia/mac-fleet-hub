# 原生客户端 Titanium 整改与浏览器 OAuth

## 已确认需求

用户指定 `logo-raised-view.svg` 第一版标识及 Titanium SPEC；为 App、后台应用和 DMG 中的应用提供真实图标。保留 SwiftUI 与独立后台，压缩文案、统一布局。服务器页改为“关联账号”；首页未关联时可直接进入。磁盘状态旁使用刷新图标。

## 交互与授权契约

原生客户端使用 OAuth 2.0 授权码流程，公共客户端 ID `fleet-hub`，仅支持 S256 PKCE。后台先绑定动态 `127.0.0.1` 端口，再生成随机 state、verifier 与 challenge；浏览器访问用户配置 origin 的 `/oauth/authorize`。浏览器保留既有邮箱、密码及 Authenticator 登录要求，明确显示设备名称及账号后提交同源 CSRF 保护的授权操作。一次性授权码回传本机 callback，匹配 state 后通过 `/oauth/token` 兑换短期入网授权；凭据不经过浏览器或设置窗口。服务端只接受 `http://127.0.0.1:<动态端口>/oauth/callback`，拒绝凭据、片段、其他 host、路径及重复参数，不允许任意重定向。

授权码仅保存哈希、绑定完整 callback 与 S256 challenge，有效 2 分钟，单次兑换；入网请求仍为 10 分钟，设备归属只来自已完成 TOTP 的网页用户。OAuth access token 仅能继续原有 enrollment claim/complete，不等同于浏览器会话或设备代理 token。原有 CLI 配对协议保持兼容，原生 UI 不显示配对码，不重开过期链接。

每次“打开网页授权”生成新 state/verifier/callback；替换尚在浏览器等待的旧请求必须先取消并等待结束。取消、超时、拒绝授权关闭本地 listener，旧回调不能改变新状态。已经入网的设备不隐式更换账号；更换账号前仍需明确解除关联。已发出真实设备凭据的失败入网先撤销旧授权，撤销失败保留锁定凭据，不覆盖归属。

网页确认后自动接入，简化重复确认；用户尚未提出其他偏好。旧 CLI 保留本机确认。自动接入不取消网页 owner 同意、PKCE、真实 Headscale 节点校验及维护守卫。失败后如存在已发出的设备凭据，状态只报告需要清理，不暴露凭据，提供明确解除入口。

## 视觉与图标

从指定 SVG 的原始路径生成图标，不重绘几何。浅色图标画布 `#EDF2F6`、标记 `#2C5D87`，96 单位母版按 `translate(88 88) scale(3.5)` 放入 512 单位画布；系统负责安装图标 mask。生成完整 macOS `.icns` 尺寸及品牌标记，主/后台 Info.plist 绑定 `CFBundleIconFile`。

SwiftUI 表面、字色、accent 全部按 Titanium 明暗令牌。品牌字标使用等宽大写 13px；导航和标准控件 44px/R12，小图标控件 36px/R8，卡片 R16。无需常驻边框、装饰分隔线、蓝色系统默认填色及长说明。只保留状态、必要错误、安全确认与授权提示；PID/版本等诊断收纳到详情。

## 完全磁盘访问

2026-10-05 真实后台探测为 restricted，系统 FDA 表中仅有旧 `~/.local/bin/fleet-agent` 许可。2026-10-07 用户已开启内嵌 `com.macfleet.fleet-agent`，安全重启后仍受限；实际 TCC 日志确认权限主体为外层 `com.macfleet.fleet-hub`。这揭示了当前嵌套安装/启动的架构缺陷，不能改成要求用户授权主应用来规避；必须使 agent 成为独立权限主体。

后台以受保护的用户 TCC.db 真实只读访问作为 FDA 证据；其他目录访问拒绝只作为诊断，不将文件 Unix 权限混同于 FDA。受保护目标返回 EPERM 才报告受限，EACCES 等普通文件权限错误为待检查。缺失目标、不明错误与后台不运行仍为待检查，不伪报成功。设置页应定位独立安装并由 launchd 运行的 `Fleet Agent.app`，不是主应用，也不是发行包中的嵌套副本。后台签名 identifier 与实际 TCC 权限主体必须分别验证；关闭 Hub 后仍运行，Hub 不读取受保护文件。授权变更后的后台重启必须经过既有空闲守卫，不能中断活动会话。系统授权由用户自行操作，不改 TCC 数据库，不代开 FDA。后续安装/启动/升级整改边界见 `2026-10-07-independent-agent-fda-design.md`。

## 验证与边界

先写失败测试：OAuth callback/PKCE/过期/重放/CSRF/跨账号、每次授权新建与取消、私有凭据不泄漏、磁盘证据分类、Titanium 控件与图标打包。再最小实现，运行 Swift/Go/JS 聚焦测试及 `bash scripts/verify.sh`，隔离构建检查 `.icns` 和全部界面。正式签名、公证、abj 部署、本机升级及真人 OAuth/FDA 验收分别报告，不以开发包替换正式应用。
