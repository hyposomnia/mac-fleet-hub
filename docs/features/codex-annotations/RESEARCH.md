# Codex 注释调研

调研日期：2026-10-16。范围：选中文字 → 添加到对话 → 评论 → composer 附件 → 模型上下文 → 行内回引。本轮没有向模型发送实验消息，没有操作生产服务。

## 结论与证据强度

**聊天注释是客户端组织的上下文协议，并非 app-server 的新输入类型。** 本机客户端把 `responseTextAnnotations` 序列化进普通用户文本；模型输出 `:codex-annotation{index="N"}` 后，由客户端结合本轮附件数组渲染引用。

已确认的是本机版本的静态实现与用户截图；尚未完成 Fleet ↔ Desktop 的真实互发验收，也没有验证 DeepSeek 模型遵从回引指令的成功率。不能把本报告称作已经实现或已经完全互通。

产品命名：Codex 使用原生 `codex-annotation`；DSH 接入使用 Fleet 自有、模型无关的 `fleet-annotation`。以下取证仍描述 Codex 实际机制，不据此推断 DSH 的原生协议。待 DSH 原生支持后再设计兼容层。

| 证据 | 发现 | 能证明什么 |
| --- | --- | --- |
| 用户截图 1 / 2 | 对话、文档选区均有“添加到对话” | 产品交互目标；截图里的业务文案仅作为内容参考 |
| 用户截图 3 | 选文蓝色高亮、蓝色编号、可选评论浮层 | 创建态的视觉与低干扰输入方式 |
| 本机 ChatGPT.app 26.1007.71326，打包 JS | 序列化、恢复解析、schema、引用组件、草稿附件组件 | 版本限定的实际客户端行为 |
| [Annotations Extensibility](https://learn.chatgpt.com/docs/annotations-extensibility) | DOM Range 可作为选择目标；评论由用户保存、发送 | 浏览器注释 API 的公开边界；不能替代聊天注释 wire 证据 |
| [Codex App Server](https://learn.chatgpt.com/docs/app-server) | 富客户端经 app-server 驱动会话；可生成版本对应 schema | Fleet 现有接入层可继续使用；没有公开承诺这里的私有注释 UI 协议 |
| 当前仓库 main 的 dashboard / agent | 输入、排队和渲染没有完整注释支持 | 必须同时设计持久化、历史恢复和 renderer，单做浮层不足以实现目标 |

## 本机客户端如何工作

1. 选区从 DOM `Range` 提取文本。聊天来源保存消息标识与 UTF-16 offset；本机 UI 另保存 DOM anchor。不是把 CSS selector 直接发给模型。
2. “添加到对话”打开可选评论编辑器。文本选区的创建态初始高度 44px，宽 294px；可保存空评论，支持保存到 composer 与单条直接发送的分支。原生有语音入口，本设计沿用 Fleet 已有输入能力，不新增录音产品能力。
3. 原文蓝色高亮使用 CSS Highlight；编号是 25×25px 蓝色标记，形状为三角圆角变体：`50% 50% 50% 3px`。移动触控应扩展透明命中区。
4. composer 用一个聚合的 annotation 附件，显示数量；详情按顺序列出选中文字、可选评论，并可编辑、删除和导航。不是把所有引文直接塞进 textarea。
5. 发给模型的项仅投影 `text`、可选 `annotation`、可选 `source`。`id`、DOM anchor 和 UI 编辑状态不进入这段 JSON。
6. 用户消息恢复解析器从普通输入文本中识别该区块，并校验项的 schema。
7. 回答的行内指令由本轮 annotation 数组解析。呈现为蓝色“注释 N”；悬停 / 聚焦展示选文和评论，来源可达时点击导航。多行选文以有滚动的等宽区域展示。
8. 引用的 `index` 必须是十进制规范正整数字符串，且存在相应项。`01`、`0`、越界项不可绑定。代码块和行内代码中的指令不转换；复制文本时将有效指令转为普通“注释 N”。

## 精确的聊天注释 wire 轮廓

下例第一字符是换行。`⏎` 只是说明，不在消息中发送。

```text
⏎# Response annotations:
[客户端固定的注释说明：按数组顺序从 1 编号；理解所有选文、回应所有评论；用行内 codex-annotation 回引]
<response-annotations>
[{"text":"选中文字","annotation":"用户评论","source":{"messageId":"真实消息 ID","startOffset":0,"endOffset":4}}]
</response-annotations>

## My request:
用户输入框正文
```

固定说明的逐字值已记录在设计原型 `prototype.js` 的 `INSTRUCTION`，仅用于准确展示该互操作格式；生产实现应有版本化 golden fixture。

客户端接受的数据结构：

```ts
type NativeResponseAnnotation = {
  text: string;                         // 非空，保持原文
  annotation?: string;                  // 非空；没有评论时省略，不能传 ""
  source?: {
    messageId: string;                  // 非空，必须是目标客户端能识别的消息 ID
    startOffset: number;               // 非负整数，UTF-16 code units
    endOffset: number;                 // 非负整数，end > start，右开区间
  };
};
```

**兼容性敏感点：** 当前原生读取器要求文本以 `\n# Response annotations:\n` 开头，并寻找独立换行包围的 `<response-annotations>` / `</response-annotations>`；一般恢复路径还检查存在 `## My request:`。不能随意 `.trim()` 整条 wire、在区块前加其他说明，或把 JSON 放到 Markdown code fence。若前面已有 Worktree 等其它前缀，要专门实测该组合；本设计的 Fleet sender 保证注释区块排在最前面。

注释不是 `text_elements` 里的新类型，也不是 `::code-comment`。后二者不能替代这套协议。

## 文档来源的边界

原生还有浏览器 / PDF / 文档的 artifact 注释路径，使用 `commentAttachments` 和文档元数据，不能与聊天的 `responseTextAnnotations` 无条件等同。公开 Browser Annotation API 也要求特定宿主，不是普通手机浏览器可依赖的通用 API。

本轮统一的是 Fleet 的“文本选区注释”交互和回引格式。文件选文可使用合法的、**不含 source** 的 response annotation，Desktop 可以恢复选文和评论并显示引用详情。文件 path / 版本 / 定位信息保存在 Fleet 的独立元数据中，模型需要的文件来源放在 annotation 区块之后的额外上下文段。该额外段为 Fleet 扩展，需要互通 UAT；不向原生 `source` 塞入 `{kind:"file",path:...}`，也不伪造 `messageId`。

因此：原生聊天注释格式可按所检版本兼容；Fleet 文档原文跳转由 Fleet 完成。Desktop 跳回 Fleet 文档尚无已验证的原生映射，不能承诺。若要求原生 artifact 批注、PDF 区域、DOCX 评论线程逐字段互通，应分别补取证和契约；不在本轮文本注释方案中冒充支持。

## 取证定位

只读读取 `/Applications/ChatGPT.app/Contents/Resources/app.asar`，提取文件保存在临时目录 `/tmp/fleet-annotation-research/`；没有修改 App 或共享 runtime。以下 offset 是**解码后的 JS 字符索引**，不是 asar byte offset。打包产物可能单行，使用文件名＋符号 / 字符串＋offset 定位更准确。

| 打包文件（webview/assets/） | 定位与关键发现 |
| --- | --- |
| `app-shared-8340f229c2b3.js` | 306972 附近 `Mae`：包络读取；2991105 附近：序列化投影；3006021 附近：固定说明；4976915 附近 `YCr`：JSON 解析；4978597 附近 `rwr`：schema |
| `app-initial-1ab6fbe93c4f.js` | 3755766：独立草稿存储；9482600 附近 `Sec`：DOM anchor 与 message source offset |
| `app-primary-2a670e99a60c.js` | 590401 `xB` / 591700 附近 `D0e`：聚合附件；558040 `N1e`：导航；923690 `F9e`：标记；998629 `Mnt`：编辑器与标记 |
| `annotation-editor-f704171cc50f.js` | 可选评论、空评论保存、44px 创建态、保存 / 直接发送回调 |
| `sites-end-resource-95f165ef7b06.js` | 92391 `Wb`：引用组件；95654 `Gb`：复制时替换、排除代码；这是共享引用实现的可定位实例，仍需实际 Codex renderer UAT |

提取文件 SHA-256：

```text
3b4cabc382e71f0da7398393a619d7c62d13dfae598a5fe2e9880ab8d31d1bf5  app-shared-8340f229c2b3.js
f3d5fedc993d0ead2720c9877d8c72a82dc5fb2d935f13ad9426e6964fb6f105  app-initial-1ab6fbe93c4f.js
c002f1b37cb1a48dfab17b89aae2d3f20521b443d65ed64b6c5c1629c6ca528f  app-primary-2a670e99a60c.js
a918e7649c137b48cb3b4bb9b9ac74b6d9197eb6d111d552d98d0417955eb97b  sites-end-resource-95f165ef7b06.js
985d0b33d39a484407c43f5cf6fa727d8abe6a3d612b6ade08bea4174fd188c5  annotation-editor-f704171cc50f.js
```

未复制整份客户端代码进仓库；未来 App 升级需重验包络、schema、引用组件和真实互发。

## 调研时的 Fleet 落点（main 基线）

| 文件 | 当前情况 | 设计影响 |
| --- | --- | --- |
| `server/dashboard/app.js` | `saveChatDraft` 只保存 textarea 文本；`submitChatInput` → `chat/queue` 传 text / displayText / images / skills | 注释必须进入会话草稿和队列完整快照，附件本身也能触发发送 |
| `server/dashboard/markdown.js` | 当前仅识别部分双冒号块级 directive | 要新增单冒号的 Markdown 行内 token，不是给已有块级白名单加名字 |
| `server/dashboard/chat_model.js` | 统一会话投影 | 需要在用户消息及回答所属 exchange 保存注释作用域并从 wire 恢复 |
| `server/dashboard/preview.js` / `workspace_tabs.js` | Markdown / 文本可预览；工作区文件在 iframe | 跨 frame 捕获选区和草稿归属必须显式桥接；跨源 / 不可读预览不能假装能抓 DOM |
| `server/dashboard/compact_composer.js` | 文件 Tab 复用输入框、失焦可收起 | 注释浮层归属同一 composer，不能因为点击浮层而误收起 |
| `mac/fleet-agent/chat_queue.go` | 已有持久化队列、clientMessageId 去重、next / steer 路径 | 复用现有投递语义；保存冻结的注释与 wire，不新增 writer |
| `mac/fleet-agent/codex_chat.go` | `codexUserInput` 发普通 text | 在现有 input 内携带包络，不发明 app-server annotation 类型 |
| `mac/fleet-agent/dsh_chat.go` | `session/prompt` 的 content 中含 text | 发送相同包络；没有 DeepSeek 原生 annotation API 的证据 |
| `server/dashboard/sw.js` | 静态 shell 版本控制，用户文件 / API 不缓存 | 新资源要一致更新版本；注释私有数据不进 Service Worker cache |
