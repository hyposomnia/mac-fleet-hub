# AGENTS.md — mac-fleet-hub 部署手册（人 / AI 通用）

本文件既是给人看的部署参考，也是给 AI 编码助手（Claude Code / Codex 等）执行部署的 runbook。

**当前授权范围：`codex/multi-user-server` 的服务器及逐用户客户端开发与独立本地部署验证。** 用户已追加客户端开发授权：网页地址、自动浏览器登录确认、自动入网登记。不得替换当前 Mac/launchd/Desktop/生产网络，不执行生产部署；正式 Mac 二进制仍须唯一构建机签名与公证。源码实现、隔离联调和正式发布分别报告，不把预期当成验证证据。

## 项目人格

Claude 项目命令 `/dev`、`/ui`、`/deploy` 在 Codex 中分别使用 `$dev`、`$ui`、`$deploy`。
人格通过私有子仓库 `.agents/skills/<name>/SKILL.md` 激活；Codex 已注册角色的稳定定义位于
`.codex/agents/<name>.toml`，由 `.codex/config.toml` 注册。启动后先读取
`.agents/<name>/memories/MEMORY.md` 索引，再按任务加载相关详情。

**如果你是 AI**：先读完本文件，判断当前机器的角色（**网关** 还是 **Mac 客户端**），按对应章节执行。
- 只问「**只有用户才知道**」的信息（域名、证书位置、是否封 443、网关地址、设备归属账号），其余用默认值；账号密码、TOTP 和恢复码由用户在浏览器内输入，不写进共享日志。
- **不要问用户「这是第几台 Mac」**——新系统编号由统一服务分配且不复用，不按现有数量猜测，也不由客户端决定 owner。
- 任何 `sudo` / 覆盖现有配置 / 重启服务前，先一句话说明要做什么。
- 完成后用真实命令给出验证证据（`curl -I`、`systemctl status`、健康检查），不要只说「好了」。

## 名词

- **用户 / 设备 / 管理员**：系统只有这三个概念，同一公开源码和流程支持单用户与多用户。每台设备只归属一个用户；管理员只管理账号和设备元信息，不获得他人的文件、终端或聊天正文访问权。
- **网关**：一台常驻 Linux 服务器（VPS、家里的小主机 / NAS 均可），是整套系统对外的唯一入口，跑 nginx + Headscale + 统一 Go 服务。`server/enroll` 的二进制仍名为 `fleet-enroll`，新系统没有 Authelia 运行依赖或全局入网 TOTP。
- **mesh**：Headscale 自建的私有组网（类 Tailscale）。各 Mac 只在 mesh 内可达，不直接暴露公网。
- **Mac 客户端**：被远程操作的 Mac，跑 ttyd（网页终端）/ filebrowser（文件）/ fleet-agent（会话管理）；Codex 自绘聊天与 Codex Desktop 默认连接同一个仅监听 `127.0.0.1` 的 app-server WebSocket，因此同一会话只有一个底层 writer。

---

## 一、网关部署

本阶段先在当前开发机运行独立本地服务器。Linux 网关配置供后续明确授权的部署使用；这里的命令与预期不表示生产已上线。

### 本地运行与验证

从仓库根目录运行，前提是 Go 和 Node.js 可用：

```bash
bash scripts/run-local-server.sh /private/tmp/macfleet-multiuser-local
```

监听 `127.0.0.1:7099`，入口 `http://127.0.0.1:7099/auth`，状态保存在传入的独立目录。不得覆盖当前 Fleet 状态、Mac LaunchAgent 或 Desktop 环境，不得把生产 Headscale key 或节点配置写进本地示例。

本地脚本设置数据库目录为 `<传入目录>/state`，独立密钥为 `<传入目录>/encryption.key`。脚本仅通过 `LOCAL_HEADSCALE_URL`、`LOCAL_HEADSCALE_API_KEY_FILE`、`LOCAL_GATEWAY_IP` 和 `LOCAL_LOGIN_SERVER` 显式启用独立测试网络；默认不继承现有服务的 Headscale 配置。

