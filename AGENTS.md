# AGENTS.md — mac-fleet-hub 部署手册（人 / AI 通用）

本文件既是给人看的部署参考，也是给 AI 编码助手（Claude Code / Codex 等）执行部署的 runbook。

## 项目人格

Claude 项目命令 `/dev`、`/ui`、`/deploy` 在 Codex 中分别使用 `$dev`、`$ui`、`$deploy`。
人格定义位于私有子仓库 `.agents/skills/<name>/SKILL.md`，启动后先读取
`.agents/<name>/memories/MEMORY.md` 索引，再按任务加载相关详情。

**如果你是 AI**：先读完本文件，判断当前机器的角色（**网关** 还是 **Mac 客户端**），按对应章节执行。
- 只问「**只有用户才知道**」的信息（域名、证书位置、是否封 443、网关地址、入网验证码），其余用默认值。
- **不要问用户「这是第几台 Mac」**——编号由系统决定（网关分配，或你按现有数量 +1 自行设定）。
- 任何 `sudo` / 覆盖现有配置 / 重启服务前，先一句话说明要做什么。
- 完成后用真实命令给出验证证据（`curl -I`、`systemctl status`、健康检查），不要只说「好了」。

## 名词

- **网关**：一台常驻 Linux 服务器（VPS、家里的小主机 / NAS 均可），是整套系统对外的唯一入口，跑 nginx + Headscale + Authelia。
- **mesh**：Headscale 自建的私有组网（类 Tailscale）。各 Mac 只在 mesh 内可达，不直接暴露公网。
- **Mac 客户端**：被远程操作的 Mac，跑 ttyd（网页终端）/ filebrowser（文件）/ fleet-agent（会话管理）；Codex 自绘聊天通过独立的 pid-backed app-server daemon 执行，fleet-agent 直接连接它的私有 Unix WebSocket。

---

## 一、网关部署

在常驻 Linux 服务器上执行。

### 前提（先检查，缺了告诉用户怎么补，本项目不替你装/签）

1. **nginx** 已安装，且 `nginx.conf` 里 include 了 `sites-enabled/*`（脚本只注入一个独立 server 块，不动其它站点）。
2. **通配符 TLS 证书** `*.<域名>`：用 acme.sh 或 certbot（DNS-01）签好，nginx 与 Headscale 共用同一对。
3. **DNS**：服务子域（如 `fleet.example.com`）已解析到网关的公网 IP。
4. **Headscale / Authelia** 由脚本自动下载安装。脚本的包管理按 Debian/Ubuntu 系（`apt`/`dpkg`）写；**其它发行版**：手动装好 headscale、authelia、nginx 后，同一套配置照样可用（系统本身不限定）。

### 配置参数（写进 `server/.env`；AI 也可直接用环境变量注入后非交互运行）

| 参数 | 作用 | 默认 | 何时改 |
|---|---|---|---|
| `DOMAIN` | 根域名，通配符证书 `*.DOMAIN` 的注册域 | （必填） | 填你的域名 |
| `FLEET_HOST` | 对外服务子域，**web 入口 + Headscale 控制面共用**；需 DNS 解析到网关公网 IP | （必填，建议 `fleet.<DOMAIN>`） | 填你的子域 |
| `SSL_CERT` | 通配符证书**完整链**（fullchain）路径 | （必填） | 指向 acme.sh/certbot 产物 |
| `SSL_KEY` | 证书私钥路径 | （必填） | 同上 |
| `GATEWAY_PORT` | web 对外端口（= 访问 URL 里的端口；nginx 内部恒听 443） | `443` | 仅 ISP 封 443 时改高位（见下） |
| `HEADSCALE_PUBLIC_PORT` | Headscale 控制面对外端口 | `8443` | 仅 ISP 封时改高位 |
| `HEADSCALE_LISTEN_PORT` | Headscale 本机实际监听端口 | `8443` | 一般不改 |
| `MAC_IPS` | 各 Mac 的 mesh IP，空格分隔，**顺序 = m1 m2 …**；首轮装网关时可留空 | （空） | 每台 Mac 入网后把其 mesh IP 追加进来，重跑脚本 |
| `TTYD_PORT` / `FB_PORT` / `AGENT_PORT` | 各 Mac 上三个服务的端口 | `7681` / `8080` / `7682` | 端口冲突才改 |
| `NGINX_SITE` | 输出的 nginx 站点文件路径 | `/etc/nginx/sites-enabled/mac-fleet-hub.conf` | 一般不改 |
| `HEADSCALE_DEB` / `AUTHELIA_TGZ` | 指向本地预下载的安装包（离线 / 国内网慢时用） | （自动从 GitHub 下载） | 下载失败时 |
| `HEADSCALE_VERSION` / `AUTHELIA_VERSION` | 指定版本号 | （自动取最新） | 需要锁版本时 |
| `AUTHELIA_USER` / `AUTHELIA_PASSWORD_HASH_FILE` | 首次非交互部署的登录用户与 argon2id 哈希文件 | `admin` / （交互时不需要） | AI 或 CI 非交互首次部署时必填哈希文件 |
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

