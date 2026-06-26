# mac-fleet-hub

> 从任意手机 / 浏览器，经一个公网入口，远程操作你**散落各处的 Mac** 上的 Claude Code 会话与文件。

你有不止一台 Mac（家里、公司、别处），想随时随地接着用上面的 Claude Code 会话——选一台机器、列出它正在跑的会话、点一条 `claude --resume` 接着聊；也能浏览 / 传文件、开新会话。这些 Mac **不暴露公网**，全部经一台你自己的网关 + 私有组网中转；整栈自托管、零容器、**改配置就能跑起自己的一套**。手机上「添加到主屏」即得一个 PWA，用起来像原生 App。

## 它长什么样

```
手机 / 浏览器 (PWA)
      │  HTTPS（默认 443；TOTP 两步登录）
      ▼
你的网关（一台常驻 Linux 服务器）
   nginx ── Authelia (2FA) ── Headscale (自建私有组网 + 内置中继)
      │
      │  仅经私有组网（mesh）可达，各 Mac 不暴露公网
      ▼
   Mac① · Mac② · … · Mac︎N
   每台跑：网页终端 (ttyd→tmux→claude) · 文件管理 (filebrowser) · 会话服务 (fleet-agent)
```

- **网关**：整套系统对外的唯一入口。VPS、云主机、家里的小主机 / NAS 都行（Linux）。
- **Mac**：被纳管的机器，**台数任意**——多一台就多一块，无需改架构，也不用关心「第几台」（网关自动编号）。
- 全链路原生 systemd / launchd，**不依赖容器**。

部署就两步：先在 Linux 服务器上**装网关**，再在每台 Mac 上**接入**。每步都有「AI 一键」和「手动」两条路，任选。

## 两个核心能力 · 各自要开什么

### ① 多机私有局域网（Headscale）——机器之间可直接 SSH / VNC 互连

各 Mac 接入后就同在一张你自建的私有组网里，彼此用 mesh 内网地址直连、不经公网（mac↔mac 全端口互通由 Headscale ACL 放行）。要用系统自带的 SSH / 屏幕共享在 Mac 之间互连，需在**目标 Mac**手动打开对应开关（安装脚本**不会替你动这两个**——它们等于把机器开放给远程，该由你决定）：

- **SSH**：目标 Mac「系统设置 ›  通用 ›  共享 ›  远程登录」打开 → 从另一台 `ssh <用户名>@<目标 mesh IP>`。
- **VNC**：目标 Mac「系统设置 ›  通用 ›  共享 ›  屏幕共享」打开 → 访达「前往 ›  连接服务器」`vnc://<目标 mesh IP>`。
- 各 Mac 的 mesh IP 安装时会打印，也可在该机 `tailscale ip -4` 查。

### ② 手机 / Web 经网关操作任意一台 Mac —— 用它的 Claude Code、看它的文件

经网关入口（两步验证登录后）选一台 Mac，即可在网页里续接它的 Claude Code 会话、浏览 / 传它的文件。每台 Mac 的前置：

- **已装 Claude Code**：网页终端续接的就是这台机器的 `claude` 会话，故每台 Mac 需 `claude` 命令可用。
- **已装 Homebrew**：安装脚本用它装 ttyd / tmux 等依赖。
- **磁盘访问**：文件管理默认根目录是整个用户主目录。macOS 会保护「桌面 / 文档 / 下载」等目录——要在网页里浏览这几个，需给文件服务（filebrowser，经 launchd 运行）授予「完全磁盘访问权限」（系统设置 ›  隐私与安全性 ›  完全磁盘访问权限，把 filebrowser 二进制加进去）；主目录其余文件无需额外授权即可读。
- 这些服务**只绑私有组网地址**、不对公网；任何外部访问都经网关 + 两步验证。

---

## 🤖 用 AI 部署（最省事）

在目标机器上打开 Claude Code 或 Codex，把对应那句话发给它——它会读部署手册 [`AGENTS.md`](AGENTS.md) 自行完成，只在需要你拍板的信息（域名、证书、是否封 443、入网验证码）上问你。

**装网关**（发给跑在你那台 Linux 服务器上的 AI）：

> 在这台 Linux 服务器上部署 mac-fleet-hub 网关：读 https://github.com/hyposomnia/mac-fleet-hub/blob/master/AGENTS.md 并按其中「网关部署」流程执行；需要我提供的信息（域名、证书位置、是否封 443）逐项问我，涉及 sudo / 覆盖配置前先说明。