```bash
curl -i http://127.0.0.1:7099/healthz
curl -i http://127.0.0.1:7099/readyz
curl -i http://127.0.0.1:7099/api/devices
curl -i -X POST http://127.0.0.1:7099/enroll/join
```

预期：存活 `/healthz` 为 200；网络未配置时 `/readyz` 为 **503**，领取入网凭据也必须失败，不能伪报设备已接入；未认证私有 API 为 401；旧版 `/enroll/join` 为 **410**。浏览器注册、验证器绑定和账号页可以独立验证，真实 Headscale、节点连接、客户端安装需分别取得证据。测试及本地运行结果由主任务提供真实输出后再报告，本阶段未迁移任何当前客户端。

### 注册、登录与管理员

1. 在 `/auth` 用邮箱、密码、确认密码注册。邮箱只是登录标识，不要求邮件验证；新注册用户不自动获得管理员权限。
2. 每个用户必须用 Authenticator 绑定 TOTP 并验证后才能获得正式访问权限；保存首次显示的一次性恢复码。恢复需密码与未使用恢复码，会撤销原会话并重新绑定验证器。
3. 正式会话自签发起绝对有效 30 天，访问不会续期。`/account` 管理密码、验证器、恢复码及会话。
4. 用同一服务器配置显式提升已注册邮箱：`fleet-enroll admin <registered-email>`。`/admin` 只展示账号/设备元信息并提供禁用、会话撤销与设备撤销；访问他人的设备代理、文件、终端或消息正文仍被拒绝。

所有私有请求由 Go 服务鉴权并检查 owner，写操作检查 Origin 和 CSRF。私有页面、API 与设备路径禁止缓存，切换或退出账号必须清理浏览器私有状态及活动连接。

### 统一服务配置

服务使用环境变量配置；网关服务环境文件可放在 `/etc/fleet-enroll/env`，应保持私有权限。真实域名、网关 mesh IP、凭据路径均取自本部署，不复制其他部署的值。

| 参数 | 作用 | 默认 / 配置要求 |
|---|---|---|
| `FLEET_ORIGIN` | 浏览器完整入口 origin，包含实际对外端口，不含路径 | 必填；HTTPS，显式 loopback HTTP 仅供本地运行 |
| `FLEET_STATE_DIR` | 持久状态目录，数据库为 `fleet.sqlite` | `/var/lib/fleet-enroll`；本地脚本使用传入目录 |
| `FLEET_STATIC_DIR` | dashboard 发布后的静态资源目录 | `/var/www/fleet`，本地使用仓库 dashboard |
| `FLEET_KEY_FILE` | TOTP 加密密钥的独立文件，权限 `0600` | 状态目录下 `encryption.key`；重启必须沿用原文件 |
| `FLEET_HEADSCALE_URL` | Go 服务访问 Headscale API 的 URL | 网络集成时必填，不等同于浏览器入口 |
| `FLEET_HEADSCALE_API_KEY_FILE` | Headscale API key 私有文件 | 网络集成时必填；`0600`，不直接把 key 写入共享环境示例 |
| `FLEET_GATEWAY_IP` | 网关实际 mesh IP，生成网关访问设备的 ACL | 网络集成时必填，从当前部署取证 |
| `ENROLL_LOGIN_SERVER` | 设备入网使用的 Headscale 控制面 URL | 按实际对外端口配置，不与本机 API URL 混淆 |
| `ENROLL_LISTEN` | 统一 Go 服务监听地址 | `127.0.0.1:7090`；本地脚本使用 `127.0.0.1:7099` |

账号、会话、设备归属、偏好等保存在 `/var/lib/fleet-enroll/fleet.sqlite`（或指定状态目录），数据库权限 `0600`。TOTP 使用独立 `encryption.key` 加密，密码用 Argon2id，恢复码与会话令牌保存哈希；访问密钥与消息队列在用户专属状态目录内隔离保存。备份/恢复必须包含一致的 SQLite 状态与原密钥，并保护备份权限。不要只恢复数据库后生成新 key。

