# mac fleet hub 重构 · 开发实现文档

> 研发对照本文件开发。配套材料：
> - [`tokens.css`](tokens.css)：设计系统**源文件**，颜色 / 间距 / 圆角 / 组件可直接抠值。
> - [`desktop.html`](desktop.html) / [`mobile.html`](mobile.html)：可运行高保真稿，可直接看 markup 与交互。
> - [`PRD.md`](PRD.md)：新功能的产品需求与建议接口（F1–F4）。
> - 落点：`server/dashboard/`（`index.html` / `style.css` / `app.js` / `sw.js`）。
>
> 硬约束：保持**纯原生 HTML/CSS/JS 离线 PWA**，不引入框架 / 构建 / 外部字体或脚本 CDN；DOM 不用 `innerHTML`（文本走 `textContent`，沿用现有 `h()` 构造）；不用原生 `alert/confirm`（用样式化弹窗 / toast）。

---

## 0. 范围

只重做**表现层与部分交互**，承载的功能与现状对应；除 PRD 列出的新接口外，不改后端协议。

现有 API 基线（`app.js`）：`GET /api/nodes.json`、`GET {id}/api/sessions?scope=`、`POST {id}/api/open{sessionId}`、`POST {id}/api/new{cwd}`、`GET {id}/api/projects`、`GET {id}/api/info`、`POST {id}/api/proxy`、`GET {id}/api/watch?sid=`、`POST {id}/api/reload`、`{id}/files/`。

---

## 1. 设计系统（合并进 `style.css :root`）

以 `tokens.css` 为准，关键令牌：

- **颜色（暗/亮双主题）**：背景层级 `--bg / --surface / --surface-1 / --surface-2 / --surface-hover`；边框 `--border / --border-1 / --border-strong`；文字 `--text / --text-1 / --text-2 / --text-3`；强调 `--accent / --accent-1 / --accent-soft / --accent-line / --accent-text`；语义 `--online`(绿) `--offline`(灰) `--danger`(红，**无新增色**)；高程 `--e1/--e2/--e3`；`--ring`(focus) `--glow`。品牌渐变仅 `--brand-grad`（logo）。
- **字号阶** `--t-2xs … --t-3xl`；**间距阶** `--s1…--s9`(4pt)；**圆角** `--r-sm/--r/--r-lg/--r-xl/--r-pill`；**布局** `--rail-w`(260) `--sess-w`(330)；**动效** `--ease / --ease-spring`、`--dur-1/2/3`。
- **字体**：系统栈 `--font` + 等宽 `--mono`（不引入 Inter/Roboto 等）。

**组件**（class，详见 tokens.css）：
| class | 用途 / 变体 |
|---|---|
| `.btn` | `.primary`(主操作) `.accent`(连接) `.danger`(Bypass连接/终止) `.ghost` `.sm` |
| `.iconbtn` | 图标按钮，`.bare` 无边框 |
| `.dot` | 状态点 `.on`(在线) `.off`(离线) `.live`(运行中，脉冲) |
| `.badge` | `.live` `.ok` `.err` |
| `.seg` | 分段控件（活跃/全部、Claude会话/文件移动版） |
| `.input` `.switch` | 表单（代理开关 / 输入） |
| `.card` | 卡片容器 |
| `.overlay`+`.modal` | 居中弹窗（桌面设置） |
| `.menu` | 浮层菜单（用户菜单：切换主题 / 退出登录） |
| `.toast` | 行内提示（替代 alert，`.ok/.err/.info`） |
| `.skel` | 骨架屏（加载态，替代「加载中…」文字） |

**图标**：内联 Unicode 字形（◧ ▤ ▦ ⟳ ⤢ ◐ ＋ ✕ ▾ ⏹ ⓘ ⋮ ←），不用 SVG/emoji/icon-font。

---

## 2. 主题与响应式

### 主题：跟随系统 + 可覆盖
- **默认跟随系统** `prefers-color-scheme`；用户在「切换主题」后写 `localStorage('fleet-theme')` 覆盖并持久。逻辑与现有 `app.js` 的 `initTheme/applyTheme/toggleTheme` **完全一致**：
  ```
  初始: theme = localStorage('fleet-theme') || (prefers-color-scheme:light ? 'light':'dark')
  应用: document.documentElement[data-theme]=theme（CSS 已按 [data-theme] 切换令牌）
  切换: 翻转并写回 localStorage
  ```
