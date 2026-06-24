# mac-fleet-hub

从任意手机 / 浏览器（PWA），经一个公网入口，远程操作三台异地 Mac 上的 **Claude Code 会话** 与文件。
三台 Mac 不暴露公网；整栈本地部署、全开源、易定制。

## 架构（实测后定稿）

```
手机 PWA  →  https://example.com:20443/fleet/        (Let's Encrypt 证书)
   │  路由 20443→本机443
   ▼
Ubuntu 网关：现有 nginx 1.28
   ├─ /  /admin/            现有 rtm 应用（不动）
   ├─ /fleet/               PWA 控制台壳（静态）
   ├─ auth_request          Authelia（原生 systemd, TOTP 2FA）
   ├─ /fleet/m{1,2,3}/term  → mesh → 各 Mac ttyd（→ tmux → claude --resume）
   ├─ /fleet/m{1,2,3}/files → mesh → 各 Mac filebrowser（整个 home）
   └─ /fleet/m{1,2,3}/api   → mesh → 各 Mac fleet-agent（列出/打开/新建会话）
Headscale（原生 systemd, 公网 28443→本机8443）：自托管组网 + 内置 DERP
   └─ mesh →  Mac1 / Mac2 / Mac3（仅 mesh 可达；mac↔mac 全端口互通）
```

核心能力：**选一台 Mac → 按项目分组列出 Claude 会话（活跃/全部）→ 点一条 `claude --resume` 带上下文续接**；
也能新建会话、浏览/传文件。全链路**原生 systemd / launchd，零容器**。设计与取舍详见 `docs/plan.md`。

## 目录

| 路径 | 作用 |
|------|------|
| `server/dashboard/` | PWA 控制台（壳 + 会话选择器；纯静态，加一台 Mac 只改 `app.js` 的 `MACS`） |
| `server/nginx/fleet.conf` | nginx include 片段（/fleet 反代 + auth_request + ws） |
| `server/authelia/` | Authelia 配置（TOTP 2FA，路径模式 /fleet） |
| `server/headscale/` | Headscale 配置模板 + ACL 策略 |
| `server/systemd/` | authelia / headscale / 节点状态定时器 的 systemd 单元 |
| `mac/fleet-agent/` | 会话管理服务（Go 单二进制，含预编译 `dist/`）+ ttyd 附着脚本 |
| `mac/install.sh` | **Mac 端一键安装**（装 Tailscale + 入网 + 起全部服务） |
| `mac/*.plist` `mac/setup-mac.sh` | 每台 Mac 的 ttyd / filebrowser / fleet-agent 常驻服务 |
| `scripts/setup-server.sh` | 网关一键部署（装 Headscale/Authelia、部署 PWA、注入 nginx） |
| `server/archive/` | 最初 Caddy 方案，仅参考 |

## 快速上手

> ⚠️ 网页终端=把 shell 暴露给网关。务必：公网只放 443/20443/28443、Mac 服务仅绑 mesh、
> Authelia 2FA 必开、Headscale ACL 写严（Mac 端访问控制只靠 ACL 这一道）。

1. **网关**（Ubuntu，需 sudo）：`cp server/.env.example server/.env` 填好域名 →
   `sudo bash scripts/setup-server.sh`（装 Headscale+Authelia、部署 PWA、注入 nginx、打印 preauthkey）。
2. **路由**：确认端口映射 公网 `20443→本机443`、`28443→本机8443`。
3. **每台 Mac（一键）**：装好 Homebrew 后 clone 本仓库，跑
   `MAC_INDEX=1 AUTHKEY=<preauthkey> bash mac/install.sh`（2、3 改 `MAC_INDEX`）。
   脚本自动装 Tailscale、起守护进程、入网 Headscale（这几步会要 sudo 密码），再装 ttyd/filebrowser/fleet-agent 并起服务。
4. **回填**：把三台 mesh IP 写回 `server/.env` 的 `MAC{1,2,3}_IP`，重跑 `setup-server.sh` 刷新 nginx；
   给网关节点打 `tag:fleet-gw`。
5. **用**：手机开 `https://<域名>:20443/fleet/` → 2FA 登录 → 选 Mac → 续接会话；可「添加到主屏」当 App。

## 配置约定

- 真实密钥/证书/用户库/真实 config **不入库**（见 `.gitignore`）；仓库提供 `*.example` 模板。
- 三台 Mac 共用 `mac/` 这套文件，区别仅 `MAC_INDEX`（终端/文件路径）与各自 mesh IP。
- `mac/fleet-agent/dist/` 内含预编译二进制（darwin arm64/amd64）；改 Go 源码后 `go build` 重出即可。
