# Fleet 文本注释设计与开发交接

状态：已确认的设计基线；SaaS 实现与验证边界见 [IMPLEMENTATION.md](IMPLEMENTATION.md)。设计依据：2026-10-16 本机 ChatGPT.app 26.1007.71326、用户参考截图和当前 main。调研详情见 [RESEARCH.md](RESEARCH.md)。

## 1. 目标与范围

让 Fleet 的 Codex 与 DeepSeek 自绘聊天共用“选文 → 可选评论 → 输入框附件 → 发送 → 注释回引”的完整流程。Codex 遵守所检原生包络、字段、编号与 `codex-annotation`；DSH 接入使用模型无关的 Fleet 自有 `fleet-annotation`，待 DSH 原生支持后再适配。两者共用逻辑注释模型与交互，普通用户不用看协议。

首轮覆盖：历史与当前已稳定渲染的对话正文、同会话工作区的 Markdown / 纯文本 / 代码选文，含文件 Tab；桌面、移动、浅色、深色。页面必须确实能读到文字；浏览器原生 PDF viewer、跨源 iframe、DOCX canvas、图片区域不承诺支持，支持清单由能力探测决定。对话中“其他文档”的首轮含义为 Fleet 当前已有的可读文本预览，不凭空添加 DOCX/PDF 原生批注能力。

本目录保留设计与隔离原型；生产实现位于 SaaS 分支。原型模型回答仍为脚本演示，不调用真实模型。

## 2. 统一交互

### A01 · 选中和填写

- 鼠标拖选、键盘 Shift 选择或移动端原生长按选择后，在选区附近显示“添加到对话”。只对完整落在一个可注释 source 内的非空文本显示；支持跨段落与代码行，拒绝跨消息、跨文件、跨 frame 的混合选区。
- selection 快照在操作菜单获得焦点前捕获，包含原文、作用域和逻辑位置。菜单 `pointerdown` 不破坏选区，不能到点击回调时才临时读取已丢失的 Selection。
- 点击空白处清除原生 Selection、临时选文高亮和“添加到对话”菜单，后续 pointerup 不得重新弹出菜单；已保存的注释、原文标记和 composer 附件保留。点击菜单、评论框、注释标记或详情内容不提前清空它们正在使用的选区。
- 按钮打开选区旁的可选评论输入。浮层是两端半圆的胶囊，单行桌面总高 44px、移动 48px，垂直内边距 2px；只留输入和保存勾选，不显示底部快捷键说明、发送或取消行。原文高亮跟随主题色，旁边出现从 1 起的主题色编号标记。单行文字、placeholder 和光标与浮层上下居中：桌面 textarea 的 20px 行高＋上下各 8px 内边距，手机 24px 行高＋上下各 6px 内边距，textarea 均为 36px。长评论自动增高，上限 120px 后内部滚动。全空白评论按缺省处理；原文非空即允许保存。
- Enter 或保存勾选保存到 composer；Shift+Enter 换行；IME 候选确认不触发保存或删除。Esc 删除当前注释：新建时移除临时标记；编辑已保存草稿时删除该条注释并重排剩余编号。点击外部仅关闭编辑器并放弃未保存修改，保留已有注释；与 Esc 删除区分。保存不调用模型、不改原文件。
- 注释先保存到 composer，再通过会话发送按钮投递；评论浮层内不直接发送。只读会话允许形成本地注释草稿、禁止投递，恢复写权限后可发。
- 新建、编辑必须重新验证仍属于原目标会话。切换模型实际会话或设备时关闭弹层，把草稿留在原作用域，不转投新会话。
- 若正在流式更新，先冻结该 source 的可见文本快照；更新不移动用户选区。重新定位失败保留引文并提示“原文已变化”，不能跳到同一句话的其它出现位置。
- 超出容量不截断：设计上每条选文最多 20,000 UTF-16 code units、每条评论最多 4,000、每条消息最多 20 条注释、合计 wire UTF-8 最多 256KiB（与现有请求上限核对后实施）。这些是 Fleet 产品限额，只有选文 20,000 与公开 Browser Range 文档相同，不声称其余来自 Codex。超限时保留选区，提示缩短后添加。

### A02 · 输入框中的展示

