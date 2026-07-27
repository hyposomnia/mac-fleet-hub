package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	calls                 []recordedCall
	notes                 chan rpcNotification
	reply                 map[string]json.RawMessage
	errs                  map[string][]error
	block                 map[string]bool
	terminatedDescendants int
}

func newFakeRPCConn() *fakeRPCConn {
	return &fakeRPCConn{
		notes: make(chan rpcNotification, 8),
		reply: map[string]json.RawMessage{},
		errs:  map[string][]error{},
		block: map[string]bool{},
	}
}

func (f *fakeRPCConn) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	f.calls = append(f.calls, recordedCall{method: method, params: params})
	if f.block[method] {
		<-ctx.Done()
		return nil, ctx.Err()
	}
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
func (f *fakeRPCConn) terminateCommandDescendants()          { f.terminatedDescendants++ }

func TestCodexChatBackendStartUsesThreadStart(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/start"] = json.RawMessage(`{
		"thread":{"id":"thread-new","sessionId":"thread-new"},
		"cwd":"/repo"
	}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Start(context.Background(), "codex", "/repo", "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "thread-new" || res.Cwd != "/repo" {
		t.Fatalf("bad start result: %+v", res)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "thread/start" {
		t.Fatalf("calls: %+v", rpc.calls)
	}
	got := mapFromParams(t, rpc.calls[0].params)
	if got["cwd"] != "/repo" {
		t.Fatalf("cwd got %v", got["cwd"])
	}
	if _, ok := got["approvalPolicy"]; ok {
		t.Fatalf("default mode should preserve Codex config: %#v", got)
	}
	if _, ok := got["sandbox"]; ok {
		t.Fatalf("default mode should preserve Codex config: %#v", got)
	}
}

func TestCodexChatBackendStartRejectsMissingThreadID(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/start"] = json.RawMessage(`{"thread":{},"cwd":"/repo"}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	if _, err := b.Start(context.Background(), "codex", "/repo", "default"); err == nil || !strings.Contains(err.Error(), "missing thread id") {
		t.Fatalf("missing thread id should fail, got %v", err)
	}
}

func TestCodexChatBackendStartThenInputSkipsResumeBeforeFirstTurn(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/start"] = json.RawMessage(`{"thread":{"id":"thread-new"},"cwd":"/repo"}`)
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-1"}}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	started, err := b.Start(context.Background(), "codex", "/repo", "default")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Input(context.Background(), "codex", started.SessionID, "hello", nil, nil, ChatTurnOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Input(context.Background(), "codex", started.SessionID, "again", nil, nil, ChatTurnOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := methods(rpc.calls); !reflect.DeepEqual(got, []string{"thread/start", "turn/start", "thread/resume", "turn/start"}) {
		t.Fatalf("only the first turn of a fresh thread should skip resume: %v", got)
	}
}

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
	if res.ThreadID != "thread-1" || res.Status != "idle" {
		t.Fatalf("bad resume result: %+v", res)
	}
	if len(rpc.calls) != 4 || rpc.calls[0].method != "thread/read" || rpc.calls[1].method != "thread/resume" ||
		rpc.calls[2].method != "thread/items/list" || rpc.calls[3].method != "model/list" {
		t.Fatalf("calls: %+v", rpc.calls)
	}
	readParams := mapFromParams(t, rpc.calls[0].params)
	if readParams["threadId"] != "thread-1" || readParams["includeTurns"] != false {
		t.Fatalf("thread/read params got %#v", readParams)
	}
	got := mapFromParams(t, rpc.calls[1].params)
	if got["threadId"] != "thread-1" {
		t.Fatalf("threadId got %v", got["threadId"])
	}
}

func TestCodexChatBackendResumeRestoresActiveTurnFromThread(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{
		"thread":{"id":"thread-1","status":{"type":"active","activeFlags":[]},"turns":[{"id":"turn-live","status":"inProgress","items":[]}]}
	}`)
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[{"turnId":"turn-old","item":{"id":"a-old","type":"agentMessage","text":"previous"}}]}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Resume(context.Background(), "codex", "thread-1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "active" || res.ActiveTurnID != "turn-live" || b.lastTurn["thread-1"] != "turn-live" {
		t.Fatalf("active resume not restored: result=%+v lastTurn=%q", res, b.lastTurn["thread-1"])
	}
}

func TestCodexChatBackendResumeRestoresInProgressTurnFromThread(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{
		"thread":{"id":"thread-1","status":{"type":"inProgress"},"turns":[{"id":"turn-live","status":"inProgress","items":[]}]}
	}`)
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[]}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Resume(context.Background(), "codex", "thread-1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "inProgress" || res.ActiveTurnID != "turn-live" || b.lastTurn["thread-1"] != "turn-live" {
		t.Fatalf("in-progress resume not restored: result=%+v lastTurn=%q", res, b.lastTurn["thread-1"])
	}
}

