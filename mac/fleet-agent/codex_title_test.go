package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func titleRPC() *fakeRPCConn {
	r := newFakeRPCConn()
	r.reply["model/list"] = json.RawMessage(`{"data":[{"model":"test-model","isDefault":true}]}`)
	r.reply["thread/start"] = json.RawMessage(`{"thread":{"id":"title-thread"}}`)
	r.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"title-turn"}}`)
	return r
}

func TestCodexTitleGenerationProtocol(t *testing.T) {
	r := titleRPC()
	r.notes <- rpcNotification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"other","item":{"type":"agentMessage","text":"ignore"}}`)}
	r.notes <- rpcNotification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"title-thread","item":{"type":"agentMessage","text":"{\"title\":\"管理多台 Mac\",\"description\":\"统一管理\"}"}}`)}
	r.notes <- rpcNotification{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"title-thread","turn":{"id":"title-turn","status":"completed"}}`)}
	title, err := generateCodexTitleOnRPC(context.Background(), r, "管理终端和文件")
	if err != nil || title != "管理多台 Mac" {
		t.Fatalf("%q %v", title, err)
	}
	if got := methods(r.calls); !reflect.DeepEqual(got, []string{"model/list", "thread/start", "turn/start", "thread/unsubscribe"}) {
		t.Fatal(got)
	}
	params := r.calls[1].params.(map[string]interface{})
	if params["ephemeral"] != true || params["permissions"] != ":read-only" || params["approvalPolicy"] != "never" || params["threadSource"] != "thread_title" {
		t.Fatal(params)
	}
	config := params["config"].(map[string]interface{})
	if config["features.shell_tool"] != false || config["features.multi_agent"] != false || config["project_doc_max_bytes"] != 0 {
		t.Fatal(config)
	}
}

func TestCodexTitleGenerationFailureCleanup(t *testing.T) {
	for _, kind := range []string{"timeout", "failed", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			r := titleRPC()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if kind != "timeout" {
				r.notes <- rpcNotification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"title-thread","item":{"type":"agentMessage","text":"{\"title\":\"\"}"}}`)}
				status := "completed"
				if kind == "failed" {
					status = "failed"
				}
				r.notes <- rpcNotification{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"title-thread","turn":{"status":"` + status + `"}}`)}
			}
			if _, err := generateCodexTitleOnRPC(ctx, r, "test"); err == nil {
				t.Fatal("expected failure")
			}
			calls := methods(r.calls)
			if calls[len(calls)-1] != "thread/unsubscribe" {
				t.Fatal(calls)
			}
			if kind == "timeout" && calls[len(calls)-2] != "turn/interrupt" {
				t.Fatal(calls)
			}
		})
	}
}

func TestCodexAutomaticTitleRespectsExistingNameAndCancelledRequest(t *testing.T) {
	for _, kind := range []string{"unnamed", "named", "archived", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			r := newFakeRPCConn()
			r.reply["thread/read"] = json.RawMessage(`{"thread":{}}`)
			if kind == "named" {
				r.reply["thread/read"] = json.RawMessage(`{"thread":{"name":"User title"}}`)
			}
			if kind == "archived" {
				r.reply["thread/read"] = json.RawMessage(`{"thread":{"archived":true}}`)
			}
			b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return r, func() {}, nil })
			req := &codexTitleRequest{}
			if kind != "cancelled" {
				b.titleRequests["target"] = req
			}
			if err := b.applyGeneratedTitle(context.Background(), "target", req, "AI title"); err != nil {
				t.Fatal(err)
			}
			writes := 0
			for _, c := range r.calls {
				if c.method == "thread/name/set" {
					writes++
				}
			}
			if (writes == 1) != (kind == "unnamed") {
				t.Fatal(kind, writes)
			}
		})
	}
}

func TestCodexTitleFirstSendAsyncAndSingleRetry(t *testing.T) {
	r := newFakeRPCConn()
	r.reply["thread/read"] = json.RawMessage(`{"thread":{}}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return r, func() {}, nil })
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	b.generateTitle = func(ctx context.Context, text string) (string, error) {
		if text != "first" {
			t.Errorf("unexpected input %q", text)
		}
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return "", errors.New("transient")
		}
		return "AI title", nil
	}
	b.prepareTitle("target")
	b.scheduleTitle("target", "first")
	<-entered // schedule returned while generation is blocked.
	b.scheduleTitle("target", "second")
	close(release)
	deadline := time.After(3 * time.Second)
	for {
		b.titleMu.Lock()
		done := b.titleRequests["target"] == nil
		b.titleMu.Unlock()
		if done {
			break
		}
		select {
		case <-deadline:
			t.Fatal("title did not finish")
		case <-time.After(time.Millisecond):
		}
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
	writes := 0
	for _, c := range r.calls {
		if c.method == "thread/name/set" {
			writes++
		}
	}
	if writes != 1 {
		t.Fatal(writes)
	}
}

func TestCodexManualRenameCancelsPendingTitle(t *testing.T) {
	r := newFakeRPCConn()
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return r, func() {}, nil })
	req := &codexTitleRequest{started: true}
	b.titleRequests["target"] = req
	if err := b.MutateThread(context.Background(), "target", "rename", "User title"); err != nil {
		t.Fatal(err)
	}
	if err := b.applyGeneratedTitle(context.Background(), "target", req, "AI title"); err != nil {
		t.Fatal(err)
	}
	if got := methods(r.calls); !reflect.DeepEqual(got, []string{"thread/name/set"}) {
		t.Fatal(got)
	}
}

func TestCodexTitleValidation(t *testing.T) {
	for _, text := range []string{"", "  ", "line\nbreak", strings.Repeat("字", 37)} {
		if validCodexTitle(text) {
			t.Fatal(text)
		}
	}
	if !validCodexTitle(strings.Repeat("字", 36)) {
		t.Fatal("Unicode title rejected")
	}
}

func TestCodexTitleStartsOnlyAfterAcceptedFirstInput(t *testing.T) {
	r := newFakeRPCConn()
	r.reply["thread/start"] = json.RawMessage(`{"thread":{"id":"new"}}`)
	r.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn"}}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return r, func() {}, nil })
	generated := make(chan string, 1)
	b.generateTitle = func(context.Context, string) (string, error) { generated <- "called"; return "", nil }
	started, err := b.Start(context.Background(), "codex", "/repo", "default")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-generated:
		t.Fatal("generation before input")
	default:
	}
	r.errs["turn/start"] = []error{errors.New("send rejected")}
	if _, err = b.Input(context.Background(), "codex", started.SessionID, "hello", nil, nil, ChatTurnOptions{}); err == nil {
		t.Fatal("expected send failure")
	}
	select {
	case <-generated:
		t.Fatal("generation on failed input")
	default:
	}
	if _, err = b.Input(context.Background(), "codex", started.SessionID, "hello", nil, nil, ChatTurnOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-generated:
	case <-time.After(time.Second):
		t.Fatal("no generation after accepted input")
	}
}
