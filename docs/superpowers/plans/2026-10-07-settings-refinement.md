# 设置整改执行计划

## 1. 原生设置

Files: `RuntimeLayout.swift`、`SettingsModel.swift`、`NativeManagement.swift`、`SettingsView.swift`，以及现有 Swift/Go 测试。

- [ ] 测试 `requiresInstallation` 规范路径仍可运行、临时与 DMG 路径仍拒绝；`swift test --filter RuntimeTests` 先红后绿。
- [ ] 测试 `validatedOrigin("fleet.example.test:7443") == "https://fleet.example.test:7443"`、非法地址拒绝。
- [ ] 测试 `save()` 写入串行且并发编辑最终保存最新草稿，旧回包不覆盖；保存失败保留有效配置。
- [ ] Go 空 origin 只用于未关联设置；HTTP 设置契约拒绝修改已绑定 origin；定向 `go test -run Desktop`。
- [ ] 自动启动移至运行状态；自动保存不锁输入，网页授权显式 flush；安装位置不符时给安全操作而非灰按钮。

## 2. 账号内容复用与统一弹窗

Files: `account.js`、`account_pages.test.mjs`、`settings_dialog.js`、`settings_dialog.test.mjs`、`index.html`、`app.js`、`account.css`。

- [ ] `createPages` 接受 content/status/nav 容器，`start('add-device')` 不渲染安全表单；账号不包含下载区。
- [ ] 测试账号更换验证器/恢复码期间 `canLeave()` 为 false，确认后恢复；注册可返回登录，verify/setup 无切换。
- [ ] 控制器复用原 DOM ID，保留自动化和会话逻辑；切换/关闭确认表单，控制焦点和旧异步结果。
- [ ] `node --test server/dashboard/account_pages.test.mjs server/dashboard/settings_dialog.test.mjs` 先红后绿。

## 3. 登录视觉

Files: `auth.html`、`account.css`、`auth_effects.js`、`auth_effects.test.mjs`。

- [ ] 测试触屏/reduced-motion 不绑定 pointermove；fine pointer 单帧更新，离开/隐藏清理。
- [ ] 克制网格与光标跟随；纯链接登录/注册、轻量主题、明暗输入对比。
- [ ] 本地独立服务浏览器检查桌面与移动布局、焦点和账号/添加设备切换。

## 4. 一次性交付

- [ ] 全量 verify 通过、实际命令输出记录、聚焦提交推送。
- [ ] 干净不可变提交唯一入口统一签名公证及更新 abj 测试；下载 SHA、签名、公证和 HTTP 验证。
- [ ] 清楚区分源码测试、下载验收、待用户安装的 TCC 和入网验证。
