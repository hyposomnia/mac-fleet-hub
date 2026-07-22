package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type recordedCall struct {
	method string
	params interface{}
}

type fakeRPCConn struct {
	calls []recordedCall
	notes chan rpcNotification
	reply map[string]json.RawMessage
	errs  map[string][]error
}

func newFakeRPCConn() *fakeRPCConn {
	return &fakeRPCConn{
		notes: make(chan rpcNotification, 8),
		reply: map[string]json.RawMessage{},
		errs:  map[string][]error{},
	}
}

func (f *fakeRPCConn) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	f.calls = append(f.calls, recordedCall{method: method, params: params})
	if len(f.errs[method]) > 0 {
		err := f.errs[method][0]
		f.errs[method] = f.errs[method][1:]
		return nil, err
	}
	if raw := f.reply[method]; raw != nil {
		return raw, nil
	}
	return json.RawMessage(`{}`), nil
}

func (f *fakeRPCConn) notify(method string, params interface{}) error { return nil }
func (f *fakeRPCConn) respond(id json.RawMessage, result interface{}) error {
	f.calls = append(f.calls, recordedCall{method: "response:" + string(id), params: result})
	return nil
}
func (f *fakeRPCConn) notifications() <-chan rpcNotification { return f.notes }

func TestCodexChatBackendResumeUsesThreadResume(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Resume(context.Background(), "codex", "thread-1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.ThreadID != "thread-1" || res.Status != "connected" {
		t.Fatalf("bad resume result: %+v", res)
	}
	if len(rpc.calls) != 3 || rpc.calls[0].method != "thread/resume" || rpc.calls[1].method != "thread/items/list" || rpc.calls[2].method != "model/list" {
		t.Fatalf("calls: %+v", rpc.calls)
	}
	got := mapFromParams(t, rpc.calls[0].params)
	if got["threadId"] != "thread-1" {
		t.Fatalf("threadId got %v", got["threadId"])
	}
}

