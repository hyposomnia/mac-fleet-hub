# 多用户服务端验收与客户端交接

验收日期：2026-10-01，开发分支：`codex/multi-user-server`。

## 本阶段交付

一套开源服务，只包含用户、设备、管理员。没有额外租户、组织、托管模式或单用户模式。

- 邮箱、密码、确认密码注册，强制绑定 Authenticator TOTP；正式登录会话绝对有效 30 天。
- 账号安全管理、一次性恢复码、密码及验证器变更、退出和会话撤销。
- 用户、设备、偏好、自动化访问密钥和消息队列隔离；管理员只能访问账号和设备元信息。
- 浏览器确认归属的 start/confirm/claim/complete 服务端协议；用 Headscale `preAuthKey.id` 关联设备，不用设备名认领。
- Headscale 按用户隔离策略、动态设备代理、SSE/WebSocket 撤销、后台对账及真实就绪检查。
- 显式管理员提升与旧数据迁移；旧消息只通过验证过的 owner/设备编号/node ID/IP 映射补齐身份。
- nginx/systemd 配置、Linux 安装流程、本地隔离运行入口及完整回归入口。

没有提交、推送、合并 main 或生产部署。没有修改本任务范围以外的现有 Mac 安装配置或签名二进制；工作区中原有 Mac、Codex 配置等并行改动予以保留。

## TDD 红绿证据

本轮收尾实际看到以下测试先失败，再修复通过：

- 安装中设备撤销不能对空 node ID 执行 expire。
- 网络就绪必须来自最近 45 秒内成功的策略同步；同步失败和过期同步返回 503。
- 包含特殊字符的合法邮箱不应破坏 TOTP URI 与二维码。
- 账号恢复立即撤回网络授权，而不是等待下一轮对账。
- 成功登录不应耗尽失败预算；认证身份限速表最多 4096 项，口令计算最多四个并发槽。
- 节点 expire 失败仍先撤回 ACL；节点消失不能保留到已回收 IP 的代理授权。
- 缺失或者不匹配的加密 key 不得替换已有数据库身份。
- 旧 queued/running 消息只有验证过的设备映射才可补齐 node ID；不覆盖已变化的目标数据。
- 原生浏览器发现账号表单自动填充问题，补失败测试后修复；收尾另修复 Unix 时间戳直接展示问题。

## 新鲜验证输出

`bash scripts/verify.sh` 最终退出 0：

```text
ok  fleet-agent (cached)
ok  fleet-enroll (cached)
ok  fleet-enroll/multiuser (cached)
tests 257
pass 257
fail 0
tailscale-utils tests passed
setup-mac shared tests passed
codex-bin-resolve tests passed
codex-keeper-launch tests passed
uninstall-restore tests passed
check-codex-idle tests passed
check-fleet-update-safe tests passed
nginx-config tests passed
multiuser-server tests passed
bash-var-brace tests passed
migrate-agent-retry tests passed
==> 全部验证通过 ✓
```

`server/enroll` 的 `go vet ./...` 退出 0；`go test -race ./... -count=1` 输出：

```text
ok  fleet-enroll           6.445s
ok  fleet-enroll/multiuser 16.375s
```

`git diff --check` 退出 0。`go mod tidy` 首次因默认代理连接超时失败，改用 `GOPROXY=https://goproxy.cn` 后成功。

## 真实本地部署

- Fleet：`http://127.0.0.1:7099`，私有状态目录 `/private/tmp/macfleet-multiuser-local-20261001`。
- Headscale 0.26.1：从官方源码构建，隔离实例 `http://127.0.0.1:56693`，配置和 API key 均在临时私有目录，未使用生产数据。
- 复用原数据库、加密 key 重启后，已有登录 Cookie、CSRF、管理员身份、偏好与 30 天绝对有效期仍有效；退出后返回 401，临时 Cookie 验证文件已删除。
- 最终 `/healthz` 200、`/readyz` 200、`/auth` 200、未登录 `/api/auth/me` 401。

真实 HTTP smoke 命令：

```bash
node scripts/local-multiuser-smoke.mjs http://127.0.0.1:7099 /private/tmp/macfleet-multiuser-local-20261001 --require-network
```

最终退出 0：