- 在现有 `.chat-composer-inner` 内、textarea 上方放**一个聚合附件**：评论线性图标＋“N 条注释”＋展开指示，末尾有移除全部按钮。它与文件附件同层级；不把选文写入 textarea，不占用一个新的输入框。
- 展开后按数组顺序显示：编号、完整选中文字、非空用户评论、来源、查看原文 / 编辑 / 移除。完整内容可滚动；小屏为底部抽屉，最大 65dvh。有来源的标题可以导航；没有来源只展示已保存文本。
- 编辑保持稳定 ID 与数组位置；删除后仅重排**尚未发送**数组的展示编号。草稿不按选中位置重新排序。清空 textarea 不删除注释，移除注释不修改正文或其他附件。
- 只有注释、没有正文和其它附件也能发送。允许只引用、没有评论，不替用户编造一句“按意见更新”。
- 文件 Tab 继续复用同一会话草稿。收起的 compact composer 显示数量附件，可展开管理；将 editor / 附件 popover 视为 composer 所属交互，点击它们不触发现有失焦收起规则。不更换 textarea 或创建第二个 store。
- 草稿按 `deviceId + assistant + sessionId` 隔离。用版本化 IndexedDB 保存 annotation / text / 附件引用，刷新可恢复；只保存必要位置，不存大段未选中的文件内容。退出账号或按现有隐私策略清理，不能进入 Service Worker 公共 shell cache。原型仅使用内存 store，不冒充持久化完成。

### A03 · 发送和已发送注释

- 提交时冻结正文、附件、注释数组、clientMessageId、wire 版本和序列化文本。服务端持久化确认后才算发送成功；失败则完整恢复，不能只恢复正文。
- 沿用 Fleet 的 `next` / `auto` / steer 路径与正在运行时的交付选择；不抢占 writer，不另起 Codex runtime，不因注释而改变投递模式。
- 排队消息仍可按现有能力编辑 / 取消。编辑 queued 时创建同 item 的新 stateVersion，并重建该次冻结 wire；进入 sending 后禁止更改。重试使用已保存 wire 和原 clientMessageId；结果不确定走现有恢复逻辑，不再次注入重复包络。
- 已投递的用户消息下方保留“N 条注释”附件，可回看选文 / 评论 / 来源。内容不可原位修改；想再次讨论需复制为新注释。队列送达后不能仅靠易清理的 queue item 来找回注释：历史中也要恢复。

### A04 · 回答中的引用

- 语法是**单冒号**：Codex 用 `:codex-annotation{index="1"}`；DSH 接入用 Fleet 自有的 `:fleet-annotation{index="1"}`。后者不带模型名称，不冒充 DSH 原生能力。两者在句内渲染成正常字号的蓝色 `注释 1`，不加卡片背景、书名号、脚注上标或泛用 code-comment 标签。按所属输入的协议版本注册对应 renderer，不混用命名空间。
- 桌面：hover / focus 预览选文与用户评论；有可靠来源时点击滚动并短暂高亮原文；无来源时点击切换详情。移动：点击打开详情抽屉，明确“查看原文”按钮执行导航，避免一次轻触直接跳走。
- 编号只解析到**触发这一模型输入的注释快照**，不能查当前 composer 或全局最近附件。消息投递 / turn 关联用 clientMessageId、实际 user item、provider turn item / exchange 显式建立；同一个 running turn 内 steer 可能追加多个用户输入，不能只用一个 turnId 字典覆盖旧数组。需要按输入边界和输出 segment 关联。
- 有多个用户输入合并到同一输出段、无法确定引用作用域时，不凭 index 猜测。显示“注释 1（来源待确认）”，提供该段候选输入的只读列表；保留原始 directive 和待绑定状态，待 app-server 事件或历史恢复明确后再绑定。实现前必须实测 Codex shared steer 与 DSH steer 的边界；若上游确实不暴露区分，带注释 steer 必须标明该限制，不能以 next 默默替换用户选择。
- 支持一条回答重复引用、多行、相邻引用；`1`、`2` 在每个已发送快照内独立成立。模型不输出 directive 时不伪造引用、不自动补造处理结果，不暗中发第二轮。原用户消息附件仍可回看。
- code fence、inline code、转义文本、HTML 属性中的类似字符串保持字面量。增量输出中未完整闭合的 directive 暂以普通文本处理，完成后再转换；不渲染成一闪而过的错误链接。
- AST 行内 token 与 DOMPurify 协同；用安全 DOM 创建引用控件。只接受规范正整数字符串 `String(n) === index && n >= 1`，并验证数组存在。未知索引保留可读文字并标记无法定位，禁止绑定别轮同号；这是比本机原生直接不渲染更清楚的 Fleet 降级。
- “复制回答”为用户输出普通“注释 N”；导出可另附本轮注释清单。原始模型文本及 wire 保留供恢复，复制行为不篡改存储。

