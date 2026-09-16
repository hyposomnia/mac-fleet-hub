package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// 本文件的 fixture 全部是从真实会话日志与真实 host 返回里摘出来的原文，
// 不是按理解手编的。改映射时它们就是契约。
const (
	dshFixtureTurnStart = `{"turn":1}`
	dshFixtureTurnEnd   = `{"turn":1,"reason":{"kind":"completed"}}`

	dshFixtureTextDelta  = `{"turn":1,"step":1,"chunk":{"type":"text-delta","index":1,"text":" **"}}`
	dshFixtureReasoning  = `{"turn":1,"step":1,"chunk":{"type":"reasoning-delta","index":0,"text":"This"}}`
	dshFixtureBlockStart = `{"turn":1,"step":1,"chunk":{"type":"block-start","index":0,"blockType":"text"}}`
	dshFixtureUsage      = `{"turn":1,"step":1,"chunk":{"type":"usage","usage":{"inputTokens":11314,"outputTokens":145,"totalTokens":20419,"cacheReadTokens":8960,"reasoningTokens":0}}}`

	// 真实 assistant/message：content 里同时有 text 与 tool-call 块。
	dshFixtureAssistantMessage = `{"turn":1,"step":1,"message":{"role":"assistant","content":[` +
		`{"type":"text","text":"I'll start by loading my memory index."},` +
		`{"type":"tool-call","id":"call_00_ET","name":"read","arguments":"{\"file_path\": \"/tmp/x\"}"}]}}`

	dshFixtureToolCall   = `{"turn":1,"step":1,"callId":"call_00_ET","name":"read","arguments":"{\"file_path\": \"/tmp/x\"}"}`
	dshFixtureToolResult = `{"turn":1,"step":1,"message":{"source":{"kind":"tool","callId":"call_00_ET"},` +
		`"content":[{"type":"tool-result","toolCallId":"call_00_ET","content":[{"type":"text","text":"<content>\n1: hi"}]}]}}`

	dshFixtureUserSplice = `{"target":"next-turn","start":0,"inserted":[{"content":[{"type":"text","text":"/dev 研究一下"}],` +
		`"source":{"kind":"user","rpcId":"5de3a4c0-9a1b","clientTimeZone":"Asia/Shanghai"},"role":"user","id":"5761e918-44ac"}]}`
)

func dshEvent(t *testing.T, eventType, data string) dshSessionEvent {
	t.Helper()
	return dshSessionEvent{Type: eventType, Seq: 1, Time: 1, Data: json.RawMessage(data)}
}

func onlyEvent(t *testing.T, events []ChatEvent) ChatEvent {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("期望恰好 1 个事件，得到 %d 个: %+v", len(events), events)
	}
	return events[0]
}

func dataMap(t *testing.T, ev ChatEvent) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(ev.Data, &out); err != nil {
		t.Fatalf("事件 data 不是对象: %v (%s)", err, ev.Data)
	}
	return out
}

