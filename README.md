# mac-fleet-hub

> 从任意手机 / 浏览器，经一个入口，登录自己的账号，远程操作自己名下 Mac 的 AI 会话与文件。

mac-fleet-hub 是一套开源多用户系统，只有**用户、设备、管理员**三个概念。每台 Mac 归属一个用户；登录后只能查看和操作自己的设备、会话、文件、偏好、访问密钥及消息记录。一个用户与多个用户使用同一套源码和部署流程。这些 Mac **不暴露公网**，全部经网关 + Headscale 私有组网访问。手机上「添加到主屏」即得一个 PWA。

**当前交付范围：服务器及逐用户客户端源码，分支 `codex/multi-user-server`，独立本地部署与验证。** 客户端填写服务网页地址后自动打开浏览器，登录并确认设备归属，自动领取授权、入网和登记。尚未迁移现有 Mac 或发布生产；正式客户端仍必须经唯一签名构建机完成 Developer ID 签名与 Apple 公证。旧二进制不支持新协议时安装器明确停止，不回退无鉴权版本。

## 账号与权限

- 注册只填邮箱、密码、确认密码；邮箱是登录标识，不要求邮件验证。
- 每个用户必须绑定 Authenticator TOTP，验证成功后才能访问设备。首次绑定时保存一次性恢复码；遗失验证器时用密码和未使用恢复码恢复并重新绑定。
- 登录会话自签发起**绝对有效 30 天**，活跃访问不会续期；账号页可修改密码、管理验证器、恢复码和会话。
- 管理员由本地 `fleet-enroll admin <registered-email>` 显式提升，首个注册用户不会自动成为管理员。后台只能管理用户和设备元信息、禁用账号或撤销授权；管理员身份不授予他人的文件、终端或聊天正文访问权。

## 它长什么样

```
手机 / 浏览器 (PWA)
      │  HTTPS（邮箱 + 密码 + Authenticator TOTP）
      ▼
你的网关（一台常驻 Linux 服务器）
   nginx ── fleet-enroll（统一 Go 服务：账号、设备、授权、动态代理）
                 ├── SQLite + 独立 encryption.key
                 └── Headscale API（私有组网 + 内置中继）
      │
      │  仅经私有组网（mesh）可达，各 Mac 不暴露公网
      ▼
   Mac① · Mac② · … · Mac︎N
   每台跑：网页终端 (ttyd→tmux→claude/codex) · 文件管理 (filebrowser)
           会话服务 (fleet-agent + Desktop→loopback WebSocket→同一 app-server)
```

- **网关**：整套系统对外的唯一入口。VPS、云主机、家里的小主机 / NAS 都行（Linux）。
- **Mac**：被纳管的机器，**台数任意**——多一台就多一块，无需改架构，也不用关心「第几台」（网关自动编号）。
- 全链路原生 systemd / launchd，**不依赖容器**。

`server/enroll` 编译的二进制仍名为 `fleet-enroll`，现在承担统一服务器职责。新部署由 Go 服务鉴权并按设备归属动态代理，不运行 Authelia，不使用全局入网 TOTP，也不按 `MAC_IPS` 生成静态 Mac 路由。

## 两个核心能力 · 各自要开什么

### ① 多机私有局域网（Headscale）——机器之间可直接 SSH / VNC 互连

设备完成归属登记后，同一有效用户的 Mac 可经 Headscale ACL 使用 mesh 地址互连；跨用户设备默认拒绝。网关只获准访问已授权设备的 Fleet 服务端口。要用系统自带的 SSH / 屏幕共享，需在**目标 Mac**手动打开对应开关（安装脚本**不会替你动这两个**——它们等于把机器开放给远程，该由你决定）：

- **SSH**：目标 Mac「系统设置 ›  通用 ›  共享 ›  远程登录」打开 → 从另一台 `ssh <用户名>@<目标 mesh IP>`。
- **VNC**：目标 Mac「系统设置 ›  通用 ›  共享 ›  屏幕共享」打开 → 访达「前往 ›  连接服务器」`vnc://<目标 mesh IP>`。
- 各 Mac 的 mesh IP 安装时会打印，也可在该机 `tailscale ip -4` 查。

