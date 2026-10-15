# 一行命令安装原生 Fleet 客户端

## 体验与范围

用户运行当前服务的 `curl -fsSL https://<服务>/install.sh | bash`。脚本下载同源已签名公证的完整应用，调用应用的无窗口安装入口，复用原生应用安装、独立后台、登录启动、旧版本迁移和 OAuth。网页授权后自动接入；Settings 应用照常安装，普通成功路径不显示设置窗口。

设备联网并通过实际后台磁盘检测后完成。FDA 已验证则结束；受限或未知则打开系统完全磁盘访问和 Settings 的磁盘页，使用已安装独立 Agent 的原文件拖拽。无法可靠区分“系统列表中没有”和“开关未开启”，不枚举或修改 TCC 授权数据库。引导中自动复检；必要的后台重启仍使用现有空闲守卫。

网页“设置 → 设备 → 新增设备”展示“一行命令安装”和“下载安装包”两种入口。命令域名来自当前网页 origin，安装脚本使用服务配置的 canonical origin。旧发行包不支持 CLI 时禁用命令入口，继续提供 DMG；不得用旧包冒充完整新流程。正式签名、公证与发布等待用户后续指令。

## 契约与验收

| ID | 契约 | 验证与 owner |
| --- | --- | --- |
| CLI-01 | 公共 GET `/install.sh` 使用配置 origin，不信任请求 Host；不需要网站登录；其他方法 405 | Go `native_install_test.go`；multiuser HTTP 路由、嵌入脚本 |
| CLI-02 | 仅下载同源不可变整包；检查协议版本、macOS、架构、SHA/大小、应用身份、版本及系统签名评估，失败不执行程序 | shell 安装脚本集成测试；`native_install.sh` |
| CLI-03 | 无窗口模式调用现有安装器；新装、旧版升级、原生后台和登录启动复用现有 owner | Swift CLI 路由与安装测试；`FleetHub` 命令入口、`NativeManagement` |
| CLI-04 | 自动保存 origin，保留已有登录启动选择，发起当前 OAuth，仅打开本次同源授权 URL 一次 | `TerminalSetupTests`；`TerminalSetup` |
| CLI-05 | 授权不等于联网；直到绑定 complete、未 locked 且 runtime running 才完成 | `TerminalSetupTests`；既有状态协议 |
| CLI-06 | 同源有效关联复用；其他 origin、锁定或不完整关联不被静默覆盖；失败、取消、超时可见，不伪报成功 | `TerminalSetupTests` |
| CLI-07 | FDA verified 且证据来源为实际后台才跳过 UI；restricted、unknown 或无效证据进入引导 | Swift 核心与 GUI 引导测试 |
| CLI-08 | 引导选中磁盘页、打开系统设置、拖出独立 Agent；自动复检，已验证后结束引导 | Swift 引导测试、开发预览；真实 TCC 行为须签名包实测 |
| CLI-09 | 新发行清单及包声明 `installation_protocol: 1`；旧清单仍能下载安装，CLI 不误启动旧版 GUI | JS release / Swift / shell 测试 |
| CLI-10 | 设备设置两种安装方式、复制当前域名命令、未发布/协议不支持状态，以及原有设备管理不受影响 | `account_pages.test.mjs` 与 Chrome 检查 |

## 实现与交付

按公开脚本 → 无窗口原生协调器 → 权限引导 → 网页入口推进，每层先确认缺失行为，再实现并运行针对性回归。脚本不处理网站密码、TOTP 或设备 token；OAuth 继续由原生后台持有本机回调与 PKCE。取消不自动撤销已有设备或清理 AI 会话。

安装失败沿用应用 staging 验证/原子替换与后台健康失败回滚。活动任务不强制中断；重复运行已安装且有效的客户端可重试，不制造第二个 Agent 或 Codex writer。提交前执行项目 `bash scripts/verify.sh`，源码、隔离验证、预览与正式发布分别报告。

