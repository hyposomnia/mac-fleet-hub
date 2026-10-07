# 原生 Titanium / OAuth 验证记录

2026-10-05，分支 `codex/multi-user-server`。本记录区分源码、隔离运行与现有安装，不代表新版本已发布。

## 已实现

- 从指定第一版 `logo-raised-view.svg` 的原始路径生成图标；主应用与后台应用均带 `AppIcon.icns`，窗口标识使用同一母版。
- Titanium 明暗令牌、44px/R12 标准控件、36px/R8 小控件和 R16 卡片；精简文字与运行详情。“关联账号”替代服务器导航，首页未关联卡片直接跳转。
- 原生后台使用 OAuth 授权码 + S256 PKCE；每次点击创建随机 state/verifier 和新的 loopback 回调，等待中的旧请求取消并关闭 listener。网页确认当前账号和设备后自动接入，无配对码和重复确认。失败且已有设备凭据时提供解除入口，不静默覆盖授权。
- 服务端授权码绑定浏览器 owner、PKCE 和完整 callback，两分钟有效、一次性兑换；入网 token 不能访问浏览器私有 API 或充当设备凭据。旧 CLI 配对协议保留。
- Web 安装说明同步授权、过期重试及正确后台授权对象；OAuth 路径禁止进入 PWA 缓存。
- FDA 只使用实际后台读取受保护用户 TCC 数据库的证据；其他目录的 Unix 权限不覆盖 FDA 结果。缺失目标、不明错误和非后台进程为待检查；状态旁刷新图标执行后台只读检查，重启仍有安全守卫。

## 自动验证

最终执行 `bash scripts/verify.sh`，退出码 0，末行：

```text
==> 全部验证通过 ✓
```

该次包含 agent/server Go 测试、296 项 Dashboard JS、11 项发行/打包测试通过（1 项 SDK 篡改缓存测试因未提供专用归档环境变量而跳过）、28 项 Swift 测试及所有 Shell 层。日志：`/private/tmp/fleet-native-titanium-final-verify.log`。

```text
go test -race -tags fleet_desktop ./...
ok  fleet-agent  11.658s

go test -race ./multiuser -run TestOAuth -count=1 -v
PASS
ok  fleet-enroll/multiuser  11.107s
```

OAuth race 测试覆盖错误 PKCE/回调、重复及畸形参数、过期、CSRF、拒绝、停用 owner、并发兑换一次性、过期请求的新建重试；native race 测试覆盖真实 loopback callback、state 校验、取消关闭、过期 CLI pending 替换及失败凭据清理元信息。日志分别为 `/private/tmp/fleet-native-titanium-race.log` 和 `/private/tmp/fleet-native-titanium-oauth-final.log`。

跨进程联调运行真正的 native 授权代码和 HTTP/SQLite 服务，从浏览器确认 API 经真实 loopback callback、token、claim、complete 验证 owner 隔离、自动接入及设备令牌。网络签发、OS join/setup 使用隔离适配器；这不是实际 Headscale 入网或真人登录的证据。

`git diff --check` 退出码 0。

## 隔离应用与窗口

最终开发包：`/private/tmp/fleet-native-titanium-final/fleet-settings.dYoB71/Fleet Hub.app`。版本设置为 `0.1.1+2` 仅用于隔离开发验证，未占用正式发行构建号。

`scripts/build-settings-app.sh --development` 成功；`AppIcon.icns` 为真实 Mac OS X icon，大小 98666 字节，主/后台 Info.plist 均绑定。应用为 x86_64/arm64 Universal，两架构 Mach-O 的最低版本均为 13.0。编译器报告新 SDK 的 x86_64 弃用警告；该警告不改变实际 13.0 最低版本声明，也不能代替 macOS 13 真机兼容测试。

`node scripts/settings-runtime-uat.mjs <开发应用路径>` 退出码 0：

```text
PASS: actual bundled agent, private control socket, unbound status, no fabricated FDA evidence, legacy update blocked
PASS: matching app/agent version, maintenance rejects settings, graceful daemon restart preserves isolated configuration
PASS: bundled filebrowser initializes and serves only isolated test files over loopback
PASS: bundled ttyd responds and bundled tmux creates an isolated session without Homebrew
```

