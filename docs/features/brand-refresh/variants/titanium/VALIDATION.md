# 钛银浅色精修 · 验证

2026-10-01，本机 loopback HTTP + Playwright / Google Chrome 实测。

```json
{"pass":true,"contrastChecks":54,"radiusChecks":12,"borderChecks":24,"errors":[],"facets":3,"maskableMaxRadius":124.72,"safeRadius":204.8}
```

- 浅色/深色 × 1440×900 桌面 / 390×844 手机，列表、聊天、文件与规范页均检查；没有横向溢出，桌面三栏仍是 260 / 330 / 弹性工作区。
- 实际 computed style 验证：桌面小按钮、搜索框、附件、审批、发送共享 8px；标准列表操作共享 12px。手机搜索、按钮、附件、审批、发送共享 12px。
- 24 项界面默认、hover、selected、手机、文件与规范状态检查，没有可见装饰性边框；功能性的加载状态环单独保留。
- 54 项文字/表面、按钮/强调、语义色对比度均 ≥4.5:1。无常驻边框，因此不再测试装饰边线 3:1；保留操作时的焦点 outline。
- 默认打开浅色；规范主题同步图标、截图和链接；会话搜索空态、手机进入/返回、消息本地反馈、文件切换、规范按钮反馈可用。
- 图标 PNG 尺寸与不透明度通过；三片折面确实互不连接，maskable 安全圆内最大半径 124.72px < 204.8px。
- 控制台与 HTTP 资源错误均为 0。证据为 `audit-v2.json` 与 `screenshots/`。

品牌总览通过内置 image_gen 工具，以当前精修图标和原生浅色截图生成；文件 `assets/brand-board.png`，完整提示 `brand-board.prompt.txt`。总览用于艺术方向展示，精确实现以 SVG/CSS/原生预览为准。

本次只修改设计目录及 UI 记忆，未集成生产前端、未提交或部署；未运行提交/部署专用的全项目验证入口。真机软键盘与 standalone 安全区尚未验证。原第 2 版资产保留在 `revisions/v1/`。
