# Fleet Hub 原生 macOS 客户端

## 交付与边界

同一个 Fleet Hub 应用支持单用户和多用户服务。用户只填写服务网页地址，后台发起配对，浏览器完成登录和确认归属，回应用核对账号与设备后接入。

正式交付形态为 Universal `Fleet Hub.app` 和 DMG，最低部署目标 macOS 13。设置窗口与独立的 `Fleet Agent.app` 后台分离。应用包内含 ttyd、tmux、filebrowser 和用户态 mesh，不要求使用者安装 Homebrew，也不切换系统现有 Tailscale 网络。

源码及隔离验证已完成；2026-10-03 已按用户授权更新 abj 独立验收服务。2026-10-04 唯一签名机的正式候选流程成功，应用和 DMG 均已签名公证，可信下载与本机图形安装通过。真实用户归属确认、设备入网和完全磁盘访问仍需用户验收。开发包不得作为官网下载或正式安装包。

## 本机状态

- 应用：`/Applications/Fleet Hub.app`。
- 后台：`Contents/Library/LoginItems/Fleet Agent.app`；它是完全磁盘访问应选择的对象。
- 服务：`com.macfleet.desktop-agent`；登录启动器：`com.macfleet.desktop-login`。
- 自有数据：`~/.macfleet/desktop`，目录 0700、配置和控制 socket 0600；设备凭据不放进 plist 或 UI 状态。
- 用户态 mesh、文件服务数据库、终端 socket、代理设置与消息队列均在自有目录，不复用旧 CLI 客户端状态。
- 原有 `com.macfleet.fleet-agent` 运行时拒绝并行启动；不自动迁移、停止或替换旧客户端。
- 现有 Codex shared app-server 只复用，不修改 Desktop 环境。全新 Mac 的 shared keeper 配置与可用性仍须独立验收，不能用终端/文件服务就绪推断聊天可用。

FDA 只能由用户在系统设置开启。状态来自实际 launchd 后台的只读探测；普通开发子进程、后台离线、保护目标缺失均不得显示已授权。权限通过也不绕过文件权限与 ACL。

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