运行使用临时 HOME、私有 socket、动态端口及测试文件，结束时清理进程与数据。日志：`/private/tmp/fleet-native-titanium-final-runtime.log`。

开发预览单独使用临时 bundle identity，避免与现有应用混淆。通过桌面 UI 实际查看运行状态、关联账号、磁盘权限、关于；最终窗口已复核首页跳转及精简文案，磁盘刷新按钮位于状态旁且保留 accessibility label。预览未安装，因此启动/授权操作保持禁用，未以未签名应用覆盖现有安装。浅色窗口已复核；深色令牌已实现并构建，尚未单独进行深色窗口人工验收。

## 现有安装与发布边界

2026-10-05 检查时，现有 `/Applications/Fleet Hub.app` 与 PID 73133 的正式后台未替换、未重启。执行其 `desktop disk-recheck`，真实输出仍是：

```json
{"state":"restricted","source":"background","checked_at":1791170702,"verified_targets":0}
```

这是旧版后台的证据，不是新探测逻辑已经获权的证明。随后用户提供“fleet-agent 已开启”的截图，再次只读核对系统 FDA 授权表，确认以下记录（client_type 1 为可执行路径、0 为 bundle identifier；auth_value 2 为允许、0 为关闭）：

```text
/Users/hjc/.local/bin/fleet-agent | 1 | 2
com.macfleet.fleet-hub           | 0 | 0
```

当时实际后台签名 identifier 为 `com.macfleet.fleet-agent`，程序位于应用包内而不是旧 CLI 路径；它没有对应的系统 FDA 授权记录。只读复查仍返回 restricted，随后指导用户添加内嵌 `Fleet Agent.app`。这一步仅比较签名与授权表，没有核对 TCC 实际归属，指引不完整，已在下方 2026-10-07 诊断中纠正。检查中安全准备接口返回 ok，随后立即 resume，当天未停止或重启进程。

本次没有部署 abj、签名/公证新版本、发布 DMG/升级 ZIP，或修改系统 TCC 数据库、launchd 定义、Desktop 环境与当前网络。新版 OAuth 必须与新版服务端同步发布。正式发布、真人 owner 确认、真实 mesh 入网及 FDA 授权仍须分别取得证据。

## 2026-10-07 实际 TCC 归属诊断

用户完成内嵌后台授权后，只读查询系统 FDA 表确认：

```text
com.macfleet.fleet-agent | 0 | 2
com.macfleet.fleet-hub   | 0 | 0
```

既有正式后台 `desktop prepare-stop` 返回 `{"ok":true,"pid":73133}` 后，执行 `launchctl kickstart -k gui/$(id -u)/com.macfleet.desktop-agent`。新 PID 为 73774，launchctl 确认 running；应用、配置及网络未替换。新进程的实际 `desktop disk-recheck` 仍返回：

```json
{"state":"restricted","source":"background","checked_at":1791334704,"verified_targets":0}
```

`log show --last 4m --info --debug` 的 Fleet 专属 TCC 日志提供了根因：

```text
BUNDLE_ATTRIBUTION: executable path .../Fleet Agent.app/Contents/MacOS/fleet-agent resolves to attributed bundle: file:///Applications/Fleet%20Hub.app/
AUTHREQ_SUBJECT: msgID=37140.572, subject=com.macfleet.fleet-hub
Handling access request to kTCCServiceSystemPolicyAllFiles, from Sub:{com.macfleet.fleet-hub} ... ReqResult(Auth Right: Denied (Service Policy) ...)
```

TCC 将内嵌后台归属于外层主应用，主应用的代码 requirement 校验 status 为 0，而其权限处于关闭状态。不能再归因于旧 CLI、签名不匹配或进程未重启。当时将应用定位按钮、Web 安装说明改为主应用；用户随后指出 Hub 只是设置程序，必须由 agent 独立获权。这一规避方案已经撤回，不能将主应用授权当作本需求的修复。

当时两项主应用定位测试从红变绿，只证明了规避方案的引导行为，不证明正确的后台权限架构。现已撤回该方案，测试约束改为 UI/Web 不要求 Hub 获权。独立后台的运行布局和生命周期实现仍未完成，不能用文案测试通过声称后台 FDA 已修复。

