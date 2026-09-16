package main

// assistantCapabilities 描述一个 assistant 在自绘聊天链路上支持哪些能力。
//
// 自绘链路（/api/chat/*）原本靠散落的 `assistant != "codex"` 守卫做分流，
// 每加一个 assistant 都要改十几处、极易漏。改由本类型集中判定后，
// 新增/收回一个 assistant 的能力只需要动 chatCapabilities 一处。
//
// 只保留两个维度，刻意不拆得更细：
//
//   - handler 层（chat.go）所有端点问的都是同一个问题——"这个 assistant 有没有
//     自绘聊天面"。审批与提问往返属于这个面的一部分，不是独立能力，因此不单列。
//   - 服务端队列与访问态（chat_queue.go）是真正可能缺席的一维：一个只读渲染
//     磁盘会话、不参与排队与 writer 租约的 assistant，就是 SelfDraw 有而 Queue 无。
//
// 等某个 assistant 真的出现第三种组合时再拆，不预先发明维度。
type assistantCapabilities struct {
	// SelfDraw 支持自绘聊天面：会话列表、历史分页、发送、流式渲染、审批往返。
	SelfDraw bool
	// Queue 支持服务端持久队列、steer 与访问态（read_only）控制。
	Queue bool
}

// chatCapabilities 是"哪个 assistant 支持什么"的唯一真源。
//
// 判定走 normAssistant，因此大小写与空白与其它入口一致；未知 assistant
// 归一为 claude，而 claude 明确不自绘（Claude 侧仍走 tmux/ttyd 终端）。
func chatCapabilities(assistant string) assistantCapabilities {
	switch normAssistant(assistant) {
	case "codex":
		return assistantCapabilities{SelfDraw: true, Queue: true}
	default:
		return assistantCapabilities{}
	}
}
