# 原生设备名称与 Web 连接排查

## 现状与范围

用户在完成 `0.1.6+7` 关联后报告：网页显示电脑名，Fleet Hub 仍显示 m2；网页会话页显示设备暂时无法连接。

只读检查确认设备编号为 m2，关联 complete=true、locked=false，管理端 runtime=running，磁盘访问 verified。没有重新要求用户授权，没有撤销或重建关联。

后台日志出现 `connect shared Codex app-server: dial tcp 127.0.0.1:47682: connect: connection refused`，随后触发 `fleet-agent will restart`。独立原生安装仅设置了不修改 Desktop 环境，但仍沿用了 shared 默认值；其配置的共享 listener 未运行。运行状态只表示已启动设备 HTTP 服务，不能证明聊天可用，也不能据此认定本机没有 Codex 服务。

此外，中继 DERP 使用 IP 地址，日志中出现 `server cert for "" failed both system roots & Let's Encrypt root validation`。先前修复只覆盖 dnscache 的控制面 TLS，中继直接调用 tls.Client，未经过该路径。

网关独立验收的四项服务 active，Headscale 节点在线，网关可以通过 direct 路径 ping 到 m2。早期经 userspace HTTP proxy 的请求曾超时，后续实际网页和网关请求已取得 HTTP 响应，详见下节。不能把一次超时、ping、节点在线或管理 running 单独用于判断完整 Web 链路；也不能将源码修复宣称为实际聊天已恢复。

当前用户给出的 AGENTS.md 将范围限定为源码、提交推送与验证，明确不授权生产部署、服务重启、网络迁移或替换当前 Mac/launchd/Desktop。此次没有执行发布、手动重启或替换现网；旧版 Agent 自身的自动重启另行记录。

## 现有 Codex 与真实网页验证（用户纠正后）

用户明确指出本机已有 Codex 服务，要求先验证。只读检查确认 Desktop app-server PID 36187 已持续运行六个多小时，父进程为 Desktop；命令没有 `--listen`，该版本 `codex app-server --help` 明确默认 `stdio://`。其 FD 0/1/2 为连接 Desktop 的未命名 Unix socket pair，没有 TCP listener。没有向这些流写入 RPC，也没有终止或重开 Desktop。

Fleet 原生 plist 仅设置 `FLEET_CODEX_DESKTOP_SHARED_DAEMON=0`，没有覆盖连接模式。现有代码仍默认 shared，连接 `127.0.0.1:47682`；该端口没有 listener，`curl --max-time 2 http://127.0.0.1:47682/readyz` 返回 connection refused。`com.macfleet.codex-app-server` 未加载，既有 Fleet Unix proxy 和官方 control socket 路径均不存在。这证明当前 Fleet 配置的传输与正在运行的 Desktop stdio 服务不匹配，不证明 Codex 未安装或未运行。

在用户已登录的独立验收网页上，DevTools Network 取得以下实际请求结果：

| 请求 | 状态 | 耗时 |
|---|---|---|
| `/api/devices` | 200 | 22 ms |
| `/m2/api/info` | 200 | 45 ms |
| `/m2/api/projects?assistant=codex` | 200 | 86–111 ms |
| `/m2/api/sessions?assistant=codex&archived=false&limit=50` | 503 | 34–72 ms |
| `/m2/api/sessions?assistant=codex&scope=active` | 503 | 32 ms |

网页因此显示“暂时无法连接”，但设备信息和项目列表已可访问；当前泛化提示不能据此解释为整台设备断网。上述 Network 证据未取得 503 的响应正文，不把具体响应 code 当作已验证事实。

网关随后经同一 userspace HTTP proxy 实测 `/api/health` 返回 `ok`、HTTP 200，耗时 0.550889 秒；无设备授权头的 `/api/info` 返回 403，耗时 0.035544 秒。此前 health 仍曾超时，连接存在间歇异常。原生管理端此时仍为 `0.1.6+7`、关联 complete=true/locked=false，但实际 PID 已从 94057 变为 4524、5542；日志在网页验证时段继续出现连接 47682 被拒与自动重启，不能用旧 PID 的一段稳定时间认定后台已稳定。

结论：本机 Codex 正在运行；Fleet 当前没有连接到它可复用的传输端点。账号关联与至少部分设备 HTTP 链路有效，聊天仍未恢复。先前“缺少 Codex 服务”和“设备 HTTP 整体不可达”的判断已撤回。此次仅验证现有部署，没有实施下文的启动草案，也没有要求用户再次选择架构或重新关联。

