# SaaS Codex 共享直接复用 main

Desktop 与 Fleet 必须同时接入同一个 loopback app-server，只有一个物理 writer。writer 由共同 listener 持有；每个 turn 的网页操作授权继续使用既有归属判断。空闲释放、TTL 或抢锁不是共享接入方案。

复用基线为 main `64ec07690ac5e8823b292b6b6f8993f1d66fa2a3`。以下文件逐字节相同：

- `mac/codex-shared-app-server.mjs`
- `mac/com.macfleet.codex-shared-app-server.plist`
- `mac/codex-desktop-env.sh`

共享链路为 launchd → ChatGPT 签名 Node → main keeper → 唯一 Codex listener。Desktop 直连 loopback WebSocket；Fleet 经过原有 0600 Unix 透明桥连接同一 listener。SaaS 不再加入另一个监督器或以探活失败为由取消 Desktop 的共享地址。旧 plist 用到的 `codex-keeper-launch.sh` 只保留路径解析与 exec 转交，既有解析器仍供安装器使用。

原生安装器继续处理应用包资源、可执行路径、user/Aqua 环境快照及所有权。已安装的旧启动定义在原有 idle 守卫通过后迁移；有活动或未知 turn 时保留原服务，迁移失败恢复原定义和环境。诊断 `doctor --fix` 仅尝试启动停止的 keeper，保留共享地址，不带 `kickstart -k` 强杀现有 listener。账号、设备授权和原生后台失败隔离保持原协议。

同时直接移入 main `dd0425d` 的跨轮持续同步修复及原回归测试：上一轮完成后保持连接对账，直到网页订阅取消，后续 Desktop turn 不需刷新页面。

验证入口为 `bash scripts/verify.sh`；在验证子进程中移除继承的 `FLEET_*`，由夹具注入测试配置，不改真实服务环境。已完成原生启动/旧定义迁移/故障回滚与跨轮同步测试的红绿验证；共享核心文件对 main 做字节比较。Mac 双架构编译只写临时目录，仓库正式签名产物未替换。

当前交付为源码分支修复。真实 Mac 安装必须走唯一签名机的正式签名、公证与升级流程；当前 Desktop turn 未被中断。完整重开后，还需验证 Desktop 与 Fleet 连接同一 listener、打开同一 thread 无 writer 冲突，并验证 App Tools 实际连接与工具清单。源码通过不代表此项 UAT 已完成。

最终验证输出（2026-10-11）：

```text
VERIFY_EXIT=0
==> 全部验证通过 ✓
BUILD_OK darwin/arm64
BUILD_OK darwin/amd64
```

完整入口包含 Go agent、原生 TLS、Go server、446 项 JS 通过（1 项 SDK 篡改缓存测试按既有条件跳过）、92 项 Swift 通过及所有 Shell 检查。正式签名产物保持原样。
