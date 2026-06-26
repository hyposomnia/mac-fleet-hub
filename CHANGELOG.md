# Changelog

mac-fleet-hub 变更记录（日期为本地时间）。

## 2026-06-26

### 连接权限模式：新增 Auto（三按钮 连接 / Bypass / Auto）
- **未在池的会话行展开三种权限模式**：`连接`（普通，逐项确认）/ `Bypass`（红，`--dangerously-skip-permissions`）/ `Auto`（琥珀，`--permission-mode auto`——自动批准 + 后台安全分类器，介于二者之间）。已在池的会话点行即瞬时切换，无按钮。
- **fleet-agent**：`/api/open|new` 的 `bypass` 布尔泛化为三态 `mode`（default/bypass/auto），`permFlag` 白名单映射（不放行任意 `--permission-mode`，`normMode` 把 plan 等收敛成 default），兼容旧前端 `bypass:true`。`watcher`/reload 沿用 mode。终端头按模式显 `⚠ 跳过权限` / `⚡ Auto` 徽标。改 main.go → 已重建 dist 双架构并升级三台 Mac。
- **rail 模式 tab 图标**：`Claude会话` 用 Claude 官方 logo（Bootstrap claude，描边填充），`文件` 用 file-text 文档图标。

### 工具栏图标 → 规整内联 SVG
- 刷新 / 重连 / 全屏 / ⓘ / ⋮ / 返回 / 设置 / 主题 / 退出 / 复制 / 发送 / 新建 / 关闭 / 空状态大图标 等 Unicode 字形换成 Feather 风格内联 SVG（统一 `.ic` 描边样式），放大到 17–18px，不再细小。品牌 logo 与 keybar 键标签保留文字。

### 终端 iframe 池（切换丝滑 + 上限可调）
- **单 iframe「导航式」终端 → iframe 池**：每个打开过的会话一个常驻 iframe，全尺寸叠放在 `#frames`，靠 `.show`（`visibility` 而非 `display:none`——后者会把 iframe 尺寸塌成 0、ttyd 把 pty resize 成 0×0、Claude TUI 排版炸掉）显隐切换。切换 = 改 class，瞬时；池内会话全程保持实时、不掉线。`#frame` 仅留给「文件」模式。
- **超上限按「最后收到输出时间」LRU 释放（排除当前窗口）**：包 `term.write` 记每个窗口的最后收到输出时刻；超上限时释放非当前窗口里最早的那个——关 iframe → WS 断 → tmux detach → 释放 1 个 pty，后台 Claude 进程不受影响，再点回来重新 attach。真实 Chrome 实测：单终端 iframe 边际成本 ~2.1MB JS 堆（+ 几 MB GPU 后备），浏览器内存非瓶颈，真正天花板是系统 pty 池。
- **选中已打开的会话即瞬时切换**：点会话行时若该会话已在池中，直接显示，不必再点「进入连接」。
- **设置弹窗（存网关、所有浏览器共享）**：user / ⋮ 菜单加「⚙ 设置」，4 项——桌面 / 移动 各设「最大终端窗口数」「每窗口最大回滚行数」（默认桌面 10、移动 4，回滚 5000）。回滚行数经 xterm `term.options.scrollback` 即时生效。存储走 `fleet-enroll` 新增 `/settings` 端点（原子写 `dashboard-settings.json`，服务端 `normalize` 钳制+缺省回退），nginx 加 `/api/settings`（Authelia 保护，镜像 `/api/names`）。
- 终止当前终端会话后从池移除并回空态（不再停在 `[exited]/Press ⏎ to Reconnect`）。

## 2026-06-25

### 网页终端体验
- **终端配色跟随 dashboard 深/浅色**：ttyd 把 xterm 实例挂在 iframe 的 `window.term`，按 `data-theme` 给 `term.options.theme` 套深/浅两套配色（取自 `style.css` 设计 token），切换实时生效、每次终端加载/重连后重套用。无需改 ttyd 启动参数。
- **网页终端可往上翻历史**：`fleet-agent` 经 `tmux -f ~/.macfleet-tmux.conf new-session` 加载 `history-limit 50000` + `mouse on`（原默认 mouse 关闭，网页里滚轮/上滑进不了 copy-mode，只能看一屏）。conf 由 agent 启动时自写。副作用：桌面端拖选复制改为 Shift+拖选；移动端无影响。改 `main.go` → 已重建 dist 双架构。
  - *修正*：初版用「建会话前 `set-option -g`」在**冷启动**（空闲会话全回收、tmux server 已退出）时哑火——无 server 可 set、空 server 又因 `exit-empty` 自杀；改用 `-f conf` 在 server 启动时加载，冷/温都生效（单测钉死 `-f` 须在 `new-session` 之前）。

### 品牌
- 新 app 图标（`icons/icon.svg`）：品牌渐变圆角砖（`#6e8bff→#9b7bff`）+ 两张等大会话窗叠层 + 品牌色提示符，体现「多 Mac 多终端会话」；矢量，各尺寸锐利。补 `<link rel="icon">` 浏览器标签页 favicon、manifest 加 `maskable`、`theme_color`/`background_color` 对齐 `#090c12`。

### dashboard
- **主机设置代理框填默认值**：未配置时 HTTP/HTTPS 代理框直接填入真实默认值 `http://127.0.0.1:7897`（而非仅 placeholder），避免「看着像填了、实际存成空 → 开关 on 但不注入代理 → claude 403」的陷阱。

### dashboard 移动端
- **终止按钮改 SVG**：原 Unicode `⏹` 在 iOS 渲染成彩色 emoji，换成内联 SVG 实心圆角方块（`currentColor`，跟随悬停变红）。
- **软键盘弹起顶起输入坞**：VisualViewport 算键盘高度 → `--kb`，移动端输入坞上移到键盘之上（修 iOS 输入框被键盘遮挡）。
- **禁页面缩放 + 全宽不溢出**：viewport 加 `maximum-scale=1, user-scalable=no, interactive-widget=resizes-content`；`html,body` 加 `overflow-x:hidden`。

### dashboard 交互优化
- **运行态会话行：静态绿点 + hover 才出停止按钮**：已起 fleet 进程的会话行，正常态显示一个**静态绿点**（`--online`，不脉冲，表示进程在跑）；鼠标 hover 时切换成**红色停止按钮**（露出 ⏹ SVG）。停止按钮边框由原圆角方块改为**圆形**（`border-radius: 50%`）。纯 CSS（`.stopbtn` 用 `::before` 画绿点、hover 互斥显隐绿点/图标）。
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
- Web 域名迁移到独立子域（`fleet.example.com`）；mesh 控制面与 web 子域解耦。