### Linux 网关前提与入口

1. **nginx** 已安装，站点 include 可用；只负责 TLS、统一 Go 入口和明确公开的安装包分发。
2. **TLS / DNS**：证书已签发，浏览器服务域名解析到网关。证书签发及现有站点修改需遵循用户授权。
3. **Headscale** 已可用，Go 服务通过 API key 文件接入；网关的实际 mesh IP 已确认。新系统不安装或启动 Authelia。
4. 新设备路由由数据库中的 owner、节点及状态动态决定；未知节点默认拒绝，**新部署不配置 `MAC_IPS` 静态反代**。nginx 不得保留绕过 Go 授权的 `/mN/` 私有代理。

TLS、Headscale 安装相关参数仍可按实际网关配置：

| 参数 | 作用 | 默认 | 何时改 |
|---|---|---|---|
| `DOMAIN` | 根域名，通配符证书 `*.DOMAIN` 的注册域 | （必填） | 填你的域名 |
| `FLEET_HOST` | 对外服务子域，**web 入口 + Headscale 控制面共用**；需 DNS 解析到网关公网 IP | （必填，建议 `fleet.<DOMAIN>`） | 填你的子域 |
| `SSL_CERT` | 通配符证书**完整链**（fullchain）路径 | （必填） | 指向 acme.sh/certbot 产物 |
| `SSL_KEY` | 证书私钥路径 | （必填） | 同上 |
| `GATEWAY_PORT` | web 对外端口（= 访问 URL 里的端口；nginx 内部恒听 443） | `443` | 仅 ISP 封 443 时改高位（见下） |
| `HEADSCALE_PUBLIC_PORT` | Headscale 控制面对外端口 | `8443` | 仅 ISP 封时改高位 |
| `HEADSCALE_LISTEN_PORT` | Headscale 本机实际监听端口 | `8443` | 一般不改 |
| `TTYD_PORT` / `FB_PORT` / `AGENT_PORT` | 各 Mac 上三个服务的端口 | `7681` / `8080` / `7682` | 端口冲突才改 |
| `NGINX_SITE` | 输出的 nginx 站点文件路径 | `/etc/nginx/sites-enabled/mac-fleet-hub.conf` | 一般不改 |
| `HEADSCALE_DEB` | 指向本地预下载的 Headscale 安装包 | （自动从 GitHub 下载） | 下载失败时 |
| `HEADSCALE_VERSION` | 指定 Headscale 版本号 | （自动取最新） | 需要锁版本时 |
| `FLEET_REPLACE_TAILNET` | 网关已连接其他 Tailscale 控制面时，明确允许退出并切换到 Fleet mesh | `0` | 仅确认可以替换当前 tailnet 时设为 `1` |

### 要不要 NAT？要的话设什么？

先判断：**你的服务器能直接用标准端口 443 对外吗？**

- **能（VPS / 云主机 / 公网不封端口）→ 不需要 NAT**。保持默认 `GATEWAY_PORT=443`、`HEADSCALE_PUBLIC_PORT=8443`，DNS 指向公网 IP 即可。脚本生成的 URL 在 443 时会自动省略端口（`https://host/`）。

- **不能（家庭宽带常封 80/443，或服务器在路由器 NAT 后）→ 需要 NAT，三步**：
  1. **路由器做端口映射（端口转发）**：把两个公网高位端口转到本机标准端口。例：
     - 公网 `20443` → 本机 `443`（web）
     - 公网 `28443` → 本机 `8443`（Headscale）
  2. **`.env` 把对外端口设成你映射的高位端口**：`GATEWAY_PORT=20443`、`HEADSCALE_PUBLIC_PORT=28443`；
     `HEADSCALE_LISTEN_PORT` **保持 8443**（本机监听不变，只是对外端口经 NAT 换了）。
  3. 不用手动处理 hairpin：脚本检测到「对外端口 ≠ 监听端口」会**自动**加一条 DERP 回环重定向，让网关也能连上自己的中继。

  高位端口号你随意（避开已占用即可），只要路由映射和 `.env` 里一致。