func TestCodexChatBackendResumeHydratesHistoryAndOptions(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	home := t.TempDir()
	cfg = Config{CodexHome: home}
	sessionID := "019f96ff-d763-7661-b3e0-4d909d9cd315"
	dir := filepath.Join(home, "sessions", "2026", "07", "25")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dir, "rollout-2026-07-25T09-59-37-"+sessionID+".jsonl")
	body := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","originator":"Codex Desktop","source":"vscode"}}`,
		`{"type":"turn_context","payload":{"approval_policy":"never","sandbox_policy":{"type":"danger-full-access"},"permission_profile":{"type":"disabled"}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(rollout, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{
		"thread":{"id":"` + sessionID + `"},"model":"gpt-new","reasoningEffort":"xhigh","serviceTier":"priority","approvalPolicy":"on-request","sandbox":{"type":"workspaceWrite"}
	}`)
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[
		{"turnId":"turn-new","item":{"id":"a-new","type":"agentMessage","text":"new answer","model":"gpt-new","reasoningEffort":"xhigh","completedAtMs":1784730000000,"usage":{"inputTokens":12,"outputTokens":3}}},
		{"turnId":"turn-old","item":{"id":"u-old","type":"userMessage","createdAtMs":1784727730000,"content":[{"type":"text","text":"old question"}]}},
		{"turnId":"turn-old","item":{"id":"u-injected","type":"userMessage","content":[{"type":"text","text":"<environment_context>hidden"}]}}
	],"nextCursor":"older-cursor"}`)
	rpc.reply["model/list"] = json.RawMessage(`{"data":[{
		"id":"gpt-new-id","model":"gpt-new","displayName":"GPT New","description":"Latest", "hidden":false,
		"isDefault":true,"defaultReasoningEffort":"high","supportedReasoningEfforts":[
			{"reasoningEffort":"medium","description":"Balanced"},
			{"reasoningEffort":"high","description":"Thorough"}
		],"defaultServiceTier":null,"serviceTiers":[
			{"id":"priority","name":"Fast","description":"Lower latency"}
		]
	}]}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Resume(context.Background(), "codex", sessionID, "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "gpt-new" || res.Effort != "xhigh" || res.ServiceTier != "priority" || res.ApprovalMode != "full-access" || res.History.NextCursor != "older-cursor" {
		t.Fatalf("bad resume metadata: %+v", res)
	}
	if len(res.Models) != 1 || res.Models[0].Value != "gpt-new" || res.Models[0].DefaultEffort != "high" ||
		len(res.Models[0].SupportedEfforts) != 2 || res.Models[0].SupportedEfforts[1].Value != "high" ||
		len(res.Models[0].ServiceTiers) != 1 || res.Models[0].ServiceTiers[0].Value != "priority" {
		t.Fatalf("bad models: %+v", res.Models)
	}
	if len(res.History.Events) != 2 || res.History.Events[0].Type != "user_done" || res.History.Events[0].ItemID != "u-old" || res.History.Events[1].ItemID != "a-new" {
		t.Fatalf("history not chronological or injected item leaked: %+v", res.History.Events)
	}
	for _, want := range []string{`"createdAtMs":1784727730000`, `"model":"gpt-new"`, `"reasoningEffort":"xhigh"`, `"inputTokens":12`, `"completedAtMs":1784730000000`} {
		if !strings.Contains(string(res.History.Events[0].Data)+string(res.History.Events[1].Data), want) {
			t.Fatalf("history metadata missing %s in %s / %s", want, res.History.Events[0].Data, res.History.Events[1].Data)
		}
	}
	params := mapFromParams(t, rpc.calls[2].params)
	if params["sortDirection"] != "desc" || params["limit"] != float64(chatHistoryPageSize) {
		t.Fatalf("initial page params: %#v", params)
	}
}