**接入一台 Mac**（网关装好后，发给跑在那台 Mac 上的 AI）：

> 在这台 Mac 上接入我的 mac-fleet-hub fleet：读 https://github.com/hyposomnia/mac-fleet-hub/blob/master/AGENTS.md 并按其中「Mac 客户端接入」流程执行（**不要克隆仓库**，从网关下载客户端包即可）；只问我网关地址和入网验证码。

---

## 手动部署

### 第一步 · 装网关

**前提**（本项目不替你装这两样，缺了脚本会直接报错）：

- 一台常驻 **Linux 服务器**，已装 **nginx**（`nginx.conf` 里 include 了 `sites-enabled/*`）。
- 一张 **`*.<你的域名>` 通配符证书**（用 acme.sh 或 certbot 的 DNS-01 签），nginx 与 Headscale 共用。
- **DNS**：服务子域（如 `fleet.example.com`）解析到网关公网 IP。

> Headscale / Authelia 由脚本自动下载安装（按 Debian/Ubuntu 的 `apt` 写；其它发行版先手动装好这两个即可，系统不限定）。

```bash
git clone https://github.com/hyposomnia/mac-fleet-hub.git
cd mac-fleet-hub
sudo bash scripts/setup-server.sh
```

首次运行若还没有 `server/.env`，脚本会**交互问你域名 / 证书 / 是否封 443** 等几项并自动写好（也可先 `cp server/.env.example server/.env` 手填，每个参数含义见 [`AGENTS.md`](AGENTS.md)）。随后它会前置校验、装 Headscale + Authelia、**交互建一个登录用户**、部署控制台、配置 nginx、起服务、把网关自身接入组网。跑完按提示扫码绑定两步验证。

> **端口与 NAT**：默认走标准端口 443 / 8443，**不需要任何 NAT**。只有当你的网络封了 80/443（部分家庭宽带）才需要高位端口 + 路由器端口映射——向导会问你，细节见 [`AGENTS.md`](AGENTS.md)。

### 第二步 · 接入每台 Mac

网关跑起来后，在每台要纳管的 Mac 上执行（先满足上节「② 的 Mac 前置」：Homebrew、Claude Code，按需开屏幕共享 / 磁盘访问）。**Mac 端无需克隆仓库**——客户端只用 `mac/` 那点文件，由网关直接提供。任选其一：

```bash
# 推荐：一行装。按提示输入网关地址 + 入网验证码即可，密钥 / 地址 / 自更新源自动下发。
curl -fsSL https://<你的网关地址>/enroll/bootstrap.sh | bash
```
```bash
# 或手动（已有预授权密钥时）：只下客户端包再装，同样不 clone。
curl -fsSL https://<你的网关地址>/enroll/mac-bundle.tar.gz | tar xz
LOGIN_SERVER=https://<你的网关地址>:8443 AUTHKEY=<预授权密钥> bash mac/install.sh
#   网关若用高位端口（封 443），把 :8443 换成你的 Headscale 对外端口（如 :28443）。
```

装完即出现在控制台里，点进去就能接它的终端 / 文件。**加更多 Mac 重复这一步就行**，编号由网关自动分配。

### 用起来

手机 / 浏览器打开 `https://<你的网关地址>/` → 两步验证登录 → 选一台 Mac → 列出会话 → 点一条续接。可「添加到主屏」当 App。

---

## 安全须知

> 网页终端 = 把一台机器的 shell 经网关暴露出来。请保持默认的安全姿态：

- 公网只开 web 与 Headscale 两个端口（默认 443 / 8443）。
- 各 Mac 的服务只绑私有组网地址，**不直接对公网**。
- Authelia 两步验证（TOTP）必开。
- Headscale ACL 写严——Mac 端的访问控制只靠它这一道。

## 配置与参数

设备相关配置都外置：网关在 `server/.env`，Mac 在安装时的交互 / 环境变量。**每个参数的含义、默认值、何时该改、NAT 怎么配，集中写在 [`AGENTS.md`](AGENTS.md)。** 真实密钥 / 证书 / 用户库不入库（见 `.gitignore`），仓库只提供 `*.example` 模板。

## 许可证

[MIT](LICENSE)
