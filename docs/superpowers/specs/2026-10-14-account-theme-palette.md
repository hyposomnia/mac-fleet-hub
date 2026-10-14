# 账号共享配色

外观页移除配色说明段落。浅色、深色的页面背景、主题色、反差色和字色保存到当前账号的网关偏好；浏览器本地仅缓存首屏与离线配色。显示模式沿用各浏览器现有选择。

| 需求 | 验收 | 实现 / 测试 |
|---|---|---|
| PAL-1 | 当前账号保存八个合法六位 hex 颜色，重新读取保持一致；设置 POST 和设备外观 PATCH 不覆盖配色 | multiuser/device_appearance.go、device_appearance_test.go |
| PAL-2 | 未设置返回 null；非法或缺失颜色返回 400，损坏偏好返回 500，失败不报告成功 | 同上 |
| PAL-3 | 旧本地配色仅在当前账号无配色时迁移；`ifAbsent` 不覆盖账号已有配色 | 账号偏好互斥锁、theme.js / Go 与 theme.test.mjs |
| PAL-4 | 各页面启动、聚焦时加载当前账号配色，缓存供首屏及离线使用；读取和写入串行，迟到读取不能覆盖保存结果 | theme.js / theme.test.mjs |
| PAL-5 | 点击保存后等待服务器确认再应用、缓存并关闭；失败保留编辑内容并显示错误 | app.js / theme.test.mjs、Chrome |
| PAL-6 | 不同账号读取和保存的配色互不影响 | multiuser/device_appearance_test.go |

沿用账号登录态保护的 `/api/settings`：GET 增加 `palette`；PUT 请求 `{palette:{light:{canvas,accent,highlight,text},dark:{...}},ifAbsent?:true}`，返回完整账号偏好，其中 `palette` 是服务器确认值。配色与设备外观、会话设置共用现有按 `user_id` 保存的偏好记录和互斥锁，不增加独立全局文件或新的业务规则。

验证后发布 SaaS 网关二进制及 Web，备份数据库、二进制和静态快照并验证 SHA、healthz、readyz 和真实 Chrome 保存/刷新。失败恢复发布文件和数据库备份。
