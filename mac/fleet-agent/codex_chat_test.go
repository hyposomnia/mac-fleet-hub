package main

import (
	"context"
	"encoding/json"
	"reflect"
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
}

func newFakeRPCConn() *fakeRPCConn {
	return &fakeRPCConn{
		notes: make(chan rpcNotification, 8),
		reply: map[string]json.RawMessage{},
	}
}

func (f *fakeRPCConn) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	f.calls = append(f.calls, recordedCall{method: method, params: params})
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
	if len(rpc.calls) != 1 || rpc.calls[0].method != "thread/resume" {
		t.Fatalf("calls: %+v", rpc.calls)
	}
	got := mapFromParams(t, rpc.calls[0].params)
	if got["threadId"] != "thread-1" {
		t.Fatalf("threadId got %v", got["threadId"])
	}
}

func TestCodexChatBackendInputUsesTurnStartTextInput(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-1","items":[],"status":"running"}}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Input(context.Background(), "codex", "thread-1", "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.TurnID != "turn-1" {
		t.Fatalf("turn id got %q", res.TurnID)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "turn/start" {
		t.Fatalf("calls: %+v", rpc.calls)
	}
	got := mapFromParams(t, rpc.calls[0].params)
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
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-img","items":[],"status":"running"}}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Input(context.Background(), "codex", "thread-1", "", []ChatAttachment{{Path: "/tmp/shot.png"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.TurnID != "turn-img" {
		t.Fatalf("turn id got %q", res.TurnID)
	}
	got := mapFromParams(t, rpc.calls[0].params)
	input, ok := got["input"].([]interface{})
	if !ok || len(input) != 1 {
		t.Fatalf("input got %#v", got["input"])
	}
	if !reflect.DeepEqual(input[0], map[string]interface{}{"type": "localImage", "path": "/tmp/shot.png"}) {
		t.Fatalf("input[0] got %#v", input[0])
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