`FLEET_ORIGIN` 必须使用浏览器实际看到的 origin；`ENROLL_LOGIN_SERVER` 使用 Headscale 对外控制面 URL，`FLEET_HEADSCALE_URL` 使用服务器实际能访问的 API URL。ACL 仅允许网关 mesh IP 到有效设备服务端口及同一有效用户设备互通，跨用户默认拒绝。

### 旧服务器数据迁移（显式、先备份）

本阶段不运行当前基础设施迁移。后续获授权时，先备份旧服务器配置、names/settings/access-key/messages、Headscale 数据及 ACL，以及已有新 SQLite 状态与 `encryption.key`，确认可恢复后再操作。停止相关写入并使用一致性备份；不能只复制正在写入的 SQLite 主文件而遗漏 WAL。

```bash
fleet-enroll migrate <owner-email>
```

使用目标实例的环境配置并显式提供旧 `MAC_IPS` 及 names/settings/access-key/messages 来源，把这些旧数据导入指定的已注册并绑定 TOTP 的 owner；顺序维持旧 `mN` 编号。迁移读取 `ENROLL_MAC_IPS`（未设置时读 `MAC_IPS`）、`ENROLL_NAMES_FILE`、`ENROLL_SETTINGS_FILE`、`ENROLL_ACCESS_KEY_FILE` 和 `ENROLL_MESSAGE_JOBS_FILE`；旧文件的默认目录为 `/var/lib/fleet-enroll`，文件名分别为 `names.json`、`dashboard-settings.json`、`access-key.json` 和 `message-jobs.json`。这些参数只用于显式导入旧数据，不用于新设备路由。

必须逐个验证 IP 与真实 Headscale node 的关联，名称不是归属凭据，未知或不匹配节点应停止迁移。迁移标记保证重复运行不重复导入；启动不隐式把旧数据交给首个用户。新部署和后续入网都不靠维护 `MAC_IPS` 工作。

### 服务器 / 客户端阶段交接

设备配对 HTTP 协议：`POST /api/enrollment/start` 返回短码、请求 ID、私有领取 token、确认 URL 和有效期；用户在 `/enroll/confirm` 登录后 `POST /api/enrollment/confirm` 确定 owner；客户端通过 `claim` 领取一次性 Headscale 密钥及服务分配的编号，再由 `complete` 按密钥 ID 验证真实 Headscale node/IP 后激活设备。短码不是领取凭据，客户端提交的 owner 或 IP 不作为归属证据。

claim 还返回独立的 device_token/proxy_token，客户端原子写入 `0700` 目录内的 `0600` 文件；设备令牌服务端仅存哈希（配对重试暂存加密值，complete 后清理），代理令牌加密保存。status/binding 只接受设备 Bearer，浏览器会话不能替代设备授权。旧 `/enroll/join` 返回 410，不得用手工 key 或 hostname 推断 owner。

---

## 二、Mac 客户端接入

前提：Homebrew，服务器已发布支持设备授权的新协议正式签名公证 agent。安装器 capabilities 检查失败时停止，不回退旧 agent。本地验证只使用临时开发产物与隔离 OS 适配器，不覆盖正式 dist。

### 配置参数（很少，多数自动）

