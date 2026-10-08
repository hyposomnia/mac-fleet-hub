# 02 · 精密钛银 · 浅色精修

用户选择第 2 版；本规范是此版当前设计来源。默认浅色，保留深色适配。原探索稿与图标保存在 `revisions/v1/`，供差异复核。

## 标识

保留六角空心核心，将粗厚的闭合轮廓重画为三片相同的等厚折面，按 120° 旋转。每片含外侧两面、内侧空心与精确断口；顶部和端点使用微圆角，减少尖锐接头。无额外细纹、轮廓线、金属渐变或高光。

- 母版 96×96，视觉中心 (48,48)。以每个 SVG 的实际曲线为准，不从品牌总览描摹。
- App icon 为 512×512 不透明方形画布，标记使用 `translate(88 88) scale(3.5)`。浅色默认画布 #EDF2F6 / 标記 #2C5D87；深色 #16202A / #B4CCE0。
- 安装产物提供 180 / 192 / 512 / 1024px、maskable 512px；小尺寸预览为 24 / 32px，favicon 16 / 32px。
- 不预裁切 PNG 四角；系统负责安装图标 mask。网页展示可按 22% 圆角模拟。
- `logo.svg` / `logo-dark.svg` 用于浅背景，`logo-on-dark.svg` 用于深背景，`logo-light.svg` 为纯白，`logo-mono.svg` 使用 currentColor。

## 浅色主规范

浅钛银页面、瓷白导航、纯白内容、钢蓝交互。表面色差和留白建立层次；只有浮层与输入器使用低强度漫反射阴影。正文保持系统字体，品牌字标使用 SF Mono / Menlo 大写；字距 .8px，产品字标字号 13px。

| 令牌 | 浅色（主规范） | 深色 |
|---|---|---|
| `--bg` | #EDF1F4 | #10141B |
| `--surface` | #F7F9FB | #181E28 |
| `--surface-1` | #FFFFFF | #202836 |
| `--surface-2` | #E3E9EF | #273143 |
| `--surface-hover` | #D8E2EA | #2D394B |
| `--text` | #253446 | #F2F5F9 |
| `--text-1` | #4B5D70 | #BBC9DB |
| `--text-2` | #516476 | #A5B6CA |
| `--text-3` | #536475 | #A0B0C5 |
| `--accent` | #2C5D87 | #B8D9FF |
| `--accent-1` | #244D70 | #D0E6FF |
| `--accent-text` | #244D70 | #D0E6FF |
| `--accent-contrast` | #FFFFFF | #111826 |
| `--online` | #386046 | #B1E1BC |
| `--wait` | #785319 | #F6D7A3 |
| `--danger` | #A23B40 | #FFB8B8 |

`--border` / `--border-1` / `--border-strong` / `--accent-line` / `--chat-border*` 全部 transparent；列边线、卡片描边、分段选择器描边、输入框描边与 inset 细线全部去除。CSS 对实际容器设 border:0，而非用背景相同色伪装描边。

## 圆角与尺寸

| 尺寸与类型 | 圆角令牌 | 具体适配 |
|---|---|---|
| 约 30–39px 小控件，主要为 36px | `--radius-compact:8px` | 搜索框、图标按钮、小按钮、分段选择项 |
| 40–56px 标准控件，主要为 44px | `--radius-control:12px` | 标准按钮、输入框、选择器、设备与会话列表操作 |
| 内容卡片、气泡、菜单 | `--radius-card:16px` | 消息卡片、审批、代码卡、菜单、规范示例卡 |
| 结构容器与弹层 | `--radius-panel:20px` | 输入器、弹窗、品牌大面板、截图容器 |

同尺寸且同层级的组件必须共用令牌，禁止单独写 5 / 6 / 7 / 9 / 10 / 14px 等局部圆角。嵌套分段容器保留外 12px / 内 8px，与 4px 内边距匹配；圆形状态点和头像属于语义形状，不改为方形。移动端小控件提升至 44px 触控目标，同时统一 12px，不能放大尺寸却保留旧圆角。

## 无边框状态

- 默认：填色、留白和字重区分表面，不绘制常驻边框。
- hover：表面色切换；不增加描边或 inset outline。
- selected：纯白卡片或钢蓝色块 + 字重 / aria 状态，操作位置不变。
- focus：仅键盘导航、输入聚焦时显示 2px 钢蓝 outline，offset 2–3px；此为瞬时交互反馈。移出焦点后消失，不作为装饰轮廓。
- disabled：沿用原有禁用语义、opacity 与不可操作行为。
- loading：功能性的旋转状态环保留，它不是填色元素的容器边框。

## 框架与交接

保留 260px 主机栏 / 330px 会话栏 / 弹性工作区、860px 断点与整体 composer，沿用原生 HTML/CSS/JS；示例不连接生产 API。

- [浅色品牌规范](index.html)
- [浅色交互预览](../../dashboard.html?variant=titanium&theme=light)
- [深色交互预览](../../dashboard.html?variant=titanium&theme=dark)
- [验证记录](VALIDATION.md)

实施参考本目录 `tokens.css` 的表面与圆角规则、`assets/` 的 SVG 母版；`refinement.css` 仅用于规范展示页面。后续落地目标仍为 `server/dashboard/style.css`、标识资产、manifest 和缓存资源列表，生产业务/API 不随视觉调整变化。本次仅更新设计目录。

## Web 落地记录

2026-10-01 已整合到实际服务器静态页面；仅独立本地验证。[实施与证据](../../implementation/VALIDATION.md)。实际设备联动不在本次验证范围内。
