# Codex / Fleet 文本注释

包含已确认设计、隔离原型和 SaaS 实现交接。生产实现、验收矩阵与真实 UAT 边界见 [IMPLEMENTATION.md](IMPLEMENTATION.md)。

- [可点击原型](index.html)：三个评审场景——选中与填写、输入框附件、回复引用。可切 Codex / DeepSeek、对话 / 文档、明 / 暗；“更多”中有空态、离线、只读、长文本和模型上下文示例。
- [调研与取证](RESEARCH.md)：本机 Codex 如何序列化、恢复、编号、渲染；证据与未验证边界。
- [设计与开发规范](SPEC.md)：状态、作用域、协议、双 provider 接入、持久化、iframe、移动与验收。
- [原型验收](VALIDATION.md)：本地浏览器验证与截图，真实互通待实施后验收。

预览（仅 loopback）：

```sh
python3 -m http.server 8873 --bind 127.0.0.1 --directory docs/features/codex-annotations
```

打开 `http://127.0.0.1:8873/`。所有发送均为脚本演示，不调用任何模型、设备或生产接口；页面顶部是评审工具，不属于正式产品 UI。可用 file:// 直接打开，推荐 HTTP 预览以复现验收环境。

建议评审顺序：先看第一场景的可选评论；保存后点击输入框内的数量附件并编辑 / 删除；切文档选一段文字添加；发送后悬停或点击“注释 N”查看内容与原文。第三场景提供现成的回复样例。

评论浮层仅保留输入与保存勾选，两端半圆、单行紧凑布局，没有底部工具行和 hover 视觉变化。Enter 保存，Esc 删除当前注释（含正在编辑的已保存草稿）；Shift+Enter 换行。

“添加到对话”菜单与输入框数量附件也使用半圆胶囊。单行评论的文字和光标上下居中，1、2 等编号标记底色及选文高亮跟随当前主题色；点击空白清除临时选区与菜单，已保存的注释和附件保留。

Codex 回引为 `:codex-annotation{index="1"}`；DSH 接入使用模型无关的 Fleet 自有格式 `:fleet-annotation{index="1"}`。待 DSH 原生提供注释协议后再适配，不提前声称原生兼容。

`prototype.js` 的序列化仅演示已取证格式；正式 serializer 在 agent 队列入口实现。该原型没有持久化、真实历史恢复、iframe 桥、原生移动选择与多输入 steer，详见 SPEC 的实现与 UAT 要求。