- `index.html` 头部两条 `<meta name="theme-color">` 跟随 `prefers-color-scheme`，改主色须同步。
- 切换入口：桌面主机栏「切换主题」+ 用户菜单；移动头部 ⋮ 菜单。

### 响应式：单页自适应
- 同一套页面；**窄屏 / 移动浏览器（≤860px）切移动端布局**（桌面三栏不适用，改头部+主机条+分段+终端 push）。
- 保留 `100dvh`、`env(safe-area-inset-*)`、`viewport-fit=cover`；尊重 `prefers-reduced-motion`。

---

## 3. 信息架构（相对现状的变化）

- **去掉**「总览」「连接」入口。导航只剩 **Claude会话 / 文件**。
- 主机设置（Mesh IP + 代理）回到主机旁 **ⓘ** 触发的弹窗（桌面）/ 底部抽屉（移动）——即现状 host-modal 内容。
- 新增**退出登录**入口（用户菜单）。
- 桌面：`主机栏 | Claude会话列 | 窗口`（三栏 grid）；文件模式隐藏会话列（两栏，同现状 `data-mode="files"`）。
- 移动：头部（品牌 + ⓘ + ⋮）+ 横向主机条 + `Claude会话/文件` 分段 + 列表；**无底部 Tab**；终端为右滑 push 视图。

---

## 4. 界面与组件详规

### 4.1 主机栏（桌面）/ 头部 + 主机条（移动）
- 主机项：状态点（`nodes.json.online` → on/off）、主机名、会话数、**ⓘ**（点击 → 设置弹窗，`event.stopPropagation`）。点主机项 = 切换当前主机。
- 模式切换：`Claude会话 / 文件`。
- 桌面底部：`切换主题` + 用户行（▾ → 菜单）。移动：头部 ⋮ → 菜单。

### 4.2 Claude会话 列表
- 头：`活跃/全部` 分段 + 计数（活跃 = `sessions.filter(live)`，全部 = `total`）；刷新；`＋ 新建会话`（→ projects 选目录 → new）。
- **分组**：按 `cwd` 分组；组头 = 项目名（`cwd` 末段，**加粗 `--t-base`**）+ 路径（`~` 缩写）+ 计数；**整行可点折叠**（chevron ▸/▾ 旋转，折叠隐藏组内会话）。
- 会话行：状态点（`live`→`.live` 脉冲 / 否则 `.off`）、标题（`title`）、meta（`gitBranch · relTime(mtime)`）。**移除「桌面使用中」徽标**。

### 4.3 会话交互（核心变化，F1/F2 详见 PRD）
- **点击 / 点按会话行 = 仅选中**（高亮），不打开终端。
- **选中后**展开两个按钮（桌面与移动一致，**不再 hover 显示**）：
  - `连接`（`.btn.accent`）：打开 / 恢复终端，正常权限 → `open{sessionId}` / `new{cwd}`。
  - `⚠ Bypass连接`（`.btn.danger`）：claude 以 `--dangerously-skip-permissions` 启动 → `open/new` 带 `bypass:true`。终端头显示「⚠ 跳过权限」徽标。
- **终止图标 ⏹**（class `.stopbtn`，**注意勿与终端面板 `.term` 同名**）：
  - 仅**连接过 / 当前有运行进程**的会话（live）显示；选中时出现于行右上。
  - 语义是「终止进程」，**不是删除**——点击 → 样式化二次确认 → 结束 Mac 端进程；**会话条目仍保留在列表**。→ 新增 `close` 接口（见 PRD F2）。

### 4.4 终端（窗口 / 移动 push）
- 头：返回（移动/可选）、状态点 + 标题（bypass 时追加「⚠ 跳过权限」徽标，标题省略不挤掉徽标）、`Mac · 项目 · 权限模式`、重连、全屏。
- **「桌面端有新内容」重载条**：`watch?sid=` 返回 `external` 时出现 → `reload`。
- 内容为 ttyd iframe；bypass 时启动命令带 `--dangerously-skip-permissions`。
- **移动端输入坞**：键条 `esc/tab/ctrl(粘滞高亮)/↑↓←→`（按键 ≥44px）+ 多行命令框 + 独立发送键。承载现状 `#mobile-input` + `sendToTerm()`。

### 4.5 文件
- 嵌入 filebrowser（`{id}/files/` iframe），统一外壳；列表交互沿用 filebrowser 本身。