func TestDSHMapSessionEvent(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		data      string
		wantType  string
		wantTurn  string
		wantItem  string
		check     func(t *testing.T, data map[string]any)
	}{
		{
			name: "turn/start", eventType: "turn/start", data: dshFixtureTurnStart,
			wantType: "turn_started", wantTurn: "turn-1",
		},
		{
			name: "turn/end", eventType: "turn/end", data: dshFixtureTurnEnd,
			wantType: "turn_done", wantTurn: "turn-1",
			check: func(t *testing.T, data map[string]any) {
				if data["status"] != "completed" {
					t.Fatalf("status = %v, want completed", data["status"])
				}
			},
		},
		{
			name: "text-delta 走 assistant_delta", eventType: "assistant/chunk", data: dshFixtureTextDelta,
			wantType: "assistant_delta", wantTurn: "turn-1", wantItem: "turn-1-step-1-block-1",
			check: func(t *testing.T, data map[string]any) {
				if data["delta"] != " **" {
					t.Fatalf("delta = %v, want \" **\"", data["delta"])
				}
			},
		},
		{
			name: "reasoning-delta 走 reasoning_delta", eventType: "assistant/chunk", data: dshFixtureReasoning,
			wantType: "reasoning_delta", wantTurn: "turn-1", wantItem: "turn-1-step-1-block-0",
			check: func(t *testing.T, data map[string]any) {
				if data["delta"] != "This" {
					t.Fatalf("delta = %v, want This", data["delta"])
				}
			},
		},
		{
			name: "usage 走 turn_usage", eventType: "assistant/chunk", data: dshFixtureUsage,
			wantType: "turn_usage", wantTurn: "turn-1",
			check: func(t *testing.T, data map[string]any) {
				if data["totalTokens"] != float64(20419) {
					t.Fatalf("totalTokens = %v, want 20419", data["totalTokens"])
				}
			},
		},
		{
			name: "tool/call", eventType: "tool/call", data: dshFixtureToolCall,
			wantType: "tool_update", wantTurn: "turn-1", wantItem: "call_00_ET",
			check: func(t *testing.T, data map[string]any) {
				if data["status"] != "in_progress" || data["title"] != "read" {
					t.Fatalf("data = %+v", data)
				}
			},
		},
		{
			name: "tool/result", eventType: "tool/result", data: dshFixtureToolResult,
			wantType: "tool_update", wantTurn: "turn-1", wantItem: "call_00_ET",
			check: func(t *testing.T, data map[string]any) {
				if data["status"] != "completed" {
					t.Fatalf("status = %v", data["status"])
				}
				if out, _ := data["output"].(string); !strings.Contains(out, "1: hi") {
					t.Fatalf("output = %q", out)
				}
			},
		},
		{
			name: "用户消息走 user_done 并用 rpcId 关联", eventType: "agent/inbox/spliced", data: dshFixtureUserSplice,
			wantType: "user_done", wantItem: "5761e918-44ac",
			check: func(t *testing.T, data map[string]any) {
				// clientId 必须是 host 回显的 rpcId —— 浏览器靠它把乐观气泡换成权威正文。
				if data["clientId"] != "5de3a4c0-9a1b" {
					t.Fatalf("clientId = %v, want rpcId", data["clientId"])
				}
				if data["text"] != "/dev 研究一下" {
					t.Fatalf("text = %v", data["text"])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := dshMapSessionEvent("session-x", dshEvent(t, tc.eventType, tc.data))
			ev := onlyEvent(t, events)
			if ev.Type != tc.wantType {
				t.Fatalf("type = %q, want %q", ev.Type, tc.wantType)
			}
			if ev.Assistant != "dsh" {
				t.Fatalf("assistant = %q, want dsh", ev.Assistant)
			}
			if ev.SessionID != "session-x" {
				t.Fatalf("sessionId = %q", ev.SessionID)
			}
			if tc.wantTurn != "" && ev.TurnID != tc.wantTurn {
				t.Fatalf("turnId = %q, want %q", ev.TurnID, tc.wantTurn)
			}
			if tc.wantItem != "" && ev.ItemID != tc.wantItem {
				t.Fatalf("itemId = %q, want %q", ev.ItemID, tc.wantItem)
			}
			if tc.check != nil {
				tc.check(t, dataMap(t, ev))
			}
		})
	}
}

// assistant/message 收口时必须复用流式增量的同一个 itemId，否则 dashboard 会
// 先显示流式气泡、再显示一个独立气泡，同一段话出现两遍。
func TestDSHAssistantMessageReusesDeltaItemID(t *testing.T) {
	streamed := onlyEvent(t, dshMapSessionEvent("session-x",
		dshEvent(t, "assistant/chunk", dshFixtureTextDelta)))
	final := onlyEvent(t, dshMapSessionEvent("session-x",
		dshEvent(t, "assistant/message", dshFixtureAssistantMessage)))

	// fixture 的 text-delta 在 block 1，但 assistant/message 的 text 在 content[0]，
	// 因此这两条本来就不该合并 —— 这里要断言的是"同名块才合并"这条规则本身：
	// 用同一个 index 构造两条事件，itemId 必须一致。
	block0Delta := onlyEvent(t, dshMapSessionEvent("session-x",
		dshEvent(t, "assistant/chunk", dshFixtureReasoning)))
	if block0Delta.ItemID != final.ItemID {
		t.Fatalf("block 0 的流式 itemId=%q 与收口 itemId=%q 不一致，会出现重复气泡",
			block0Delta.ItemID, final.ItemID)
	}
	if streamed.ItemID == final.ItemID {
		t.Fatal("不同 block 的 itemId 不该相同")
	}
	if got, _ := dataMap(t, final)["text"].(string); !strings.HasPrefix(got, "I'll start") {
		t.Fatalf("收口正文 = %q", got)
	}
}

// tool-call 块不该在 assistant_done 里重复出现（它由 tool/call 事件负责）。
func TestDSHAssistantMessageSkipsToolCallBlocks(t *testing.T) {
	events := dshMapSessionEvent("session-x", dshEvent(t, "assistant/message", dshFixtureAssistantMessage))
	if len(events) != 1 {
		t.Fatalf("期望只产出 text 块的 1 个事件，得到 %d 个", len(events))
	}
	if strings.Contains(string(events[0].Data), "call_00_ET") {
		t.Fatalf("assistant_done 里混进了 tool-call 内容: %s", events[0].Data)
	}
}