```text
PASS registration, mandatory Authenticator, real PNG QR, recovery codes, 30-day cookie
PASS ordinary-user admin denial, device authorization, CSRF, user preference isolation
PASS explicit local administrator promotion and metadata-only user detail
PASS real Headscale one-use key issuance and idempotent claim
PASS browser-confirmed owner, claim-token protection, cross-account device isolation
PASS isolated automation keys and immediate disabled-user session/API denial
PASS retired global enrollment path; local multi-user smoke complete
```

初始 Headscale fixture 的一小时 API key 在较长验收过程中到期，服务正确转为 readyz 503，后台写操作明确返回网络待收敛状态。只更新隔离 fixture 的 API key 为 24 小时并重启本地 Fleet，严格要求真实网络就绪的 smoke 随后通过。未绕过错误、未修改生产 key。临时 key 到期后需要重新创建并重启本地服务；到期不影响基本注册、登录和账号页面。

真实 Headscale API race 联调再次通过 `TestHeadscaleLocalIntegration`：用户复用、独立一次性 key、拒绝未归属节点、deny-all/隔离/自定义服务端口策略 PUT 和 readback、缺失节点撤销幂等。另一个独立 fixture 完成 `TestHeadscaleLiveKeyAttribution`，实际 userspace Tailscale 节点入网，改名后仍以 `preAuthKey.id` 关联、策略接受、重复 expire 成功。该测试只使用独立 socket/state，没有触碰已安装 tailscaled；测试 daemon 已清理，多余 Headscale fixture 已停止。

实际 Chrome 浏览器（不是 mock）验收了注册、二维码、TOTP、恢复码确认、账号页、退出其他会话、退出登录、next 返回、管理员搜索/详情/设备元信息与失效跳转。正常页面未观察到 JavaScript Console 错误；Dashboard 有四条非阻塞 label 关联建议，裸 403 页面有 favicon 404。测试恢复码曾在一次工具观察中遮蔽失败，已通过本地维护重置整个临时账号，确认原密码、TOTP、恢复码和会话失效；仓库无可用凭据。

浏览器细节与截图索引见 `server/dashboard/multi-user-frontend.md`。最后时间格式修复由回归测试验证，浏览器截图是修复前的版本。

## 构建产物

`CGO_ENABLED=0 GOOS=linux GOARCH=amd64/arm64 go build -trimpath -ldflags='-s -w'` 均成功。

| 架构 | 位置 | SHA256 |
|---|---|---|
| amd64 | `server/enroll/dist/fleet-enroll-linux-amd64` | `b989e70979f7e07f3bad93b1f4b944800e14c869f9cba8544c12cf37ebdac232` |
| arm64 | `/private/tmp/macfleet-multiuser-local-20261001/fleet-enroll-linux-arm64` | `4c8c46937a4d3e225fef6d508a2beefc03b0823282fb27452dc8d55e04364483` |

amd64 更新既有跟踪产物；arm64 用于构建验证。未发布任何 Mac fleet-agent 新二进制。

## 客户端下一阶段

服务器阶段完成，不代表现有 Mac 已支持逐用户授权。当前 `bootstrap.sh` 仍是旧全局 TOTP 客户端，旧 join 已明确返回 410。

后续必须实现并验收：

1. Mac 安装器发起 start，私下保存 claim token，只展示确认短码/浏览器链接；用户登录并明确确认归属。
2. 安装器 claim 获取一次性 Headscale key，完成实际入网后 complete，由服务器关联 node ID；不让客户端指定 owner 或自行登记 IP。
3. 正常客户端使用自己的授权与状态；首次安装、重试、升级和旧 Mac 显式迁移均保持正确 owner，撤销和禁用不能继续访问。
4. 真实 Mac 终端、文件、聊天、上传、SSE/WebSocket，以及 filebrowser/DSH 嵌入应用写请求的 CSRF 联调。服务端未放宽鉴权来迁就旧客户端。
5. 两用户真实网络访问的跨账号拒绝、同账号访问、设备移除、断线重连和长连接撤销验收。
6. Mac 新二进制若需要变更，仍由唯一签名构建机走 Developer ID、公证和正式发布流程；不覆盖现有已签名 agent。

Linux nginx/systemd 的真实生产主机安装、TLS/NAT 和客户端完整端到端验收不在本地 Mac 服务端验证中冒充完成。