## 3. 视觉、主题与响应式

沿用当前 `style.css` 的系统字体、4pt 间距、8 / 12 / 16 / 20 圆角、无常驻装饰描边、青铜绿表面层次和整体 composer。选文高亮使用当前 `--accent` 推导的透明底色；编号标记底色也直接使用当前 `--accent`，数字用 `--accent-contrast` 保证可读；行内引用保留 Codex 的蓝色语义。注释控件不因 hover 变底色、加下划线或缩放；保留键盘 focus-visible 和回引 hover 查看详情的功能。

原型以“Codex”标明研究与评审对象；生产实现沿用现有 ChatGPT / DeepSeek 客户端名称，不把名称调整夹带进注释功能。

| 组件 | 桌面 | 移动 / 无障碍 |
| --- | --- | --- |
| 选区操作 | “添加到对话”浮层与内部按钮均为两端半圆，随选区锚定，边缘翻转，至少 12px 边距 | 不盖系统选区拖柄；原生菜单仍可复制，Web 操作提供 44px 命中区 |
| 评论输入 | 294px 宽，单行总高 44px，左右半圆；最多 120px 评论区 | 最多 344px / 视口减 24px，单行总高 48px；输入 16px 防 iOS 聚焦缩放；适配 VisualViewport |
| 原文标记 | 主题 accent 底色 25px，对比字色 11px，三圆角一小圆角 | 透明命中区至少 44px，重叠命中区需错位避让 |
| 聚合附件 | 数量附件胶囊两端半圆，36px 高，数量稳定，不占正文；删除为圆形按钮 | 44px 高，删除独立命中区；多附件换行，数量不省略 |
| 详情 | 370px 宽 / 最大 480px；逐条滚动 | 底部抽屉，最大 65dvh，safe-area；关闭按钮 44px |
| 行内引用 | 继承正文 14–15px，`#1f66c7` | 深色 `#79b5ff`；用实际主题测对比；独立点击不改变正文行距 |
| 选文高亮 | 当前主题 `--accent` 的 22% alpha，正在选择 / 编辑为 32% | 深色分别为 26% / 36%；普通选区、暂存与已保存高亮随主题切换，不能只依赖颜色表达编号 |

键盘支持选择、Tab 到“添加到对话”、Enter 保存、Esc 删除当前编辑注释并恢复焦点；只读详情中的 Esc 仍为关闭。非模态编辑器不锁整个页面；模态移动抽屉需 focus trap、背景 inert、aria-modal、可见关闭。引用是 button，有“注释 N：选中文字”可访问名称。只读详情、代码多行内容可选择复制；容器滚动可由键盘操作。减少动效时停用原文脉冲。

移动端原生选区菜单、软键盘 / VisualViewport、PWA standalone 和真机拖柄不能靠桌面模拟宣布验收通过。

## 4. 数据与边界

以下为设计数据模型；实际队列 API 接入与验证状态见 [IMPLEMENTATION.md](IMPLEMENTATION.md)。

```ts
type AnnotationDraft = {
  id: string;                   // UUID，编辑身份稳定
  text: string;                 // 冻结的原选文
  annotation?: string;          // 用户评论，trim 后空值省略
  source?: {                    // 仅在确认原生映射正确时设置
    messageId: string;
    startOffset: number;
    endOffset: number;
  };
  fleetSource: {
    deviceId: string;
    assistant: "codex" | "dsh";  // 网页 DeepSeek 的 backend assistant ID
    sessionId: string;           // 目标草稿归属；来源会话另由 source locator 约束
    kind: "message" | "file";
    locator: {
      itemId?: string; path?: string;
      version: string;           // 消息可见文本 hash 或文件内容版本
      startOffset: number; endOffset: number;
      exact: string; prefix?: string; suffix?: string;
      lineStart?: number; lineEnd?: number;
    };
  };
};

type AnnotationEnvelope = {
  version: "codex-response-annotations-v1" | "fleet-annotations-v1";
  items: AnnotationDraft[];      // 数组顺序是协议编号的唯一来源
};
```

Codex 序列化给模型时只投影 `{text, annotation?, source?}`；Fleet 自有格式使用 `{text, annotation?}`，位置留在 Fleet 元数据；不发送整个 fleetSource / DOM anchor。`source` 的 offset 是原生对应**渲染文本根**中的 UTF-16 单位，半开区间；不是 Markdown 字节、源文件行列或 Go rune 数。只有真实消息 ID 与原生 DOM 文本映射均可核对时填写。不能用 DOM Node 引用做持久化键。

