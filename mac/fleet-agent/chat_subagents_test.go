package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCodexSubagentsProjectsAllDescendants(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/list"] = json.RawMessage(`{
		"data":[
			{"id":"child-1","parentThreadId":"parent-1","createdAt":10,"updatedAt":12,"status":{"type":"active"},"agentNickname":"Russell","agentRole":"explorer","source":{"subAgent":{"thread_spawn":{"parent_thread_id":"parent-1","depth":1,"agent_path":"/root/review_icons","agent_nickname":"Russell","agent_role":"explorer"}}}},
			{"id":"child-2","createdAt":20,"updatedAt":22,"status":{"type":"notLoaded"},"source":{"subagent":{"thread_spawn":{"parent_thread_id":"child-1","depth":2,"agent_path":"/root/review_icons/assets","agent_nickname":"Planck","agent_role":"worker"}}}}
		],"nextCursor":null}`)
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	page, err := backend.Subagents(context.Background(), "codex", "parent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items=%+v", page.Items)
	}
	if got := page.Items[0]; got.ThreadID != "child-1" || got.Name != "review_icons" || got.Status != "running" || got.Role != "explorer" || got.Depth != 1 {
		t.Fatalf("first item=%+v", got)
	}
	if got := page.Items[1]; got.ParentThreadID != "child-1" || got.Name != "review_icons/assets" || got.Status != "unknown" || got.Nickname != "Planck" || got.Depth != 2 {
		t.Fatalf("nested item=%+v", got)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "thread/list" {
		t.Fatalf("calls=%+v", rpc.calls)
	}
	params, _ := rpc.calls[0].params.(map[string]interface{})
	if params["ancestorThreadId"] != "parent-1" || params["useStateDbOnly"] != true {
		t.Fatalf("params=%+v", params)
	}
}

func TestCodexSubagentStatusUsesRolloutLifecycle(t *testing.T) {
	cases := []struct {
		name  string
		state codexRolloutTaskState
		want  string
	}{
		{"running", codexRolloutTaskState{turnID: "turn-1", status: "inProgress"}, "running"},
		{"completed", codexRolloutTaskState{turnID: "turn-1", status: "completed", terminal: true}, "completed"},
		{"failed", codexRolloutTaskState{turnID: "turn-1", status: "failed", terminal: true}, "failed"},
		{"interrupted", codexRolloutTaskState{turnID: "turn-1", status: "interrupted", terminal: true}, "interrupted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := codexSubagentStatus("notLoaded", tc.state, true); got != tc.want {
				t.Fatalf("status=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestSubagentToolProjectionKeepsIdentityAndStaysOutOfMainTranscript(t *testing.T) {
	raw := json.RawMessage(`{"id":"sub-1","type":"subAgentActivity","kind":"started","agentThreadId":"child-1","agentPath":"/root/review"}`)
	ev, ok := projectCodexToolItem("parent-1", "turn-1", raw, "completed")
	if !ok {
		t.Fatal("subagent activity was not projected")
	}
	var data map[string]interface{}
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["agentThreadId"] != "child-1" || data["agentPath"] != "/root/review" || data["activityKind"] != "started" || data["internal"] != true {
		t.Fatalf("data=%+v", data)
	}
}