func TestCodexChatBackendResumeHydratesHistoryAndOptions(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{
		"thread":{"id":"thread-1"},"model":"gpt-new","approvalPolicy":"never","sandbox":{"type":"workspaceWrite"}
	}`)
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[
		{"turnId":"turn-new","item":{"id":"a-new","type":"agentMessage","text":"new answer"}},
		{"turnId":"turn-old","item":{"id":"u-old","type":"userMessage","content":[{"type":"text","text":"old question"}]}},
		{"turnId":"turn-old","item":{"id":"u-injected","type":"userMessage","content":[{"type":"text","text":"<environment_context>hidden"}]}}
	],"nextCursor":"older-cursor"}`)
	rpc.reply["model/list"] = json.RawMessage(`{"data":[{
		"id":"gpt-new-id","model":"gpt-new","displayName":"GPT New","description":"Latest", "hidden":false,
		"isDefault":true,"defaultReasoningEffort":"high","supportedReasoningEfforts":[]
	}]}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Resume(context.Background(), "codex", "thread-1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "gpt-new" || res.ApprovalMode != "never" || res.History.NextCursor != "older-cursor" {
		t.Fatalf("bad resume metadata: %+v", res)
	}
	if len(res.Models) != 1 || res.Models[0].Value != "gpt-new" || res.Models[0].DefaultEffort != "high" {
		t.Fatalf("bad models: %+v", res.Models)
	}
	if len(res.History.Events) != 2 || res.History.Events[0].Type != "user_done" || res.History.Events[0].ItemID != "u-old" || res.History.Events[1].ItemID != "a-new" {
		t.Fatalf("history not chronological or injected item leaked: %+v", res.History.Events)
	}
	params := mapFromParams(t, rpc.calls[1].params)
	if params["sortDirection"] != "desc" || params["limit"] != float64(chatHistoryPageSize) {
		t.Fatalf("initial page params: %#v", params)
	}
}

func TestVisibleCodexHistoryUserTextKeepsRequestAfterFilePrelude(t *testing.T) {
	got := visibleCodexHistoryUserText("# Files mentioned by the user:\n\n## My request for Codex:\n实现历史加载")
	if got != "实现历史加载" {
		t.Fatalf("visible text got %q", got)
	}
	if got := visibleCodexHistoryUserText("<environment_context>hidden</environment_context>"); got != "" {
		t.Fatalf("injected text leaked: %q", got)
	}
}

func TestCodexChatBackendHistoryPassesCursorAndReturnsChronologicalEvents(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[
		{"turnId":"t2","item":{"id":"a2","type":"agentMessage","text":"two"}},
		{"turnId":"t1","item":{"id":"a1","type":"agentMessage","text":"one"}}
	],"nextCursor":null}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	page, err := b.History(context.Background(), "codex", "thread-1", "cursor-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || page.Events[0].ItemID != "a1" || page.Events[1].ItemID != "a2" || page.NextCursor != "" {
		t.Fatalf("bad history page: %+v", page)
	}
	params := mapFromParams(t, rpc.calls[0].params)
	if rpc.calls[0].method != "thread/items/list" || params["cursor"] != "cursor-1" || params["sortDirection"] != "desc" || params["limit"] != float64(chatHistoryPageSize) {
		t.Fatalf("history params: %#v", params)
	}
}

func TestCodexChatBackendHistoryFallsBackToItemPagingWithinTurns(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.errs["thread/items/list"] = []error{errors.New("thread/items/list is not supported yet")}
	items := make([]json.RawMessage, 42)
	for i := range items {
		items[i], _ = json.Marshal(map[string]interface{}{
			"id": "a-" + string(rune('A'+i)), "type": "agentMessage", "text": "answer",
		})
	}
	raw, _ := json.Marshal(codexTurnsPage{Data: []codexHistoryTurn{{ID: "turn-1", Items: items}}})
	rpc.reply["thread/turns/list"] = raw
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	first, err := b.History(context.Background(), "codex", "thread-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 40 || first.Events[0].ItemID != "a-C" || first.Events[39].ItemID != "a-j" || !strings.HasPrefix(first.NextCursor, codexTurnsCursorPrefix) {
		t.Fatalf("bad first fallback page: len=%d first=%q last=%q cursor=%q", len(first.Events), first.Events[0].ItemID, first.Events[39].ItemID, first.NextCursor)
	}
	if got := methods(rpc.calls); !reflect.DeepEqual(got, []string{"thread/items/list", "thread/turns/list"}) {
		t.Fatalf("first page calls got %#v", got)
	}

	second, err := b.History(context.Background(), "codex", "thread-1", first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != 2 || second.Events[0].ItemID != "a-A" || second.Events[1].ItemID != "a-B" || second.NextCursor != "" {
		t.Fatalf("bad second fallback page: %+v", second)
	}
	if got := methods(rpc.calls); !reflect.DeepEqual(got, []string{"thread/items/list", "thread/turns/list"}) {
		t.Fatalf("second page calls got %#v", got)
	}
	params := mapFromParams(t, rpc.calls[1].params)
	if params["itemsView"] != "full" || params["sortDirection"] != "desc" {
		t.Fatalf("turn fallback params: %#v", params)
	}
}

func TestCodexChatBackendInputUsesTurnStartTextInput(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-1","items":[],"status":"running"}}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Input(context.Background(), "codex", "thread-1", "hello", nil, ChatTurnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TurnID != "turn-1" {
		t.Fatalf("turn id got %q", res.TurnID)
	}
	if len(rpc.calls) != 2 || rpc.calls[0].method != "thread/resume" || rpc.calls[1].method != "turn/start" {
		t.Fatalf("calls: %+v", rpc.calls)
	}
	got := mapFromParams(t, rpc.calls[1].params)
	if got["threadId"] != "thread-1" {
		t.Fatalf("threadId got %v", got["threadId"])
	}
	input, ok := got["input"].([]interface{})
	if !ok || len(input) != 1 {
		t.Fatalf("input got %#v", got["input"])
	}
	if !reflect.DeepEqual(input[0], map[string]interface{}{"type": "text", "text": "hello"}) {
		t.Fatalf("input[0] got %#v", input[0])
	}
}

func TestCodexChatBackendInputUsesLocalImages(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-img","items":[],"status":"running"}}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Input(context.Background(), "codex", "thread-1", "", []ChatAttachment{{Path: "/tmp/shot.png"}}, ChatTurnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TurnID != "turn-img" {
		t.Fatalf("turn id got %q", res.TurnID)
	}
	got := mapFromParams(t, rpc.calls[1].params)
	input, ok := got["input"].([]interface{})
	if !ok || len(input) != 1 {
		t.Fatalf("input got %#v", got["input"])
	}
	if !reflect.DeepEqual(input[0], map[string]interface{}{"type": "localImage", "path": "/tmp/shot.png"}) {
		t.Fatalf("input[0] got %#v", input[0])
	}
}

func TestCodexChatBackendInputPassesModelAndApprovalOverrides(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-1"}}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	_, err := b.Input(context.Background(), "codex", "thread-1", "hello", nil, ChatTurnOptions{
		Model: "gpt-new", Effort: "high", ApprovalMode: "full-access",
	})
	if err != nil {
		t.Fatal(err)
	}
	params := mapFromParams(t, rpc.calls[1].params)
	if params["model"] != "gpt-new" || params["effort"] != "high" || params["approvalPolicy"] != "never" {
		t.Fatalf("turn params: %#v", params)
	}
	sandbox, ok := params["sandboxPolicy"].(map[string]interface{})
	if !ok || sandbox["type"] != "dangerFullAccess" {
		t.Fatalf("sandbox params: %#v", params["sandboxPolicy"])
	}
}

func TestCodexChatBackendInputRetriesThreadNotFoundOnFreshRPC(t *testing.T) {
	rpc1 := newFakeRPCConn()
	rpc1.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	rpc1.errs["turn/start"] = []error{errors.New("codex app-server turn/start failed: thread not found: thread-1")}

	rpc2 := newFakeRPCConn()
	rpc2.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	rpc2.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-2","items":[],"status":"running"}}`)

	connects := 0
	cleanups := 0
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		connects++
		if connects == 1 {
			return rpc1, func() { cleanups++ }, nil
		}
		return rpc2, func() { cleanups++ }, nil
	})

	res, err := b.Input(context.Background(), "codex", "thread-1", "hello", nil, ChatTurnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TurnID != "turn-2" {
		t.Fatalf("turn id got %q", res.TurnID)
	}
	if connects != 2 || cleanups != 1 {
		t.Fatalf("connects=%d cleanups=%d", connects, cleanups)
	}
	if got := methods(rpc1.calls); !reflect.DeepEqual(got, []string{"thread/resume", "turn/start"}) {
		t.Fatalf("rpc1 calls got %#v", got)
	}
	if got := methods(rpc2.calls); !reflect.DeepEqual(got, []string{"thread/resume", "turn/start"}) {
		t.Fatalf("rpc2 calls got %#v", got)
	}
}

func TestCodexChatBackendEventsDispatchBySessionID(t *testing.T) {
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := b.Events(ctx, "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}

	rpc.notes <- rpcNotification{
		Method: "item/agentMessage/delta",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"msg-1","delta":"ok"}`),
	}

	select {
	case ev := <-events:
		if ev.Type != "assistant_delta" || ev.SessionID != "thread-1" || ev.ItemID != "msg-1" {
			t.Fatalf("bad event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("event timeout")
	}
}

func TestCodexChatBackendApproveRespondsToServerRequestID(t *testing.T) {
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := b.Events(ctx, "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	rpc.notes <- rpcNotification{
		ID:     json.RawMessage(`42`),
		Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"cmd1","command":"pwd"}`),
	}
	select {
	case <-events:
	case <-time.After(time.Second):
		t.Fatal("approval event timeout")
	}

	if err := b.Approve(context.Background(), "codex", "thread-1", "42", "approved"); err != nil {
		t.Fatal(err)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "response:42" || rpc.calls[0].params != "accept" {
		t.Fatalf("calls got %+v", rpc.calls)
	}
}

func methods(calls []recordedCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.method)
	}
	return out
}

func mapFromParams(t *testing.T, params interface{}) map[string]interface{} {
	t.Helper()
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
