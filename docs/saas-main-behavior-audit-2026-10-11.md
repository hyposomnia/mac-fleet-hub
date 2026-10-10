# SaaS 与 main 的业务实现对照

对照 main `a76b924872a9c951b6966d6f61b69468cef09e37`，SaaS 基线为 `894d2acdc44cb8d0c8dddf959b91801f4432b344`。维护原则是复用现有业务实现，租户适配限于账号、数据归属及访问隔离；原生客户端、iOS 和设置界面按各自已确认需求维护。

## 复用与适配边界

| 链路 | 对照结果 |
| --- | --- |
| 文件、预览、会话读取 | `file_browser.go`、`file_preview.go`、`session_read.go` 与 main 逐字节相同；终端及会话操作 handler 位于 `main.go`，差异集中在启动和路由包装。 |
| 聊天及 Codex / DSH | main 原有 25 个生产 Go 文件中 20 个逐字节相同，包括 app-server 连接、共享 runtime、会话归属、takeover、App Tools、DSH、聊天路由及能力发现。其余 5 个文件增加设备访问包装、原生生命周期或聊天依赖故障隔离。 |
| Codex shared | keeper、shared plist 和 Desktop 环境脚本与 main 逐字节相同。共同 listener 持有唯一物理 writer；turn 操作归属沿用 main。 |
| 自动化 | 继续使用原 `messageAPI`、队列状态机、超时、重试和回调；每账号有独立实例与存储目录。设备请求增加 owner / node / IP 校验与设备凭据。队列取消随账号、设备撤销生效。 |
| 配置及外观 | 账号独立持久化；设备操作按当前归属检查。默认值、可配置范围、图标与色值沿用已有实现。空闲回收通过已有设备状态协议同步。 |
| 网页 | 22 个原有 JS 文件逐字节相同。`app.js`、设备外观、workspace reset 和 service worker 的差异包含账号缓存隔离、已确认设置菜单、原生桥接及跨助手请求修复。 |
| 账号及网络 | 注册、登录、TOTP、会话、管理员、设备配对、逐设备凭据与按 owner 的 mesh ACL 是新增账号及隔离实现。代理保留 Range、上传、SSE、WebSocket；凭据只注入目标设备。 |

账号认证的 Origin / CSRF、凭据存储、会话撤销和设备授权续期用于已有登录及隔离契约。设备授权 lease 只限制 Fleet API 访问，不管理 Codex writer，不结束 Desktop turn。原生升级的空闲守卫、签名与公证校验属于已确认的客户端发布流程。

## 本次对齐及验收

| 需求 | 可观察行为 | 测试 | 实现 |
| --- | --- | --- | --- |
| REQ-HISTORY-1 | 设备撤销、移除、owner、node 或 IP 变化后，原账号仍能查询其已保存消息；其他账号读不到。 | `TestUserApplicationsHistorySurvivesDeviceChanges` | `message_api.go` 的消息查询及记录列表，`application.go` 的 owner 实例 |
| REQ-HISTORY-2 | 相同幂等请求返回原 message_id，不创建任务；无授权设备上的新请求仍被拒绝，禁用账号仍不能读取。 | 上述测试，`TestUserApplicationsGuardEveryAgentRequest` | `submitMessage` 及实际请求的 owner / device guard |
| REQ-PREF-1 | 撤销设备后保留 owner 已存外观；保存其他设置不清除它，其他账号不可见。 | `TestDeviceAppearanceIsOwnerScopedAndRequiresAnOwnedDevice` | `multiuser/device_appearance.go` |
| REQ-IDLE-1 | 设备状态下发本账号 autoCloseMinutes × 60；默认 1800 秒，7 / 41 分钟分别为 420 / 2460 秒。偏好错误不撤销设备授权。 | `TestDeviceStatusUsesOwnerIdleSettings` | `multiuser/device_auth.go` |
| REQ-IDLE-2 | agent 只在有效授权响应时更新原回收时长；503、owner 不符、缺失或非正值保留当前值。 | `TestDeviceStatusSynchronizesIdleSettingsOnlyWithValidAuthorization` | `device_access.go`；`main.go` 初始化及原 reaper |
| REQ-DOCTOR-1 | 诊断按当前 readyz 判定；旧状态文件不触发降级或操作，路径解析只读报告。 | `TestDoctorUsesLiveReadinessDespiteOldStateFile`、`TestDoctorHealthyReport` | `doctor.go` |

各行为先以失败测试确认，再恢复现有契约。消息查询、幂等响应、记录列表直接恢复 main 控制流。诊断不再依赖独立监督器的状态文件。

网页同时移入 main `a76b924` 的浮层与标题更新及原回归，外壳缓存 v217；设置菜单、图标外观选择和会话归档入口保持已确认交互。

验证入口为 `bash scripts/verify.sh`；测试子进程隔离继承的 `FLEET_*`。Linux 服务使用 `go build -trimpath -ldflags="-s -w"` 重建。Mac 源码修改不等于正式签名客户端升级，需由唯一签名机按既有入口发布。

2026-10-11 验证输出：

```text
VERIFY_EXIT=0
==> 全部验证通过 ✓
BUILD_OK darwin/arm64
BUILD_OK darwin/amd64
LINUX_BUILD_OK aee5b4265ab4c9e84bb35bfabcb93d6c981d500c98f5cd43040bf6bb6c3e74a2
```

Go、446 项 JS、Swift 和 Shell 检查通过；1 项已有 SDK 缓存篡改测试按环境条件跳过。Mac 双架构仅编译到临时目录，正式签名产物未替换。
