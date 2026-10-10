# 原生预览停止 Agent 验证

用户要求 Hub 的“停止”能真正停止后台。本轮沿用 `codex/multi-user-server` 的原有 `NativeManagement`，只更新源码与临时调试预览；没有构建、签名、公证或发布新的正式客户端。

## 原因与修复

`SettingsView.perform` 在预览模式下直接返回，但运行页仍展示了启动、重启、停止。因此这些按钮不会发出任何后台管理操作。现仅允许用户主动使用这三个运行按钮执行原管理逻辑，自动刷新仍不准备或启动后台，安装、升级、卸载和解除关联仍沿用原预览保护。

停止继续使用原有 `desktop prepare-stop` 活动任务守卫，再 `launchctl bootout gui/<uid>/com.macfleet.desktop-agent`；失败继续执行 `desktop resume`，错误进入主状态。没有新增强制杀进程、Codex 传输或共享服务停止逻辑。

停止后的状态不再把缺少后台响应解释成“尚未关联账号”；持久化关联没有被移除。

## 实机停止

将当前编译的 `FleetCore` 链接到临时 Swift 验证程序，通过与预览相同的 `NativeManagement.stop()` 执行停止，随后检查原 PID、launchd 服务、管理控制端点和 `SettingsModel.refresh()`：

```text
stop confirmed: old_pid=57378 process=exited service=unloaded control=offline model=stopped
```

通过原生界面读取确认预览显示“后台未运行”与“启动”。自动点击工具无法可靠定位运行按钮，所以本轮不宣称完成鼠标点击的完整 GUI 验收；管理 API 的实际执行及界面状态刷新已验证。

## 重启恢复的实际限制

同一个 `NativeManagement.start()` 启动了 PID `68481`，但独立网络仍处于 `connecting`。原有 30 秒健康检查未等到网络就绪并按原逻辑回滚，未返回启动成功。

随后通过已安装、已签名 Agent 的原有 `desktop autostart-start` 恢复服务，退出 0，PID 为 `71540`。它在自身网络接入超时后报告：

```text
runtime=failed
error=设备网络接入失败，请检查服务器连接后重试
```

进程启动与联网成功分开报告：本轮已经恢复后台进程，但没有恢复设备联网。只读排查确认 abj 独立验收 Headscale、mesh、web 与多用户服务运行中，`mac2` / 节点 3 的登记保留，但不在线。现有服务器证书通过 macOS `security verify-cert`；Go 默认 TLS 的 `/key?v=108` 返回 200。没有修改网络配置、节点、关联凭据、证书或信任设置，也没有使用跳过 TLS 校验。

运行前后的 `settings.json`、`binding.json`、Agent/shared plist、登录 helper marker，以及已安装 Hub/Agent 可执行文件 SHA 均一致。原有 Desktop app-server PID `41037` 和 `62273` 保持运行。开始验证时 shared keeper/47682 listener 已不在运行；本轮没有把这个先前状态当成停止 Agent 导致的变化。

## 源码验证

新增三项生命周期回归覆盖：先通过空闲守卫再仅卸载 desktop-agent、活动任务拒绝停止、bootout 失败时恢复维护状态并保留错误。

最终 `bash scripts/verify.sh` 退出 0，实际输出摘要：

```text
ok  fleet-agent (cached)
ok  fleet-agent [native IP/DERP TLS regression]
ok  fleet-enroll (cached)
ok  fleet-enroll/multiuser (cached)
JavaScript: 447 tests, 446 passed, 0 failed, 1 conditional SDK test skipped
Swift: 92 tests, 0 failures
Shell/install/idle/uninstall/deployment regressions: passed
==> 全部验证通过 ✓
```

证据：`/private/tmp/fleet-stop-verify-20261011.log`、`/private/tmp/fleet-stop-live-20261011.swift`、`/private/tmp/fleet-stop-live-baseline-20261010.json`。调试预览位于 `/private/tmp/fleet-native-list-preview-20261010/Fleet Hub Preview.app`；正式包等待用户后续消息。
