# Fleet Hub 原生 macOS 客户端

## 交付与边界

同一个 Fleet Hub 应用支持单用户和多用户服务。新版用户在“关联账号”填写服务网页地址，点击“打开网页授权”；后台每次生成新的 OAuth 授权码 + S256 PKCE 请求，浏览器登录并确认账号与设备后自动接入。原生应用不使用配对码，不重开过期链接；旧 CLI 配对协议保留兼容。

正式交付形态为 Universal `Fleet Hub.app` 和 DMG，最低部署目标 macOS 13。设置窗口与独立的 `Fleet Agent.app` 后台分离。应用包内含 ttyd、tmux、filebrowser 和用户态 mesh，不要求使用者安装 Homebrew，也不切换系统现有 Tailscale 网络。

源码及隔离验证已完成；2026-10-03 已按用户授权更新 abj 独立验收服务。2026-10-04 唯一签名机的正式候选流程成功，应用和 DMG 均已签名公证，可信下载与本机图形安装通过。真实用户归属确认、设备入网和完全磁盘访问仍需用户验收。开发包不得作为官网下载或正式安装包。

## 本机状态

- 应用：`/Applications/Fleet Hub.app`。
- 新版后台运行副本：`~/.macfleet/desktop/runtime/Fleet Agent.app`；发行载荷仍携带在 `Contents/Library/LoginItems/Fleet Agent.app`，只供校验、复制及管理命令使用，不作为 launchd 或 FDA 授权目标。当前已安装的 0.1.0+1 尚未替换。
- 服务：`com.macfleet.desktop-agent`；登录启动器：`com.macfleet.desktop-login`。
- 自有数据：`~/.macfleet/desktop`，目录 0700、配置和控制 socket 0600；设备凭据不放进 plist 或 UI 状态。
- 用户态 mesh、文件服务数据库、终端 socket、代理设置与消息队列均在自有目录，不复用旧 CLI 客户端状态。
- 原有 `com.macfleet.fleet-agent` 运行时拒绝并行启动；不自动迁移、停止或替换旧客户端。
- 现有 Codex shared app-server 只复用，不修改 Desktop 环境。全新 Mac 的 shared keeper 配置与可用性仍须独立验收，不能用终端/文件服务就绪推断聊天可用。

FDA 只能由用户在系统设置开启。状态来自实际 launchd 后台的只读探测；普通开发子进程、后台离线、保护目标缺失均不得显示已授权。权限通过也不绕过文件权限与 ACL。

新版仅以实际后台能否读取受保护的用户 TCC 数据库作为完全磁盘访问证据；其他目录的 Unix 权限拒绝保留为文件诊断，不再覆盖已验证的 FDA 状态。缺失数据库或不明错误仍为“待检查”。磁盘状态旁的刷新图标重新执行后台检查；添加应用授权后需通过安全守卫重启后台。授权对象是独立运行的 `Fleet Agent.app`，Hub 只负责设置。2026-10-07 旧版嵌套后台被 TCC 归到 Hub 是应用架构缺陷，不是用户没有授权 agent；本次源码已整改，正式签名安装后的 TCC 权限主体与真实读取仍需分别验证，不能从源码或隔离测试推断 FDA 已成功。

## 2026-10-07 独立后台整改

Hub 打开后准备独立运行副本，不因查看设置而启动原本已停止的后台。已有嵌套后台迁移前检查空闲；相同版本、独立启动定义及运行进程无需每次重启。启动目标、资源 PATH 与磁盘页定位都指向独立副本，管理命令只通过原有 0600 私有控制 socket 请求状态和设置。

主应用、载荷与复制后的后台分别检查身份、版本、同团队 Developer ID 及 Gatekeeper。部署有独占锁，拒绝符号链接或位于其他 `.app` 内的目标。先验证再停止，原子替换；新进程或设备服务健康失败先停止新服务，再恢复旧后台及原启动定义。若无法安全停止或恢复，则保留备份并报错，不覆盖仍运行的进程。账号/设备凭据和用户网络不迁移到其他目录或账号。

登录启动器改为独立 `fleet-login-launcher`，签名 identity 为 `com.macfleet.desktop-login`；它只验证固定后台和私有启动定义后调用 launchctl，不处理受保护磁盘内容，不再把真正的 agent 注册为 Hub 的嵌套登录程序。首次升级注册会刷新该启动器定义。卸载停止 Fleet 服务并删除独立运行目录；保留设置时不删除状态或其他用户文件。

Sparkle 完整应用更新恢复总会同步后台版本，即使旧 daemon 仍能回答状态请求；恢复未完成时不另行启动后台迁移。唯一签名入口增加后台单独公证 Accepted、stapler 和 Gatekeeper 验证，再签名/公证主应用与 DMG。固定 agent identifier 和团队要求不变，不通过重新授予 Hub 权限处理迁移。