### ② 手机 / Web 经网关操作自己的 Mac —— 会话与文件

经网关入口完成两步验证后，选择自己名下已授权的 Mac，查看 AI 会话与文件。客户端运行前提与 Desktop keeper 说明：

- **已装 Claude Code 或 Codex**：网页终端续接的就是这台机器的本地会话；要用 Claude 需 `claude` 命令可用，要用 Codex 需 `codex` 命令可用。
- **Codex 真共享后台**：安装脚本默认起一份只监听 `127.0.0.1:47682` 的 app-server，并让 fleet-agent 与完全退出后重开的 Desktop 都连接它。两端共享同一底层 writer，不再由两个 app-server 互相抢锁；显式 `isolated` 仍可作为兼容回退。
  - **路径不写死**：ChatGPT.app 的内部布局会随自动更新变化（2026-09 就发生过 `Contents/Resources/codex` → `Contents/Resources/codex-cli/bin/codex`）。codex 可执行文件由 `mac/codex-bin-resolve.sh` 在每次启动时重新解析（优先读 App 自带的 `codex-cli/codex-package.json`），安装脚本、keeper 与 fleet-agent 共用同一套候选与校验，App 更新后无需重装即可自愈。
  - **起不来也不会拖垮桌面端**：app-server 由 `mac/codex-keeper-launch.sh` 监督（就绪探针 + 退避 2/10/30s + 连续失败熔断），日志在 `~/Library/Logs/macfleet/`，状态在 `~/Library/Application Support/macfleet/state/`；GUI 域的 `CODEX_APP_SERVER_WS_URL` 只在 app-server 真正 ready 之后才注入，未就绪即 fail-open（不注入并清除残留），避免出现「ChatGPT 被指向死端口、启动报 ECONNREFUSED」。
  - 一条命令体检：`~/.local/bin/fleet-agent doctor`（`--fix` 会在 shared 未就绪时摘除域变量并重启 app-server）。
- **已装 Homebrew**：安装脚本用它装 ttyd / tmux 等依赖。
- **磁盘访问**：文件管理默认根目录是整个用户主目录。macOS 会保护「桌面 / 文档 / 下载」等目录——要在网页里浏览这几个，需给文件服务（filebrowser，经 launchd 运行）授予「完全磁盘访问权限」（系统设置 ›  隐私与安全性 ›  完全磁盘访问权限，把 filebrowser 二进制加进去）；主目录其余文件无需额外授权即可读。
- 对 Fleet 提供的服务**只绑私有组网地址**、不对公网；共享 Codex WebSocket 更严格，只绑本机 loopback，任何外部访问仍经网关 + 两步验证。

### fleet-agent 发布签名

`fleet-agent` 的发布产物必须由同一 Team 的 **Developer ID Application** 证书签名，并固定使用
`com.macfleet.fleet-agent` identifier。这样自更新替换二进制后，macOS 仍能以同一代码身份识别它，
已经授予的隐私权限不会因每次重新编译而反复询问。

```bash
# 钥匙串中只有一个有效 Developer ID Application identity 时自动选择
bash mac/fleet-agent/build.sh

# 多 Team / 多证书时明确指定证书名称或 SHA-1 identity
FLEET_CODESIGN_IDENTITY='Developer ID Application: Example (TEAMID)' \
  bash mac/fleet-agent/build.sh
```

已经向 Mac 分发后不要更改证书 Team 或 `FLEET_CODESIGN_IDENTIFIER`，否则系统会将其视为另一个
程序。构建脚本会启用 hardened runtime、加入可信时间戳、立即严格验签，并用钥匙串中的
`mac-fleet-hub-notary` profile 将两个架构一并提交 Apple 公证，只有 `Accepted` 才成功；可用
`FLEET_NOTARY_PROFILE` 指定另一 profile。Apple Development 证书不会被接受，因为它生成的
独立程序会被其他 Mac 的 Gatekeeper/XProtect 拒绝。