### 执行

```bash
cp server/.env.example server/.env      # 按上表填写（AI 可直接用 env 注入，跳过此步）
sudo bash scripts/setup-server.sh
```
脚本会：前置校验（nginx / 证书 / 解析）→ 装 Headscale + Authelia → **交互式建一个登录用户**（用户名 + 密码，2FA 之后会发 TOTP 二维码）→ 部署 PWA → 按 `MAC_IPS` 渲染 nginx → 起服务 → 网关自身入网并自动打 `tag:fleet-gw` → 部署自助入网服务。

### 验证

```bash
systemctl status headscale authelia --no-pager
curl -I https://<FLEET_HOST>[:GATEWAY_PORT]/        # 期望 302 → /auth（登录门户）
curl -I https://<FLEET_HOST>[:GATEWAY_PORT]/auth    # 期望 200
```
浏览器打开入口 → 应跳转 Authelia 登录。首次需扫脚本打印的「登录 2FA」二维码绑定 TOTP。

---

## 二、Mac 客户端接入

**网关跑起来之后**，在每台要纳管的 Mac 上执行。前提：已装 Homebrew。

### 配置参数（很少，多数自动）

| 参数 | 作用 | 来源 |
|---|---|---|
| 网关地址 | Headscale 控制面 URL，如 `https://fleet.example.com:8443`（NAT 则用对外端口如 `:28443`） | 交互询问 / bootstrap 自动下发 |
| 入网验证码 | 一次性 TOTP，换取入网密钥（走 bootstrap 时用） | 用户的「入网专用」Authenticator 条目 |
| `AUTHKEY` | Headscale 预授权密钥（走手动 install 时用） | 网关 `headscale preauthkeys create` / bootstrap 自动 |
| `FLEET_UPDATE_BASE` | 自更新源 `https://<网关>/enroll/dist`，使 `fleet-agent update` 可用 | bootstrap 自动注入 |
| 编号 / `MAC_INDEX` | 该 Mac 的路径标识 `/mN/` | **自动**，别问用户 |
| `TTYD_PORT`/`FB_PORT`/`AGENT_PORT`/`FB_ROOT` | 服务端口 / 文件管理根目录 | 默认即可（FB_ROOT 默认整个 home） |
| `FLEET_CLAUDE_HOME`/`FLEET_CLAUDE_BIN` | Claude 会话库与命令路径 | 默认 `~/.claude` / 自动发现 `claude` |
| `FLEET_CODEX_HOME`/`FLEET_CODEX_BIN` | Codex 会话库与命令路径 | 默认 `~/.codex` / 自动发现 `codex` |
| `FLEET_CODEX_APPSERVER_MODE` | Codex app-server 连接模式：`isolated` 使用 Fleet 专属常驻 sidecar、与 Desktop 进程隔离；`shared` 显式复用默认 daemon；`auto` 为旧兼容回退；`stdio` 为应急旧行为 | `isolated` |
| `FLEET_CODEX_APPSERVER_SOCK` | Fleet sidecar Unix socket；`isolated` 留空时使用 `~/.macfleet/codex-app-server.sock` | （自动） |
| `FLEET_CODEX_DESKTOP_SHARED_DAEMON` | 仅 `shared` 模式下允许后续启动的 Codex.app 复用默认 daemon；`isolated` 始终取消该 GUI 环境开关，保证 Desktop 最高优先级 | `0` |
| `FLEET_REPLACE_TAILNET` | 当前已连接其他 Tailscale 控制面时，明确允许退出并切换到 Fleet mesh | `0` | 仅确认可以替换当前 tailnet 时设为 `1` |

