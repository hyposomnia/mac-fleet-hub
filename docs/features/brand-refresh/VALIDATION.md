# 品牌设计验证

日期：2026-10-01。使用 Playwright + 已安装 Google Chrome，对本地 HTTP 设计稿进行原生浏览器检查与截图。安装图标由原生 SVG 经浏览器渲染导出，品牌总览使用内置 image_gen 生成。

## 实际结果

```text
主规范、dashboard 和登录页面：控制台异常 0，HTTP 资源错误 0
桌面 1440 × 900：三栏实际计算宽度 260px 330px 850px
桌面 / 移动 390 × 844，明暗状态：横向溢出 false
三种配色 × 两种主题：168 项颜色对比检查全部通过
安装图标：180 / 192 / 512 / 1024px，完全不透明
maskable：核心图形最大半径 155.67px，安全半径 204.8px，通过
HTML 引用：缺失文件 0
node --check guide.js：通过
node --check preview.js：通过
```

正常信息文字和强调按钮的最低实测对比度为 4.55:1；关键控件边界另按 3:1 核验。禁用控件透明度遵循现有不可操作表现，不计入正常文字验收。

交互检查：会话搜索、无匹配空态、移动会话打开/返回、只在本地的消息反馈、文件模式切换、规范页配色和明暗同步、登录示例四态无溢出。截图检查包含图标尺寸、文件行结构和完成主题过渡后的状态。

- [详细颜色与交互检查](audit.json)
- [布局状态结果](validation.json)
- [浅色桌面](screenshots/desktop-light.png) / [深色桌面](screenshots/desktop-dark.png)
- [浅色移动列表](screenshots/mobile-list-light.png) / [深色移动聊天](screenshots/mobile-chat-dark.png)
- [浅色移动文件](screenshots/mobile-files-light.png)
- [规范总览](screenshots/brand-overview.png)

## 验证边界

真机软键盘、PWA standalone 安全区、实际安装与生产 Service Worker 尚未验证。设计原型不加载生产业务脚本，也不连接服务。本次交付为品牌设计；生产集成、项目验证入口与安装验收按 SPEC.md 和 AGENTS.md 执行。

本地预览服务器仅监听 127.0.0.1:8876，保留供此次设计评审使用。