文件的路径、版本、选文精确定位归 Fleet。只把 `index + 用户已授权的文件位置` 作为额外 source context 放在原生包络**之后**，确保模型能区分多文件同文选区。没有可靠 source 时省略原生 source；native reader 仍可恢复 text / annotation。额外 context 明确声明文档正文是被引用材料；用户评论、输入框请求是用户指令。文档中的命令不能因被选中而升级成用户授权。

重复选文用 offset＋prefix/suffix＋版本区分。同一段可以有多条不同评论；默认按创建顺序分别编号，不擅自合并。原文变化：先用版本和精确范围匹配，再用 exact＋context 唯一匹配；多候选或权限失效则仅展示 snapshot，不猜位置，不越权重新读取文件。

## 5. 序列化与两个 provider

服务端为唯一 wire 组装点。浏览器提交结构化注释和原始正文，服务端校验并冻结包络后交队列；客户端“查看模型上下文”仅为原型评审工具，不进入产品默认界面。

```text
草稿正文 + AnnotationEnvelope
 → 校验权限 / 容量 / 来源，冻结提交快照
 → 依 provider 选择原生 Codex / Fleet 自有协议 + 可选文件来源上下文 + My request
 → 持久化队列（同 clientMessageId / wireVersion / wireText）
 → Codex turn/start 或 turn/steer 的现有 text input
   DeepSeek session/prompt 的现有 text content（queue / steer）
 → SSE / 历史投影恢复对应输入的注释数组
 → 回复 Markdown 行内引用绑定到这个输入的快照
```

- Codex：固定包络与说明采用所检客户端内容；无需新增 JSON-RPC 方法 / `UserInput` 类型。单一 shared writer 约束完全延续。
- DSH 接入：采用 `fleet-annotations-v1`，复用中性的 `# Response annotations:` / `<response-annotations>` 包络和选文、评论 JSON，提示词写 assistant response 与 `fleet-annotation`；不包含 Codex 或 DSH 模型品牌。来源留在 Fleet 元数据或明确的来源上下文。此为 Fleet 自有格式，不依据 DSH 原生 API 定义。后续 DSH 原生支持时新增版本化适配器，不回写既有历史的协议版本。原型没有调用 DeepSeek 实测。
- 无正文时保留空 `## My request:` 区段，不能漏掉包络 / 请求分隔。删除了全部注释时按原普通输入路径处理，不产生空包络。
- 不直接拼接用户文本生成 JSON。通过 JSON serializer 编码；对 `<` / `>` 做 `\u003c` / `\u003e` 值等价编码，防选文里的结束标签干扰外层扫描。索引、引号、换行、中文、emoji 必须 round-trip。
- 后端长度检查可以用 trimmed 副本，发送 wire 不能整体 trim，必须保留首换行。原 native parser 的匹配严格，格式变化需 golden fixture 验证。
- 不把已拼过包络的 `text` 当原始正文再次注入。队列冻结结构应明确 `displayText` 是用户正文，`wireText` 是最终 payload；编辑从结构化原稿重建，重试复用 wire。

## 6. 历史与跨端兼容

“完全兼容”作为实施验收目标，分成可验证的三件事：

1. Fleet → Codex Desktop：收到相同普通用户输入，Desktop 能识别聚合注释附件、选文 / 评论及回复行内引用；可靠 source 才保证原消息导航，文件来源用 popover 降级。
2. Desktop → Fleet：读取已有原生包络，恢复注释到 user item / exchange；渲染模型的原生 directive，即使该消息从未经过 Fleet 队列也应有效。不能仅维护 Fleet 私有 sidecar 才能显示。
3. DSH 接入 → Fleet：验证 Fleet 自有 `fleet-annotation` 的上下文、交互、历史恢复一致。DSH Desktop 的原生注释兼容等上游正式支持后再做；本轮不将自有协议声称为原生。

历史 reader 只在已知用户输入、满足包络结构与 schema 时提取；其它代码 / 引文里的字符串不当控制元数据。缺少结构化 sidecar 时从 wire 重建 text / comment / native source，有 sidecar 则补定位信息。旧消息无包络照常呈现。

开始实现前冻结该版本黄金 fixture；上线前在独立测试会话实测两端互发、刷新、shared live steer、历史分页 / compact 后恢复和真实 DeepSeek 回引。结果必须附事件关联证据及截图。本轮调研与原型不等于这些 UAT 已通过。