正式发布统一在持有证书私钥的 Mac 上运行：先将
`scripts/release-fleet-agent.env.example` 复制为 `~/.config/mac-fleet-hub/release.env` 并填写私有
网关/节点清单，之后 SSH 连接该 Mac，在仓库工作树干净时执行：

```bash
bash scripts/release-fleet-agent.sh
```

脚本会先 `git pull --ff-only`，再依次完成验证、签名、公证、产物提交推送、网关备份替换、公网 SHA
校验和所有 Mac 的滚动更新/健康检查。Git、SSH、SCP 与远端验证输出均实时显示，适合 AI 非交互调用
和人工审阅；证书、私钥、公证密码和真实节点配置均不进入仓库。

---

## 本地运行服务器

需要 Go 和 Node.js；从仓库根目录运行，使用独立临时状态目录：

```bash
bash scripts/run-local-server.sh /private/tmp/macfleet-multiuser-local
```

浏览器打开 `http://127.0.0.1:7099/auth`，注册并绑定验证器。脚本监听 loopback `7099`，本地状态与现有 Fleet、Mac LaunchAgent 和 Desktop 配置分开。以下是检查命令及预期，不代表已经取得验证结果：

```bash
curl -i http://127.0.0.1:7099/healthz
curl -i http://127.0.0.1:7099/readyz
curl -i http://127.0.0.1:7099/api/devices
```

存活检查 `/healthz` 预期 200；未配置 Headscale 网络时 `/readyz` 必须为 **503**，此时不能签发入网凭据；未登录的设备 API 预期 401。本地账号页面可验证不等于真实 mesh、Mac 安装或生产部署已经验证。

## 服务器配置与迁移

网关使用 nginx、Headscale 与统一 Go 服务，私有请求由 Go 检查会话及设备归属。服务配置见 [`AGENTS.md`](AGENTS.md)：

| 环境变量 | 用途 |
|---|---|
| `FLEET_ORIGIN` | 浏览器入口的完整 origin，含实际对外端口；HTTP 仅允许 loopback 本地运行 |
| `FLEET_STATE_DIR` | 状态目录，默认 `/var/lib/fleet-enroll`；数据库为 `fleet.sqlite` |
| `FLEET_STATIC_DIR` | dashboard 静态资源目录，默认 `/var/www/fleet` |
| `FLEET_KEY_FILE` | 独立 `0600` TOTP 加密密钥文件，默认状态目录下 `encryption.key` |
| `FLEET_HEADSCALE_URL` | 服务器访问 Headscale API 的地址 |
| `FLEET_HEADSCALE_API_KEY_FILE` | Headscale API key 的私有文件路径 |
| `FLEET_GATEWAY_IP` | 本部署实际网关 mesh IP，用于生成访问策略 |
| `ENROLL_LOGIN_SERVER` | 客户端将使用的 Headscale 控制面地址 |
| `ENROLL_LISTEN` | Go 服务监听地址，默认 `127.0.0.1:7090`；本地脚本使用 `7099` |

SQLite 保存账号、会话、设备归属和偏好；TOTP 用独立密钥加密，密码、恢复码及会话令牌按其安全契约保存哈希。恢复服务器时必须同时恢复一致的数据库和原 `encryption.key`，不能用新密钥替代。访问密钥与消息队列按用户隔离保存。

管理员提升与旧数据导入是显式运维动作，使用同一实例的配置：

```bash
fleet-enroll admin <registered-email>
fleet-enroll migrate <owner-email>
```

迁移前先备份旧配置、状态、Headscale 数据与策略，以及已有的新数据库和加密密钥。`migrate` 只把显式提供的旧 `MAC_IPS`、names/settings/access-key/messages 导入指定的已注册并绑定 TOTP 的 owner，必须验证每个 IP 与 Headscale node 的关联；设备名称不作为归属凭据，启动不自动导入。旧文件来源参数见 [`AGENTS.md`](AGENTS.md)。本阶段没有迁移当前客户端。

