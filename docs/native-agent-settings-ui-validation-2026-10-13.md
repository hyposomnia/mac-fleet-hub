# Fleet Agent Settings 界面核对

2026-10-13，分支 `codex/multi-user-server`。本轮仅修改源码并运行独立 Swift 调试预览，未执行正式签名、公证、产物发布、本机替换或服务器部署。

## 需求与实测

| 用户要求 | 实现与核对结果 |
| --- | --- |
| 版本和进程移到详情列表，运行页去掉设备名 | 主状态只显示运行状态；版本、PID 分别显示在原生详情行。账号与设备名称在关联页保留。实际 UI 为 `0.1.9+10` / `66108`。 |
| 打开网页端移到下方并放大 | 下方居中的 240×56 按钮，品牌蓝底和白色图标/文字；保留原有网页地址与打开逻辑。已检查实际布局。 |
| 解除关联移到右上角 | 页头红色文字链接；实际点击成功打开确认框，取消后关联保留。 |
| 磁盘权限颜色统一 | 权限标题和功能图标使用品牌蓝；访问正常/受限继续使用语义状态颜色。 |
| 大图标恢复拖拽 | 保留居中的 144×144 图标；AppKit 拖拽源放在白底 ScrollView 中，接受窗口未激活时的首次鼠标操作。实际鼠标拖拽后，drag pasteboard 的唯一 file URL 对应 `~/.macfleet/desktop/runtime/Fleet Agent.app` 原文件。最终预览验证 `originalStandaloneAgent true`。 |
| 说明磁盘权限用途 | 居中说明授权后可从网页访问本机文件，并查看 AI Coding 会话关联的文件。实际 UI 已核对。 |
| 明确自动启动的是 Agent，必要时提供系统设置入口 | 开关改为“登录后启动 Fleet Agent”；沿用原有保存与系统登录项逻辑。macOS 需要批准时显示“前往系统登录项设置”。本机只读检查为 `auto_start: true` 且 `com.macfleet.desktop-login => enabled`。本轮未切换真实偏好，也未注销/登录。 |
| 卸载范围、配置删除与会话归属明确 | 确认框说明同时卸载设置程序和 Agent；根据是否勾选删除 Fleet 配置分别显示“保留”或“也会删除”。明确聊天会话由 Codex / Claude 保存，独立于 Fleet。两种配置选项的确认文字均已观察。真实卸载未执行。 |
| 预览点击卸载不能无反馈 | 预览保留安装/解除关联/卸载保护；确认操作后显示“界面预览”弹窗和到已安装设置程序操作的提示。实际点击确认后已观察该弹窗，关闭后后台状态保持正常。 |
| 设置程序统一命名 | 窗口、侧栏、关于、菜单、安装/卸载、后续构建 App、DMG 卷名、appcast 与 Web 安装说明使用 Fleet Agent Settings。当前预览已核对可见名称。源码兼容旧安装位置，并保持现有签名/OAuth/升级 URL 契约。 |

## 验证

新命名的安装位置先取得失败回归：`RuntimeTests.testInstallationUsesNormalizedPathRatherThanLaunchSpelling` 在原实现中拒绝 `/Applications/Fleet Agent Settings.app`。修复后，新旧安装名的规范化路径、临时/DMG/下载目录拒绝，以及真实临时目录下的安装选择测试通过。

```bash
FLEET_SPARKLE_ARCHIVE="$HOME/Library/Caches/fleet-hub/Sparkle-2.10.0.zip" bash scripts/verify.sh
```

真实输出摘要（退出码 0）：

```text
Go: fleet-agent / 原生路由与 TLS / fleet-enroll / fleet-enroll/multiuser = ok
JS: tests 447, pass 447, fail 0, skipped 0
FleetHub: Executed 17 tests, with 0 failures
FleetCore: Executed 81 tests, with 0 failures
tailscale-utils tests passed
setup-mac shared tests passed
client-authorization tests passed
uninstall-restore tests passed
check-codex-idle tests passed
check-fleet-update-safe tests passed
==> 全部验证通过 ✓
```

验证日志：`/private/tmp/fleet-agent-settings-ui-final-verify-2026-10-13.log`。调试预览实际运行在独立临时目录中，使用已安装后台读取状态；构建成功并通过 CUA 逐页核对。正式构建和签名后的安装、改名升级及系统登录验收留待后续发行。

## 当前安装保留

前后两次检查，正式设置程序、Agent、binding.json 和 settings.json 的 SHA-256 完全一致。后台始终为 PID `66108` / `0.1.9+10`，runtime `running`，FDA `verified`，已关联。原 shared listener PID `6474` 继续监听 `127.0.0.1:47682`。

```text
Hub executable: 1ff1adf7325b72cd876d46ffc89d5f196e9189b33c81a5d5929cd7314cc9ad1d
Agent executable: 1a92172a1c54d9ac09f992c8bb09c8084d5610b9a59773be702248aa8cee794f
binding.json: c9cdfa97ee506d410d50a3c45676ef5717adc62982febff3ca99446a3584b28f
settings.json: a4e920684ed0a45bf88a270edeb353c947e0f4b579a3529654be15307b29fdd0
```
