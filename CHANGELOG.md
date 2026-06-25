# Changelog

mac-fleet-hub 变更记录（日期为本地时间）。

## 2026-06-25

### 品牌
- 新 app 图标（`icons/icon.svg`）：品牌渐变圆角砖（`#6e8bff→#9b7bff`）+ 两张等大会话窗叠层 + 品牌色提示符，体现「多 Mac 多终端会话」；矢量，各尺寸锐利。补 `<link rel="icon">` 浏览器标签页 favicon、manifest 加 `maskable`、`theme_color`/`background_color` 对齐 `#090c12`。

### dashboard 移动端
- **终止按钮改 SVG**：原 Unicode `⏹` 在 iOS 渲染成彩色 emoji，换成内联 SVG 实心圆角方块（`currentColor`，跟随悬停变红）。
- **软键盘弹起顶起输入坞**：VisualViewport 算键盘高度 → `--kb`，移动端输入坞上移到键盘之上（修 iOS 输入框被键盘遮挡）。
- **禁页面缩放 + 全宽不溢出**：viewport 加 `maximum-scale=1, user-scalable=no, interactive-widget=resizes-content`；`html,body` 加 `overflow-x:hidden`。

### dashboard 交互优化
- **会话行去掉常驻状态点**：默认不显示行首点；仅「等待你回复 / 选择」的会话显示**棕色点**（由 fleet-agent `waiting` 信号驱动，见下）。行首点位**恒定留出**（无点时透明占位，标题统一对齐）；行距收紧；棕色点**静态不脉冲**。
- **分组折叠箭头**改用清晰的内联 SVG chevron（原 Unicode `▾` 太淡）。
- **「终止 ⏹」按钮**改由真实进程状态驱动（fleet-agent 新增 `pty` 字段）：仅对**已起 fleet 进程**的会话显示，且**不选中也显示**；未起进程的不显示。
- **已连接的会话**再选中时按钮显示「**进入连接**」，点击仅回到已有终端、**不重连、不重载**（tmux 进程持久）。
- 去掉「选择项目目录」（新建会话）弹窗里无意义的 `+` 图标。
- 会话列表刷新改为 stale-while-revalidate：仅空列表显示骨架，刷新已有内容不再闪。
- 修复「每次点击都闪一下」：`$$('[data-mode]')` 误把带 `data-mode`（CSS 切栅格用）的 `#app` 根容器选中并挂上 `setMode` onclick，导致点页面任意处都冒泡触发重渲染；收窄为 `button[data-mode]`。

### fleet-agent
- `GET /api/sessions` 每个会话新增 `pty` 字段：按 `tmux` 实况标记该会话是否已有 fleet 进程（供前端显示「终止 / 进入连接」）。
- `GET /api/sessions` 每个会话新增 `waiting` 字段：读 jsonl 尾部，最后一条是 assistant 且 `stop_reason==tool_use` → 卡在「等你回答/授权」（AskUserQuestion 待答或工具待授权）。供前端显示棕色点。按 mtime 缓存，仅瞬态（工具执行中/子 agent）可能短暂为真。

## 此前

- **dashboard 重构 + F1–F4**：连接 / Bypass连接（`--dangerously-skip-permissions`）、终止进程（`POST /api/close`，会话保留）、登录有效期 30 天（Authelia）、退出登录（`/auth/logout`）。
- **fleet-agent 自管理子命令**：`update / start / stop / restart / status`；pty 耗尽精确提示（503 + 可读 message）。
- Web 域名 `mfh` → `fleet.example.com`（mesh 控制面在根域 `example.com:28443`，与 web 子域解耦）。
