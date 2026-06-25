# mac-fleet-hub

> 从任意手机 / 浏览器，经一个公网入口，远程操作你**散落各处的 Mac** 上的 Claude Code 会话与文件。

你有不止一台 Mac（家里、公司、别处），想随时随地接着用上面的 Claude Code 会话——选一台机器、列出它正在跑的会话、点一条 `claude --resume` 接着聊；也能浏览/传文件、开新会话。这些 Mac **不暴露公网**，全部经一台你自己的网关 + 私有组网中转；整栈自托管、零容器、**改配置就能部署自己的一套**。

手机上「添加到主屏」即得一个 PWA，用起来像原生 App。

## 它长什么样

```
手机 / 浏览器 (PWA)
      │  HTTPS（默认 443；2FA 登录）
      ▼
你的网关（一台常驻 Linux 服务器）
   nginx ── Authelia (TOTP 两步验证) ── Headscale (自建私有组网 + 内置中继)
      │
      │  仅经私有组网（mesh）可达，各 Mac 不暴露公网
      ▼
   Mac① · Mac② · … · Mac︎N
   每台跑：网页终端(ttyd→tmux→claude) · 文件管理(filebrowser) · 会话服务(fleet-agent)
```

- **网关**：整套系统对外的唯一入口。VPS、云主机、家里的小主机 / NAS 都行（Linux）。
- **Mac**：被纳管的机器，台数任意——多一台就多一块，无需改架构。
- 全链路原生 systemd / launchd，**不依赖容器**。

---

## 🤖 用 AI 一键部署（最省事）

在目标机器上打开 Claude Code 或 Codex，把下面这句话发给它即可——它会读仓库里的 [`AGENTS.md`](AGENTS.md) 自行完成部署，只在需要你拍板的信息（域名、证书、是否封 443、入网验证码）上问你。

**部署网关**（发给跑在你那台 Linux 服务器上的 AI）：

> 把 https://github.com/hyposomnia/mac-fleet-hub 克隆到这台服务器，阅读其中的 `AGENTS.md` 并按「网关部署」流程，把 mac-fleet-hub 网关装好。需要我提供的信息逐项问我。

**接入一台 Mac**（网关装好后，发给跑在那台 Mac 上的 AI）：

> 把 https://github.com/hyposomnia/mac-fleet-hub 克隆到这台 Mac，阅读 `AGENTS.md` 并按「Mac 接入」流程接入我的 fleet。只问我网关地址和入网验证码。

---

## 手动部署

### 第一步：装网关

**前提**（本项目不替你装这两样，缺了脚本会直接报错提示）：
- 一台常驻 **Linux 服务器**，已装 **nginx**（`nginx.conf` include 了 `sites-enabled/*`）。
- 一张 **`*.<你的域名>` 通配符证书**（用 acme.sh 或 certbot 的 DNS-01 签），nginx 与 Headscale 共用。
- **DNS**：服务子域（如 `fleet.example.com`）解析到网关公网 IP。
- Headscale / Authelia 由脚本自动下载安装（脚本按 Debian/Ubuntu 的 `apt` 写；其它发行版手动装好这两个即可，系统不限定）。

```bash
git clone https://github.com/hyposomnia/mac-fleet-hub.git
cd mac-fleet-hub
cp server/.env.example server/.env     # 填域名 / 证书路径等，逐项含义见 AGENTS.md
sudo bash scripts/setup-server.sh
```
脚本会前置校验、装 Headscale + Authelia、**交互式建一个登录用户**、部署控制台、配置 nginx、起服务、把网关自身接入组网。跑完按提示扫码绑定 2FA。

> **端口与 NAT**：默认走标准端口 443 / 8443，**不需要任何 NAT**。只有当你的网络封了 80/443（部分家庭宽带）才需要高位端口 + 路由器端口映射——具体怎么设见 [`AGENTS.md` 的「要不要 NAT」](AGENTS.md)。

### 第二步：接入每台 Mac

网关跑起来后，在每台要纳管的 Mac 上（已装 Homebrew）执行。**无需 clone 仓库**——客户端只用到 `mac/` 那点文件，由网关直接提供。任选其一：

```bash
# 推荐：一行装。跟着提示输入网关地址 + 入网验证码即可，密钥/地址/自更新源自动下发。
curl -fsSL https://<你的网关地址>/enroll/bootstrap.sh | bash
```
```bash
# 或手动（已有预授权密钥时）：只下客户端包再装，同样不 clone
curl -fsSL https://<你的网关地址>/enroll/mac-bundle.tar.gz | tar xz
LOGIN_SERVER=https://<你的网关地址>:8443 AUTHKEY=<预授权密钥> bash mac/install.sh
#   （网关若用高位端口/封 443，把 :8443 换成你的 Headscale 对外端口，如 :28443）
```
装完这台 Mac 即出现在控制台里，点进去就能接它的终端 / 文件。**加更多 Mac？重复这一步就行。**

### 用起来

手机 / 浏览器打开 `https://<你的网关地址>/` → 2FA 登录 → 选一台 Mac → 列出会话 → 续接。可「添加到主屏」当 App。

---

## 安全须知

> 网页终端 = 把一台机器的 shell 经网关暴露出来。请务必保持默认的安全姿态：

- 公网只开 web 与 Headscale 两个端口（默认 443 / 8443）。
- 各 Mac 的服务只绑私有组网内网地址，**不直接对公网**。
- Authelia 两步验证（TOTP）必开。
- Headscale ACL 写严——Mac 端的访问控制只靠它这一道。

## 配置 & 参数

所有设备相关配置都外置到 `server/.env`（网关）与安装时的交互/环境变量（Mac），**每个参数的含义、默认值、何时该改，以及 NAT 怎么配，集中写在 [`AGENTS.md`](AGENTS.md)**。真实密钥 / 证书 / 用户库不入库（见 `.gitignore`），仓库只提供 `*.example` 模板。

## 许可证

[MIT](LICENSE)