| 参数 | 作用 | 来源 |
|---|---|---|
| 服务网页地址 | Web origin，例如 `https://fleet.example.com` | 唯一需填写的连接地址 |
| 浏览器配对 | 登录并明确核对名称、短码、当前账号后确认 owner | login 自动打开确认 URL |
| `FLEET_BINDING_FILE` | 私有逐设备授权记录，不把 secret 放进 plist/env | 默认 `~/.macfleet/binding.json` |
| `FLEET_UPDATE_BASE` | 自更新源 `https://<网关>/enroll/dist`，使 `fleet-agent update` 可用 | bootstrap 自动注入 |
| 编号 / `MAC_INDEX` | 该 Mac 的路径标识 `/mN/` | **自动**，别问用户 |
| `TTYD_PORT`/`FB_PORT`/`AGENT_PORT`/`FB_ROOT` | 服务端口 / 文件管理根目录 | 默认即可（FB_ROOT 默认整个 home） |
| `FLEET_CLAUDE_HOME`/`FLEET_CLAUDE_BIN` | Claude 会话库与命令路径 | 默认 `~/.claude` / 自动发现 `claude` |
| `FLEET_CODEX_HOME`/`FLEET_CODEX_BIN` | Codex 会话库与命令路径 | 默认 `~/.codex`；shared 未显式指定时优先 ChatGPT.app bundled Codex，再查找 PATH |
| `FLEET_CODEX_APPSERVER_MODE` | Codex app-server 连接模式：`shared` 让 Fleet 与 Codex Desktop 复用同一 loopback WebSocket；`isolated` 使用 Fleet 专属 Unix-socket sidecar；`auto`、`daemon`、`stdio` 为旧兼容模式 | `shared` |
| `FLEET_CODEX_APPSERVER_SOCK` | Fleet 的 app-server endpoint；shared 默认使用 keeper 提供的 `0600` Unix proxy，以兼容现有已公证 agent，isolated 使用同路径的独立 sidecar；新版 agent 也支持显式 loopback WS | `~/.macfleet/codex-app-server.sock` |
| `FLEET_CODEX_DESKTOP_WS_URL` | Desktop 直连同一 shared server 的 loopback WebSocket；只能是 `ws://127.0.0.1:<端口>[/路径]` | `ws://127.0.0.1:47682/rpc` |
| `FLEET_CODEX_DESKTOP_SHARED_DAEMON` | `shared` 模式下记录 Desktop WebSocket 接入意图并让 agent 持续校正 GUI 环境；设为 `0` 可显式关闭 Desktop 接入 | `1` |
| `FLEET_REPLACE_TAILNET` | 当前已连接其他 Tailscale 控制面时，明确允许退出并切换到 Fleet mesh | `0` | 仅确认可以替换当前 tailnet 时设为 `1` |

### 安装与授权

只填写服务网页地址；邮箱、密码和 Authenticator 留在浏览器。控制面、编号及端口由确认后的服务下发。换账号/服务器先 logout；同控制面也使用本次一次性 key 重新认证，不复用旧 hostname 猜归属。

Mac 无需 clone，服务在 `/enroll/` 提供安装包和正式签名二进制。

```bash
curl -fsSL https://<网关地址>/enroll/bootstrap.sh | bash
fleet-agent login https://<服务网页地址>
fleet-agent status
fleet-agent logout
```
首次安装会装 Tailscale、浏览器确认后入网 Headscale（需 sudo 密码）、起 ttyd / filebrowser / fleet-agent，并默认安装 `com.macfleet.codex-app-server`。默认 `shared` 的 LaunchAgent 先用 ChatGPT 自带、OpenAI 签名的 Node 启动 keeper，再由 keeper 启动唯一一份 `codex -c features.code_mode_host=true app-server --listen ws://127.0.0.1:47682`；Desktop 直连 TCP WebSocket，Fleet 通过 keeper 的 `0600` Unix→TCP 透明桥进入同一 listener（逐用户授权须新协议 agent，桥接方式不变）。这条父子链同时保留 Desktop 对 `codex_app` MCP 的签名校验。监听地址写死为 loopback，不绑定 mesh IP，也不暴露公网。shared 不再 bootstrap 或依赖官方 control socket daemon。