全量验证、Universal 开发构建和实际独立后台隔离运行通过，证据见 `native-titanium-oauth-validation.md`。本次未替换 `/Applications/Fleet Hub.app`、当前 launchd 服务或 abj 安装源，正式包与真实 FDA 验收仍未执行。

## 2026-10-05 源码整改

指定第一版 `logo-raised-view.svg` 已纳入构建：主/后台应用均绑定完整 `.icns`，DMG 中的应用使用同一图标。设置窗口使用 Titanium 明暗表面、钢蓝交互与统一控件尺寸，缩减文案，运行详情收纳；首页未关联入口直达“关联账号”。

服务端新增 `/oauth/authorize`、`/oauth/consent`、`/oauth/token` 与受登录及 CSRF 保护的确认 API。回调只接受动态 loopback HTTP 地址，授权码绑定 PKCE、callback 和浏览器账号，两分钟有效、一次兑换；入网令牌仅有 `device:enroll` 权限。所有 OAuth 页面不进入 Service Worker 缓存。浏览器等待中的请求可取消或替换，旧 listener 关闭；已签发设备凭据的失败关联仍须先解除，UI 提供清理入口，不静默覆盖授权。Web 安装说明同步新流程。

本次源码与隔离验证不替换已安装的 0.1.0+1，不部署 abj，也不发布开发包。新 OAuth 需要新版服务器与客户端同时发布。正式签名、公证及实际 FDA/浏览器验收另行执行。

完整命令、真实输出、窗口检查与未验收边界见 [本次验证记录](native-titanium-oauth-validation.md)。

## 构建与验证

开发需要 Go（agent 模块指定工具链）、Node、Xcode/Swift 及 CMake。下载依赖均锁版本和 SHA；缓存路径可以通过变量指定。

```bash
export FLEET_CMAKE=/path/to/cmake
export FLEET_RUNTIME_CACHE=/private/tmp/fleet-runtime-cache
bash scripts/build-settings-runtime.sh /private/tmp/fleet-runtime
bash scripts/build-settings-filebrowser.sh /private/tmp/fleet-runtime
bash scripts/check-settings-runtime.sh /private/tmp/fleet-runtime/universal
bash scripts/prepare-settings-sdk.sh
FLEET_SETTINGS_RUNTIME=/private/tmp/fleet-runtime \
  bash scripts/build-settings-app.sh --development /private/tmp/fleet-preview
node scripts/settings-runtime-uat.mjs '/private/tmp/fleet-preview/实际构建目录/Fleet Hub.app'
bash scripts/verify.sh
```

隔离运行测试仅使用临时 HOME、私有 socket、动态 loopback 端口及测试文件，不安装 launchd、不注册系统登录项、不授予 FDA、不接入真实 mesh。它验证真实捆绑二进制运行，不等于安装验收。

macOS 新 SDK 可能在 configure 阶段误检测旧系统不支持的 `pipe2`。构建明确关闭 libevent/libwebsockets 的该探测，并检查 Mach-O 架构、最低系统版本与外部库依赖。最低版本声明不代替 macOS 13 实机验证。

## 发布与升级

唯一入口仍是签名机的 `scripts/release-fleet-agent.sh`。新增 `--native-candidate-check` 与 `--native-candidate`，复用签名机、干净分支、私有配置、专属实例标记及全量验证守卫。当前任务已执行 `--native-candidate`；只有流程完整成功才算下载源发布完成。

私有配置新增 `FLEET_SETTINGS_VERSION`、递增的 `FLEET_SETTINGS_BUILD`、`FLEET_CMAKE`、明确的 `FLEET_CODESIGN_IDENTITY`、`FLEET_SPARKLE_ACCOUNT`。Sparkle 更新密钥由签名机管理员一次性建立并保管在 Keychain；发布脚本只读取已有公钥并签署包，不自动生成、导出或轮换私钥。

候选流程重新从锁定源码构建依赖，构建应用、隔离运行验证、嵌套签名、应用公证 Accepted/staple、生成完整 ZIP 和 DMG、DMG 公证 Accepted/staple，才生成清单和发布。任一步失败均停止，不能将开发包作为回退。

公开路径：

- `/enroll/client-release.json`：当前正式候选清单，禁止缓存旧清单。
- `/enroll/appcast.xml`：从配置 origin 生成的 Sparkle feed。
- `/enroll/clients/<build>/Fleet-Hub.dmg`：不可变安装包。
- `/enroll/clients/<build>/Fleet-Hub-update.zip`：不可变整包升级。

