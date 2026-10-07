# 安装与磁盘拖拽引导验证

2026-10-07，分支 `codex/multi-user-server`。本记录仅证明源码、独立开发构建和隔离运行；未替换 Applications、当前 launchd、Desktop 或真实网络，也未发布本次安装包。

## 实现

- 首页未安装状态直接“安装并启动”；Hub 内置真实 Fleet Agent.app，安装后使用独立运行副本。新实例通过显式安装启动标志启动后台，普通查看设置不启动已停止的后台。
- 完全磁盘访问入口打开系统设置并显示不抢激活的浮动窗；112×112 图标拖拽写入独立 agent 文件 URL，拒绝 Hub、嵌套载荷、符号链接与缺失目标，只允许复制。用户手动打开开关，返回 Hub 重启并检查；无法拖入时可在访达定位。
- 共用侧栏行、按钮和首页账号卡片扩大到完整区域可点击。
- 应用图标按既有 SVG 轮廓等比居中，约占画布 78%，不裁切、不改变窗口品牌标识。Web 安装说明同步。

## 自动测试

安装动作、拖拽主体和图标留白均先取得失败回归，再实施修复。失败日志位于 `/private/tmp/fleet-fda-drag-red-{swift,setup,icon}.log`。

```bash
FLEET_SPARKLE_ARCHIVE="$HOME/Library/Caches/fleet-hub/Sparkle-2.10.0.zip" bash scripts/verify.sh
```

真实输出摘要：

```text
Go: fleet-agent / fleet-enroll / fleet-enroll/multiuser = ok
Dashboard: tests 320, pass 320, fail 0
发行/打包: tests 16, pass 16, fail 0, skipped 0
FleetHub: Executed 9 tests, with 0 failures
FleetCore: Executed 43 tests, with 0 failures
Shell: 全部通过
==> 全部验证通过 ✓
```

退出码 0，日志 `/private/tmp/fleet-fda-drag-verify.log`。图标测试实际运行 Swift 渲染并扫描 PNG 像素检查尺寸、边距与居中；拖拽测试使用独立 NSPasteboard 验证真实文件 URL，不操作用户剪贴板或系统权限。

提交前再从暂存区树导出独立干净源码 `/private/tmp/fleet-native-drag-source.udVnpp`，不包含并行 Web/UI 改动，并执行同一验证入口：Dashboard 296 pass、发行/打包 16 pass（0 skip）、Swift 52 tests（0 failures），Go 与 Shell 全部通过，退出码 0。日志 `/private/tmp/fleet-native-drag-clean-verify.log`。`git diff --check` 与暂存区检查均通过。

## 真实构建与组件

Universal 开发包：`/private/tmp/fleet-fda-drag-preview/fleet-settings.NGCKMn/Fleet Hub.app`，版本 `0.1.2+3`。构建成功，主应用、登录启动器及 agent 均为双架构，资源与 Info.plist 检查通过；日志 `/private/tmp/fleet-fda-drag-build.log`。这是未签名公证的开发产物，不作为安装包分发。

```bash
node scripts/settings-runtime-uat.mjs '/private/tmp/fleet-fda-drag-preview/fleet-settings.NGCKMn/Fleet Hub.app'
```

退出码 0，日志 `/private/tmp/fleet-fda-drag-runtime.log`：

```text
PASS: standalone copy of actual bundled agent, private control socket, unbound status, no fabricated FDA evidence, legacy update blocked
PASS: matching app/agent version, maintenance rejects settings, graceful daemon restart preserves isolated configuration
PASS: bundled filebrowser initializes and serves only isolated test files over loopback
PASS: bundled ttyd responds and bundled tmux creates an isolated session without Homebrew
```

测试使用临时 HOME、私有 socket、动态 loopback 端口和测试文件；没有安装或重启真实 launchd，也没有授予 FDA。

## 原生窗口验证

独立临时应用 `/private/tmp/fleet-fda-drag-ui/Fleet Hub Preview.app`，临时 bundle identity `com.macfleet.fleet-hub.development-privacy`，与正式 Hub 分开。首页实际显示“尚未安装”与启用的“安装并启动”，没有强制跳转关于或灰色启动按钮。

实际点击四个侧栏行文字右侧空白区域 `(307,260)`、`(307,365)`、`(307,469)`、`(307,573)`，分别切换至运行状态、关联账号、磁盘权限与关于；选中状态和页面内容同步更新。首页账号卡片的空白区域 `(1120,570)` 也成功跳转关联账号。坐标为当次原生截图坐标，不是固定自动化接口。

未点击安装、授权或系统权限开关。开发预览已退出；真实浮动窗与系统设置之间拖放、正式安装的启动项与 FDA 仍须单独验收，不以 panel 属性测试替代实际授权结果。

## 发行阻塞与后续验收

只读检查：

```text
xcrun notarytool history --keychain-profile mac-fleet-hub-notary
Error: No Keychain password item found for profile: mac-fleet-hub-notary
```

退出码 69。公证 profile 必须由用户在本机终端恢复，密码不进入聊天或共享日志。恢复后从最新干净不可变提交重走 `scripts/release-fleet-agent.sh --native-candidate`，完成 agent、Hub、DMG 公证 Accepted、Sparkle 签名与真实 HTTPS 下载验证后，才晋级 abj 测试下载源。上一轮 `0.1.1+2` 中间产物也不能替代本次发行。

正式包发布后的验收顺序：

1. 从测试服务下载真实 DMG，验证签名、公证与包内 agent；在未安装环境点击“安装并启动”，检查独立运行路径及后台 PID。
2. 打开系统完全磁盘访问，将浮动窗图标拖入，确认列表对象为 Fleet Agent.app，开启开关。
3. 返回 Hub 重启并检查，验证新 PID、独立 agent 的真实受保护读取及 TCC 权限主体；不要求 Hub 获权，不从设置开关推断 FDA 成功。
4. 关闭设置窗口确认后台常驻；按保存偏好验证下次登录启动。设置服务器地址并由用户浏览器登录确认 owner，分别取得真实 mesh、终端、文件与聊天证据。