首次从旧 `isolated` / control-socket 方案切换时，安装会停止现有 Fleet app-server 与仍在运行的旧 control-socket daemon，把旧 agent、plist 与 keeper/helper 留在 `~/.macfleet/migration-backups/`，加载新的 shared LaunchAgent，并在提交迁移前验证 loopback `/readyz`、监听地址和 agent health；失败会恢复旧文件与服务。Aqua one-shot LaunchAgent 会在当前及后续登录会话中设置 Desktop 环境，但脚本**不会自动中断 ChatGPT.app 的活动 turn**；确认 turn 完成并退出 App 后，用 `/usr/bin/open --env CODEX_APP_SERVER_WS_URL=ws://127.0.0.1:47682/rpc -a /Applications/ChatGPT.app` 确定性重开。

shared 只让两端共用同一个 app-server 与物理 writer，不混淆 turn 的操作归属：同一服务上的未知 active turn 保守视为 Desktop，只有 Fleet 自己成功 `turn/start` 返回的 turn 才能由网页 steer、停止或处理审批；外部 turn 走服务端持久化队列。keeper 读取目标版本的 `desktop-mcp.json`，只认 ChatGPT 主进程持有、同 UID 且权限为 `0600` 的 App Tools socket，并原子更新稳定软链；pipe 换代时只 reload MCP，不重启 shared app-server。App tools 仍须在目标 Desktop 版本上以 `runtimeStatus=connected` 和实际工具清单做 UAT。

若目标机需要进程级隔离，可显式设置 `FLEET_CODEX_APPSERVER_MODE=isolated FLEET_CODEX_DESKTOP_SHARED_DAEMON=0`。该回退使用 `~/.macfleet/codex-app-server.sock`，并同时取消 GUI 中的 `CODEX_APP_SERVER_WS_URL` 与 `CODEX_APP_SERVER_USE_LOCAL_DAEMON`。
ttyd/filebrowser 仅监听 loopback，网关统一经 agent 转发并注入逐设备凭据。设备授权独立于浏览器 30 天会话；agent 每 15 秒校验，45 秒 lease 到期 fail closed；本地 logout 后存量访问由 100ms 私有文件观察周期关闭。失败保留锁定凭据供 logout 重试。

### 验证

```bash
tailscale ip -4                                   # 拿到 100.x mesh IP = 入网成功
curl -s http://<本机meshIP>:7682/api/health       # 期望 ok
curl -fsS http://127.0.0.1:47682/readyz            # shared 默认：期望成功
launchctl getenv CODEX_APP_SERVER_WS_URL           # shared 默认：期望 ws://127.0.0.1:47682/rpc
launchctl getenv CODEX_APP_SERVER_USE_LOCAL_DAEMON # shared 默认：期望为空
launchctl print gui/$(id -u)/com.macfleet.codex-app-server | grep state
```
上述配置和健康检查只证明 shared server 已启动；必须在完全重开的 Desktop 与 Fleet 中打开同一个 thread，确认不会出现“已在另一个应用中打开”。用 `lsof -nP -iTCP:47682` 验证 Desktop 与 keeper proxy 均连接同一 listener，再用 thread writer lock 验证物理 writer 只有该 listener PID。显式 `isolated` 时应期望两个 GUI 环境变量都为空。
当前 Desktop 的 WebSocket client 无法附加认证 header，所以 listener 必须保持 loopback；它隔离 mesh/公网，但不是同机多用户之间的安全边界。多用户共享 Mac 不应启用该模式。
回到手机/浏览器打开网关入口 → 登录 → 应能看到这台 Mac 并进入它的终端 / 文件。

---

## 三、fleet-agent 统一签名发布

fleet-agent 的正式产物只能由持有 **Developer ID Application** 私钥和 Apple 公证凭据的唯一签名
构建机生成。固定代码身份为 `com.macfleet.fleet-agent`；禁止使用 Apple Development、ad-hoc、
未公证产物，禁止在其他 Mac 自行编译后直接覆盖生产分发源。Developer ID 签名后必须提交 Apple
公证并取得 `Accepted`；裸 Mach-O 的 `spctl --type execute` 可能只报告“不是 app”，最终以公证
Accepted、目标机实际执行及 launchd canary 为准。

