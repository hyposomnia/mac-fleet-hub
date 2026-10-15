# 原生升级安装操作

发现新版本不等于可以安装。安装操作由 Sparkle 提供的待恢复安装回调控制，不能仅按整个更新会话是否存在展示按钮。

| ID | 可观察行为 | 测试 / 实现 |
| --- | --- | --- |
| UP-A01 | 检查、下载、解压期间不显示重试安装或自动等待；普通安装继续由 Sparkle 窗口完成 | UpdaterDelegateTests / AppUpdater、SettingsView |
| UP-A02 | 收到延迟重开回调后先执行既有安装准备；仅准备失败且回调仍有效时展示重试与自动等待，保留实际原因 | UpdaterDelegateTests、UpdatePreparationTests / AppUpdater |
| UP-A03 | 等待立即反馈并只展示取消；等待中不能重复准备，取消后保留待安装回调供重试 | UpdaterDelegateTests、MaintenanceWaitTests / AppUpdater |
| UP-A04 | 更新取消、报错或会话结束后清理安装入口；过期任务不能覆盖最新结束原因或执行安装 | UpdaterDelegateTests / AppUpdater |

沿用 UpdatePreparation 的空闲检查、备份、停止及恢复顺序与 MaintenanceWait 的轮询，不改变签名、同源清单或健康回滚规则。本轮只提交源码与验证，正式包中的 Sparkle 下载、替换与重开留待下次签名包验收。

## 验证

- 新增委托回归先红后绿，复现准备失败后入口状态缺失、等待无即时反馈及旧等待覆盖升级取消原因。实际调用 Sparkle 委托入口与现有 NativeManagement 执行注入；不启动更新控制器，不访问真实后台或替换应用。
- 升级委托 6 项、安装准备 5 项、维护等待 2 项专项测试通过。
- `bash scripts/verify.sh` 退出 0，真实输出“全部验证通过 ✓”；原生界面 31 项、FleetCore 121 项及 Go、Dashboard、shell 回归通过。验证子进程清除继承的 `FLEET_*`，完整日志 `/private/tmp/fleet-update-actions-full-verify-20261015.log`。
- 核对 Sparkle 2.10.0 的 SPUInstallerDriver 和 SPUUpdater：延迟安装回调在恢复安装前暂停，更新驱动结束才发送完成通知；下载、解压通知按 SDK 声明注册。
