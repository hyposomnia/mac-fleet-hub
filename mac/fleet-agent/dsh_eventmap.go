package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件把 DSH 的会话事件投影成浏览器侧的 ChatEvent。
//
// 所有 data 形状都取自真实日志与真实 host 返回，不是猜的：
//
//	assistant/chunk  data = {turn, step, chunk:{type, index, blockType|text|id|name|argumentsDelta|usage|reason|block}}
//	  chunk.type ∈ block-start | block-end | text-delta | reasoning-delta |
//	               tool-call-delta | usage | finish
//	assistant/message data = {turn, step, message:{role, content:[{type:'text',text}|{type:'tool-call',id,name,arguments}]}}
//	tool/call         data = {turn, step, callId, name, arguments}
//	tool/result       data = {turn, step, message:{source:{kind:'tool',callId}, content:[{type:'tool-result',toolCallId,content:[{type:'text',text}]}]}}
//	turn/start        data = {turn}
//	turn/end          data = {turn, reason:{kind:'completed'|…}}
//	agent/inbox/spliced data = {target, start, inserted:[{content:[{type:'text',text}], source:{kind:'user',rpcId}, role, id}]}
//
// 刻意不映射的事件都写在 dshMapSessionEvent 的 default 注释里，不静默吞掉。

// dshHistoryRecord 是 session/follow 的一条 records 元素。
type dshHistoryRecord struct {
	Type  string          `json:"type"` // "event" | "chunks"
	Event json.RawMessage `json:"event"`
}

// dshSessionEvent 是会话日志里的单个事件。
type dshSessionEvent struct {
	Type string          `json:"type"`
	Seq  int64           `json:"seq"`
	Time int64           `json:"time"`
	Data json.RawMessage `json:"data"`
}

// dshPackedChunkRow 是打包行。线上内层 type 带 chunkrow/ 前缀。
type dshPackedChunkRow struct {
	Type string `json:"type"`
	Seq  int64  `json:"seq"`
	Time int64  `json:"time"`
	Data struct {
		Turn  int64    `json:"turn"`
		Step  int64    `json:"step"`
		Index int64    `json:"index"`
		ID    string   `json:"id"`
		Name  string   `json:"name"`
		DT    []int64  `json:"dt"`
		Texts []string `json:"texts"`
		Args  []string `json:"args"`
	} `json:"data"`
}