## 浏览器确认接入

服务器的设备授权协议为 `start → 浏览器 confirm → claim → complete`：客户端保存领取凭据，用户在 `/enroll/confirm` 登录并确认短码后确定 owner；服务分配设备编号，发一次性 Headscale 密钥，再从 Headscale 验证实际节点和 IP 后激活设备。短码不是领取密钥，客户端不能自行指定 owner 或上报任意 IP 完成绑定。

发布支持新协议的正式签名客户端及安装脚本后，在 Mac 执行：

```bash
curl -fsSL https://<服务网页地址>/enroll/bootstrap.sh | bash
# 已安装客户端：
fleet-agent login https://<服务网页地址>
fleet-agent status
fleet-agent logout
```

只填写网页地址，不填写控制面、编号、密码、TOTP 或入网 key。浏览器显示设备名称、短码和当前账号，点击确认后客户端自动完成。中断可重试相同 `login`；重启无需重新绑定；换账号或服务器先 `logout`。现有其他 mesh 不会静默覆盖，需明确使用 `login --replace-tailnet`。

`~/.macfleet/binding.json` 是 `0600` 私有授权记录，所在目录 `0700`。设备访问服务器与网关访问设备使用不同随机凭据，均不在浏览器、日志或 plist 暴露。授权独立于浏览器 30 天会话；网关撤销即时切断代理，设备端 lease 最多 45 秒失效，断网不无限延续。`logout` 先本地锁定，再撤销远端；失败保留锁定凭据供重试，不删除本地文件或停止 Desktop turn。初装下载并验证正式二进制，协议检查通过后才操作 mesh；自更新禁止降级到不支持授权的 agent。

以下继续保留现有 Desktop 运行说明，供客户端阶段使用：首次启用 Codex 会话共控时，确认当前 turn 完成、完全退出 ChatGPT/Codex Desktop，然后运行 `/usr/bin/open --env CODEX_APP_SERVER_WS_URL=ws://127.0.0.1:47682/rpc -a /Applications/ChatGPT.app`；此后 App 与 Fleet 会显式连接同一个本机 app-server，可以同时查看和操作同一会话。安装脚本不会擅自中断正在运行的 App。

卸载用 `bash mac/uninstall.sh`（按其写入的安装清单精确清理，并按快照还原 GUI 域环境变量；结束后自检域环境必须无残留）。日志在 `~/Library/Logs/macfleet/`，安装清单在 `~/Library/Application Support/macfleet/manifest.json`。

### 用起来

账号入口 `/auth`，安全管理 `/account`，管理员元信息后台 `/admin`。完成客户端安装及绑定后，在控制台选择自己的 Mac，继续会话或浏览文件。

---

## 安全须知

> 网页终端 = 把一台机器的 shell 经网关暴露出来。请保持默认的安全姿态：

- 公网只开 web 与 Headscale 两个端口（默认 443 / 8443）。
- fleet-agent 是唯一 mesh 业务入口，校验逐设备凭据；ttyd/filebrowser 仅监听 `127.0.0.1`，不直接对 mesh 或公网开放。
- 统一 Go 服务强制每用户 Authenticator TOTP，并按会话、owner 与设备状态授权全部私有请求。
- Headscale ACL 只允许网关到已授权设备的服务端口、同一有效用户的设备互连，跨用户默认拒绝。
- 管理员只看元信息；所有私有页面、API 与设备流量禁止缓存，退出或切换账号清理连接和浏览器私有状态。

## 配置与参数

服务器参数与本地验证步骤集中写在 [`AGENTS.md`](AGENTS.md)。真实密钥、证书、密码、数据库与节点配置均留在仓库外；文档中的域名及邮箱只作示例。本次文档变更不代表生产部署、客户端迁移或验收已完成。

## 许可证

[MIT](LICENSE)