## 本次源码修复

1. 入网领取响应包含服务器保存的名称，从首次关联起保持一致；`/api/device/status` 在原有设备身份及授权检查之后返回 `device_name`。Agent 只在 ID、owner、lease 全部有效时同步名称，使用现有 login/logout 私有文件锁并重新核对关联，避免覆盖并发解除关联或安装完成。名称属于展示元信息，不作为授权身份比较条件；旧服务器缺少名称时保留已同步值。
2. 本机公开管理响应只增加设备名称，不暴露 token。Swift 模型兼容旧响应；Fleet Hub 运行与关联页改为设备名称，与网页的服务器记录一致。内部 mN 编号保持不变。
3. 签名构建使用的临时 Tailscale 模块补丁增加 DERP 的 IP 身份保留，仍调用原证书验证器。版本、原文件 SHA、唯一补丁位置全部固定，模块缓存与系统信任不变。全量入口覆盖 dnscache 与真实 DERP 客户端的握手回归。
4. 原生托管模式的 Codex 初始化及恢复失败返回 `appserver_unavailable`，保留设备 mesh、文件、终端与管理进程。后续请求通过既有 ensure 路径重新连接。旧 CLI 模式保留原来的重启恢复语义。

第 4 项隔离聊天连接故障，不会改变正在运行的 Desktop 服务或自动补齐 Fleet 的共享端点。此前的 [启动草案](superpowers/specs/2026-10-09-native-codex-startup-design.md) 尚未确认或实施；后续接入修复须以本节现有服务的验证为前提。

## 回归与验证

- 名称：先复现服务器状态缺少名称、Agent/Swift 字段缺失；修复后有效租约同步、网页改名同步、旧服务器兼容和错误身份拒绝均通过。改名保持现有请求 scope，凭据与设备 ID 不变。
- DERP：真实 derphttp.DialRegionTLS 握手先复现错误 IP 被接受；补丁后可信正确 IP 成功、错误 IP 与不可信证书失败。dnscache 原有三项证书验证保持通过。
- Codex：先复现原生缺少 listener 被错误标记为 agent_restarting；修复后不调用进程重启，依赖恢复时下次会话列表请求成功。旧 CLI 恢复失败仍只调度一次重启。
- 两轮完整 `bash scripts/verify.sh` 均 exit 0，最终输出保存于 `/private/tmp/fleet-device-connectivity-final-verify.log`。Go agent/enroll/multiuser、两条原生 TLS 路径及全部 Shell 层通过；JS 五组 21/342/15/22/4 项，Swift 14+54=68 项，0 fail、0 skip。

最终实际输出节选：

```text
ok  fleet-agent 4.670s
ok  fleet-agent 0.599s
ok  fleet-enroll 1.716s
ok  fleet-enroll/multiuser 7.434s
JS: tests 21 / 342 / 15 / 22 / 4; fail 0; skipped 0
Swift: Executed 14 tests, with 0 failures
Swift: Executed 54 tests, with 0 failures
==> 全部验证通过 ✓
```

回归日志位于本机 `/private/tmp/fleet-build8-*-red.log` 和对应 green 日志，仅为准备下一发行的源码测试标记，不能代表 build 8 已构建或发布。

现有服务核验后，再次完整执行 `bash scripts/verify.sh`，exit 0，输出为 `==> 全部验证通过 ✓`；日志 `/private/tmp/fleet-existing-codex-verification-20261009.log`。Go、原生 TLS、Swift 68 项及全部 Shell 层通过。JS 五组共 404 项、0 失败，其中 SDK 缓存恢复测试因未显式设置 `FLEET_SPARKLE_ARCHIVE` 跳过；指定现有已校验 Sparkle archive 后补跑 `scripts/settings-app-package.test.mjs`，17/17 通过、0 跳过，日志 `/private/tmp/fleet-existing-codex-sdk-verification-20261009.log`。这轮验证没有构建或发布新客户端。

## 后续验收门槛

修复 Fleet 对现有 Codex 的接入后，运行全量验证、唯一签名公证发行入口，获得 Agent/Hub/DMG 三项 Accepted。取得部署及本机替换授权后，保留 m2 关联和设置，安装新版本；核对后台 PID 持续稳定、DERP 无证书错误、网关 health/info/文件及会话实际返回成功，浏览器能打开已有会话。没有通过这些实测前不报告“Web 连接已恢复”。