### 执行（二选一）

**Mac 端不需要 clone 仓库**——客户端只用 `mac/` 那点文件（agent 二进制 + 附着脚本 + plist + 安装逻辑），由网关在 `/enroll/` 直接提供。

```bash
# A) 一行装（推荐，网关已起时）：自动下发入网密钥 / 控制面地址 / 自更新源
curl -fsSL https://<网关地址>/enroll/bootstrap.sh | bash

# B) 手动（已有预授权密钥时）：只下客户端包，不 clone
curl -fsSL https://<网关地址>/enroll/mac-bundle.tar.gz | tar xz
LOGIN_SERVER=https://<网关地址>:8443 AUTHKEY=<预授权密钥> bash mac/install.sh
#   网关用高位端口/封 443 时把 :8443 换成 Headscale 对外端口（如 :28443）
```
两者都会装 Tailscale、入网 Headscale（需 sudo 密码）、起 ttyd / filebrowser / fleet-agent，并额外安装 `com.macfleet.codex-app-server`：它以同一个 `~/.codex` 为会话库，但使用 Fleet 专属进程和 `~/.macfleet/codex-app-server.sock`，不与 Codex Desktop 共享 app-server 或 thread 订阅。socket 仅当前用户可访问，不新增 TCP / 公网监听。安装会取消 GUI 环境中的 `CODEX_APP_SERVER_USE_LOCAL_DAEMON`；**完全退出再重开一次已经运行的 Codex.app 后**，Desktop 恢复自己的 app-server。Fleet 浏览会话时只读 rollout / history，不调用 `thread/resume`，因此不会抢占 Desktop writer。Desktop turn 由 Fleet 增量对账显示；Desktop 持有该 thread 时，Fleet 输入先持久化到目标 Mac 的 fleet-agent 服务端队列，等 Desktop 切换或关闭该会话、writer 真正释放后再由服务端投递，不能跨 app-server steer / stop / 审批。浏览器只展示带 epoch/version 的服务端控制快照，不保存或推断 access、writer、turn 与 queue 状态。Fleet 真正发送时才尝试取得 writer，自己的 turn 可 steer、停止和处理审批，并在 turn 完成后立即 `thread/unsubscribe` 释放 writer；后台定时按真实 writer lock 与 rollout 回收孤儿占用。Desktop 始终最高优先，Fleet 检测到 writer 冲突时保守退让。
AI 执行 B 时：用 env 把 LOGIN_SERVER/AUTHKEY/MAC_INDEX 传入即可非交互；编号自行按现有数量 +1，别问用户。

### 验证

```bash
tailscale ip -4                                   # 拿到 100.x mesh IP = 入网成功
curl -s http://<本机meshIP>:7682/api/health       # 期望 ok
codex app-server daemon version                   # 期望返回本机 CLI / daemon 版本 JSON
test -S ~/.macfleet/codex-app-server.sock         # 期望 Fleet 专属 sidecar socket 存在
launchctl getenv CODEX_APP_SERVER_USE_LOCAL_DAEMON # 期望为空（Desktop 不共享 Fleet daemon）
```
回到手机/浏览器打开网关入口 → 登录 → 应能看到这台 Mac 并进入它的终端 / 文件。

---

## 给 AI 的收尾准则

- **提交/部署前必须运行项目验证入口 `bash scripts/verify.sh` 并贴出真实输出**（按序执行 mac/fleet-agent 的 `go test ./...`、server/dashboard 的 `node --test chat_model.test.mjs`、tests/tailscale-utils_test.sh 三个测试层；任一失败则先修复再继续）。
- 安装后跑上面的「验证」命令，把真实输出贴给用户。
- Mac 入网后，提醒/协助把它的 mesh IP 追加到网关 `server/.env` 的 `MAC_IPS` 并重跑 `setup-server.sh`（除非网关已实现自动回填）。
- 真实密钥 / 证书 / 密码不要打印到共享日志或写进提交。
