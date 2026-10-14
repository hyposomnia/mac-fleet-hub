# 旧 Fleet 后台升级迁移

状态：源码实现与隔离回归已完成；真实签名安装、launchd 切换及正式发布未执行。

## 行为契约

设置应用在正式安装位置准备后台时，识别用户旧命令行客户端的 `com.macfleet.fleet-agent`，进入现有独立后台安装事务。新后台成功前保留旧启动定义，失败恢复；旧进程若已加载，保持运行意图，已停止的后台不因查看设置而启动。日常登录启动器不承担迁移。

只处理当前用户 `~/Library/LaunchAgents/com.macfleet.fleet-agent.plist` 与其记录的同团队 Developer ID `com.macfleet.fleet-agent`。新载荷继续执行现有版本、签名、公证及 staging 验证。旧启动定义在 `~/.macfleet/desktop/migrations/<UUID>/` 私有备份，成功后移出 LaunchAgents，避免下次登录再启动旧服务。原二进制、配置、设备凭据、系统 Tailscale、ttyd/filebrowser 和 AI 会话文件保留。

已有原生客户端的关联与网络状态保持原样。旧 CLI 的系统 Tailscale 状态不复制为原生 tsnet 状态；首次使用原生客户端仍通过现有网页关联流程建立原生网络，不伪造原生联网成功。

原生登录启动器接续旧登录启动偏好，已有原生设置优先，否则采用旧 `RunAtLoad`。新 Agent 健康后应用登录启动设置；macOS 要求后台批准时保留已升级且健康的 Agent，显示系统批准错误，由既有登录项入口处理。

迁移使用现有 `check-codex-idle.sh`，活动或无法确认的 Codex turn 须完成后重试；沿用更新守卫的投递状态拒绝 `sending/steering/taking_over/recovering/takeover_check`。不改 shared transport、不抢 writer、不停止 Codex listener。普通原生后台升级继续沿用原有维护接口。

## 验收映射

测试均位于 `mac/settings-app/Tests/FleetCoreTests/NativeRuntimeTests.swift`。

| 需求 | 验收 | 测试方法 | 实现 |
| --- | --- | --- | --- |
| UPG-01 | 旧加载服务空闲后切换，启动新后台，原启动定义私有备份 | `testLegacyServiceIsRetiredWhenInstallingTheNativeBackground` | `LegacyFleetMigration.prepare` / `NativeManagement.synchronizeBackground` |
| UPG-02 | 已停止旧客户端不因准备运行时启动；旧登录项退出活动目录 | `testStoppedLegacyServiceIsRetiredWithoutStartingEitherBackground` | 同上 |
| UPG-03 | 活动 turn 或投递中的消息保留原进程与配置 | `testBusyLegacyServiceAndItsConfigurationRemainUntouched` / `testLegacyServiceWithInFlightMessagesIsNotRetired` | `LegacyFleetMigration.prepare` / `checkQueueIdle` |
| UPG-04 | 新后台健康失败恢复旧定义和服务 | `testFailedLegacyMigrationRestoresTheOldLaunchDefinitionAndService` | 既有 `BackgroundRuntimeInstaller.deploy` 回滚 / `LegacyFleetMigration.restore` |
| UPG-05 | bootout/bootstrap 已生效但命令超时时核对实际服务再恢复 | `testLegacyStopTimeoutRestoresTheOldServiceWhenItWasUnloaded` / `testBootstrapTimeoutStopsTheNewRuntimeBeforeRestoringLegacy` | `restore` / `NativeManagement` quiesce |
| UPG-06 | launchctl 动态统计变化不被误认作新进程 | `testMigrationIgnoresChangingLaunchStatisticsForTheSameLegacyProcess` | `LegacyFleetMigration.processIdentity` |
| UPG-07 | 未通过旧二进制签名验证不停止它 | `testUntrustedLegacyExecutableIsNeverStoppedOrReplaced` | `LegacyFleetMigration.inspect` / `BackgroundRuntimeVerifier` |
| UPG-08 | 旧设置程序不降级较新原生 Agent | `testOlderSettingsApplicationDoesNotDowngradeTheInstalledAgent` | `NativeManagement.synchronizeBackground` |
| UPG-09 | 迁移接续原登录启动选择；已有原生偏好不被覆盖 | `testLegacyMigrationKeepsLoginStartupEnabled` / `testLegacyMigrationRespectsExistingNativeStartupPreferences` / `testDisabledLegacyLoginStartupIsNotReenabledDuringMigration` | `LegacyFleetMigration.autoStart` / `NativeManagement.synchronizeBackground` |

## 验证与交付

首轮四项迁移回归在旧实现上失败；第二轮确认命令超时、动态统计及降级边界失败后修复；三项登录启动接续回归也先确认缺失行为再实现。26 项后台生命周期回归通过。提交前运行项目 `bash scripts/verify.sh`，正式签名、公证与实机迁移等待用户后续指令。

安装事务使用既有后台安装锁与原子 staging 交换。回滚不能安全停止新进程或恢复旧服务时保留备份并报错，不把一次命令超时当作未生效，也不强杀未知 turn。