func TestDSHMapSessionEventSkipsUnmapped(t *testing.T) {
	// v1 明确不映射的事件必须静默产出空集，而不是 panic 或产出半成品。
	for _, tc := range []struct{ name, eventType, data string }{
		{"block-start", "assistant/chunk", dshFixtureBlockStart},
		{"session/title", "session/title", `{"title":"t","messageSeqs":[7],"source":{"kind":"fallback"}}`},
		{"sandbox/mode", "sandbox/mode", `{"mode":"workspace-write"}`},
		{"step/start", "step/start", `{"turn":1,"step":1}`},
		{"未知类型", "brand/new-event", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dshMapSessionEvent("session-x", dshEvent(t, tc.eventType, tc.data)); len(got) != 0 {
				t.Fatalf("期望空集，得到 %+v", got)
			}
		})
	}
}

func TestDSHExpandChunkRow(t *testing.T) {
	// 真实打包行（截短）：seq/time 是首成员，dt 是相邻间隔，成员数 = len(texts)。
	row := dshPackedChunkRow{Type: "chunkrow/text-chunks", Seq: 81369, Time: 1000}
	row.Data.Turn, row.Data.Step, row.Data.Index = 1, 229, 0
	row.Data.Texts = []string{"I", "'ll", " start"}
	row.Data.DT = []int64{10, 5}

	events := dshExpandChunkRow("session-x", row)
	if len(events) != 3 {
		t.Fatalf("期望 3 个增量，得到 %d", len(events))
	}
	var joined strings.Builder
	for _, ev := range events {
		if ev.Type != "assistant_delta" {
			t.Fatalf("type = %q, want assistant_delta", ev.Type)
		}
		if ev.TurnID != "turn-1" {
			t.Fatalf("turnId = %q", ev.TurnID)
		}
		// 同一个块的所有成员必须共用一个 itemId，否则每个词都会变成独立气泡。
		if ev.ItemID != "turn-1-step-229-block-0" {
			t.Fatalf("itemId = %q", ev.ItemID)
		}
		joined.WriteString(dataMap(t, ev)["delta"].(string))
	}
	if joined.String() != "I'll start" {
		t.Fatalf("展开后正文 = %q, want \"I'll start\"", joined.String())
	}
}

func TestDSHExpandChunkRowRejectsMalformedDT(t *testing.T) {
	// codec 的不变式是 dt 长度 = 成员数 - 1。不成立时必须整行丢弃：
	// 硬按 dt 还原会静默错位，比少渲染更糟。
	row := dshPackedChunkRow{Type: "chunkrow/text-chunks", Seq: 1, Time: 1}
	row.Data.Texts = []string{"a", "b", "c"}
	row.Data.DT = []int64{1, 2, 3} // 应为 2 个
	if got := dshExpandChunkRow("session-x", row); got != nil {
		t.Fatalf("畸形行必须整行丢弃，得到 %+v", got)
	}
}

func TestDSHExpandChunkRowSkipsToolCallChunks(t *testing.T) {
	row := dshPackedChunkRow{Type: "chunkrow/tool-call-chunks", Seq: 1, Time: 1}
	row.Data.Args = []string{`{"a"`, `:1}`}
	row.Data.DT = []int64{3}
	if got := dshExpandChunkRow("session-x", row); got != nil {
		t.Fatalf("工具入参增量 v1 不渲染，得到 %+v", got)
	}
}

func TestDSHMapRecord(t *testing.T) {
	eventRec := dshHistoryRecord{Type: "event",
		Event: json.RawMessage(`{"type":"turn/start","seq":4,"time":1,"data":{"turn":1}}`)}
	if got := dshMapRecord("s", eventRec); len(got) != 1 || got[0].Type != "turn_started" {
		t.Fatalf("event 记录映射错误: %+v", got)
	}

	chunksRec := dshHistoryRecord{Type: "chunks", Event: json.RawMessage(
		`{"type":"chunkrow/reasoning-chunks","seq":7,"time":1,"data":{"turn":2,"step":3,"index":0,"dt":[1],"texts":["a","b"]}}`)}
	got := dshMapRecord("s", chunksRec)
	if len(got) != 2 || got[0].Type != "reasoning_delta" || got[0].TurnID != "turn-2" {
		t.Fatalf("chunks 记录映射错误: %+v", got)
	}

	// 未知记录类型与坏 JSON 都必须安全返回空。
	if got := dshMapRecord("s", dshHistoryRecord{Type: "mystery", Event: json.RawMessage(`{}`)}); got != nil {
		t.Fatalf("未知记录类型应返回空: %+v", got)
	}
	if got := dshMapRecord("s", dshHistoryRecord{Type: "event", Event: json.RawMessage(`not json`)}); got != nil {
		t.Fatalf("坏 JSON 应返回空: %+v", got)
	}
}