## 7. 开发落点

| 位置 | 责任 |
| --- | --- |
| `server/dashboard/annotation_model.js`（建议新增） | 草稿身份、顺序、作用域、fixture reader 与快照模型；不混入 provider 操作 |
| `server/dashboard/annotation_ui.js`（建议新增） | Range 采集、选择浮层、editor、聚合附件、详情、焦点与标记 |
| `server/dashboard/app.js` | 接入现有 chat store / submit / restore / queue edit，annotations-only 空态与状态切换 |
| `server/dashboard/chat_model.js` | 原生包络输入投影、user item 与输出 segment 注释作用域、分页历史恢复 |
| `server/dashboard/markdown.js` | 单冒号行内 AST token、安全渲染 / 复制，不修改块级 directive 语义 |
| `server/dashboard/preview.js` / `workspace_tabs.js` | 同源预览选择与定位，精确 frame capability 和消息桥 |
| `server/dashboard/compact_composer.js` | 将注释 popover 归入 composer ownership，文件 Tab 延续草稿 |
| `server/dashboard/index.html` / `style.css` / `sw.js` | 容器、主题 token、响应式、静态资源版本一致 |
| `mac/fleet-agent/chat.go` / `chat_queue.go` | 注释字段、验证、持久化、冻结 wire、错误恢复、兼容读写 |
| `mac/fleet-agent/codex_chat.go` / `dsh_chat.go` 及历史投影 | 复用现有 text input，provider 边界映射；不新增模型调用或 writer |

iframe 桥只接受当前已注册预览 frame 的 `event.source`、同源 `event.origin`、带 session / device / tab 的 nonce 与版本；父层决定目标会话，不相信文档发来的任意 sessionId。外部 HTML 可显示内容但不授予操作宿主草稿的权限。selection 事件只能记录选文，不能自动发送消息。

## 8. 验收清单

| 编号 | 操作 / 情况 | 必须成立 |
| --- | --- | --- |
| A01 | 对话拖选、文件跨段选文、代码选文、移动长按 | 选文准确；菜单不丢 selection；注释与源可回看 |
| A02 | 空 / 多行评论、IME、Enter / Shift+Enter / Esc、外部关闭 | Enter 保存、Esc 删除、外部仅关闭，空评论省略，候选不误提交或删除 |
| A03 | 2 条以上、编辑、删除首项、移除全部 | 草稿 ID 稳定，未发送编号重排，正文 / 其它附件保留 |
| A04 | 只有注释，正文为空；浮层没有底部工具行 | 保存后可从 composer 发送，不伪造用户意图 |
| A05 | 文件 Tab / 对话切换、会话 / 设备 / provider 切换、刷新 | 同会话延续；不同作用域隔离；已保存草稿可恢复 |
| A06 | 排队 / steer / 重试 / 离线 / 只读 / 权限恢复 | 注释与正文同快照，投递模式不变，不重复发送、不丢评论 |
| A07 | Codex / Fleet 各自单冒号引用、重复、相邻、代码中字符串、错误 index | 正确协议，代码不替换，未知不串号，无 XSS |
| A08 | 两轮均有注释 1，turn 内多次 steer，历史分页和刷新 | 明确输入边界，老引用不随当前草稿变化；歧义显式处理 |
| A09 | Desktop 创建注释后 Fleet 读；反向互发 | 选文、评论、聚合附件、原生 directive 可恢复；必要降级有记录 |
| A10 | 关闭文件 / 原文更新 / 同文重复 / 权限失效 | snapshot 仍在；唯一匹配才导航；不越权、不猜定位 |
| A11 | 明 / 暗 × 桌面 / 手机；长文本；hover | 左右半圆、单行 44 / 48px；hover 无视觉变化；无横向溢出，焦点可见，移动操作目标满足命中要求 |
| A12 | iOS / Android 原生选区、软键盘、PWA standalone | 有真机截图 / 录屏；系统复制不受损；键盘不挡保存与发送 |
| A13 | 协议 golden fixtures + 真实 DeepSeek | 首换行 / 标签 / 可选字段 / UTF-16 / emoji / malicious delimiters round-trip；回引成功率有真实样本 |

实施时运行项目要求的 `bash scripts/verify.sh`，并补最有价值的协议 / 隔离 / 恢复回归。设计阶段只验证原型交互与布局，不冒称项目生产测试、跨端 UAT 或部署完成。