func TestCodexApprovalModeFromRolloutUsesLatestStructuredSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	body := strings.Join([]string{
		`{"type":"turn_context","payload":{"approval_policy":"never","sandbox_policy":{"type":"danger-full-access"},"permission_profile":{"type":"disabled"}}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"danger-full-access"}]}}`,
		`{"type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"approval_policy":"on-request","permission_profile":{"type":"workspace"},"active_permission_profile":{"id":":workspace"}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	if got := codexApprovalModeFromRollout(path); got != "on-request" {
		t.Fatalf("approval mode got %q want on-request", got)
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

func TestCodexChatBackendHistoryBackfillsUsageFromRollout(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	home := t.TempDir()
	cfg = Config{CodexHome: home}
	sessionID := "019f8f79-7f02-7692-b2de-808c33945a83"
	dir := filepath.Join(home, "sessions", "2026", "07", "23")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dir, "rollout-2026-07-23T22-55-32-"+sessionID+".jsonl")
	body := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","originator":"Codex Desktop","source":"vscode"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"},"content":[{"type":"output_text","text":"old answer"}]}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":114146,"cached_input_tokens":108439,"output_tokens":1935}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(rollout, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	rpc := newFakeRPCConn()
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[
		{"turnId":"turn-1","item":{"id":"a1","type":"agentMessage","text":"old answer","model":"gpt-5.6-sol"}}
	]}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	page, err := b.History(context.Background(), "codex", sessionID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("events got %+v", page.Events)
	}
	for _, want := range []string{`"inputTokens":114146`, `"cachedInputTokens":108439`, `"outputTokens":1935`} {
		if !strings.Contains(string(page.Events[0].Data), want) {
			t.Fatalf("usage missing %s from %s", want, page.Events[0].Data)
		}
	}
}

func TestCodexTurnUsageFromRolloutKeepsLatestUsagePerTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	body := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"message","role":"assistant","internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"},"content":[{"type":"output_text","text":"first"}]}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":12,"cached_input_tokens":8,"output_tokens":3}}}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"},"content":[{"type":"output_text","text":"final"}]}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":120,"cached_input_tokens":96,"output_tokens":30}}}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","internal_chat_message_metadata_passthrough":{"turn_id":"turn-2"},"content":[{"type":"output_text","text":"other"}]}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"inputTokens":7,"cachedInputTokens":5,"outputTokens":2}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	usage := codexTurnUsageFromRollout(path)
	if got := usage["turn-1"]; got.InputTokens != 120 || got.CachedInputTokens != 96 || got.OutputTokens != 30 {
		t.Fatalf("turn-1 usage got %+v", got)
	}
	if got := usage["turn-2"]; got.InputTokens != 7 || got.CachedInputTokens != 5 || got.OutputTokens != 2 {
		t.Fatalf("turn-2 usage got %+v", got)
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
	if params["itemsView"] != "full" || params["sortDirection"] != "desc" || params["limit"] != float64(1) {
		t.Fatalf("turn fallback params: %#v", params)
	}
}

func TestProjectCodexHistoryIncludesNonFileTools(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		kind string
	}{
		{"web search", `{"id":"web1","type":"webSearch","query":"Codex tools","action":{"type":"search","query":"Codex tools"}}`, "webSearch"},
		{"dynamic tool", `{"id":"dyn1","type":"dynamicToolCall","namespace":"browser","tool":"open","arguments":{"url":"https://example.com"},"status":"completed","contentItems":[{"type":"inputText","text":"ok"}],"success":true,"durationMs":10}`, "dynamicToolCall"},
		{"image view", `{"id":"img1","type":"imageView","path":"/tmp/shot.png"}`, "imageView"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, ok := projectCodexHistoryItem("thread-1", "turn-1", json.RawMessage(tc.raw))
			if !ok || ev.Type != "tool_update" {
				t.Fatalf("history tool projection failed: ok=%v event=%+v", ok, ev)
			}
			if !strings.Contains(string(ev.Data), `"kind":"`+tc.kind+`"`) {
				t.Fatalf("history tool kind missing from %s", ev.Data)
			}
		})
	}
}

func TestProjectCodexHistoryLocalImageKeepsRenderablePath(t *testing.T) {
	ev, ok := projectCodexHistoryItem("thread-1", "turn-1", json.RawMessage(
		`{"id":"user-1","type":"userMessage","content":[{"type":"text","text":"看这张图"},{"type":"localImage","path":"/Users/test/Library/Caches/mac-fleet-hub/chat-uploads/session/image.png"}]}`,
	))
	if !ok || ev.Type != "user_done" {
		t.Fatalf("local image projection failed: ok=%v event=%+v", ok, ev)
	}
	var data struct {
		Images []map[string]string `json:"images"`
	}
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Images) != 1 || data.Images[0]["name"] != "image.png" ||
		data.Images[0]["path"] != "/Users/test/Library/Caches/mac-fleet-hub/chat-uploads/session/image.png" {
		t.Fatalf("local image data got %+v", data.Images)
	}
}

func TestCodexChatBackendInputUsesTurnStartTextInput(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-1","items":[],"status":"running"}}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Input(context.Background(), "codex", "thread-1", "hello", nil, nil, ChatTurnOptions{})
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

func TestCodexChatBackendSkillsUsesSkillsList(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["skills/list"] = json.RawMessage(`{
		"data":[{
			"cwd":"/repo",
			"skills":[
				{"name":"dev","description":"Long development instructions","shortDescription":"Develop","path":"/repo/.agents/skills/dev/SKILL.md","scope":"repo","enabled":true},
				{"name":"disabled","description":"Disabled","path":"/repo/.agents/skills/disabled/SKILL.md","scope":"repo","enabled":false},
				{"name":"tavily","description":"Agents copy","path":"/Users/test/.agents/skills/tavily/SKILL.md","scope":"user","enabled":true},
				{"name":"tavily","description":"Codex copy","path":"/Users/test/.codex/skills/tavily/SKILL.md","scope":"user","enabled":true}
			],
			"errors":[]
		},{
			"cwd":"/another-repo",
			"skills":[
				{"name":"foreign","description":"Other cwd","path":"/another-repo/.agents/skills/foreign/SKILL.md","scope":"repo","enabled":true}
			],
			"errors":[]
		}]
	}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	got, err := b.Skills(context.Background(), "codex", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "dev" || got[0].Path != "/repo/.agents/skills/dev/SKILL.md" ||
		got[0].Description != "Develop" || got[0].Scope != "repo" {
		t.Fatalf("skills got %+v", got)
	}
	if got[0].ID == "" || got[1].ID == "" || got[2].ID == "" ||
		got[1].Name != "tavily" || got[2].Name != "tavily" ||
		got[1].Path == got[2].Path || got[1].ID == got[2].ID {
		t.Fatalf("same-name skills must remain distinct: %+v", got)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "skills/list" {
		t.Fatalf("calls: %+v", rpc.calls)
	}
	params := mapFromParams(t, rpc.calls[0].params)
	if !reflect.DeepEqual(params["cwds"], []interface{}{"/repo"}) {
		t.Fatalf("skills/list params: %#v", params)
	}
}

func TestCodexChatBackendInputUsesStructuredSkills(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-1","status":"running"}}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	skills := []ChatSkill{{Name: "dev", Path: "/repo/.agents/skills/dev/SKILL.md"}}

	if _, err := b.Input(context.Background(), "codex", "thread-1", "fix it", nil, skills, ChatTurnOptions{}); err != nil {
		t.Fatal(err)
	}
	params := mapFromParams(t, rpc.calls[1].params)
	input, ok := params["input"].([]interface{})
	if !ok || len(input) != 2 {
		t.Fatalf("input got %#v", params["input"])
	}
	if !reflect.DeepEqual(input[0], map[string]interface{}{"type": "skill", "name": "dev", "path": "/repo/.agents/skills/dev/SKILL.md"}) {
		t.Fatalf("skill input got %#v", input[0])
	}
	if !reflect.DeepEqual(input[1], map[string]interface{}{"type": "text", "text": "fix it"}) {
		t.Fatalf("text input got %#v", input[1])
	}
}

func TestCodexChatBackendInputRejectsMissingTurnID(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"status":"failed"}}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	if _, err := b.Input(context.Background(), "codex", "thread-1", "hello", nil, nil, ChatTurnOptions{}); err == nil || !strings.Contains(err.Error(), "missing turn id") {
		t.Fatalf("missing turn id should fail, got %v", err)
	}
	if b.lastTurn["thread-1"] != "" {
		t.Fatalf("missing turn id must not create active turn: %#v", b.lastTurn)
	}
}

func TestCodexChatBackendInputUsesLocalImages(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-img","items":[],"status":"running"}}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Input(context.Background(), "codex", "thread-1", "", []ChatAttachment{{Path: "/tmp/shot.png"}}, nil, ChatTurnOptions{})
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

	serviceTier := "priority"
	_, err := b.Input(context.Background(), "codex", "thread-1", "hello", nil, nil, ChatTurnOptions{
		Model: "gpt-new", Effort: "high", ServiceTier: &serviceTier, ApprovalMode: "full-access",
	})
	if err != nil {
		t.Fatal(err)
	}
	params := mapFromParams(t, rpc.calls[1].params)
	if params["model"] != "gpt-new" || params["effort"] != "high" || params["serviceTier"] != "priority" || params["approvalPolicy"] != "never" {
		t.Fatalf("turn params: %#v", params)
	}
	sandbox, ok := params["sandboxPolicy"].(map[string]interface{})
	if !ok || sandbox["type"] != "dangerFullAccess" {
		t.Fatalf("sandbox params: %#v", params["sandboxPolicy"])
	}
}

func TestCodexChatBackendSettingsUpdatesApprovalImmediately(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/settings/update"] = json.RawMessage(`{}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	if err := b.Settings(context.Background(), "codex", "thread-1", "full-access"); err != nil {
		t.Fatal(err)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "thread/settings/update" {
		t.Fatalf("calls: %#v", rpc.calls)
	}
	params := mapFromParams(t, rpc.calls[0].params)
	if params["threadId"] != "thread-1" || params["approvalPolicy"] != "never" {
		t.Fatalf("settings params: %#v", params)
	}
	sandbox, ok := params["sandboxPolicy"].(map[string]interface{})
	if !ok || sandbox["type"] != "dangerFullAccess" {
		t.Fatalf("sandbox params: %#v", params["sandboxPolicy"])
	}
}

func TestCodexChatBackendSteerUsesActiveTurnAndLocalImages(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["turn/steer"] = json.RawMessage(`{"turnId":"turn-1"}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.lastTurn["thread-1"] = "turn-1"

	res, err := b.Steer(context.Background(), "codex", "thread-1", "follow-1", "follow up", []ChatAttachment{{Path: "/tmp/shot.png"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.TurnID != "turn-1" {
		t.Fatalf("turn id got %q", res.TurnID)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "turn/steer" {
		t.Fatalf("calls: %+v", rpc.calls)
	}
	params := mapFromParams(t, rpc.calls[0].params)
	if params["threadId"] != "thread-1" || params["expectedTurnId"] != "turn-1" || params["clientUserMessageId"] != "follow-1" {
		t.Fatalf("steer params: %#v", params)
	}
	input, ok := params["input"].([]interface{})
	if !ok || len(input) != 2 {
		t.Fatalf("steer input: %#v", params["input"])
	}
}

func TestCodexChatBackendSteerUsesStructuredSkills(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["turn/steer"] = json.RawMessage(`{"turnId":"turn-1"}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.lastTurn["thread-1"] = "turn-1"
	skills := []ChatSkill{{Name: "dev", Path: "/repo/.agents/skills/dev/SKILL.md"}}

	if _, err := b.Steer(context.Background(), "codex", "thread-1", "follow-1", "keep going", nil, skills); err != nil {
		t.Fatal(err)
	}
	params := mapFromParams(t, rpc.calls[0].params)
	input, ok := params["input"].([]interface{})
	if !ok || len(input) != 2 {
		t.Fatalf("input got %#v", params["input"])
	}
	if !reflect.DeepEqual(input[0], map[string]interface{}{"type": "skill", "name": "dev", "path": "/repo/.agents/skills/dev/SKILL.md"}) {
		t.Fatalf("skill input got %#v", input[0])
	}
}

func TestCodexChatBackendEventsReplaysLargeBacklogWithoutBlocking(t *testing.T) {
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	const eventCount = 96
	for index := 0; index < eventCount; index++ {
		b.backlog["thread-1"] = append(b.backlog["thread-1"], ChatEvent{
			Type: "item/completed",
			Data: json.RawMessage(`{"status":"completed"}`),
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := b.Events(ctx, "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < eventCount; index++ {
		select {
		case <-ch:
		default:
			t.Fatalf("backlog event %d was not buffered", index)
		}
	}
}

func TestCodexChatBackendInterruptWithoutActiveTurn(t *testing.T) {
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	err := b.Interrupt(context.Background(), "codex", "thread-1")
	if !errors.Is(err, errNoActiveChatTurn) {
		t.Fatalf("interrupt without active turn got %v", err)
	}
	if len(rpc.calls) != 0 {
		t.Fatalf("interrupt should not call RPC without a turn: %+v", rpc.calls)
	}
}

func TestCodexChatBackendInterruptTerminatesAppServerDescendants(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["turn/interrupt"] = json.RawMessage(`{}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.lastTurn["thread-1"] = "turn-1"

	if err := b.Interrupt(context.Background(), "codex", "thread-1"); err != nil {
		t.Fatal(err)
	}
	if rpc.terminatedDescendants != 1 {
		t.Fatalf("descendant cleanup calls got %d", rpc.terminatedDescendants)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "turn/interrupt" {
		t.Fatalf("interrupt calls got %+v", rpc.calls)
	}
}

func TestCodexTurnStartParamsClearsServiceTier(t *testing.T) {
	standard := ""
	params := codexTurnStartParams("thread-1", []map[string]string{{"type": "text", "text": "hello"}}, ChatTurnOptions{ServiceTier: &standard})
	value, ok := params["serviceTier"]
	if !ok || value != nil {
		t.Fatalf("serviceTier should be explicit null, got %#v", params)
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

	res, err := b.Input(context.Background(), "codex", "thread-1", "hello", nil, nil, ChatTurnOptions{})
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

func TestUpdateCodexRolloutTaskStateTracksLatestTurnIncrementally(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	body := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-old"}}`,
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-old"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-live"}}`,
	}, "\n") + "\n" + `{"type":"event_msg","payload":{"type":"task_complete"`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	state := codexRolloutTaskState{}
	if err := updateCodexRolloutTaskState(testCodexRolloutStamp(t, path), &state); err != nil {
		t.Fatal(err)
	}
	if state.turnID != "turn-live" || state.terminal || state.status != "inProgress" {
		t.Fatalf("active state got %+v", state)
	}
	if state.offset <= 0 || state.offset >= int64(len(body)) {
		t.Fatalf("partial record changed offset to %d for %d bytes", state.offset, len(body))
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString(`,"turn_id":"turn-old"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-live"}}` + "\n")
	closeErr := f.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err := updateCodexRolloutTaskState(testCodexRolloutStamp(t, path), &state); err != nil {
		t.Fatal(err)
	}
	if state.turnID != "turn-live" || !state.terminal || state.status != "completed" {
		t.Fatalf("completed state got %+v", state)
	}

	if err := os.WriteFile(path, []byte(
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-new"}}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := updateCodexRolloutTaskState(testCodexRolloutStamp(t, path), &state); err != nil {
		t.Fatal(err)
	}
	if state.turnID != "turn-new" || state.terminal || state.status != "inProgress" {
		t.Fatalf("truncated rollout state got %+v", state)
	}
}

func TestChatEventFingerprintIgnoresAssistantUsageOnly(t *testing.T) {
	first := newChatEvent("assistant_done", "codex", "thread-1", "turn-1", "msg-1", map[string]interface{}{
		"text": "same answer", "usage": map[string]int{"inputTokens": 10},
	})
	second := newChatEvent("assistant_done", "codex", "thread-1", "turn-1", "msg-1", map[string]interface{}{
		"text": "same answer", "usage": map[string]int{"inputTokens": 20},
	})
	if chatEventFingerprint(first) != chatEventFingerprint(second) {
		t.Fatal("assistant usage changed the content fingerprint")
	}

	second.Data = json.RawMessage(`{"text":"updated answer","usage":{"inputTokens":20}}`)
	if chatEventFingerprint(first) == chatEventFingerprint(second) {
		t.Fatal("assistant text change did not change the content fingerprint")
	}

	usageA := newChatEvent("turn_usage", "codex", "thread-1", "turn-1", "", map[string]int{"inputTokens": 10})
	usageB := newChatEvent("turn_usage", "codex", "thread-1", "turn-1", "", map[string]int{"inputTokens": 20})
	if chatEventFingerprint(usageA) == chatEventFingerprint(usageB) {
		t.Fatal("turn usage change did not change its fingerprint")
	}
}

func TestCodexChatBackendReconcileConnectedSessionUsesRolloutLifecycle(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/turns/list"] = json.RawMessage(`{
		"data":[{"id":"turn-live","status":"interrupted","items":[]}]
	}`)
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[
		{"turnId":"turn-live","item":{"id":"reason-1","type":"reasoning","summary":["new thought"]}},
		{"turnId":"turn-old","item":{"id":"msg-old","type":"agentMessage","text":"old answer"}}
	]}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	ch := make(chan ChatEvent, 16)
	b.subs["thread-1"] = map[chan ChatEvent]struct{}{ch: {}}
	oldEvent, ok := projectCodexHistoryItemWithUsage("thread-1", "turn-old",
		json.RawMessage(`{"id":"msg-old","type":"agentMessage","text":"old answer"}`), codexTokenUsage{})
	if !ok {
		t.Fatal("old history item was not projected")
	}
	b.rememberSyncEvents("thread-1", []ChatEvent{oldEvent})

	state := codexRolloutTaskState{turnID: "turn-live", status: "inProgress"}
	completed, err := b.reconcileConnectedSession(context.Background(), rpc, "thread-1", state)
	if err != nil {
		t.Fatal(err)
	}
	if completed {
		t.Fatal("running turn reported complete")
	}
	first := receiveChatEvents(t, ch, 2)
	if first[0].Type != "turn_started" || first[0].TurnID != "turn-live" ||
		first[1].Type != "reasoning_update" || first[1].ItemID != "reason-1" {
		t.Fatalf("first reconciliation got %+v", first)
	}

	if _, err := b.reconcileConnectedSession(context.Background(), rpc, "thread-1", state); err != nil {
		t.Fatal(err)
	}
	assertNoChatEvent(t, ch)

	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[
		{"turnId":"turn-live","item":{"id":"reason-1","type":"reasoning","summary":["updated thought"]}},
		{"turnId":"turn-old","item":{"id":"msg-old","type":"agentMessage","text":"old answer"}}
	]}`)
	if _, err := b.reconcileConnectedSession(context.Background(), rpc, "thread-1", state); err != nil {
		t.Fatal(err)
	}
	updated := receiveChatEvents(t, ch, 1)
	if updated[0].Type != "reasoning_update" || !strings.Contains(string(updated[0].Data), "updated thought") {
		t.Fatalf("updated event got %+v", updated[0])
	}

	state.terminal = true
	state.status = "completed"
	completed, err = b.reconcileConnectedSession(context.Background(), rpc, "thread-1", state)
	if err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("completed turn kept synchronization running")
	}
	done := receiveChatEvents(t, ch, 1)
	if done[0].Type != "turn_done" || done[0].TurnID != "turn-live" {
		t.Fatalf("completion event got %+v", done[0])
	}
	if got := methods(rpc.calls); !reflect.DeepEqual(got, []string{
		"thread/items/list", "thread/items/list", "thread/items/list", "thread/items/list",
	}) {
		t.Fatalf("reconciliation used app-server turn status: %#v", got)
	}
}

func TestCodexChatBackendConnectedSyncStopsWithLastSubscriber(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[]}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.syncInterval = 5 * time.Millisecond
	rollout := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(rollout, []byte(
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-live"}}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b.rolloutStamp = func(string) (codexRolloutStamp, bool) {
		return testCodexRolloutStamp(t, rollout), true
	}

	ctx, cancel := context.WithCancel(context.Background())
	events, err := b.Events(ctx, "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	started := receiveChatEvents(t, events, 1)
	if started[0].Type != "turn_started" {
		t.Fatalf("sync start got %+v", started[0])
	}
	cancel()

	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		_, running := b.syncers["thread-1"]
		b.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connected sync did not stop after subscriber cancellation")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCodexChatBackendConnectedSyncSurvivesOtherSubscriberDisconnect(t *testing.T) {
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	if _, err := b.Events(firstCtx, "codex", "thread-1"); err != nil {
		t.Fatal(err)
	}
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	if _, err := b.Events(secondCtx, "codex", "thread-1"); err != nil {
		t.Fatal(err)
	}
	cancelFirst()

	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		subscriberCount := len(b.subs["thread-1"])
		_, running := b.syncers["thread-1"]
		b.mu.Unlock()
		if subscriberCount == 1 {
			if !running {
				t.Fatal("sync stopped while another subscriber remained connected")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first subscriber was not removed")
		}
		time.Sleep(time.Millisecond)
	}

	cancelSecond()
	deadline = time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		_, running := b.syncers["thread-1"]
		b.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sync did not stop after all subscribers disconnected")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCodexChatBackendRespondsToServerRequestID(t *testing.T) {
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

	if err := b.Respond(context.Background(), "codex", "thread-1", "42", json.RawMessage(`{"decision":"accept"}`)); err != nil {
		t.Fatal(err)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "response:42" ||
		!reflect.DeepEqual(rpc.calls[0].params, map[string]interface{}{"decision": "accept"}) {
		t.Fatalf("calls got %+v", rpc.calls)
	}
}

func TestCodexChatBackendRespondsToPermissionRequest(t *testing.T) {
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
		ID:     json.RawMessage(`43`),
		Method: "item/permissions/requestApproval",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"permission-1","cwd":"/repo","permissions":{"network":{"enabled":true}}}`),
	}
	select {
	case ev := <-events:
		if ev.Type != "interaction_request" {
			t.Fatalf("event got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("permission event timeout")
	}

	response := json.RawMessage(`{"permissions":{"network":{"enabled":true}},"scope":"session"}`)
	if err := b.Respond(context.Background(), "codex", "thread-1", "43", response); err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{
		"permissions": map[string]interface{}{"network": map[string]interface{}{"enabled": true}},
		"scope":       "session",
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "response:43" || !reflect.DeepEqual(rpc.calls[0].params, want) {
		t.Fatalf("calls got %#v", rpc.calls)
	}
}

func TestCodexChatBackendRespondsToUserInputAndElicitationRequests(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		params   string
		response string
		want     map[string]interface{}
	}{
		{
			name:     "user input",
			method:   "item/tool/requestUserInput",
			params:   `{"threadId":"thread-1","turnId":"turn-1","itemId":"ask-1","questions":[{"id":"branch","header":"Branch","question":"Which branch?"}]}`,
			response: `{"answers":{"branch":{"answers":["main"]}}}`,
			want: map[string]interface{}{
				"answers": map[string]interface{}{"branch": map[string]interface{}{"answers": []string{"main"}}},
			},
		},
		{
			name:     "MCP form elicitation",
			method:   "mcpServer/elicitation/request",
			params:   `{"threadId":"thread-1","turnId":"turn-1","serverName":"demo","mode":"form","message":"Details","requestedSchema":{"type":"object","required":["name"],"properties":{"name":{"type":"string"},"count":{"type":"integer"}}}}`,
			response: `{"action":"accept","content":{"name":"Fleet","count":2}}`,
			want: map[string]interface{}{
				"action":  "accept",
				"content": map[string]interface{}{"name": "Fleet", "count": float64(2)},
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
			requestID := fmt.Sprintf("%d", 50+index)
			rpc.notes <- rpcNotification{ID: json.RawMessage(requestID), Method: test.method, Params: json.RawMessage(test.params)}
			select {
			case <-events:
			case <-time.After(time.Second):
				t.Fatal("request event timeout")
			}
			if err := b.Respond(context.Background(), "codex", "thread-1", requestID, json.RawMessage(test.response)); err != nil {
				t.Fatal(err)
			}
			if len(rpc.calls) != 1 || rpc.calls[0].method != "response:"+requestID ||
				!reflect.DeepEqual(rpc.calls[0].params, test.want) {
				t.Fatalf("calls got %#v", rpc.calls)
			}
		})
	}
}

func TestCodexChatBackendReplaysPendingRequestToReconnectedEvents(t *testing.T) {
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	first, err := b.Events(firstCtx, "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	rpc.notes <- rpcNotification{
		ID:     json.RawMessage(`61`),
		Method: "item/fileChange/requestApproval",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"patch-1","reason":"write files"}`),
	}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("first request event timeout")
	}
	cancelFirst()

	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	second, err := b.Events(secondCtx, "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-second:
		if event.Type != "interaction_request" || !strings.Contains(string(event.Data), `"requestId":"61"`) {
			t.Fatalf("replayed event got %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("replayed request event timeout")
	}
}

func methods(calls []recordedCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.method)
	}
	return out
}

func receiveChatEvents(t *testing.T, ch <-chan ChatEvent, count int) []ChatEvent {
	t.Helper()
	events := make([]ChatEvent, 0, count)
	deadline := time.After(time.Second)
	for len(events) < count {
		select {
		case event, ok := <-ch:
			if !ok {
				t.Fatalf("event stream closed after %d/%d events", len(events), count)
			}
			events = append(events, event)
		case <-deadline:
			t.Fatalf("event timeout after %d/%d events", len(events), count)
		}
	}
	return events
}

func testCodexRolloutStamp(t *testing.T, path string) codexRolloutStamp {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return codexRolloutStamp{path: path, size: info.Size(), modTime: info.ModTime().UnixNano()}
}

func assertNoChatEvent(t *testing.T, ch <-chan ChatEvent) {
	t.Helper()
	select {
	case event := <-ch:
		t.Fatalf("unexpected duplicate event: %+v", event)
	case <-time.After(25 * time.Millisecond):
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