应用从已保存的服务 origin 检查升级，不使用硬编码 IP。升级拒绝降级、错误包身份、未公证声明、无签名、跨源路径及与清单不符的 appcast。Sparkle 校验更新签名并替换整个应用；裸 agent 的旧更新命令在应用内部被禁用。

安装前检查活动操作、保留旧应用私有备份，然后停止自有后台。取消升级会中断准备流程，若后台已停止则尝试恢复。新应用重开后核对后台 PID、精确版本和构建号、设备关联与运行状态；已有设备不能以启动瞬间的 unbound 状态通过检查。失败保留恢复记录并尝试受守卫保护的回滚，不强杀未知活动任务。该升级/回滚代码尚未经过两个正式签名版本之间的实机验证，不应宣称升级已验收。

卸载允许从后台已停止的状态发起：先临时启动后台、读取真实设备关联并通过空闲检查，再撤销授权和移除应用。后台启动失败、无法确认关联或设备繁忙时保留应用，不猜测“未绑定”来跳过撤销。

SDK 准备每次从校验通过的固定归档重新提取并替换自有 Vendor 缓存，避免信任已被修改的解压目录；并发准备会拒绝。正式候选预检在执行 Sparkle 签名工具前也执行这一校验。

## 当前证据与待验收

2026-10-03：隔离分支通过项目全量验证、`go test -race -tags fleet_desktop ./...`、原生模型/安全回调测试，以及真实内置 agent、filebrowser、ttyd、tmux 的临时目录运行测试。原生窗口已实际打开，FDA 页面正确显示“待验证”，未操作系统授权。

同日合回主工作区后的复验：`bash scripts/verify.sh` 输出“全部验证通过 ✓”，包含 295 项 Dashboard JS、10 项发行/打包测试、26 项 Swift 测试及 Go/Shell 层；原生 Go race 测试通过。最终 Universal 开发应用的两架构最低版本声明均为 13.0，内置服务隔离运行的四项检查全部 PASS，涵盖配置保存、维护锁、正常退出与重启恢复。未留存测试后台进程，也未替换正式应用。

服务端部署：源码 `78840d060c52212f240e0ca4c55d3cd00f36ec5c` 已合并最新 main 并推送特性分支。abj 的 server release 已切换到该 revision；`/healthz`、`/readyz`、`/auth` 均为 200，未登录 `/api/devices` 为 401，服务、Web、Headscale、mesh 四个 systemd 单元均 active。停写一致性备份后保留数据库与原加密密钥，部署时注册用户 1、设备 0。该证据不表示其他现有部署或 Mac 已迁移。

客户端发行：用户解锁并允许钥匙串授权后，重新执行完整唯一发布入口，源 revision 为文档更新后的 `be163f2a3d861d72eab3c017c2ce6bdd6d743f02`，版本 `0.1.0`、构建号 `1`。应用公证 `a9df96eb-9a89-4405-b943-e75a9996a36b`、DMG 公证 `1d730fc1-2300-4826-ac4b-5edf738f9d71` 均为 Accepted；staple、严格深层验签和 Gatekeeper 评估通过。服务器正式候选清单、appcast、DMG、升级 ZIP 已上线且真实下载逐字节比对通过。

本机安装：真实下载的 DMG 根目录仅有 `Fleet Hub.app` 和 `Applications` 链接；通过应用内“安装到应用程序”成功复制并自动打开 `/Applications/Fleet Hub.app`。安装后的 Gatekeeper 输出 `accepted`、`source=Notarized Developer ID`。自有后台 `com.macfleet.desktop-agent` 实际启动，首次观察 PID `73133`、版本 `0.1.0+1`，私有目录 0700、控制 socket 0600。登录启动项注册成功，其实际任务退出码为 0；尚未实际注销/登录验收。保存本服务 origin 后由后台发起配对，浏览器自动打开真实确认页，等待用户确认；FDA 实际后台探测为 restricted，不显示虚假授权。

尚需：停止/卸载及断电失败路径的进一步集成覆盖、全新机器的 Codex shared 配置、真实浏览器 owner 确认与 Headscale 入网、关闭窗口后常驻、实际注销/登录后的自动启动、FDA 真后台授权、两个发行版本升级/回滚、卸载残留核验。以上均不可由单元测试或安装成功替代。

候选发布已加入发布锁、不可变构建目录、晋级前的真实包下载比对、并发指针检查和清单下载失败回滚；这些保护的源码测试通过，首次真实发布完整成功，失败回滚分支未在真实服务器主动注入故障。仍须补齐新 UI 完全无法启动时的独立恢复机制、升级下载重定向的同源约束。当前恢复入口依赖新应用能够启动；不能据此承诺任何崩溃都自动回滚。
