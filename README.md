# mac-fleet-hub

从任意手机 / 浏览器（PWA），经一个公网入口，远程操作**任意台数**异地 Mac 上的 **Claude Code 会话** 与文件。
这些 Mac 不暴露公网；整栈本地部署、全开源、易定制——**部署自己的 fleet 只需改配置文件**（域名、证书、Mac IP 等全部外置到 `server/.env`）。

## 架构（实测后定稿）

```
手机 PWA  →  https://fleet.example.com/             (通配符证书 *.example.com)
   │  默认 443，SNI 区分子域（ISP 封 443 时可改高位端口 + 路由 NAT，见 server/.env）
   ▼
Ubuntu 网关：nginx（与同机其它站点共存，靠 SNI 区分，互不影响）
   └─ fleet.example.com      本项目独立 server 块（你的通配符证书）
        ├─ /                  PWA 控制台壳（静态）
        ├─ /auth + auth_request   Authelia（原生 systemd, TOTP 2FA）
        ├─ /enroll/           自助入网（TOTP → 一次性 preauthkey，不过 2FA）
        ├─ /m{N}/term  → mesh → 各 Mac ttyd（→ tmux → claude --resume）
        ├─ /m{N}/files → mesh → 各 Mac filebrowser（整个 home）
        └─ /m{N}/api   → mesh → 各 Mac fleet-agent（列出/打开/新建会话）
Headscale（原生 systemd, 控制面 fleet.example.com:8443）：自托管组网 + 内置 DERP
   └─ mesh →  Mac1 … MacN（仅 mesh 可达；mac↔mac 全端口互通）
```

核心能力：**选一台 Mac → 按项目分组列出 Claude 会话（活跃/全部）→ 点一条 `claude --resume` 带上下文续接**；
也能新建会话、浏览/传文件。全链路**原生 systemd / launchd，零容器**。

## 目录

| 路径 | 作用 |
|------|------|
| `server/dashboard/` | PWA 控制台（壳 + 会话选择器；纯静态，按已入网节点名 `mac<N>` 动态枚举 Mac，无需改前端） |
| `server/nginx/fleet.conf` | 独立 nginx 站点模板（子域 server 块：反代 + auth_request + ws）；`fleet-mac.conf` 是单 Mac 反代块模板 |
| `server/authelia/` | Authelia 配置（TOTP 2FA，保护整个子域） |
| `server/headscale/` | Headscale 配置模板 + ACL 策略 |
| `server/systemd/` | authelia / headscale / 节点状态定时器 的 systemd 单元 |
| `mac/fleet-agent/` | 会话管理服务（Go 单二进制，含预编译 `dist/`）+ ttyd 附着脚本 |
| `mac/install.sh` | **Mac 端一键安装**（装 Tailscale + 入网 + 起全部服务） |
| `mac/*.plist` `mac/setup-mac.sh` | 每台 Mac 的 ttyd / filebrowser / fleet-agent 常驻服务 |
| `scripts/setup-server.sh` | 网关一键部署（装 Headscale/Authelia、部署 PWA、按 `MAC_IPS` 循环注入 nginx） |

## 装机前提（网关侧）

本项目**不替你装 nginx、也不签证书**——这两样需提前就绪（`setup-server.sh` 开头会校验，缺了直接报错）：

- **OS**：网关脚本按 **Debian/Ubuntu** 编写（`apt`/`dpkg`）；其他发行版需自行调整。
- **nginx**：已安装，且 `nginx.conf` include 了 `sites-enabled/*`（脚本只注入一个独立 server 块，不动你的其它站点）。
- **通配符证书**：用 acme.sh 或 certbot（DNS-01）签好 `*.<你的域名>`，把 `fullchain` / `key` 路径填进 `server/.env` 的 `SSL_CERT` / `SSL_KEY`（nginx 与 Headscale 共用这一对）。
- **DNS**：`FLEET_HOST`（如 `fleet.example.com`）解析到网关公网 IP。
- **端口**：默认标准 443/8443、无需 NAT；ISP 封 443 时见 `server/.env` 注释改高位端口 + 路由映射。
- Headscale / Authelia 二进制由脚本自动下载（国内慢可用 `HEADSCALE_DEB` / `AUTHELIA_TGZ` 指向预下载文件）。

## 快速上手

> ⚠️ 网页终端=把 shell 暴露给网关。务必：公网只放 web 与 Headscale 两个端口（默认 443/8443）、Mac 服务仅绑 mesh、
> Authelia 2FA 必开、Headscale ACL 写严（Mac 端访问控制只靠 ACL 这一道）。

1. **网关**（Ubuntu，需 sudo）：`cp server/.env.example server/.env` 填好域名 →
   `sudo bash scripts/setup-server.sh`（装 Headscale+Authelia、部署 PWA、注入 nginx、打印 preauthkey）。
2. **（可选）端口**：默认标准端口 443/8443，无需任何路由设置。仅当 ISP 封 443 时，在 `server/.env` 改高位端口并在路由器做 NAT 映射（如 `20443→本机443`、`28443→本机8443`）。
3. **每台 Mac**：装好 Homebrew 后——
   - **免 clone 一行装（推荐）**：`curl -fsSL https://<你的网关地址>/enroll/bootstrap.sh | bash`
     （交互问「第几台 + 入网验证码」；网关自动下发入网密钥、控制面地址与自更新源，零手填）。
   - **或本地有 clone**：`MAC_INDEX=1 AUTHKEY=<preauthkey> bash mac/install.sh`（第 2、3… 台改 `MAC_INDEX`）。
   两者都会装 Tailscale、入网 Headscale（要 sudo 密码），再装 ttyd/filebrowser/fleet-agent 并起服务。
4. **回填**：把各 Mac 的 mesh IP 按顺序写进 `server/.env` 的 `MAC_IPS`（空格分隔，第 N 个对应 mac N），
   重跑 `setup-server.sh` 刷新 nginx（按台数自动生成反代块）。网关节点的 `tag:fleet-gw` 脚本会自动打。
5. **用**：手机开 `https://<你的子域>/`（如 `https://fleet.example.com/`）→ 2FA 登录 → 选 Mac → 续接会话；可「添加到主屏」当 App。

## 配置约定

- 真实密钥/证书/用户库/真实 config **不入库**（见 `.gitignore`）；仓库提供 `*.example` 模板。设备相关值（域名、证书路径、Mac IP、台数）全部外置到 `server/.env`。
- 所有 Mac 共用 `mac/` 这套文件，区别仅 `MAC_INDEX`（终端/文件路径）与各自 mesh IP。台数任意——加一台只需多一个 `MAC_INDEX` 与 `MAC_IPS` 里多一个 IP。
- `mac/fleet-agent/dist/` 内含预编译二进制（darwin arm64/amd64）；改 Go 源码后 `go build` 重出即可。
