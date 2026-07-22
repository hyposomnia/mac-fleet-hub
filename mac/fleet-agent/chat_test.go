package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatEventConstructorsShape(t *testing.T) {
	ev := newChatEvent("assistant_delta", "codex", "thread-1", "turn-1", "item-1", map[string]string{"delta": "hi"})
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"type":"assistant_delta"`, `"assistant":"codex"`, `"sessionId":"thread-1"`, `"turnId":"turn-1"`, `"itemId":"item-1"`, `"delta":"hi"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("event json missing %s in %s", want, s)
		}
	}
}

func TestMapCodexNotificationAgentMessageDelta(t *testing.T) {
	n := rpcNotification{
		Method: "item/agentMessage/delta",
		Params: json.RawMessage(`{"threadId":"t1","turnId":"turn1","itemId":"i1","delta":"hello"}`),
	}
	evs := mapCodexNotification(n)
	if len(evs) != 1 {
		t.Fatalf("events len=%d", len(evs))
	}
	ev := evs[0]
	if ev.Type != "assistant_delta" || ev.Assistant != "codex" || ev.SessionID != "t1" || ev.TurnID != "turn1" || ev.ItemID != "i1" {
		t.Fatalf("bad event: %+v", ev)
	}
	if !strings.Contains(string(ev.Data), "hello") {
		t.Fatalf("data missing delta: %s", ev.Data)
	}
}

func TestMapCodexNotificationCommandOutputAndApproval(t *testing.T) {
	out := mapCodexNotification(rpcNotification{
		Method: "item/commandExecution/outputDelta",
		Params: json.RawMessage(`{"threadId":"t1","turnId":"turn1","itemId":"cmd1","stream":"stdout","delta":"ok\n"}`),
	})
	if len(out) != 1 || out[0].Type != "tool_delta" || out[0].ItemID != "cmd1" {
		t.Fatalf("bad command output mapping: %+v", out)
	}

	ap := mapCodexNotification(rpcNotification{
		ID:     json.RawMessage(`42`),
		Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(`{"threadId":"t1","turnId":"turn1","itemId":"cmd1","command":"pwd","cwd":"/tmp"}`),
	})
	if len(ap) != 1 || ap[0].Type != "approval_request" || ap[0].ItemID != "cmd1" {
		t.Fatalf("bad approval mapping: %+v", ap)
	}
	if !strings.Contains(string(ap[0].Data), `"requestId":42`) {
		t.Fatalf("approval missing request id: %s", ap[0].Data)
	}
}

func TestMapCodexNotificationCompletedItems(t *testing.T) {
	done := mapCodexNotification(rpcNotification{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"t1","turnId":"turn1","completedAtMs":1,"item":{"id":"msg1","type":"agentMessage","text":"done"}}`),
	})
	if len(done) != 1 || done[0].Type != "assistant_done" || done[0].ItemID != "msg1" {
		t.Fatalf("bad assistant done mapping: %+v", done)
	}

	cmd := mapCodexNotification(rpcNotification{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"t1","turnId":"turn1","completedAtMs":1,"item":{"id":"cmd1","type":"commandExecution","command":"pwd","cwd":"/tmp","status":"completed","aggregatedOutput":"/tmp\n","exitCode":0,"durationMs":12,"commandActions":[]}}`),
	})
	if len(cmd) != 1 || cmd[0].Type != "tool_update" || cmd[0].ItemID != "cmd1" {
		t.Fatalf("command completion should become a tool update: %+v", cmd)
	}
	for _, want := range []string{`"kind":"commandExecution"`, `"summary":"pwd"`, `"status":"completed"`, `"exitCode":0`} {
		if !strings.Contains(string(cmd[0].Data), want) {
			t.Fatalf("command tool data missing %s in %s", want, cmd[0].Data)
		}
	}

	mcp := mapCodexNotification(rpcNotification{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"t1","turnId":"turn1","completedAtMs":1,"item":{"id":"mcp1","type":"mcpToolCall","server":"notion","tool":"search","status":"completed","arguments":{"query":"fleet"},"appContext":{"appName":"Notion","actionName":"Search"},"result":{"content":[{"type":"text","text":"ok"}],"structuredContent":null,"_meta":null},"error":null,"durationMs":80}}`),
	})
	if len(mcp) != 1 || mcp[0].Type != "tool_update" || mcp[0].ItemID != "mcp1" {
		t.Fatalf("MCP completion should become a tool update: %+v", mcp)
	}
	for _, want := range []string{`"kind":"mcpToolCall"`, `"title":"Notion · Search"`, `"status":"completed"`} {
		if !strings.Contains(string(mcp[0].Data), want) {
			t.Fatalf("MCP tool data missing %s in %s", want, mcp[0].Data)
		}
	}
}

func TestMapCodexNotificationStartedAndMcpProgress(t *testing.T) {
	started := mapCodexNotification(rpcNotification{
		Method: "item/started",
		Params: json.RawMessage(`{"threadId":"t1","turnId":"turn1","startedAtMs":1,"item":{"id":"web1","type":"webSearch","query":"Codex tools","action":{"type":"search","query":"Codex tools"}}}`),
	})
	if len(started) != 1 || started[0].Type != "tool_update" || started[0].ItemID != "web1" {
		t.Fatalf("web search start should become a tool update: %+v", started)
	}
	if !strings.Contains(string(started[0].Data), `"kind":"webSearch"`) {
		t.Fatalf("web search kind missing: %s", started[0].Data)
	}

	progress := mapCodexNotification(rpcNotification{
		Method: "item/mcpToolCall/progress",
		Params: json.RawMessage(`{"threadId":"t1","turnId":"turn1","itemId":"mcp1","message":"正在读取页面"}`),
	})
	if len(progress) != 1 || progress[0].Type != "tool_delta" || !strings.Contains(string(progress[0].Data), `"kind":"mcpToolCall"`) {
		t.Fatalf("MCP progress mapping failed: %+v", progress)
	}
}

func TestMapCodexNotificationIgnoresUnknown(t *testing.T) {
	if got := mapCodexNotification(rpcNotification{Method: "something/new", Params: json.RawMessage(`{}`)}); len(got) != 0 {
		t.Fatalf("unknown notification should be ignored, got %+v", got)
	}
}
