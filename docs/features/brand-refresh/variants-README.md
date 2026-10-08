# Fleet Hub · 五版品牌方向

当前选定：[精密钛银 · 浅色精修](variants/titanium/index.html)。最新验证见 [钛银精修验证](variants/titanium/VALIDATION.md)；原第 2 版探索资产保留在 `variants/titanium/revisions/v1/`。

[打开五版对比](compare.html)。保留原版香槟金，新增五种完整设计语言。A/B 可独立选择原版或任一方案，在相同内容与视口下比较深浅主题。

| 方案 | 语言 | 品牌页 | 规范 |
|---|---|---|---|
| 01 瑞士信号红 | 双向折角、粗体大写、硬边分区、信号红 | [预览](variants/signal/index.html) | [SPEC](variants/signal/SPEC.md) |
| 02 精密钛银 | 精细折面、等宽字标、无边框表面、浅钛银 | [预览](variants/titanium/index.html) | [SPEC](variants/titanium/SPEC.md) |
| 03 书卷墨绿 | 连续门廊、衬线字标、书籍排版、暖纸墨绿 | [预览](variants/atelier/index.html) | [SPEC](variants/atelier/SPEC.md) |
| 04 瓷白雾蓝 | 圆弧端口、小写字标、柔和浮层、瓷白雾蓝 | [预览](variants/porcelain/index.html) | [SPEC](variants/porcelain/SPEC.md) |
| 05 夜航玫瑰 | 开口门环、宽字距、留白对照、葡萄灰玫瑰 | [预览](variants/nocturne/index.html) | [SPEC](variants/nocturne/SPEC.md) |

每版目录含 `index.html`、`SPEC.md`、`tokens.css`、`manifest.proposed.json`、`brand-board.prompt.txt`、`assets/` 与 `screenshots/`。Logo 提供 SVG 母版及单色/反白适配；App icon 提供 180/192/512/1024px、浅色版和 maskable 512px。

品牌总览使用内置 image_gen 工具，以各版原生图标与真实界面截图为参考生成，最终保存于 `variants/<id>/assets/brand-board.png`。完整提示见各版 `brand-board.prompt.txt`；总览用于展示气质，标志、颜色和界面细节以原生 SVG、CSS 与实际浏览器预览为准。

统一保留 260px 主机栏 / 330px 会话栏 / 弹性工作区与 860px 断点。只调整品牌、颜色与局部组件表面；模拟交互不连接生产 API。此批方案尚待评审，生产前端未因本次设计修改。

验证证据：[验证说明](VARIANTS-VALIDATION.md)、`variants-audit.json`、`variants-layout.json` 与各版截图。
