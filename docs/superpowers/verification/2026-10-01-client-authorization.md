# 逐用户客户端授权验证记录

日期：2026-10-01。分支：codex/multi-user-server。服务器阶段记录是此前快照；本文件记录客户端追加开发及最新服务端产物。

## 实现范围

- 只有用户、设备、管理员。网页地址 → 浏览器登录 → 核对设备名称、短码和当前账号 → 显式确认 → 自动领取授权、入网、登记。
- CLI login/logout/capabilities；私有原子绑定文件与进程锁；中断续装、入网成功但响应丢失的恢复；重装服务不重新绑定。换账号或服务器必须先解绑。
- device_token 与 proxy_token 独立。设备令牌仅用于服务器 Bearer 接口；代理令牌仅由网关向指定 agent 注入。临时原令牌加密保存供重试，完成登记和清理在一个 SQLite 事务中提交。
- 无凭据的旧设备代理明确失败。网关及自动化重验 owner/node/IP，删除伪造 Fleet 头，不向设备转发浏览器密码、Cookie 或访问密钥。
- agent 统一业务守卫；ttyd/filebrowser 仅监听 loopback，经 agent 代理。45 秒 lease、15 秒校验；服务错误不延长 lease；本地锁定立即拒绝新请求，100ms 文件观察取消存量请求。
- 浏览器会话仍为绝对 30 天，退出网页不解绑设备。撤销只切断访问，不删除用户文件或停止 Desktop turn。
- 安装器先下载、验正式签名及协议，再操作 mesh；一次性入网 key 经私有文件传递，不放在 argv 明文。其他控制面切换需要明确授权。同控制面也用本次 key 重新认证，不用 hostname 认领旧编号。
- 自更新验证相同 Developer ID 团队、固定 identifier 和完整授权协议，拒绝授权降级。正式签名、公证仍由唯一构建机负责。

## TDD 与故障回归

本次实际观察到测试先失败、最小实现后通过：preview 404；旧设备无凭据仍可代理；自动化未注入逐设备凭据；私有绑定/lease/CLI 接口缺失；子服务监听 mesh；确认页未读取 preview；离线 lease 处理；重装未重新配置服务；入网响应丢失导致重用一次性 key；完成标志错误地取消有效 lease。

SQLite trigger 注入清理失败时，原实现提前提交 complete，回归测试失败；现改为原子事务，失败保持可重试状态，重试成功后原令牌清空。网络撤销失败仍先撤销访问并返回 access_revoked=true；禁用、错方向令牌、跨用户访问、符号链接、宽权限和并发命令均有回归。

## 最终项目验证

命令：bash scripts/verify.sh。退出 0，真实输出摘录：

```text
==> Go 测试：mac/fleet-agent (go test ./...)
ok  fleet-agent (cached)
==> Go 测试：server/enroll (go test ./...)
ok  fleet-enroll (cached)
ok  fleet-enroll/multiuser
ℹ tests 257
ℹ pass 257
ℹ fail 0
client-authorization tests passed
setup-mac shared tests passed
nginx-config tests passed
multiuser-server tests passed
==> 全部验证通过 ✓
```

两个模块 go test -race ./... 与 go vet ./... 均退出 0；最新 server race 输出：

```text
ok  fleet-enroll          6.237s
ok  fleet-enroll/multiuser 21.298s
```

git diff --check 退出 0。未提交、推送、合并 main 或覆盖其他会话改动。

## 原生进程与真实入网联调

TestClientCLIAndAgentAgainstRealHTTPServer 编译当前原生 CLI 和 agent 测试进程，使用真实 HTTP、SQLite、登录会话、浏览器确认、私有绑定文件、生产授权守卫和生产 loopback 代理组合。验证自己的 API/终端/文件代理可达，另一用户 404，直接无凭据访问 403，重启保留绑定，CLI logout 撤销并关闭 SSE。

测试进程不是完整 Desktop/launchd 守护进程：业务数据使用隔离 payload，普通模式的 mesh/setup 是明确的 OS 适配器。不会改 GUI 域、启动 Desktop、访问真实会话或重启已安装 tailscaled。

可选 HEADSCALE_UAT_* 模式进一步使用真实 Headscale 0.26.1、私有 userspace Tailscale daemon/socket/state 与实际 CLI 入网命令。命令在独立 fixture 上运行：

```text
go test -race ./multiuser -run TestClientCLIAndAgent -count=1 -v
live mode: real Headscale and private userspace Tailscale join; no system daemon or sudo
real HTTP/SQLite + native CLI + native agent: pairing, loopback proxies, ownership, restart and stream revocation passed; OS setup adapters isolated
--- PASS: TestClientCLIAndAgentAgainstRealHTTPServer (8.28s)
PASS
```

该模式真实验证一次性 key 文件传递、自动加入指定控制面、Headscale 节点归属登记及策略收敛。业务代理仍走隔离 loopback，而非冒充公网 TLS/真实 mesh 业务流量验收。初次发现 macOS t.TempDir 过长导致 Unix socket 创建失败，改用 /private/tmp 短私有目录后通过。附加 Headscale fixture 已停止，临时 daemon/helper 均已清理。

TestHeadscaleLocalIntegration、TestHeadscaleLiveKeyAttribution 也在真实隔离实例通过，包括 preAuthKey.id 归属、改名后仍可发现、独立 key、ACL PUT/readback、自定义端口和重复撤销；此次 live key 测试已改用 --auth-key=file 与 --force-reauth。

## 本地部署

当前统一服务在 http://127.0.0.1:7099 运行，复用私有临时数据库和原加密 key，仅重启该隔离进程。配置的 Headscale 是 loopback 测试实例，不是生产控制面。

```text
node scripts/local-multiuser-smoke.mjs http://127.0.0.1:7099 /private/tmp/macfleet-multiuser-local-20261001 --require-network
PASS registration, mandatory Authenticator, real PNG QR, recovery codes, 30-day cookie
PASS ordinary-user admin denial, device authorization, CSRF, user preference isolation
PASS explicit local administrator promotion and metadata-only user detail
PASS real Headscale one-use key issuance and idempotent claim
PASS private device/proxy credentials, token direction isolation and installing lease denial
PASS browser-confirmed owner, claim-token protection, cross-account device isolation
PASS isolated automation keys and immediate disabled-user session/API denial
PASS retired global enrollment path; local multi-user smoke complete
healthz=200
readyz=200
auth=200
api/auth/me=401
api/device/status=401
```

## 产物与发布边界

Linux amd64/arm64 CGO_ENABLED=0、trimpath、-s -w 构建成功。最新 SHA256：

- server/enroll/dist/fleet-enroll-linux-amd64：3d2ca74b82ad2e1b48e6857c20532ed55bf5f93523da25c0de4589d4bd3c1694
- 临时 fleet-enroll-linux-arm64：ca028fb37e2988e019ab849760173ec73a543220a47a809139161a57b964d9c1

Darwin amd64/arm64 开发构建均成功，仅放在 /private/tmp/macfleet-multiuser-local-20261001/client-builds。原生开发 CLI capabilities 输出 device_authorization/browser_pairing/agent_proxy 均为 1。

没有替换 mac/fleet-agent/dist，没有 Developer ID 新产物或公证发布，没有迁移当前 Mac、替换 launchd/Desktop 或修改生产网络。正式 Mac 初装、真实终端/filebrowser/DSH/聊天全流程、TLS/NAT 和生产迁移须在唯一构建机正式签名公证后按 runbook 验收；不把源码和隔离联调冒充正式发布。旧签名 agent 的协议检查会明确失败。
