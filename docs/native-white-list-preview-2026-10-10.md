# Fleet Hub 白底列表界面预览

本轮在 `codex/multi-user-server` 修改原生界面。按用户要求，仅编译 Swift 调试界面并更新临时预览程序；没有运行正式发行入口、重新签名公证、制作安装包、部署网关或替换已安装应用。用户后续明确要求登录启动 switch 真正生效，因此本地预览的该开关已获准保存真实启动偏好并控制现有登录项。

## 界面调整

- 内容区、侧栏统一白底，删除所有软底卡片及旧自绘按钮样式，改用平整原生列表、分隔线与原生控件。
- 统一文字层级、功能图标、柔和蓝/绿/金/紫色和间距；侧栏保留完整点击热区与选中态。
- 主状态直接显示连接失败、连接中、启动中、授权锁定及操作异常；健康态仅保留“后台运行中”、版本和 PID。
- 运行页新增“打开网页端”，使用现有 HTTPS origin 校验器，优先打开当前关联服务器。
- 登录启动移至单独的“设置”页；磁盘页保留 144px 居中可拖拽的独立 Agent 图标。
- 未关联时授权按钮放在服务器地址输入框右侧；已关联账号显示只读服务器地址，移除不再适用的置灰授权按钮。解除关联保留可点击文字链和原确认弹窗。
- 关于页居中展示实际应用图标、版本和检查更新入口；卸载文字链仍明确包含 Hub 与 Agent。

## 验证

执行 `bash scripts/verify.sh`，退出码 0。真实输出摘要：

```text
ok  fleet-agent (cached)
ok  fleet-agent 0.834s [native IP/DERP TLS regression]
ok  fleet-enroll (cached)
ok  fleet-enroll/multiuser (cached)
JavaScript: 405 tests, 404 passed, 0 failed, 1 skipped
Swift: 85 tests, 0 failures
tailscale-utils tests passed
shared/install/authorization/idle/uninstall/deployment regressions: passed
==> 全部验证通过 ✓
```

跳过的是需要显式 `FLEET_SPARKLE_ARCHIVE` 的 SDK 缓存破坏测试；实际 SDK 校验及 Swift 编译、测试已执行。主状态的三项行为回归覆盖错误、运行/连接/锁定与操作失败。

当前 Desktop 会话继承了真实 keeper 的显式 Codex 路径和日志地址，导致旧有路径/监督测试误用这些值。只调整测试环境：路径测试清除继承的显式路径，监督测试指定临时 Codex 和日志位置。生产解析器、监督包装和运行配置未修改；两个测试及完整验证均重新执行通过。

此前界面验证证据：`/private/tmp/fleet-native-list-preview-verify-20261010.log`。调试预览位于 `/private/tmp/fleet-native-list-preview-20261010/Fleet Hub Preview.app`，跳过后台准备和生命周期操作；后续授权的登录启动开关可保存真实设置，服务器地址不单独自动保存。Release 构建不启用预览模式。

先前预览已核对全白背景、运行页、磁盘页及关于页布局，并实际打开、取消解除关联确认弹窗。用户后续截图仍是旧预览进程，显示了“未安装”和置灰授权按钮。前者来自临时预览 bundle 的 `SMAppService.notFound`，并不是已安装 Hub/Agent 的状态；预览现在跳过该临时 bundle 的登录项异常提示，正式应用仍保留真实异常。

更新后的调试程序已重开：仅退出旧预览 PID `79330`，新的预览 PID 为 `96775`；其可执行文件与当前 Swift 编译结果 SHA 相同。已确认新进程启动，本轮工具未提供自动界面操作能力，尚未对最后一轮配色和输入框右侧按钮逐页目视复检；运行中的最新版供用户验收。

## 已安装运行状态保留

通过本地 control socket 读取状态，并与预览前基线比较：

```text
version          0.1.8+9 (unchanged)
pid              57378 (unchanged)
runtime          running
disk_access      verified
binding          complete, unlocked
settings.json / binding.json / agent.plist /
codex-app-server.plist / login-helper-version SHA: unchanged
installed Hub / Agent executable SHA: unchanged
```

设备关联保持原样；下述开关实测结束后也恢复了原启动偏好。正式发行等待用户后续消息。

## 登录启动开关功能验收

用户指出仅隐藏“未安装”提示不够，switch 必须真实生效。删除开关下所有状态提示及无用的状态映射，改为用户操作开关时保存；初始化、刷新、失败回滚不会触发重复保存。保存失败恢复已生效的开关值。

预览无法使用自身 bundle 的 `SMAppService` 管理正式 Hub，因此只通过 launchd 启用／禁用现有的 `com.macfleet.desktop-login`，先确认其所属 bundle 是已安装 Hub，并回读系统结果。没有注册新服务、改写 plist 或替换正式辅助程序。正常安装的 Hub 继续使用原有 `SMAppService`，按保存偏好恢复注册，并清除该登录项的预览禁用覆盖。

通过当前编译的 `FleetCore`，使用与预览相同的 `SettingsModel.save` 和 `NativeManagement` 执行真实关闭、开启及恢复：

```text
switch=off saved=false system=disabled agent_pid=57378
switch=on saved=true system=enabled agent_pid=57378
original startup setting restored: true
```

恢复后的 settings、binding、Agent/shared plist、login-helper-version SHA 均与操作前一致，后台仍为 `0.1.8+9` / PID `57378`。新版临时预览 PID `58704`，可执行文件 SHA 与当前 Swift 编译结果一致。没有重启 Agent 或 Codex shared server。

新增保存失败回滚回归先红后绿；三个注册登录项回归覆盖开关控制、拒绝其它应用所属项及系统结果未生效的回滚。完整 `bash scripts/verify.sh` 退出 0：447 项 JS 中 446 通过、1 项 SDK 条件测试跳过，89 项 Swift 通过，Go/TLS/Shell 检查全部通过。证据：`/private/tmp/fleet-startup-verify-20261010.log`、`/private/tmp/fleet-startup-live-20261010.swift`、`/private/tmp/fleet-startup-live-baseline-20261010.json`。
