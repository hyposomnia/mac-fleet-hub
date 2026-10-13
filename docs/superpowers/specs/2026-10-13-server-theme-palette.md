# 服务器共享配色

外观弹窗移除配色说明段落。浅色、深色的页面背景、主题色、反差色和字色保存到网关；浏览器本地仅缓存首屏与离线配色。显示模式沿用各浏览器现有选择。

| 需求 | 验收 | 实现 / 测试 |
|---|---|---|
| PAL-1 | 保存八个合法六位 hex 颜色，重新读取保持一致；旧设置 POST 和设备外观 PATCH 不覆盖配色 | theme_palette.go、main.go / theme_palette_test.go |
| PAL-2 | 未设置返回 null；非法/缺失颜色返回 400，损坏或不可写存储返回 500，失败不报告成功 | 同上 |
| PAL-3 | 旧本地配色仅在服务器无配色时迁移；并发迁移和服务器已有配色均不被旧缓存覆盖 | Go 原子 ifAbsent、theme.js / Go 与 theme.test.mjs |
| PAL-4 | 各页面启动、聚焦时加载服务器配色，缓存供首屏及离线使用；读取和写入串行，迟到读取不能覆盖保存结果 | theme.js / theme.test.mjs |
| PAL-5 | 点击保存后等待服务器确认再应用、缓存并关闭；失败保留编辑内容并显示错误 | app.js / theme.test.mjs、Chrome |

沿用 Authelia 保护的 `/api/settings`：GET 增加 `palette`；PUT 请求 `{palette:{light:{canvas,accent,highlight,text},dark:{...}},ifAbsent?:true}`，返回 `{palette:...}`。写入独立原子 JSON 文件，不改变现有设置 POST 或设备外观 PATCH 协议。

验证后发布网关二进制及 Web，备份两者并验证 SHA、healthz 和真实 Chrome 保存/刷新。失败恢复发布文件，配色状态文件不随代码回滚覆盖。