在签名构建机上唯一支持的发布入口：

```bash
ssh <签名构建机用户>@<签名构建机当前 mesh IP>
cd ~/Git_Repositories/mac-fleet-hub
bash scripts/release-fleet-agent.sh --check   # 只读预检
bash scripts/release-fleet-agent.sh           # 签名二进制发布 + 逐台自更新
```

完整脚本按以下顺序执行，AI 不得拆开、跳步或用手工替换冒充完成：

1. 确认当前 mesh IP 是签名机、工作树干净、分支正确，再 `git pull --ff-only origin main`。
2. 运行 `bash scripts/verify.sh`，并在构建或修改网关前预检所有 Mac 的 Fleet 自更新安全状态；Desktop-owned Codex turn 可继续运行。
3. 双架构构建，以固定 identifier 做 Developer ID 签名、可信时间戳和严格验签，并等待 Apple 公证
   `Accepted`。
4. 提交/push 精确产物；从同一不可变提交取得新安装入口脚本。
5. 网关先备份现有 dist 和 bootstrap，再替换，核 amd64/arm64 SHA 与核心服务状态；从公网下载再次核 SHA。新 Mac 初装从 bundle 取脚本，安装器从 dist 下载并验证支持新授权协议的正式二进制，检查通过后才进行入网。仅二进制更新不重打 bundle；安装协议变化则必须同步分发新脚本包。
6. 按私有节点清单逐台检查 Fleet-owned turn 和进行中的消息投递、备份、运行正式 `fleet-agent update`、验签并核新 PID、磁盘 SHA、mesh health。仅 agent 更新不重新打包 `mac-bundle.tar.gz`。
7. SSH/SCP 和远端验证 stdout/stderr 原样输出；SSH 连续三次失败或任一步异常立即停止，不带病继续。

真实基础设施值只放在签名机 `~/.config/mac-fleet-hub/release.env`（`0600`），格式参考
`scripts/release-fleet-agent.env.example`。证书、私钥、App 专用密码、notarytool 凭据和真实节点配置
不得进入仓库。其他开发机只提交源码；正式签名、公证和 rollout 统一由签名机构建脚本完成。
若需迁移 plist/keeper/Desktop 启动环境、并明确保留仓库内已有的已签名公证 agent，可在签名机运行
`bash scripts/deploy-shared-config.sh`；它只替换 `mac-bundle.tar.gz`，不构建或发布新二进制，并逐台执行
空闲守卫、shared UAT 与 SHA/验签。不得用它冒充新 agent 的正式发布。
签名构建机 mesh IP 由每套 Headscale/Fleet 当前分配，并非 mac-fleet-hub 的固定/保留地址，重新入网
后可能变化。AI 必须从该部署的私有 `FLEET_RELEASE_BUILDER_IP`、节点列表或 `tailscale ip -4` 取证，
不得把文档示例或其他部署的 `100.x` 地址当作通用配置。

---

## 给 AI 的收尾准则

- **提交/部署前必须运行项目验证入口 `bash scripts/verify.sh` 并贴出真实输出**（按序执行 mac/fleet-agent 的 `go test ./...`、server/dashboard 的 `node --test chat_model.test.mjs`、tests/tailscale-utils_test.sh 三个测试层；任一失败则先修复再继续）。
- 安装后跑上面的「验证」命令，把真实输出贴给用户。
- 新系统通过浏览器确认 owner 和 Headscale node/IP 校验登记设备，动态路由；不要为新入网设备追加静态 `MAC_IPS`。客户端源码已接入逐用户授权；本地测试不等于已迁移当前 Mac 或正式签名发布。
- 本阶段只修改服务器及授权文档并独立本地验证，不启动生产发布流程。报告已执行命令与实际结果，明确区分服务器 HTTP 接口、客户端安装器和真实设备验证。
- 真实密钥 / 证书 / 密码不要打印到共享日志或写进提交。
