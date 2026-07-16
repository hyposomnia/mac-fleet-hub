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

func TestMapCodexNotificationCompletedOnlyAgentMessage(t *testing.T) {
	done := mapCodexNotification(rpcNotification{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"t1","turnId":"turn1","completedAtMs":1,"item":{"id":"msg1","type":"agentMessage","text":"done"}}`),
	})
	if len(done) != 1 || done[0].Type != "assistant_done" || done[0].ItemID != "msg1" {
		t.Fatalf("bad assistant done mapping: %+v", done)
	}

	cmd := mapCodexNotification(rpcNotification{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"t1","turnId":"turn1","completedAtMs":1,"item":{"id":"cmd1","type":"commandExecution","command":"pwd","cwd":"/tmp","status":"completed","commandActions":[]}}`),
	})
	if len(cmd) != 0 {
		t.Fatalf("command item completion should not be assistant_done: %+v", cmd)
	}
}

func TestMapCodexNotificationIgnoresUnknown(t *testing.T) {
	if got := mapCodexNotification(rpcNotification{Method: "something/new", Params: json.RawMessage(`{}`)}); len(got) != 0 {
		t.Fatalf("unknown notification should be ignored, got %+v", got)
	}
}
