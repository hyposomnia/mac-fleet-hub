# mac-fleet-hub

从任意浏览器 / 手机(H5)远程操作 3 台异地 Mac 的 **Claude Code 会话** 与 **文件**，
通过自建公网服务器做认证网关，三台 Mac 不暴露公网。

## 架构

```
手机/浏览器(任意网络)
   │  https://term1.<域名>   (先过 Authelia 登录 + 2FA)
   ▼
公网服务器  Caddy(自动HTTPS) + Authelia(认证/2FA)
   │  经 Tailscale 内网转发(仅内网可达 Mac 服务)
   ├─ term1/files1 → Mac1: ttyd(tmux→claude) + Filebrowser
   ├─ term2/files2 → Mac2: 同上
   ├─ term3/files3 → Mac3: 同上
   └─ shared       → 服务器共享目录 Filebrowser (三台 Syncthing 同步)
```

完整设计见 [docs/plan.md](docs/plan.md)。

## 目录

| 路径 | 作用 |
|------|------|
| `docs/plan.md` | 完整方案与验证步骤 |
| `server/` | 部署到公网服务器：Caddy 反代 + Authelia 2FA + 共享 Filebrowser |
| `mac/` | 每台 Mac 的常驻服务：ttyd→tmux→claude、Filebrowser（launchd + 安装脚本） |
| `scripts/` | 一键部署辅助脚本 |

## 快速上手

> ⚠️ 本项目把网页终端暴露到公网 = 把 shell 暴露到公网。**务必**配齐 Authelia 2FA、
> 仅服务器 443 对外、Mac 服务只绑 Tailscale 内网。详见 plan 的「安全要点」。

1. **网络**：服务器 + 三台 Mac 装 Tailscale，组同一 tailnet（免费版即可），`tailscale status` 确认互通。
2. **每台 Mac**：`bash mac/setup-mac.sh`（装 ttyd/tmux/filebrowser/syncthing 并起服务，绑 Tailscale IP）。
3. **服务器**：填好 `server/.env`（域名、各 Mac 的 Tailscale IP），`bash scripts/setup-server.sh`。
4. **验证**：浏览器开 `https://term1.<域名>` → 登录+2FA → 终端跑 `claude`；`shared.<域名>` 传文件。

## 配置约定

- 真实密钥/证书/用户库**不入库**（见 `.gitignore`）；仓库内提供 `*.example` 模板。
- 三台 Mac 共用 `mac/` 这套文件，区别仅在各自 Tailscale IP / 主机名（脚本参数化）。