// dshChunk 是 assistant/chunk 的 chunk 载荷（一次只填其中一组字段）。
type dshChunk struct {
	Type           string `json:"type"`
	Index          int64  `json:"index"`
	BlockType      string `json:"blockType"`
	Text           string `json:"text"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	ArgumentsDelta string `json:"argumentsDelta"`
	Usage          *struct {
		InputTokens     int64 `json:"inputTokens"`
		OutputTokens    int64 `json:"outputTokens"`
		TotalTokens     int64 `json:"totalTokens"`
		CacheReadTokens int64 `json:"cacheReadTokens"`
		ReasoningTokens int64 `json:"reasoningTokens"`
	} `json:"usage"`
}

// dshTurnID 把 DSH 的回合序号变成 ChatEvent 的 turnId。
func dshTurnID(turn int64) string {
	return fmt.Sprintf("turn-%d", turn)
}

// dshBlockItemID 是流式块的稳定身份。
//
// 必须对同一块的每个增量都返回同一个 id：dashboard 的 assistant_delta 是按 itemId
// 累加正文的，id 一变就会把每个 token 渲染成独立气泡。
// index 是 message content 数组的下标，因此 assistant/message 收口时能用同一个 id 覆盖。
func dshBlockItemID(turn, step, index int64) string {
	return fmt.Sprintf("turn-%d-step-%d-block-%d", turn, step, index)
}

// dshMapRecord 把一条 history 记录投影成 0..n 个 ChatEvent。
func dshMapRecord(sessionID string, rec dshHistoryRecord) []ChatEvent {
	switch rec.Type {
	case "event":
		var ev dshSessionEvent
		if json.Unmarshal(rec.Event, &ev) != nil {
			return nil
		}
		return dshMapSessionEvent(sessionID, ev)
	case "chunks":
		var row dshPackedChunkRow
		if json.Unmarshal(rec.Event, &row) != nil {
			return nil
		}
		return dshExpandChunkRow(sessionID, row)
	default:
		return nil
	}
}

// dshExpandChunkRow 把打包行还原成逐成员的增量事件。
//
// 只处理 text/reasoning 两类：tool-call-chunks 是工具**入参**的增量，渲染成工具输出
// 是误导性的，而收口的 tool/call 事件本来就带完整参数。
func dshExpandChunkRow(sessionID string, row dshPackedChunkRow) []ChatEvent {
	kind := strings.TrimPrefix(row.Type, "chunkrow/")
	payload := row.Data.Texts
	if len(payload) == 0 {
		payload = row.Data.Args
	}
	if len(payload) == 0 {
		return nil
	}
	// 不变式取自 dsh-session 的 chunk-rows 编解码器：dt[i] = time[i+1] - time[i]，
	// 因此 dt 长度必须恰好是成员数 − 1。不成立说明这行畸形或是我们不认识的形态，
	// 宁可整行不渲染，也不要静默错位。
	if len(row.Data.DT) != len(payload)-1 {
		return nil
	}
	if kind != "text-chunks" && kind != "reasoning-chunks" {
		return nil
	}

	turnID := dshTurnID(row.Data.Turn)
	itemID := dshBlockItemID(row.Data.Turn, row.Data.Step, row.Data.Index)
	eventType := "assistant_delta"
	if kind == "reasoning-chunks" {
		eventType = "reasoning_delta"
	}

	events := make([]ChatEvent, 0, len(payload))
	for _, text := range payload {
		if text == "" {
			continue
		}
		events = append(events, newChatEvent(eventType, "dsh", sessionID, turnID, itemID,
			map[string]any{"delta": text}))
	}
	return events
}

// dshMapSessionEvent 把单个会话事件投影成 0..n 个 ChatEvent。
func dshMapSessionEvent(sessionID string, ev dshSessionEvent) []ChatEvent {
	switch ev.Type {
	case "turn/start":
		var data struct {
			Turn int64 `json:"turn"`
		}
		_ = json.Unmarshal(ev.Data, &data)
		return []ChatEvent{newChatEvent("turn_started", "dsh", sessionID, dshTurnID(data.Turn), "",
			map[string]any{"turnId": dshTurnID(data.Turn)})}

	case "turn/end":
		var data struct {
			Turn   int64 `json:"turn"`
			Reason struct {
				Kind string `json:"kind"`
			} `json:"reason"`
		}
		_ = json.Unmarshal(ev.Data, &data)
		return []ChatEvent{newChatEvent("turn_done", "dsh", sessionID, dshTurnID(data.Turn), "",
			map[string]any{"turnId": dshTurnID(data.Turn), "status": data.Reason.Kind})}

	case "assistant/chunk":
		var data struct {
			Turn  int64    `json:"turn"`
			Step  int64    `json:"step"`
			Chunk dshChunk `json:"chunk"`
		}
		if json.Unmarshal(ev.Data, &data) != nil {
			return nil
		}
		turnID := dshTurnID(data.Turn)
		itemID := dshBlockItemID(data.Turn, data.Step, data.Chunk.Index)
		switch data.Chunk.Type {
		case "text-delta":
			if data.Chunk.Text == "" {
				return nil
			}
			return []ChatEvent{newChatEvent("assistant_delta", "dsh", sessionID, turnID, itemID,
				map[string]any{"delta": data.Chunk.Text})}
		case "reasoning-delta":
			if data.Chunk.Text == "" {
				return nil
			}
			return []ChatEvent{newChatEvent("reasoning_delta", "dsh", sessionID, turnID, itemID,
				map[string]any{"delta": data.Chunk.Text})}
		case "usage":
			if data.Chunk.Usage == nil {
				return nil
			}
			u := data.Chunk.Usage
			return []ChatEvent{newChatEvent("turn_usage", "dsh", sessionID, turnID, "", map[string]any{
				"turnId":             turnID,
				"inputTokens":        u.InputTokens,
				"outputTokens":       u.OutputTokens,
				"totalTokens":        u.TotalTokens,
				"cachedInputTokens":  u.CacheReadTokens,
				"reasoningTokens":    u.ReasoningTokens,
			})}
		default:
			// block-start / block-end / tool-call-delta / finish 都不单独成项：
			// 前两者是块的边界，tool-call-delta 是入参增量（收口由 tool/call 负责），
			// finish 只是模型侧的收尾信号。
			return nil
		}

	case "assistant/message":
		var data struct {
			Turn    int64 `json:"turn"`
			Step    int64 `json:"step"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(ev.Data, &data) != nil {
			return nil
		}
		turnID := dshTurnID(data.Turn)
		var out []ChatEvent
		for i, block := range data.Message.Content {
			if block.Type != "text" || block.Text == "" {
				// tool-call 块由 tool/call 事件负责，这里跳过以免重复渲染。
				continue
			}
			out = append(out, newChatEvent("assistant_done", "dsh", sessionID, turnID,
				dshBlockItemID(data.Turn, data.Step, int64(i)),
				map[string]any{"text": block.Text}))
		}
		return out

	case "tool/call":
		var data struct {
			Turn      int64  `json:"turn"`
			Step      int64  `json:"step"`
			CallID    string `json:"callId"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if json.Unmarshal(ev.Data, &data) != nil || data.CallID == "" {
			return nil
		}
		return []ChatEvent{newChatEvent("tool_update", "dsh", sessionID, dshTurnID(data.Turn), data.CallID,
			map[string]any{
				"kind":    "tool",
				"title":   data.Name,
				"summary": dshFirstLine(data.Arguments),
				"status":  "in_progress",
			})}

	case "tool/result":
		var data struct {
			Turn    int64 `json:"turn"`
			Message struct {
				Source struct {
					CallID string `json:"callId"`
				} `json:"source"`
				Content []struct {
					ToolCallID string `json:"toolCallId"`
					Content    []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(ev.Data, &data) != nil {
			return nil
		}
		callID := data.Message.Source.CallID
		var text strings.Builder
		for _, part := range data.Message.Content {
			if callID == "" {
				callID = part.ToolCallID
			}
			for _, inner := range part.Content {
				text.WriteString(inner.Text)
			}
		}
		if callID == "" {
			return nil
		}
		return []ChatEvent{newChatEvent("tool_update", "dsh", sessionID, dshTurnID(data.Turn), callID,
			map[string]any{"status": "completed", "output": text.String()})}

	case "agent/inbox/spliced":
		var data struct {
			Inserted []struct {
				ID      string `json:"id"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Source struct {
					Kind  string `json:"kind"`
					RPCID string `json:"rpcId"`
				} `json:"source"`
			} `json:"inserted"`
		}
		if json.Unmarshal(ev.Data, &data) != nil {
			return nil
		}
		var out []ChatEvent
		for _, msg := range data.Inserted {
			if msg.Role != "user" {
				continue
			}
			var text strings.Builder
			for _, part := range msg.Content {
				if part.Type == "text" {
					text.WriteString(part.Text)
				}
			}
			// clientId 用 host 回显的 rpcId：它是我们发 prompt 时带的 requestId，
			// 浏览器据此把乐观气泡替换成权威正文（等价于 Codex 的 clientUserMessageId）。
			out = append(out, newChatEvent("user_done", "dsh", sessionID, "", msg.ID, map[string]any{
				"clientId": msg.Source.RPCID,
				"text":     text.String(),
			}))
		}
		return out

	default:
		// v1 不映射：session/title（列表刷新时自然可见）、sandbox/mode 与
		// approval/policy（权限面板二期）、model/selection、step/start 与 step/end、
		// todo/write、compaction/*、command/*、goal/change、subagent/descriptor、
		// request/header 与 request/context、session/end-seed、tool/code-dispatch*。
		// 这些事件到达时会被丢弃，但不影响正文与工具渲染。
		return nil
	}
}

func dshFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
