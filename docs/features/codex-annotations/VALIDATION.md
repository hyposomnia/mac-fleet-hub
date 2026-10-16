# 设计原型验收

日期：2026-10-16。仅验证本目录 HTML/CSS/JS 设计稿。使用 CUA 控制本机 Google Chrome，在 `http://127.0.0.1:8873/` 验证；未连接生产网关、Mac agent、Codex app-server 或 DeepSeek 模型。

## 结果

编号底色追加验证：1、2 标记都使用当前主题 `--accent`；浅色为 `#356b5b`、深色为 `#78b59d`，数字使用主题对比字色。明暗 × 桌面 / 手机及编号 2 共 5 个状态检查通过，见 [marker-theme-evidence.json](marker-theme-evidence.json)。效果：[浅色编号](screenshots/marker-theme-light.png)、[深色编号](screenshots/marker-theme-dark.png)。

本轮细节检查 **9 项通过**，没有 error / warn 日志，记录见 [detail-evidence.json](detail-evidence.json)。此前的协议与精简浮层检查记录为 [browser-evidence.json](browser-evidence.json)，未将其计入本轮新增检查。

本轮验证了：选区菜单和数量附件的半圆胶囊、实际拖选后点空白取消且菜单不重开、保留已保存注释、四态光标行框上下居中（中心偏差实测 0px）、选区 RGB 来自当前主题色、多行编辑和布局。临时选择与保存操作均通过真实浏览器输入完成。

覆盖内容：

- 浮层只保留输入与一个保存勾选，没有底部提示、发送、取消行。
- Enter 保存；Esc 删除新建临时标记或正在编辑的已保存注释；删除首项后其余项重排。
- 空评论可以保存，并从 composer 发送；Shift+Enter 正常换行，多行评论保留胶囊外形。
- Codex 输出与提示保持 `codex-annotation`、原生首换行及包络。
- DeepSeek 接入的输出与提示使用模型无关的 `fleet-annotation`；上下文不含 Codex / DSH 名称，不携带原生消息 source。
- 明暗 × 桌面 / 手机四态没有横向溢出；CSS 没有 hover 视觉样式，保留键盘 focus-visible。

## 布局与视觉

| 状态 | 视口 | 文档 scrollWidth | 评论浮层 | 保存按钮 |
| --- | --- | --- | --- | --- |
| 桌面浅色 | 1440 × 900 | 1440 | 294 × 44 | 32 × 32 |
| 桌面深色 | 1440 × 900 | 1440 | 294 × 44 | 32 × 32 |
| 手机浅色模拟 | 390 × 844 | 390 | 344 × 48 | 44 × 44 |
| 手机深色模拟 | 390 × 844 | 390 | 344 × 48 | 44 × 44 |

浮层垂直内边距 2px，圆角值 999px，实际两端为半圆。多行评论操作后实测总高 60px、半圆形态保持。已检查截图，桌面与手机输入内容、编号、保存勾选没有被裁切；临时浏览器视口在验证后恢复。

光标单行行框使用桌面 20px 行高＋上下各 8px 内边距、手机 24px 行高＋上下各 6px 内边距；四态中心偏差均为 0px。选文使用主题 accent 的透明色，浅色 RGB 对应 `#356b5b`，深色对应 `#78b59d`。“添加到对话”和注释数量附件外层、内层均取 999px 圆角。

截图：

- [紧凑浮层细节](screenshots/compact-editor-detail.png)
- [添加到对话胶囊](screenshots/selection-menu-detail.png)
- [输入框数量附件胶囊](screenshots/attachment-capsule-detail.png)
- [桌面浅色：填写注释](screenshots/desktop-light-select.png)
- [桌面深色：填写注释](screenshots/desktop-dark-select.png)
- [手机浅色：填写注释](screenshots/mobile-light-select.png)
- [手机深色：填写注释](screenshots/mobile-dark-select.png)
- [桌面浅色：输入框附件与管理](screenshots/desktop-light-composer.png)
- [桌面深色：输入框附件与管理](screenshots/desktop-dark-composer.png)
- [桌面深色：引用与详情](screenshots/desktop-dark-reference.png)
- [文档内选文注释](screenshots/desktop-light-file.png)
- [手机深色：注释抽屉](screenshots/mobile-dark-composer.png)
- [手机浅色：已发送注释回看](screenshots/mobile-light-history.png)
- [手机浅色：DeepSeek 共用输入框注释](screenshots/mobile-light-deepseek.png)

## 未在设计阶段验证

- Codex Desktop ↔ Fleet 的真实互发、原生 parser 恢复与 source 导航。
- 真实 DeepSeek 模型对 Fleet 自有 directive 的遵从性；DSH 原生兼容等其正式支持后再做。
- 队列持久化、刷新恢复、历史分页、多输入 steer 的作用域。
- 同源 iframe 桥、富文档 / 浏览器 PDF viewer、跨源文档。
- iOS / Android 系统选择菜单、拖柄、软键盘、VisualViewport、PWA standalone 和触屏。

本轮只修改设计目录，没有提交、部署或改生产源码，因此未运行提交 / 部署前入口 `bash scripts/verify.sh`；实施与发布须按 AGENTS.md 执行。临时静态预览服务在验收结束后关闭；原型可离线打开或按 README 重新预览。