### 4.6 主机设置（ⓘ）
- 字段：Mesh IP（`info.meshIP`，可复制）、代理开关（`proxy.enabled`）、HTTP 代理（`proxy.http`）、**HTTPS 代理（`proxy.https`）**。保存 → `POST proxy`。
- 桌面 = 居中 `.modal`；移动 = 底部抽屉。离线主机显示「连不上」。

### 4.7 用户菜单 / 退出登录（F4）
- 菜单项：`切换主题`、`退出登录`（→ Authelia 退出端点，登出后回登录页）。

### 4.8 状态反馈
- 加载 = `.skel` 骨架屏；操作结果 / 错误 = `.toast`（**不用** `alert`）；二次确认 = `.modal` / 底部抽屉（**不用** `confirm`）；主机离线 = 明确「离线 / 连不上」而非空白。

---

## 5. 交互行为速查

| 动作 | 行为 |
|---|---|
| 点主机项 | 切换当前主机；重载其会话 / 文件 |
| 点主机 ⓘ | 打开该主机设置弹窗（Mesh IP + 代理） |
| 切 Claude会话 / 文件 | 切模式；文件模式隐藏会话列 |
| 点会话行 | **仅选中**（高亮） |
| 选中后点「连接」 | 打开终端（正常权限） |
| 选中后点「Bypass连接」 | 打开终端（`--dangerously-skip-permissions`） |
| 点会话 ⏹（仅 live） | 二次确认 → 终止 Mac 进程；**会话条目保留** |
| 点分组标题 | 折叠 / 展开该组 |
| 从终端返回 | **不杀进程**（tmux 持久） |
| 切换主题 | 翻转明/暗并写 localStorage（默认跟随系统） |
| 退出登录 | 跳 Authelia 退出端点 |

---

## 6. 新功能与接口（摘要，详见 [PRD.md](PRD.md)）

| | 功能 | 后端改动 |
|---|---|---|
| F1 | 连接 / Bypass连接 | `open`/`new` 加 `bypass` 参数（带 `--dangerously-skip-permissions`） |
| F2 | ⏹ 终止进程（会话保留） | 新增 `POST {id}/api/close{sessionId}`（kill tmux/claude） |
| F3 | 登录有效期 30 天 | Authelia `session` 配置（`server/authelia/configuration.yml`） |
| F4 | 退出登录 | 前端指向 Authelia 退出端点 |

---

## 7. 元素 ↔ 现有实现映射

见 [README.md](README.md) 的「严格对应」表（每个稿中元素对应 `app.js` 的真实字段 / 接口）。

---

## 8. 验收清单

- [ ] 明 / 暗 × 桌面 / 移动 四态正常；主题默认跟随系统、切换后持久。
- [ ] 触控目标 ≥44px；focus 态可见；`prefers-reduced-motion` 生效。
- [ ] 无总览 / 连接入口；主机 ⓘ 开设置（含 **HTTP + HTTPS**）。
- [ ] 点会话仅选中；连接 / Bypass连接 选中后显示；Bypass 走 `--dangerously-skip-permissions` 且终端有警示。
- [ ] ⏹ 仅 live 会话出现、终止后**会话保留**、二次确认非原生 confirm。
- [ ] 分组标题加粗 + 可折叠（桌面 + 移动）。
- [ ] 会话行无「桌面使用中」徽标；「会话」文案为「Claude会话」。
- [ ] 退出登录可用；登录态约 30 天。
- [ ] 仍为离线 PWA（无框架 / 构建 / 外部 CDN），无 `innerHTML` / `alert` / `confirm`。

---

## 9. 落点与注意

| 文件 | 改动 |
|---|---|
| `style.css` | 合并 tokens；按本文件组件实现样式 |
| `index.html` | 结构改为「主机栏 + Claude会话列 + 窗口」；主机弹窗 = 现状 host-modal 内容；保留被 `app.js` 选取的 `id`/`class` 钩子 |
| `app.js` | 视图/交互逻辑（选中-连接分离、Bypass 参数、终止确认、分组折叠、主题、退出）；保持 `h()` 安全 DOM、无 `innerHTML/alert/confirm`（属 `/dev`） |
| `authelia/configuration.yml` | F3 会话有效期 |
| 退出端点 | F4 |

> 实现中有疑问或要改设计，回到 `docs/features/ui-polish/` 找我迭代；定稿以本文件 + tokens.css + 两份高保真稿为准。
