# 原生 Fleet 的 Codex 启动方案（待用户选择）

## 目标

完成账号关联后，网页能够读取并打开本机 Codex 会话。缺少 Codex 或连接失败时，只影响聊天；设备文件、终端、mesh 和管理进程继续运行。保留 m2 关联及凭据，不中断当前 Desktop 的活动任务。

## 推荐：Fleet 管理独立进程

原生后台采用现有 isolated 连接及 writer 守卫，使用私有 `.macfleet/desktop` 目录下的 Unix socket。由独立 Fleet Agent 管理 Codex 子进程，随后台停止而关闭，启动或恢复失败返回聊天不可用并允许后续重试；不向公网或 mesh 暴露 Codex socket。

复用现有 Codex 可执行文件解析器、RPC 连接/恢复与物理 writer 归属检查，读取当前用户的 Codex 配置及会话库。不写 Desktop 的 GUI 环境，不修改旧 Fleet launchd 服务，不启动官方 control-socket daemon。存在 Desktop writer 的同一会话继续按既有外部 writer 规则排队，不抢占它。原生侧的重启目标必须明确为自己管理的子进程，不能调用旧 launchd sidecar 的重启入口。

Codex 不存在时保持文件和终端可用并返回明确聊天错误，不下载未签名的第三方组件或伪装空会话成功。独立进程的 Desktop App Tools 能力单独验证，不声称继承 Desktop 的 MCP 连接。

验收覆盖：干净启动、进程退出后恢复、停止后的子进程/socket 清理、现有 Desktop writer 不被中断、重复连接不创建多个子进程、缺少 Codex 不重启 Agent，以及真实网页会话列表和已有会话打开。

## 备选：共享服务

按现有 shared keeper 的完整迁移及回滚流程提供 loopback WebSocket，使 Fleet 与 Desktop 复用同一 listener 和物理 writer。需要调整 Desktop 启动环境，并在当前 Desktop turn 完成后完全退出重开。

验收覆盖：唯一 listener、双方实际连接、同一会话可共同打开、App Tools 实际 connected 及工具清单、失败回滚和旧环境恢复。这一方案涉及用户当前 Desktop 和 launchd，需用户另行授权。

## 发布边界

此文档仅供方案选择，未实现任何新 Codex 启动方式。已完成的名称/TLS/聊天故障隔离修复独立提交。后续实现须测试先行，完整验证后通过唯一签名公证入口发行。当前源码授权不包含部署、服务重启或本机应用替换；正式应用与 Web 全链路实测需追加授权后执行。
