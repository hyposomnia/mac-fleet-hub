# mac fleet hub —— 重构设计稿

> `docs/features/ui-polish/` 下可在浏览器直接打开的高保真稿。**确认后交开发对照修改**，不直接改 `server/dashboard/`。
> 原则：每个元素都对应代码里真实存在的数据 / 接口 / 状态；**新增功能单独写进 [PRD.md](PRD.md)**，不夹带其它凭空发明。

## 文件

| 文件 | 用途 |
|---|---|
| [`SPEC.md`](SPEC.md) | **开发实现文档（主文档，研发对照这份开发）**：设计系统 / 结构 / 各界面组件 / 交互细则 / 主题+响应式 / 验收清单 / 落点 |
| [`PRD.md`](PRD.md) | **新功能需求**（连接模型 / 终止进程 / 30天登录 / 退出登录）与建议接口 |
| [`tokens.css`](tokens.css) | 设计系统**源文件**：令牌（明/暗）+ 共用组件。**开发可直接抠值** |
| [`desktop.html`](desktop.html) | Web 端高保真；窄屏 ≤860px 显示「切移动端」提示 |
| [`mobile.html`](mobile.html) | 移动 H5 高保真，可点击跑通 |
| [`index.html`](index.html) | 封面 / 导航 |

> 主题**默认跟随系统**（`prefers-color-scheme`），用户切换后写 `localStorage` 覆盖——与现有 `app.js` 的 `initTheme` 一致。

打开方式：在 `docs/features/ui-polish/` 起静态服务（`python3 -m http.server`）访问，或直接打开 `index.html`。每份稿件可切明暗主题。

---

## 结构（按你的要求重做）

```
主机栏                         Claude会话 / 文件              窗口
┌──────────────┐             ┌──────────────────┐         ┌──────────────┐
│ ◧ fleet hub  │             │ 活跃/全部  ⟳      │         │ 终端 / 文件   │
│ [Claude会话|文件]│           │ ─ 项目分组 ─      │         │              │
│ ─ 主机 ─      │             │ 会话（点=选中）    │  ──▶    │              │
│ ● Mac1   ⓘ   │             │   ↳[连接][Bypass] ✕│         │              │
│ ● Mac2   ⓘ   │             │ ＋ 新建会话        │         │              │
│ ◐ 主题 / 用户▾│             └──────────────────┘         └──────────────┘
└──────────────┘   ⓘ→设置弹窗(Mesh IP+代理)   用户▾→退出登录
```

- **去掉「总览」「连接」两个菜单**；导航只剩 **Claude会话 / 文件**。
- 主机设置（Mesh IP + 代理）回到主机旁 **ⓘ** 触发的弹窗（桌面）/ 底部抽屉（移动）。
- **响应式**：同一套页面，窄屏 / 移动浏览器（≤860px）整页切到移动端布局；移动端因只剩两视图，**不再用底部 Tab**，改头部 + 横向主机条 + 分段控件。
- **退出登录**：桌面左下用户行 ▾ 菜单 / 移动头部 ⋮ 菜单。

## 会话连接模型（核心交互变化，详见 PRD F1）

- 点击 / 点按会话行 = **仅选中**，不打开终端。
- 选中（移动）或 hover（桌面）展开两个按钮：**连接**（正常权限）/ **Bypass连接**（`claude --dangerously-skip-permissions`，红色警示 + ⚠）。
- 每行 **✕** = 手动关闭该会话在 Mac 上的进程（返回不杀进程，✕ 才杀；二次确认）。详见 PRD F2。

## 严格对应：稿中元素 ↔ 现有实现

| 稿中元素 | 对应实现（app.js / index.html） |
|---|---|
| 主机在线/离线点、计数 | `nodes.json` 的 `online`；会话数 |
| 会话分组、标题、分支、时间 | `sessions` 的 `cwd` / `title` / `gitBranch` / `mtime`（**已去掉「桌面使用中」tag**） |
| 活跃 / 全部 + 计数 | `scope=active\|all`、`live` 计数 / `total` |
| 新建会话 | `projects` → `new` |
| 终端 + 重连 + 全屏 + 重载提示 | `open`/`new` ttyd url、reconnect、`watch`→`reload` |
| 文件 | `{id}/files/` filebrowser iframe |
| 主机设置弹窗（Mesh IP / 代理开关 / HTTP(S)） | `info` / `proxy` |
| 移动端键条 + 命令框 + 发送 | 现状 `#mobile-input` + `sendToTerm()` |
| 主题切换 | `data-theme` |
| **连接 / Bypass连接 / ✕关闭 / 退出登录 / 30天登录** | **新功能 → 见 [PRD.md](PRD.md)** |

> 上一版曾凭空发明「需要关注聚合 / 等待输入状态 / ⌘K / 重连全部 / 总览」等——已全部移除。本版仅保留你确认的功能 + 你新提的功能（后者进 PRD）。

---

## 设计系统（[`tokens.css`](tokens.css)）

保留现有视觉 DNA（冷蓝强调、系统字体栈、SF Mono、单色字形图标、暗色默认 + 双主题、`.16s` 缓动）；提升仅服务清晰度：模块化字号阶、4pt 间距阶、三级高程、状态色（online 绿 / offline 灰 / danger 红，无新增色）。danger 红用于 Bypass连接（贴合 `--dangerously-` 语义）。

## 给开发的对照指引

1. **令牌先行**：`tokens.css` 令牌合并进 `style.css` 的 `:root`。
2. **结构**：`index.html` 改为「主机栏 + Claude会话列 + 窗口」，去掉总览/连接；主机弹窗内容即现状 host-modal；保留被 `app.js` 选取的 `id`/`class` 钩子或同步告知改动。
3. **新功能**：按 [PRD.md](PRD.md) 实现 F1–F4（F1/F2 需后端加参数/接口，F3 改 Authelia 配置，F4 接 Authelia 退出端点）。
4. **占位**：文件区 = filebrowser iframe；终端 = ttyd iframe（稿中静态模拟）。

> 修改意见告诉我，我在 `docs/features/ui-polish/` 内迭代；定稿后再交开发。
