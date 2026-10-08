# Fleet Hub 品牌更新提案

已选方向：[第 2 版 · 浅钛银精修](variants/titanium/index.html) · [精修规范](variants/titanium/SPEC.md)。精细折面图标、浅色主规范、统一圆角、无边框表面。

新增：[五版对比](compare.html) · [五版交付说明](variants-README.md)。

入口：[可交互设计规范](index.html) · [原布局换肤预览](dashboard.html) · [登录配色](auth.html) · [开发规范](SPEC.md)。

推荐墨黑 / 暖瓷白 / 香槟金。规范页和换肤预览可切换深玉绿、冷铂灰与明暗主题。保留生产页面布局、组件与交互框架；示例数据和原型逻辑只在设计目录运行。

## 本地预览

在仓库根目录执行：

```bash
python3 -m http.server 8876 --bind 127.0.0.1 --directory docs/features/brand-refresh
```

浏览器打开 http://127.0.0.1:8876/ 。规范入口可直接浏览，完整交互建议使用 HTTP。

## 交付

- `assets/logo-*.svg`：主标识、单色版、组合示意。
- `assets/app-icon*.svg`、`icon-*.png`：矢量母版与安装尺寸；`favicon.svg`：小尺寸浏览器图标。
- `tokens.css`：可直接映射现有变量的颜色提案。
- `SPEC.md`：Logo 几何、色彩语义、可访问性与生产文件映射。
- `screenshots/`、`validation.json`、`VALIDATION.md`：原生浏览器验证证据。
- `brand-board.prompt.txt`：品牌总览的完整生成提示与参考文件。
- `assets/brand-board.png`：以本项目原生 SVG 与预览截图为参考生成的品牌总览；用于审阅风格。精确图形和颜色以 SVG 与 tokens.css 为准。
- `base.css` / `account-base.css`：原有样式快照，供原型沿用；不得覆盖生产文件。
- `manifest.proposed.json`：设计目录的清单参考，不直接替换生产清单。

## 评审操作

在规范页切换配色与明暗，会同步嵌入预览；全屏预览可独立切换。会话支持选择/移动端返回、搜索、文件模式与本地消息反馈。示例不连接服务，不发送消息或文件。