## 源码验收记录（2026-10-15）

- CLI-01：`TestNativeInstallPublicCanonicalOrigin` 通过。匿名公开路由、配置 origin、自定义端口、恶意 Host 与方法拒绝均覆盖；输出脚本通过真实 `bash -n`。
- CLI-02、CLI-09：`scripts/native-install.test.mjs` 的正向与拒绝路径通过；macOS/网络/签名命令在测试边界替换，真实 shell 控制流及 SHA 校验执行。另用当前 macOS 系统 `plutil` 实测 JSON 数组与协议字段读取。发行清单测试验证新字段；实际 Apple 签名评估待正式包验证。
- CLI-03～CLI-07：`TerminalSetupTests` 9 项、`TerminalInstallationTests` 1 项通过，覆盖现有同源关联、授权 URL、运行态等待、锁定/不完整绑定、授权失败、超时和磁盘证据。安装与迁移复用既有原生 owner 及其回归。
- CLI-08：`DiskAccessGuideTests` 8 项通过，包含独立文件拖拽、系统设置打开失败、unknown/restricted 继续等待、verified 才返回，以及取消退出。电脑控制会话本轮已停止，未完成窗口、真实拖拽及 TCC 开关验收。
- CLI-10：`account_pages.test.mjs` 验证两入口、复制当前带端口 origin、旧版/未发布/未知协议兼容；真实 Chrome 外观验收待补。
- 正式客户端未签名、公证或替换，未将开发包用于生产下载源。代码通过不等同于正式一行命令整链路安装验收。

最终在同步远端 `f5cb497` 后执行 `bash scripts/verify.sh`，退出 0，输出 `==> 全部验证通过 ✓`；完整日志 `/private/tmp/fleet-terminal-final-verify-20261015.log`。同步带入的设备 Mesh 地址专项测试另外通过。

## 网页与服务端发布

- 源码提交 `9cb45a4c68038965a6e0aef409ac7c0b41609d22` 已推送 `codex/multi-user-server`，包含此前远端 `f5cb497`。只构建 Linux amd64 服务端，未签名、公证或替换 Mac 安装。
- 当前私有发布配置将 `https://fleet.818.work` 指向 SSH `pm`。发布目录 `/opt/macfleet-saas-uat/releases/full-9cb45a4c68038965a6e0aef409ac7c0b41609d22`，145 个发布文件的清单及 SHA-256 完整匹配；旧发布保留。
- SQLite 在线备份通过 `integrity_check`，原配置及加密密钥保存在私有备份 `backups/terminal-9cb45a4c68038965a6e0aef409ac7c0b41609d22-20261015T082413Z`。未改数据库 schema 和现有密钥。current 原子切换后重启 SaaS 服务；四个相关 systemd 单元均 `active`。
- 新服务 PID `219320`，运行进程与磁盘服务端 SHA-256 均为 `5606b5500710435ff836973d469ba6dc2483cd24f8454a358a81a867354d47d0`。
- 真实公网输出：`healthz HTTP 200`、`readyz HTTP 200`、`install.sh HTTP 200`、`POST install.sh HTTP 405`、`auth HTTP 200`。account.js / account.css 与提交逐字节一致，安装脚本与 canonical origin 渲染逐字节一致并通过真实 `bash -n`。未登录的 sw.js 按既有策略返回 303，未将其误报为公开下载成功。
- 公网客户端清单仍为 `0.1.11+12`，无 `installation_protocol`；网页展示两种方式，命令入口明确暂不可用，DMG 继续提供。下一版正式签名包发布后才会启用可复制命令，真实安装、登录项、权限窗口和拖拽仍待验收。
- 本机发布证据目录 `/private/tmp/fleet-terminal-deploy-x0328jtb`，公网下载保存在其 `public/`。全量验证包含 496 项 JS（495 通过、1 项既有条件跳过）、144 项 Swift 及 Go/Shell；另运行同步带入的 Mesh IP 专项 1 项通过。
