# Fleet Hub 白底列表界面预览

本轮在 `codex/multi-user-server` 修改原生界面。按用户要求，仅编译 Swift 调试界面并更新临时预览程序；没有运行正式发行入口、重新签名公证、制作安装包、部署网关或替换已安装应用。

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

本地证据：`/private/tmp/fleet-native-list-preview-verify-20261010.log`。调试预览位于 `/private/tmp/fleet-native-list-preview-20261010/Fleet Hub Preview.app`，只读现有 Agent 状态，跳过后台准备、生命周期和设置写入；Release 构建不启用该模式。

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

未实际解除设备关联，未修改登录启动设置。正式发行等待用户后续消息。