本次重新运行 `bash scripts/verify.sh`，退出码 0，末行 `==> 全部验证通过 ✓`；Dashboard 为 296 pass、0 fail，打包/发行层为 11 pass、0 fail、1 skip（专用 SDK 缓存测试缺少环境变量），Swift 与 Go、Shell 层均通过。新鲜日志为 `/private/tmp/fleet-fda-guide-verify-20261007.log`。`git diff --check` 退出码 0。末次只读核对主应用仍为 auth_value 0，实际后台 `checked_at=1791334852` 仍返回 restricted；尚未取得主应用授权后的成功证据。

用户纠正后，本需求的最终验收改为“Hub 不获 FDA，独立 agent 实际读取成功且 TCC 权限主体为 agent”；不再要求主应用授权。方案边界记录在 `docs/superpowers/specs/2026-10-07-independent-agent-fda-design.md`。当前仅撤回错误引导、保留诊断并修正规格，未安装独立后台或发布新包。

## 2026-10-07 独立后台源码与隔离验收

用户要求直接实施后，已将运行副本改为 `~/.macfleet/desktop/runtime/Fleet Agent.app`，与 Hub 发行载荷分离；新增独占安装锁、同团队 Developer ID/版本检查、空闲迁移、原子替换和健康失败回滚。登录启动器为独立 Swift 可执行文件，仅验证私有定义并启动独立后台。设置页与 Web 定位实际运行副本，Hub 不获 FDA；Go 后台拒绝外层 `.app` 中的载荷作为成功权限证据。Sparkle 恢复同步独立后台，即使旧 daemon 可回答状态也不会误跳过升级。卸载仅移除 Fleet 自己的运行目录。

测试先失败再实现：启动路径旧值、缺失安装/启动接口、旧登录程序、未单独公证的发布入口、错误升级恢复、匹配版本的嵌套符号链接及缺失运行目录清理均取得红灯。随后对应测试通过。独立链路聚焦 Swift 为 22 pass、0 fail，包括首装不启动、旧嵌套迁移、忙碌守卫、失败恢复、相同版本不重复重启、符号链接拒绝、包验证、锁互斥、保留失败备份及卸载范围。

重新执行 `bash scripts/verify.sh`，exit 0：

```text
Dashboard: 296 pass, 0 fail
发行/打包: 13 pass, 0 fail, 1 skip
Swift: FleetCore 43 + FleetHub 3, 0 failures
Go agent/server 及全部 Shell 测试通过
==> 全部验证通过 ✓
```

skip 为缺少 `FLEET_SPARKLE_ARCHIVE` 时的专用缓存篡改测试。日志：`/private/tmp/fleet-independent-final-verify.log`。

Universal 开发构建 exit 0，产物为 `/private/tmp/fleet-independent-final/fleet-settings.VW8ALY/Fleet Hub.app`（0.1.1+2，仅开发编号）。Hub、Fleet Login 与 agent 都包含 x86_64/arm64，`vtool -show-build` 确认六个切片的 minos 均为 13.0。日志：`/private/tmp/fleet-independent-final-build.log`。

`node scripts/settings-runtime-uat.mjs <该开发包>` 从载荷复制真正的后台到独立临时 HOME 后执行，exit 0：

```text
PASS: standalone copy of actual bundled agent, private control socket, unbound status, no fabricated FDA evidence, legacy update blocked
PASS: matching app/agent version, maintenance rejects settings, graceful daemon restart preserves isolated configuration
PASS: bundled filebrowser initializes and serves only isolated test files over loopback
PASS: bundled ttyd responds and bundled tmux creates an isolated session without Homebrew
```

实际启动器拒绝无效操作，也未操作真实 launchd。日志：`/private/tmp/fleet-independent-final-runtime.log`。`go test -race -tags fleet_desktop ./...` exit 0，`ok fleet-agent 7.178s`，日志：`/private/tmp/fleet-independent-final-race.log`。`git diff --check` exit 0。

本次没有部署 abj、安装新客户端、替换现有 launchd、修改 TCC 或执行签名发布；只读核对现有 `/Applications/Fleet Hub.app` 仍为 0.1.0+1。新包未签名、公证，不提供为正式下载。隔离后台为普通子进程，FDA 输出保持 unknown，不能据此声称真实授权正常。最终仍须用正式签名产物验证实际 launchd 进程的 TCC 权限主体与受保护目标读取，不能再要求 Hub 获权来补救。
