# SaaS 注释实现

目标分支：`codex/multi-user-server`。接受的交互以 SPEC 为准；本页记录已实现的契约和实际验证边界。

## 交付契约

- 服务端 `ChatQueueItem.annotations` 接收版本化选文数组，保留原始 `text` / `displayText`，由 queue owner 校验并生成 `wireText`。浏览器传入的 wire 被忽略。注释独立冻结，Codex / DeepSeek 继续使用各自现有的普通文本 start / steer 路径。
- Codex 使用 `codex-response-annotations-v1`、原生说明和 `:codex-annotation{index="N"}`；DeepSeek 使用无模型品牌的 `fleet-annotations-v1` 和 `:fleet-annotation{index="N"}`。首换行、空请求段、可选评论、JSON 角括号转义由 native golden fixture 约束。
- 控制快照增加 `annotationVersion`。旧 agent 仍可读取历史；发送带注释消息时明确提示更新客户端，避免旧队列忽略字段后静默丢失选文。
- 注释草稿以账号、设备、助手、会话为键保存至 IndexedDB；同步 localStorage journal 覆盖浏览器立即刷新的事务窗口。正文及已上传附件引用一同恢复。退出账号立即隐藏私有界面、清除 journal，等待该账号的 IndexedDB 清理后跳转；账号页等退出入口也加载同一存储模块。
- 聊天缓存加入助手身份，允许不同 provider 具有相同 session ID。退出账号清空内存缓存。文件 Tab 继续复用当前会话 composer。
- 排队编辑沿用“取消原投递→回到 composer”，保存时以原 item ID / clientMessageId 和取消后的 stateVersion 创建新版本。只有明确未投递的状态可改；sending / recovering / uncertain / sent 等输入不可改绑。相同修订丢失应答时，重试返回原快照。普通注释提交丢失应答且草稿未改时，也复用 clientMessageId。
- SSE、resume 和分页历史从对应 queue item 补本地定位；没有 Fleet 记录的原生输入直接从 native envelope 恢复。引用按 provider output item 保存输入边界；同一个输出 item 在 steer 后继续增长时，保留所有候选输入并显示“来源待确认”。重复历史事件不把已结束的旧输出改绑到后来输入。
- Markdown 在行内 token 阶段识别引用，DOMPurify 清理后创建安全按钮；代码、转义冒号、HTML 属性和未闭合 token 保持字面量。未知索引不借用其它消息的同号注释。

## 来源与预览

聊天、Markdown 和 CodeMirror 文本预览支持选文。位置使用 UTF-16、内容指纹、精确选文和相邻上下文；源变化后只有唯一匹配才导航。关闭文件仍可回看快照，再打开时重新验证。

当前 Fleet 选文生成 Fleet 本地定位，不假定其 DOM 与 Desktop 渲染根完全相同，因此不会伪造 native `source`。Desktop 输入已有的 native `source` 可读取；实际 Fleet→Desktop 原消息导航仍须真实跨端 UAT。文件来源作为包络后的有界 context 传入，不把文件字段塞进 native source。

受信任 `/view` wrapper 由当前工作区直接注册，持有实际 frame、会话及设备身份；只采集该 wrapper 内的 Markdown / 文本。已发送附件可通过“再次注释”复制为独立的新草稿。未增加文档→宿主的 postMessage 写入口，也不在 `sandbox=allow-scripts` 的 HTML 原型内注入宿主注释能力。

## 验证矩阵

| 需求 | 自动化 / 实际证据 | 实现 owner |
| --- | --- | --- |
| A01–A03 | Chrome 聊天/Markdown 选文、Enter 保存、Esc 删除、空白取消、编辑与数量附件；截图见 implementation-evidence | annotation_ui.js、annotations.css、preview.js |
| A04、A06 | annotation_submit.test.mjs；annotations_test.go 的持久化、重试、修订 CAS 与投递 adapter 测试；Chrome 纯注释发送 | app.js、chat_queue.go、annotations.go |
| A05 | annotation_model.test.mjs 的立即刷新 journal / 账号隔离；auth.test.mjs 的清理等待；chat_model.test.mjs 的相同 session ID provider 隔离；Chrome 刷新和双助手切换 | annotation_model.js、auth-client.js、app.js |
| A07 | annotation_markdown.test.mjs、规范 index 测试；Chrome 重复/相邻引用、代码字面量、未知编号 | markdown.js、annotation_ui.js |
| A08 | annotation_model.test.mjs 的多轮、同 item steer、重复事件、DeepSeek 无 turn ID 输入；已有分页回归 | chat_model.js、annotation_model.js |
| A09 | native golden 文件逐字节序列化及 reader；真实 Desktop 两端互发尚未执行 | annotations.go、codex_chat.go、annotation_model.js |
| A10 | locate 测试；Chrome 文件引用回跳；失配保留快照 | annotation_model.js、annotation_ui.js、preview.js |
| A11 | Chrome 桌面 44px / 手机视口 48px，输入 36px、主题编号、抽屉；截图见 implementation-evidence | annotations.css |
| A12 | 真机长按、系统菜单、软键盘、standalone PWA 尚未执行；视口截图不能替代真机验收 | 待设备 UAT |
| A13 | 原生黄金文件、emoji、结束标签、来源与容量边界；真实 DeepSeek 模型服从引用指令尚未执行 | annotations_test.go、annotation_model.test.mjs |

## 本地浏览器复现

```bash
cd mac/fleet-agent
go test -tags annotation_uat -run '^TestAnnotationBrowserServer$' -timeout 0 -v .
```

仅监听 `127.0.0.1:8873`。使用真实 dashboard、queue、serializer 和 SSE；账号、设备、文件和 provider 是测试边界替身。合成回复验证渲染与快照绑定，不是实际模型回引证据。普通项目验证不编入此手动浏览器 harness。

源码测试、网页部署、Mac Developer ID 签名/公证发布和真实跨端验收分别记录。部署新网页并不表示现有设备已升级 agent。

项目全量验证实际输出摘要：[verify-summary.txt](implementation-evidence/verify-summary.txt)。最终界面：[final-v258.png](implementation-evidence/final-v258.png)。本地 provider 为测试替身；真实跨端及真机验收状态保持上表所列。

登录页依赖的 `annotation_model.js` 由 SaaS 静态路由公开提供；`annotation_ui.js`、聊天脚本与 Service Worker 仍要求登录，见 multiuser/theme_asset_test.go。带 Skill 的输入从实际 text content 解析原生包络，队列 sidecar 另保留 wire 和用户展示正文，避免投影后的 Skill 标签破坏包络起始位置。
