# 原生设备名称与 Web 连接排查

## 现状与范围

用户在完成 `0.1.6+7` 关联后报告：网页显示电脑名，Fleet Hub 仍显示 m2；网页会话页显示设备暂时无法连接。

只读检查确认设备编号为 m2，关联 complete=true、locked=false，管理端 runtime=running，磁盘访问 verified。没有重新要求用户授权，没有撤销或重建关联。

后台日志持续出现 `connect shared Codex app-server: dial tcp 127.0.0.1:47682: connect: connection refused`，随后触发 `fleet-agent will restart`。独立原生安装仅设置了不修改 Desktop 环境，但仍沿用了 shared 默认值；没有提供其依赖的共享 listener。运行状态只表示已启动设备 HTTP 服务，不能证明聊天可用。

此外，中继 DERP 使用 IP 地址，日志中出现 `server cert for "" failed both system roots & Let's Encrypt root validation`。先前修复只覆盖 dnscache 的控制面 TLS，中继直接调用 tls.Client，未经过该路径。

网关独立验收的四项服务 active，Headscale 节点在线，网关可以通过 direct 路径 ping 到 m2；经现有 userspace HTTP proxy 请求设备 health、文件端口仍超时。ping、节点在线、管理 running 都不能证明 Web 数据访问已恢复。不能将以上两个源码修复宣称为实际连接已恢复；稳定后台与完整 HTTP 链路需要正式版本后实测。

当前用户给出的 AGENTS.md 将范围限定为源码、提交推送与验证，明确不授权生产部署、服务重启、网络迁移或替换当前 Mac/launchd/Desktop。此次没有发布、重启或替换现网。

## 本次源码修复

1. 入网领取响应包含服务器保存的名称，从首次关联起保持一致；`/api/device/status` 在原有设备身份及授权检查之后返回 `device_name`。Agent 只在 ID、owner、lease 全部有效时同步名称，使用现有 login/logout 私有文件锁并重新核对关联，避免覆盖并发解除关联或安装完成。名称属于展示元信息，不作为授权身份比较条件；旧服务器缺少名称时保留已同步值。
2. 本机公开管理响应只增加设备名称，不暴露 token。Swift 模型兼容旧响应；Fleet Hub 运行与关联页改为设备名称，与网页的服务器记录一致。内部 mN 编号保持不变。
3. 签名构建使用的临时 Tailscale 模块补丁增加 DERP 的 IP 身份保留，仍调用原证书验证器。版本、原文件 SHA、唯一补丁位置全部固定，模块缓存与系统信任不变。全量入口覆盖 dnscache 与真实 DERP 客户端的握手回归。
4. 原生托管模式的 Codex 初始化及恢复失败返回 `appserver_unavailable`，保留设备 mesh、文件、终端与管理进程。后续请求通过既有 ensure 路径重新连接。旧 CLI 模式保留原来的重启恢复语义。

第 4 项隔离依赖故障，但不会凭空启动缺失的 Codex 服务。已向用户提供两种启动选择：由 Fleet 管理独立 Codex 进程，或迁移 Desktop 到共享服务；启动方式尚未实施。具体边界及验收见 [启动方案](superpowers/specs/2026-10-09-native-codex-startup-design.md)。

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

## 后续验收门槛

选定并实现 Codex 启动方式后，运行全量验证、唯一签名公证发行入口，获得 Agent/Hub/DMG 三项 Accepted。取得部署及本机替换授权后，保留 m2 关联和设置，安装新版本；核对后台 PID 持续稳定、DERP 无证书错误、网关 health/info/文件及会话实际返回成功，浏览器能打开已有会话。没有通过这些实测前不报告“Web 连接已恢复”。
