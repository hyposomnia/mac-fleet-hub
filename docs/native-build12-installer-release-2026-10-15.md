# Fleet Agent Settings 0.1.11+12

修复源码：`7d6ffefd9033e4461adb04696ee0ad0c64b87234`。通过正式 `release-fleet-agent.sh --native-candidate` 完整构建、签名、公证并发布至 pm。

## 修复与验证

- Applications 使用带自有系统图标的 Finder 替身。只读测试 DMG 在 Finder 中确认显示蓝色 Applications 文件夹；最终公网下载的只读 DMG 中 FinderInfo `0x8400` 同时包含替身及自定义图标标记，资源数据保留，应用严格验签通过。
- 已有应用或旧 CLI 后台统一确认一次“卸载旧版并安装新版”，确认后整体替换，不再仅打开旧版。应用包最终验证失败恢复旧包；旧后台迁移失败恢复原启动项与组件。
- 真实问题会话经旧 agent 只读 API 核验：`turnPhase=running`、`turnOwner=desktop`、队列为空。新守卫允许该状态，不再全量扫描 `task_started` 后误拦。
- 检查 Fleet 投递与 DeepSeek 活动；保留 Codex shared listener 和 Claude tmux。旧 CLI 协议没有原子维护入口，已在规格中记录检查至停止间的竞态边界，并在确认内提示替换期间旧网页短暂断开、不要发送新任务。
- 原生升级保留关联；旧 CLI 迁移保留原始记录和会话，提示需网页确认新的原生设备关联。
- 完整 Swift 回归 132 项通过；项目 `scripts/verify.sh` 通过。正式发布脚本在独立源码目录再次执行验证，捆绑 Agent/filebrowser/ttyd/tmux 运行 UAT 通过。
- 最终自动化读取桌面窗口不可用，未验证最终安装确认弹窗，也未对当前 Mac 执行实际卸载替换。已发布不等于当前 Mac 已升级。

## 公证与分发证据

三项均为 `Accepted`，并完成票据附加及校验：

| 产物 | Apple submission |
| --- | --- |
| Agent | `17848ef8-be94-4647-8071-092c47af2b84` |
| 设置应用 | `033d7e48-787f-48da-9d22-f32b5ee0d817` |
| DMG | `8c018029-99f9-4f14-9b6b-b6643a703ec5` |

- pm 当前指针：`/opt/macfleet-saas-uat/client-native-releases/12`，旧 build 11 保留。
- DMG SHA-256：`2d13a5f3f1ed718c79dac4e784f306b97de04e7e84449810e19e471314acff26`。
- 更新 ZIP SHA-256：`5f6cf5e65b43f2daf4f2e35413f08bcda8e6917daa9fde9db0faef652224520c`。
- DMG、ZIP 均从公网实际下载并与构建产物比较一致；Sparkle 更新签名有效，公网安装清单和 appcast 逐字节一致。
- 服务端与 Web 服务均 `active`；本次未重启服务。
- 构建证据：`/private/tmp/fleet-native-release.ilEGfJ`；日志：`/tmp/fleet-native-build12-release.log`。

安装包：`https://fleet.818.work/enroll/clients/12/Fleet-Hub.dmg`。
