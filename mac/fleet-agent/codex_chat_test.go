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
	responses             chan recordedCall
	reply                 map[string]json.RawMessage
	errs                  map[string][]error
	block                 map[string]bool
	terminatedDescendants int
}

func newFakeRPCConn() *fakeRPCConn {
	return &fakeRPCConn{
		notes:     make(chan rpcNotification, 8),
		responses: make(chan recordedCall, 128),
		reply:     map[string]json.RawMessage{},
		errs:      map[string][]error{},
		block:     map[string]bool{},
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
	call := recordedCall{method: "response:" + string(id), params: result}
	f.calls = append(f.calls, call)
	f.responses <- call
	return nil
}
func (f *fakeRPCConn) notifications() <-chan rpcNotification { return f.notes }
func (f *fakeRPCConn) terminateCommandDescendants()          { f.terminatedDescendants++ }

func TestCodexChatBackendStartUsesThreadStart(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/start"] = json.RawMessage(`{
		"thread":{"id":"thread-new","sessionId":"thread-new"},
		"cwd":"/repo",
		"model":"gpt-new",
		"reasoningEffort":"high",
		"serviceTier":"priority",
		"approvalPolicy":"on-request",
		"sandbox":{"type":"workspaceWrite"}
	}`)
	rpc.reply["model/list"] = json.RawMessage(`{"data":[{
		"id":"gpt-new-id","model":"gpt-new","displayName":"GPT New","isDefault":true,
		"defaultReasoningEffort":"high",
		"supportedReasoningEfforts":[{"reasoningEffort":"high","description":"Thorough"}],
		"serviceTiers":[{"id":"priority","name":"Fast","description":"Lower latency"}]
	}]}`)
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
	if res.Model != "gpt-new" || res.Effort != "high" || res.ServiceTier != "priority" ||
		res.ApprovalMode != "on-request" || len(res.Models) != 1 || res.Models[0].Value != "gpt-new" {
		t.Fatalf("start options missing: %+v", res)
	}
	if len(rpc.calls) != 2 || rpc.calls[0].method != "thread/start" || rpc.calls[1].method != "model/list" {
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
	if got := methods(rpc.calls); !reflect.DeepEqual(got, []string{"thread/start", "model/list", "turn/start", "thread/resume", "turn/start"}) {
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

func TestCodexChatBackendIsolatedResumeStaysReadOnly(t *testing.T) {
	previousCfg := cfg
	t.Cleanup(func() { cfg = previousCfg })
	cfg.CodexMode = "isolated"
	rpc := newFakeRPCConn()
	rpc.reply["thread/read"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[]}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	res, err := b.Resume(context.Background(), "codex", "thread-1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.WriterOwner != "" || b.loadedThreads["thread-1"] {
		t.Fatalf("read-only resume claimed writer: result=%+v loaded=%v", res, b.loadedThreads["thread-1"])
	}
	if got := methods(rpc.calls); !reflect.DeepEqual(got, []string{"thread/read", "thread/items/list", "model/list"}) {
		t.Fatalf("isolated resume must not call thread/resume: %v", got)
	}
}

func TestCodexChatBackendIsolatedResumeIgnoresActiveRolloutWithoutWriter(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
	})
	cfg.CodexMode = "isolated"
	cfg.CodexHome = t.TempDir()
	codexThreadWriterProcessOwner = func(string) string { return "" }
	sessionID := "11111111-2222-4333-8444-777777777777"
	dir := filepath.Join(cfg.CodexHome, "sessions", "2026", "08", "13")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-08-13T00-00-00-"+sessionID+".jsonl"), []byte(
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-orphan"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rpc := newFakeRPCConn()
	rpc.reply["thread/read"] = json.RawMessage(`{"thread":{"id":"` + sessionID + `"}}`)
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[]}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.lastTurn[sessionID] = "turn-orphan"
	b.turnOwners[sessionID] = "desktop"
	b.writerOwners[sessionID] = "desktop"

	res, err := b.Resume(context.Background(), "codex", sessionID, "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "idle" || res.ActiveTurnID != "" || res.TurnOwner != "" || res.WriterOwner != "" {
		t.Fatalf("read-only resume revived orphaned writer: %+v", res)
	}
	if b.lastTurn[sessionID] != "" || b.turnOwners[sessionID] != "" || b.writerOwners[sessionID] != "" {
		t.Fatalf("read-only resume retained orphaned cache: turn=%q owner=%q writer=%q",
			b.lastTurn[sessionID], b.turnOwners[sessionID], b.writerOwners[sessionID])
	}
}

func TestCodexChatBackendIsolatedResumePreservesCurrentFleetLeaseWithoutLockFile(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
	})
	cfg.CodexMode = "isolated"
	cfg.CodexHome = t.TempDir()
	codexThreadWriterProcessOwner = func(string) string { return "" }
	sessionID := "11111111-2222-4333-8444-888888888888"
	dir := filepath.Join(cfg.CodexHome, "sessions", "2026", "08", "13")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-08-13T00-00-00-"+sessionID+".jsonl"), []byte(
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-fresh"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rpc := newFakeRPCConn()
	rpc.reply["thread/read"] = json.RawMessage(`{"thread":{"id":"` + sessionID + `"}}`)
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[]}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.loadedThreads[sessionID] = true
	b.lastTurn[sessionID] = "turn-fresh"
	b.turnOwners[sessionID] = "fleet"
	b.writerOwners[sessionID] = "fleet"

	res, err := b.Resume(context.Background(), "codex", sessionID, "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "running" || res.ActiveTurnID != "turn-fresh" || res.TurnOwner != "fleet" || res.WriterOwner != "fleet" {
		t.Fatalf("read-only resume discarded the current Fleet lease: %+v", res)
	}
}

func TestCodexWriterProcessClassifierTreatsAnyNonFleetLockHolderAsExternal(t *testing.T) {
	fleetSocket := "/Users/test/.macfleet/codex-app-server.sock"
	for _, command := range []string{
		"/Applications/ChatGPT.app/Contents/Resources/codex -c features.code_mode_host=true app-server --analytics-default-enabled",
		"/opt/homebrew/bin/codex app-server",
	} {
		if got := codexWriterProcessCommandOwner(command, fleetSocket); got != "desktop" {
			t.Fatalf("command %q owner=%q", command, got)
		}
	}
	fleet := "/Users/test/.codex/packages/standalone/current/codex app-server --remote-control --listen unix://" + fleetSocket
	if got := codexWriterProcessCommandOwner(fleet, fleetSocket); got != "fleet" {
		t.Fatalf("Fleet sidecar owner=%q", got)
	}
}

func TestCodexRolloutProcessClassifierIgnoresFleetReaderButFindsExternalCLI(t *testing.T) {
	fleetSocket := "/Users/test/.macfleet/codex-app-server.sock"
	fleet := "/Users/test/.codex/packages/standalone/current/codex app-server --remote-control --listen unix://" + fleetSocket
	external := "/Users/test/.codex/packages/standalone/current/codex exec resume thread-1"
	if got := codexProcessCommandsOwner([]string{fleet}, fleetSocket, false); got != "" {
		t.Fatalf("Fleet rollout reader was treated as a writer: %q", got)
	}
	if got := codexProcessCommandsOwner([]string{fleet, external}, fleetSocket, false); got != "desktop" {
		t.Fatalf("external CLI rollout holder owner=%q", got)
	}
	if got := codexProcessCommandsOwner([]string{fleet}, fleetSocket, true); got != "fleet" {
		t.Fatalf("Fleet lock holder owner=%q", got)
	}
}

func TestCodexControlReportsIdleDesktopWriterFromPhysicalLock(t *testing.T) {
	previousOwner := codexThreadWriterProcessOwner
	previousState := codexActiveRolloutTaskState
	t.Cleanup(func() {
		codexThreadWriterProcessOwner = previousOwner
		codexActiveRolloutTaskState = previousState
	})
	codexThreadWriterProcessOwner = func(string) string { return "desktop" }
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) { return codexRolloutTaskState{}, false }
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return newFakeRPCConn(), func() {}, nil })

	got, err := b.Control(context.Background(), "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "idle" || got.WriterOwner != "desktop" || got.TurnOwner != "" || got.ActiveTurnID != "" {
		t.Fatalf("physical Desktop lock was not authoritative: %+v", got)
	}
}

func TestCodexControlClearsCachedTurnWhenRolloutIsTerminal(t *testing.T) {
	previousOwner := codexThreadWriterProcessOwner
	previousState := codexActiveRolloutTaskState
	t.Cleanup(func() {
		codexThreadWriterProcessOwner = previousOwner
		codexActiveRolloutTaskState = previousState
	})
	codexThreadWriterProcessOwner = func(string) string { return "" }
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) {
		return codexRolloutTaskState{turnID: "turn-done", terminal: true}, true
	}
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return newFakeRPCConn(), func() {}, nil })
	b.lastTurn["thread-1"] = "turn-done"
	b.turnOwners["thread-1"] = "fleet"
	b.writerOwners["thread-1"] = "fleet"

	got, err := b.Control(context.Background(), "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "idle" || got.WriterOwner != "" || got.TurnOwner != "" || got.ActiveTurnID != "" {
		t.Fatalf("terminal rollout did not clear cached control state: %+v", got)
	}
}

func TestCodexControlClearsActiveRolloutWithoutPhysicalWriterInIsolatedMode(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	previousState := codexActiveRolloutTaskState
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
		codexActiveRolloutTaskState = previousState
	})
	cfg.CodexMode = "isolated"
	codexThreadWriterProcessOwner = func(string) string { return "" }
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) {
		return codexRolloutTaskState{turnID: "turn-orphan", terminal: false}, true
	}
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return newFakeRPCConn(), func() {}, nil })
	b.lastTurn["thread-1"] = "turn-orphan"
	b.turnOwners["thread-1"] = "desktop"
	b.writerOwners["thread-1"] = "desktop"

	got, err := b.Control(context.Background(), "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "idle" || got.WriterOwner != "" || got.TurnOwner != "" || got.ActiveTurnID != "" {
		t.Fatalf("active rollout without a physical writer remained authoritative: %+v", got)
	}
	if b.lastTurn["thread-1"] != "" || b.turnOwners["thread-1"] != "" || b.writerOwners["thread-1"] != "" {
		t.Fatalf("orphaned external ownership remained cached: turn=%q owner=%q writer=%q",
			b.lastTurn["thread-1"], b.turnOwners["thread-1"], b.writerOwners["thread-1"])
	}
}

func TestCodexControlPreservesCurrentFleetLeaseWithoutPhysicalLock(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	previousState := codexActiveRolloutTaskState
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
		codexActiveRolloutTaskState = previousState
	})
	cfg.CodexMode = "isolated"
	codexThreadWriterProcessOwner = func(string) string { return "" }
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) {
		return codexRolloutTaskState{turnID: "turn-fresh", terminal: false}, true
	}
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return newFakeRPCConn(), func() {}, nil })
	b.loadedThreads["thread-1"] = true
	b.lastTurn["thread-1"] = "turn-fresh"
	b.turnOwners["thread-1"] = "fleet"
	b.writerOwners["thread-1"] = "fleet"

	got, err := b.Control(context.Background(), "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" || got.WriterOwner != "fleet" || got.TurnOwner != "fleet" || got.ActiveTurnID != "turn-fresh" {
		t.Fatalf("current Fleet lease without a lock file was discarded: %+v", got)
	}
}

func TestCodexControlPreservesFleetTurnOwnershipInSharedDaemon(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	previousState := codexActiveRolloutTaskState
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
		codexActiveRolloutTaskState = previousState
	})
	cfg.CodexMode = "shared"
	codexThreadWriterProcessOwner = func(string) string { return "desktop" }
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) {
		return codexRolloutTaskState{turnID: "turn-live", terminal: false}, true
	}
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return newFakeRPCConn(), func() {}, nil })
	b.lastTurn["thread-1"] = "turn-live"
	b.turnOwners["thread-1"] = "fleet"
	b.writerOwners["thread-1"] = "fleet"

	got, err := b.Control(context.Background(), "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" || got.WriterOwner != "fleet" || got.TurnOwner != "fleet" || got.ActiveTurnID != "turn-live" {
		t.Fatalf("shared daemon process identity overrode logical Fleet ownership: %+v", got)
	}
}

func TestCodexChatBackendIsolatedInputWaitsForDesktopWriterThenClaims(t *testing.T) {
	previousCfg := cfg
	t.Cleanup(func() { cfg = previousCfg })
	cfg.CodexMode = "isolated"
	rpc := newFakeRPCConn()
	rpc.errs["thread/resume"] = []error{errors.New("thread thread-1 already has an active writer")}
	rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-fleet"}}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	if _, err := b.Input(context.Background(), "codex", "thread-1", "queued", nil, nil, ChatTurnOptions{}); !errors.Is(err, errExternalChatTurn) {
		t.Fatalf("Desktop writer conflict got %v", err)
	}
	res, err := b.Input(context.Background(), "codex", "thread-1", "queued", nil, nil, ChatTurnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TurnID != "turn-fleet" || b.writerOwners["thread-1"] != "fleet" {
		t.Fatalf("Fleet did not claim released writer: result=%+v owner=%q", res, b.writerOwners["thread-1"])
	}
	if got := methods(rpc.calls); !reflect.DeepEqual(got, []string{"thread/resume", "thread/resume", "turn/start"}) {
		t.Fatalf("queued retry calls got %v", got)
	}
}

func TestCodexChatBackendRestoresFleetOwnedTurnAfterAgentRestart(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	t.Cleanup(func() { cfg = previousCfg; codexThreadWriterProcessOwner = previousOwner })
	cfg.CodexMode = "isolated"
	codexThreadWriterProcessOwner = func(sessionID string) string {
		if sessionID == "thread-1" {
			return "fleet"
		}
		return ""
	}
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-next"}}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	if !b.restoreFleetTurnOwner("thread-1", "turn-live") {
		t.Fatal("Fleet sidecar lock was not restored")
	}
	if b.lastTurn["thread-1"] != "turn-live" || b.turnOwners["thread-1"] != "fleet" || b.writerOwners["thread-1"] != "fleet" {
		t.Fatalf("ownership not restored: last=%q turn=%q writer=%q", b.lastTurn["thread-1"], b.turnOwners["thread-1"], b.writerOwners["thread-1"])
	}
}

func TestCodexChatBackendIsolatedTurnCompletionReleasesWriter(t *testing.T) {
	previousCfg := cfg
	t.Cleanup(func() { cfg = previousCfg })
	cfg.CodexMode = "isolated"
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	if _, err := b.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.loadedThreads["thread-1"] = true
	b.writerOwners["thread-1"] = "fleet"
	b.lastTurn["thread-1"] = "turn-1"
	b.turnOwners["thread-1"] = "fleet"
	b.mu.Unlock()
	rpc.notes <- rpcNotification{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}`)}

	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		loaded := b.loadedThreads["thread-1"]
		owner := b.writerOwners["thread-1"]
		b.mu.Unlock()
		if !loaded && owner == "" && reflect.DeepEqual(methods(rpc.calls), []string{"thread/unsubscribe"}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Fleet writer not released: loaded=%v owner=%q calls=%v", loaded, owner, methods(rpc.calls))
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCodexChatBackendReapsIdleFleetWriterWithoutBrowserLifecycle(t *testing.T) {
	previousCfg := cfg
	previousState := codexActiveRolloutTaskState
	previousSessions := codexFleetWriterSessions
	t.Cleanup(func() {
		cfg = previousCfg
		codexActiveRolloutTaskState = previousState
		codexFleetWriterSessions = previousSessions
	})
	cfg.CodexMode = "isolated"
	codexFleetWriterSessions = func() []string { return []string{"thread-stale"} }
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) {
		return codexRolloutTaskState{turnID: "turn-done", terminal: true}, true
	}
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	if _, err := b.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.loadedThreads["thread-stale"] = true
	b.writerOwners["thread-stale"] = "fleet"
	b.lastTurn["thread-stale"] = "turn-done"
	b.turnOwners["thread-stale"] = "fleet"
	b.mu.Unlock()

	b.reapIdleFleetWriters()

	b.mu.Lock()
	loaded := b.loadedThreads["thread-stale"]
	owner := b.writerOwners["thread-stale"]
	b.mu.Unlock()
	if loaded || owner != "" || !reflect.DeepEqual(methods(rpc.calls), []string{"thread/unsubscribe"}) {
		t.Fatalf("stale Fleet writer not reaped: loaded=%v owner=%q calls=%v", loaded, owner, methods(rpc.calls))
	}
}

func TestCodexChatBackendReleaseInterruptsActiveFleetTurnBeforeUnsubscribe(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	previousState := codexActiveRolloutTaskState
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
		codexActiveRolloutTaskState = previousState
	})
	cfg.CodexMode = "isolated"
	codexThreadWriterProcessOwner = func(string) string { return "fleet" }
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) {
		return codexRolloutTaskState{turnID: "turn-live", terminal: false}, true
	}
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	if _, err := b.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.loadedThreads["thread-live"] = true
	b.writerOwners["thread-live"] = "fleet"
	b.lastTurn["thread-live"] = "turn-live"
	b.turnOwners["thread-live"] = "fleet"
	b.mu.Unlock()

	if err := b.Release(context.Background(), "codex", "thread-live"); err != nil {
		t.Fatal(err)
	}
	if got := methods(rpc.calls); !reflect.DeepEqual(got, []string{"turn/interrupt", "thread/unsubscribe"}) {
		t.Fatalf("release call order = %v", got)
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

func TestCodexChatOptionsFromRolloutUsesLatestTurnContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	body := strings.Join([]string{
		`{"type":"turn_context","payload":{"model":"gpt-old","effort":"medium","service_tier":"default"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"type":"turn_context","payload":{"model":"gpt-new","effort":"high","service_tier":"priority"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	model, effort, tier := codexChatOptionsFromRollout(path)
	if model != "gpt-new" || effort != "high" || tier != "priority" {
		t.Fatalf("latest rollout options got model=%q effort=%q tier=%q", model, effort, tier)
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

func TestCodexCodeModeToolItemsRecoverNestedCommandsAndImageViews(t *testing.T) {
	input := `const results = await Promise.all([
	tools.exec_command({
		cmd: "sed -n '1,80p' docs/brand.md",
		workdir: "/Users/test/repo",
		yield_time_ms: 10000
	}),
	tools.exec_command({
		cmd: "sips -g pixelWidth /tmp/avatar.png",
		workdir: "/Users/test/repo"
	}),
	tools.view_image({path:"/tmp/avatar.png",detail:"original"}),
	tools.apply_patch("*** Begin Patch\n*** End Patch")
]);`

	items := codexCodeModeToolItems("ctc-1", input, "completed")
	if len(items) != 3 {
		t.Fatalf("recovered items got %d, want 3: %s", len(items), items)
	}
	var first struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(items[0], &first); err != nil {
		t.Fatal(err)
	}
	if first.ID != "ctc-1:exec_command:1" || first.Type != "commandExecution" ||
		first.Command != "sed -n '1,80p' docs/brand.md" || first.Cwd != "/Users/test/repo" || first.Status != "completed" {
		t.Fatalf("first command got %+v", first)
	}
	var image struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(items[2], &image); err != nil {
		t.Fatal(err)
	}
	if image.ID != "ctc-1:view_image:3" || image.Type != "imageView" || image.Path != "/tmp/avatar.png" {
		t.Fatalf("image view got %+v", image)
	}
}

func TestMergeCodexRolloutToolsKeepsCommentaryBoundaries(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"id":"u1","type":"userMessage","content":[{"type":"text","text":"request"}]}`),
		json.RawMessage(`{"id":"r1","type":"reasoning","summary":["before"]}`),
		json.RawMessage(`{"id":"a1","type":"agentMessage","text":"first commentary"}`),
		json.RawMessage(`{"id":"r2","type":"reasoning","summary":["between"]}`),
		json.RawMessage(`{"id":"d1","type":"fileChange","changes":[]}`),
		json.RawMessage(`{"id":"a2","type":"agentMessage","text":"second commentary"}`),
	}
	markers := []codexRolloutTurnMarker{
		{AssistantText: "first commentary"},
		{Item: json.RawMessage(`{"id":"cmd1","type":"commandExecution","command":"pwd"}`)},
		{Item: json.RawMessage(`{"id":"img1","type":"imageView","path":"/tmp/shot.png"}`)},
		{AssistantText: "second commentary"},
	}

	merged := mergeCodexRolloutTools(items, markers)
	got := make([]string, 0, len(merged))
	for _, raw := range merged {
		var item struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatal(err)
		}
		got = append(got, item.Type)
	}
	want := []string{"userMessage", "reasoning", "agentMessage", "commandExecution", "imageView", "reasoning", "fileChange", "agentMessage"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged types got %#v want %#v", got, want)
	}
}

func TestCodexRolloutToolCacheExtendsWithoutDuplicatingHistory(t *testing.T) {
	previousHome := cfg.CodexHome
	t.Cleanup(func() { cfg.CodexHome = previousHome })
	cfg.CodexHome = t.TempDir()
	sessionID := "019fb126-9cb0-7f70-8d3f-0248b4bb97c5"
	turnID := "turn-1"
	dir := filepath.Join(cfg.CodexHome, "sessions", "2026", "07", "30")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-07-30T03-52-05-"+sessionID+".jsonl")
	first := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"message","id":"m1","role":"assistant","phase":"commentary","internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"},"content":[{"type":"output_text","text":"first commentary"}]}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","id":"ctc-1","name":"exec","status":"completed","input":"const r = await tools.exec_command({cmd: \"pwd\", workdir: \"/repo\"});","internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(first), 0644); err != nil {
		t.Fatal(err)
	}

	page := codexTurnsPage{Data: []codexHistoryTurn{{ID: turnID, Items: []json.RawMessage{
		json.RawMessage(`{"id":"u1","type":"userMessage","content":[{"type":"text","text":"request"}]}`),
		json.RawMessage(`{"id":"a1","type":"agentMessage","text":"first commentary"}`),
		json.RawMessage(`{"id":"r1","type":"reasoning","summary":["between"]}`),
		json.RawMessage(`{"id":"a2","type":"agentMessage","text":"second commentary"}`),
	}}}}
	b := newCodexChatBackend(nil)
	firstPage := b.enrichCodexTurnsPage(sessionID, page)
	if got := codexTurnItemTypes(t, firstPage.Data[0].Items); !reflect.DeepEqual(got,
		[]string{"userMessage", "agentMessage", "commandExecution", "reasoning", "agentMessage"}) {
		t.Fatalf("first enriched page got %#v", got)
	}
	firstOffset := b.rolloutTools[sessionID].offset

	appendBody := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"custom_tool_call","id":"ctc-2","name":"exec","status":"completed","input":"const r = await tools.view_image({path: \"/tmp/shot.png\", detail: \"original\"});","internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}}`,
		`{"type":"response_item","payload":{"type":"message","id":"m2","role":"assistant","phase":"commentary","internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"},"content":[{"type":"output_text","text":"second commentary"}]}}`,
	}, "\n") + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(appendBody); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	secondPage := b.enrichCodexTurnsPage(sessionID, page)
	if got := codexTurnItemTypes(t, secondPage.Data[0].Items); !reflect.DeepEqual(got,
		[]string{"userMessage", "agentMessage", "commandExecution", "imageView", "reasoning", "agentMessage"}) {
		t.Fatalf("incremental enriched page got %#v", got)
	}
	if b.rolloutTools[sessionID].offset <= firstOffset {
		t.Fatalf("rollout offset did not advance: first=%d second=%d", firstOffset, b.rolloutTools[sessionID].offset)
	}
}

func codexTurnItemTypes(t *testing.T, items []json.RawMessage) []string {
	t.Helper()
	types := make([]string, 0, len(items))
	for _, raw := range items {
		var item struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatal(err)
		}
		types = append(types, item.Type)
	}
	return types
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

func TestCodexChatBackendOperationsRecoverFleetOwnershipBeforeRejecting(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	previousState := codexActiveRolloutTaskState
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
		codexActiveRolloutTaskState = previousState
	})
	cfg.CodexMode = "isolated"
	codexThreadWriterProcessOwner = func(string) string { return "fleet" }
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) {
		return codexRolloutTaskState{turnID: "turn-live"}, true
	}
	rpc := newFakeRPCConn()
	rpc.reply["thread/settings/update"] = json.RawMessage(`{}`)
	rpc.reply["turn/steer"] = json.RawMessage(`{"turnId":"turn-live"}`)
	rpc.reply["turn/interrupt"] = json.RawMessage(`{}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"settings", func() error { return b.Settings(context.Background(), "codex", "thread-1", "full-access") }},
		{"steer", func() error {
			_, err := b.Steer(context.Background(), "codex", "thread-1", "client-1", "next", nil, nil)
			return err
		}},
		{"interrupt", func() error { return b.Interrupt(context.Background(), "codex", "thread-1") }},
	} {
		b.mu.Lock()
		b.lastTurn["thread-1"] = "turn-live"
		b.turnOwners["thread-1"] = "desktop"
		b.writerOwners["thread-1"] = "desktop"
		b.mu.Unlock()
		if err := operation.run(); errors.Is(err, errExternalChatTurn) {
			t.Fatalf("%s rejected Fleet-owned turn as Desktop: %v", operation.name, err)
		}
	}
}

func TestCodexChatBackendPhysicalDesktopOwnerOverridesStaleFleetCache(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	previousState := codexActiveRolloutTaskState
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
		codexActiveRolloutTaskState = previousState
	})
	cfg.CodexMode = "isolated"
	codexThreadWriterProcessOwner = func(string) string { return "desktop" }
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) {
		return codexRolloutTaskState{turnID: "turn-external"}, true
	}
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.lastTurn["thread-1"] = "turn-external"
	b.turnOwners["thread-1"] = "fleet"
	b.writerOwners["thread-1"] = "fleet"

	_, err := b.Steer(context.Background(), "codex", "thread-1", "client-1", "must not cross", nil, nil)
	if !errors.Is(err, errExternalChatTurn) {
		t.Fatalf("physical Desktop owner did not override stale Fleet cache: %v", err)
	}
	if len(rpc.calls) != 0 {
		t.Fatalf("external turn was steered through Fleet sidecar: %v", methods(rpc.calls))
	}
}

func TestCodexChatBackendFullAccessAutoApprovesCurrentTurnRequests(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/settings/update"] = json.RawMessage(`{}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := b.Events(ctx, "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Settings(context.Background(), "codex", "thread-1", "full-access"); err != nil {
		t.Fatal(err)
	}

	rpc.notes <- rpcNotification{
		ID:     json.RawMessage(`42`),
		Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"cmd1","command":"git push"}`),
	}
	select {
	case call := <-rpc.responses:
		if call.method != "response:42" ||
			!reflect.DeepEqual(call.params, map[string]interface{}{"decision": "accept"}) {
			t.Fatalf("response got %#v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("auto-approval response timeout")
	}
	select {
	case ev := <-events:
		t.Fatalf("full access request leaked to UI: %+v", ev)
	case <-time.After(20 * time.Millisecond):
	}
	b.mu.Lock()
	_, pending := b.pending["42"]
	b.mu.Unlock()
	if pending {
		t.Fatal("auto-approved request remained pending")
	}
}

func TestCodexChatBackendLeavingFullAccessStopsAutoApproval(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/settings/update"] = json.RawMessage(`{}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := b.Events(ctx, "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Settings(context.Background(), "codex", "thread-1", "full-access"); err != nil {
		t.Fatal(err)
	}
	if err := b.Settings(context.Background(), "codex", "thread-1", "on-request"); err != nil {
		t.Fatal(err)
	}

	rpc.notes <- rpcNotification{
		ID:     json.RawMessage(`44`),
		Method: "item/fileChange/requestApproval",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"patch1"}`),
	}
	select {
	case ev := <-events:
		if ev.Type != "interaction_request" {
			t.Fatalf("event got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("approval event timeout")
	}
	select {
	case call := <-rpc.responses:
		t.Fatalf("on-request mode unexpectedly auto-approved: %#v", call)
	default:
	}
}

func TestCodexChatBackendFullAccessResolvesAlreadyPendingRequest(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/settings/update"] = json.RawMessage(`{}`)
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
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"permission-1","permissions":{"network":{"enabled":true}}}`),
	}
	select {
	case ev := <-events:
		if ev.Type != "interaction_request" {
			t.Fatalf("event got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("pending approval event timeout")
	}

	if err := b.Settings(context.Background(), "codex", "thread-1", "full-access"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Type != "interaction_resolved" {
			t.Fatalf("event got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("auto-resolved approval event timeout")
	}
	want := map[string]interface{}{
		"permissions": map[string]interface{}{"network": map[string]interface{}{"enabled": true}},
		"scope":       "session",
	}
	last := rpc.calls[len(rpc.calls)-1]
	if rpc.calls[0].method != "thread/resume" || last.method != "response:43" || !reflect.DeepEqual(last.params, want) {
		t.Fatalf("calls got %#v", rpc.calls)
	}
	b.mu.Lock()
	_, pending := b.pending["43"]
	b.mu.Unlock()
	if pending {
		t.Fatal("resolved request remained pending")
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

func TestCodexChatBackendSteerReconcilesRemoteNoActiveTurn(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.errs["turn/steer"] = []error{&rpcCallError{
		Method: "turn/steer", Code: -32001, Message: "no active turn to steer",
	}}
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.lastTurn["thread-1"] = "turn-old"
	events := make(chan ChatEvent, 1)
	b.subs["thread-1"] = map[chan ChatEvent]struct{}{events: {}}

	_, err := b.Steer(context.Background(), "codex", "thread-1", "follow-1", "continue", nil, nil)
	if !errors.Is(err, errNoActiveChatTurn) {
		t.Fatalf("steer error got %v", err)
	}
	if b.lastTurn["thread-1"] != "" {
		t.Fatalf("stale turn remained: %#v", b.lastTurn)
	}
	select {
	case event := <-events:
		if event.Type != "thread_status" || !strings.Contains(string(event.Data), `"reconciledTurnId":"turn-old"`) {
			t.Fatalf("reconciliation event got %+v", event)
		}
	default:
		t.Fatal("missing inactive-turn reconciliation event")
	}
}

func TestCodexChatBackendInactiveReconciliationPreservesNewTurn(t *testing.T) {
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return newFakeRPCConn(), func() {}, nil
	})
	b.lastTurn["thread-1"] = "turn-new"

	if b.reconcileInactiveTurn("thread-1", "turn-old") {
		t.Fatal("stale reconciliation unexpectedly succeeded")
	}
	if b.lastTurn["thread-1"] != "turn-new" {
		t.Fatalf("new turn was cleared: %#v", b.lastTurn)
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

func TestCodexChatBackendInterruptReconcilesRemoteNoActiveTurn(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.errs["turn/interrupt"] = []error{errors.New("codex app-server turn/interrupt failed: no active turn to interrupt")}
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.lastTurn["thread-1"] = "turn-old"

	err := b.Interrupt(context.Background(), "codex", "thread-1")
	if !errors.Is(err, errNoActiveChatTurn) {
		t.Fatalf("interrupt error got %v", err)
	}
	if b.lastTurn["thread-1"] != "" {
		t.Fatalf("stale turn remained: %#v", b.lastTurn)
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

func TestCodexChatBackendInterruptPreservesOtherTurnDescendants(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["turn/interrupt"] = json.RawMessage(`{}`)
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.lastTurn["thread-1"] = "turn-1"
	b.lastTurn["thread-2"] = "turn-2"

	if err := b.Interrupt(context.Background(), "codex", "thread-1"); err != nil {
		t.Fatal(err)
	}
	if rpc.terminatedDescendants != 0 {
		t.Fatalf("another active turn's descendants were terminated: %d cleanup calls", rpc.terminatedDescendants)
	}
}

func TestCodexChatBackendPublicationDisconnectsSlowSubscriber(t *testing.T) {
	publishers := []struct {
		name string
		run  func(*codexChatBackend, ChatEvent)
	}{
		{name: "live", run: func(b *codexChatBackend, event ChatEvent) { b.publish(event) }},
		{name: "sync", run: func(b *codexChatBackend, event ChatEvent) { b.publishSyncEvent(event) }},
	}
	for _, publisher := range publishers {
		t.Run(publisher.name, func(t *testing.T) {
			b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
				return newFakeRPCConn(), func() {}, nil
			})
			ch := make(chan ChatEvent, 1)
			ch <- newChatEvent("queued", "codex", "thread-1", "turn-1", "", nil)
			b.subs["thread-1"] = map[chan ChatEvent]struct{}{ch: {}}

			done := make(chan struct{})
			go func() {
				publisher.run(b, newChatEvent("next", "codex", "thread-1", "turn-1", "", nil))
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("publication blocked on a full subscriber")
			}

			b.mu.Lock()
			_, stillSubscribed := b.subs["thread-1"][ch]
			removedTwice := b.removeSubscriberLocked("thread-1", ch)
			b.mu.Unlock()
			if stillSubscribed || removedTwice {
				t.Fatalf("slow subscriber cleanup state: subscribed=%v removedTwice=%v", stillSubscribed, removedTwice)
			}
			<-ch // drain the event buffered before disconnection
			if _, open := <-ch; open {
				t.Fatal("slow subscriber channel remained open")
			}
		})
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

func TestAssistantDoneIdentityIgnoresSourceItemIDAndEnvelope(t *testing.T) {
	direct := withAssistantHistorySyncIDs([]ChatEvent{newChatEvent("assistant_done", "codex", "thread-1", "turn-1", "item-17", map[string]interface{}{
		"text":  "same commentary",
		"phase": "commentary",
	})})[0]
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return newFakeRPCConn(), func() {}, nil
	})
	nested := b.withLiveAssistantSyncID(newChatEvent("assistant_done", "codex", "thread-1", "turn-1", "msg-native", map[string]interface{}{
		"item":  map[string]interface{}{"id": "live-msg", "type": "agentMessage", "text": "same commentary"},
		"phase": "commentary",
	}))
	if chatEventSyncKey(direct) != chatEventSyncKey(nested) {
		t.Fatalf("same logical message got different keys: %q != %q", chatEventSyncKey(direct), chatEventSyncKey(nested))
	}
	if chatEventFingerprint(direct) != chatEventFingerprint(nested) {
		t.Fatal("same logical message got different fingerprints")
	}

	changed := nested
	changed.Data = json.RawMessage(`{"item":{"id":"live-msg","type":"agentMessage","text":"different commentary"}}`)
	changed.syncID = ""
	changed = withAssistantHistorySyncIDs([]ChatEvent{changed})[0]
	if chatEventSyncKey(direct) == chatEventSyncKey(changed) {
		t.Fatal("different assistant messages shared an identity")
	}
}

func TestProjectCodexHistoryPagePreservesRepeatedTextWithStableOccurrences(t *testing.T) {
	page := codexItemsPage{Data: []codexHistoryItemEntry{
		{TurnID: "turn-1", Item: json.RawMessage(`{"id":"history-msg","type":"agentMessage","text":"same commentary"}`)},
		{TurnID: "turn-1", Item: json.RawMessage(`{"id":"rollout-msg","type":"agentMessage","text":"same commentary"}`)},
		{TurnID: "turn-1", Item: json.RawMessage(`{"id":"other-msg","type":"agentMessage","text":"different commentary"}`)},
	}}
	history := projectCodexHistoryPage("thread-1", page, nil)
	if len(history.Events) != 3 {
		t.Fatalf("assistant history was collapsed: %+v", history.Events)
	}
	texts := make([]string, 0, len(history.Events))
	for _, event := range history.Events {
		texts = append(texts, chatEventAssistantText(event))
	}
	if !reflect.DeepEqual(texts, []string{"different commentary", "same commentary", "same commentary"}) {
		t.Fatalf("history order/content changed: %#v", texts)
	}
	if history.Events[1].syncID == history.Events[2].syncID || history.Events[1].syncID == "" || history.Events[2].syncID == "" {
		t.Fatalf("repeated messages did not get stable occurrences: %q %q", history.Events[1].syncID, history.Events[2].syncID)
	}
}

func TestPublishSyncEventSuppressesAssistantDuplicateFromAnotherSource(t *testing.T) {
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return newFakeRPCConn(), func() {}, nil
	})
	ch := make(chan ChatEvent, 1)
	b.subs["thread-1"] = map[chan ChatEvent]struct{}{ch: {}}
	history := withAssistantHistorySyncIDs([]ChatEvent{newChatEvent("assistant_done", "codex", "thread-1", "turn-1", "item-17", map[string]interface{}{
		"text":  "same commentary",
		"phase": "commentary",
	})})[0]
	live := b.withLiveAssistantSyncID(newChatEvent("assistant_done", "codex", "thread-1", "turn-1", "msg-native", map[string]interface{}{
		"item":  map[string]interface{}{"id": "live-msg", "type": "agentMessage", "text": "same commentary"},
		"phase": "commentary",
	}))
	b.rememberSyncEvents("thread-1", []ChatEvent{history})
	b.publishSyncEvent(live)
	assertNoChatEvent(t, ch)
}

func TestDispatchPreservesDistinctRepeatedAssistantCompletions(t *testing.T) {
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	ch := make(chan ChatEvent, 2)
	b.subs["thread-1"] = map[chan ChatEvent]struct{}{ch: {}}
	for _, itemID := range []string{"event-msg", "response-item"} {
		params := fmt.Sprintf(`{"threadId":"thread-1","turnId":"turn-1","item":{"id":%q,"type":"agentMessage","text":"same commentary"}}`, itemID)
		rpc.notes <- rpcNotification{Method: "item/completed", Params: json.RawMessage(params)}
	}
	close(rpc.notes)
	b.dispatch(rpc, rpc.notes)

	events := receiveChatEvents(t, ch, 2)
	for _, event := range events {
		if event.Type != "assistant_done" || chatEventAssistantText(event) != "same commentary" {
			t.Fatalf("assistant completion got %+v", event)
		}
	}
	assertNoChatEvent(t, ch)
}

func TestFinishResumeDropsBufferedAssistantAlreadyPresentInHistory(t *testing.T) {
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return newFakeRPCConn(), func() {}, nil
	})
	b.beginResume("thread-1")
	b.buffered["thread-1"] = []ChatEvent{
		b.withLiveAssistantSyncID(newChatEvent("assistant_done", "codex", "thread-1", "turn-1", "live-msg", map[string]interface{}{
			"item": map[string]interface{}{"text": "same commentary"},
		})),
		b.withLiveAssistantSyncID(newChatEvent("assistant_done", "codex", "thread-1", "turn-1", "other-msg", map[string]interface{}{
			"item": map[string]interface{}{"text": "different commentary"},
		})),
	}
	b.rememberSyncEvents("thread-1", withAssistantHistorySyncIDs([]ChatEvent{
		newChatEvent("assistant_done", "codex", "thread-1", "turn-1", "history-msg", map[string]interface{}{
			"text": "same commentary",
		}),
	}))
	b.finishResume("thread-1")

	if len(b.backlog["thread-1"]) != 1 || chatEventAssistantText(b.backlog["thread-1"][0]) != "different commentary" {
		t.Fatalf("resume backlog retained duplicate: %+v", b.backlog["thread-1"])
	}
}

func TestCodexChatBackendReconcileConnectedSessionUsesRolloutLifecycle(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
	})
	cfg.CodexMode = "isolated"
	codexThreadWriterProcessOwner = func(string) string { return "desktop" }
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

func TestCodexChatBackendConnectedSyncDoesNotReviveOrphanedRollout(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
	})
	cfg.CodexMode = "isolated"
	codexThreadWriterProcessOwner = func(string) string { return "" }
	rpc := newFakeRPCConn()
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[]}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	ch := make(chan ChatEvent, 2)
	b.subs["thread-1"] = map[chan ChatEvent]struct{}{ch: {}}
	b.lastTurn["thread-1"] = "turn-orphan"
	b.turnOwners["thread-1"] = "desktop"
	b.writerOwners["thread-1"] = "desktop"

	completed, err := b.reconcileConnectedSession(context.Background(), rpc, "thread-1", codexRolloutTaskState{
		turnID: "turn-orphan", status: "inProgress",
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed {
		t.Fatal("orphaned nonterminal rollout should keep polling for a future terminal append")
	}
	if b.lastTurn["thread-1"] != "" || b.turnOwners["thread-1"] != "" || b.writerOwners["thread-1"] != "" {
		t.Fatalf("connected sync revived orphaned writer: turn=%q owner=%q writer=%q",
			b.lastTurn["thread-1"], b.turnOwners["thread-1"], b.writerOwners["thread-1"])
	}
	event := receiveChatEvents(t, ch, 1)[0]
	if event.Type != "thread_status" || !strings.Contains(string(event.Data), `"releaseReason":"missing_writer_lease"`) {
		t.Fatalf("connected sync orphan event=%+v", event)
	}
	assertNoChatEvent(t, ch)
}

func TestCodexChatBackendConnectedSyncPreservesCurrentFleetLeaseWithoutLockFile(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
	})
	cfg.CodexMode = "isolated"
	codexThreadWriterProcessOwner = func(string) string { return "" }
	rpc := newFakeRPCConn()
	rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[]}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.loadedThreads["thread-1"] = true
	b.lastTurn["thread-1"] = "turn-fresh"
	b.turnOwners["thread-1"] = "fleet"
	b.writerOwners["thread-1"] = "fleet"

	completed, err := b.reconcileConnectedSession(context.Background(), rpc, "thread-1", codexRolloutTaskState{
		turnID: "turn-fresh", status: "inProgress",
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed || !b.loadedThreads["thread-1"] || b.lastTurn["thread-1"] != "turn-fresh" ||
		b.turnOwners["thread-1"] != "fleet" || b.writerOwners["thread-1"] != "fleet" {
		t.Fatalf("connected sync discarded current Fleet lease: completed=%v loaded=%v turn=%q owner=%q writer=%q",
			completed, b.loadedThreads["thread-1"], b.lastTurn["thread-1"], b.turnOwners["thread-1"], b.writerOwners["thread-1"])
	}
}

func TestCodexChatBackendLastBrowserDisconnectDoesNotReleaseWriter(t *testing.T) {
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
	waitForCodexSyncStop(t, b, "thread-1")
	for _, call := range rpc.calls {
		if call.method == "thread/unsubscribe" {
			t.Fatalf("browser disconnect changed writer lifecycle: calls=%v", methods(rpc.calls))
		}
	}
}

func TestCodexChatBackendReleaseKeepsLeaseWhenUnsubscribeFails(t *testing.T) {
	previousCfg := cfg
	t.Cleanup(func() { cfg = previousCfg })
	cfg.CodexMode = "isolated"
	rpc := newFakeRPCConn()
	rpc.errs["thread/unsubscribe"] = []error{errors.New("unsubscribe failed")}
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	if _, err := b.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.loadedThreads["thread-1"] = true
	b.writerOwners["thread-1"] = "fleet"
	b.mu.Unlock()

	if err := b.Release(context.Background(), "codex", "thread-1"); err == nil {
		t.Fatal("release reported success after unsubscribe failure")
	}
	b.mu.Lock()
	loaded, owner := b.loadedThreads["thread-1"], b.writerOwners["thread-1"]
	b.mu.Unlock()
	if !loaded || owner != "fleet" {
		t.Fatalf("failed release discarded lease: loaded=%v owner=%q", loaded, owner)
	}
}

func TestCodexChatBackendStaleTurnDoneCannotReleaseNewWriter(t *testing.T) {
	previousCfg := cfg
	t.Cleanup(func() { cfg = previousCfg })
	cfg.CodexMode = "isolated"
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	if _, err := b.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.loadedThreads["thread-1"] = true
	b.writerOwners["thread-1"] = "fleet"
	b.lastTurn["thread-1"] = "turn-new"
	b.turnOwners["thread-1"] = "fleet"
	b.mu.Unlock()
	b.releaseCompletedFleetTurn(rpc, "thread-1", "turn-old")
	if got := methods(rpc.calls); len(got) != 0 {
		t.Fatalf("stale completion released new writer: %v", got)
	}
}

func TestCodexChatBackendReaperFindsOrphanLockOutsideMemoryMaps(t *testing.T) {
	previousCfg := cfg
	previousLocks := codexFleetWriterSessions
	previousState := codexActiveRolloutTaskState
	previousRestart := restartCodexFleetSidecar
	t.Cleanup(func() {
		cfg = previousCfg
		codexFleetWriterSessions = previousLocks
		codexActiveRolloutTaskState = previousState
		restartCodexFleetSidecar = previousRestart
	})
	cfg.CodexMode = "isolated"
	codexFleetWriterSessions = func() []string { return []string{"thread-orphan"} }
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) {
		return codexRolloutTaskState{turnID: "turn-done", terminal: true}, true
	}
	restarts := 0
	restartCodexFleetSidecar = func() error { restarts++; return nil }
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return newFakeRPCConn(), func() {}, nil
	})
	b.reapIdleFleetWriters()
	if restarts != 1 {
		t.Fatalf("orphan sidecar restarts=%d", restarts)
	}
}

func TestCodexChatBackendReaperClearsExternalTurnWithoutPhysicalWriter(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
	})
	cfg.CodexMode = "isolated"
	codexThreadWriterProcessOwner = func(string) string { return "" }
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return newFakeRPCConn(), func() {}, nil
	})
	b.lastTurn["thread-external"] = "turn-orphan"
	b.turnOwners["thread-external"] = "desktop"
	b.writerOwners["thread-external"] = "desktop"
	ch := make(chan ChatEvent, 1)
	b.subs["thread-external"] = map[chan ChatEvent]struct{}{ch: {}}

	b.reapMissingExternalWriters()

	if b.lastTurn["thread-external"] != "" || b.turnOwners["thread-external"] != "" || b.writerOwners["thread-external"] != "" {
		t.Fatalf("external orphan remained cached: turn=%q owner=%q writer=%q",
			b.lastTurn["thread-external"], b.turnOwners["thread-external"], b.writerOwners["thread-external"])
	}
	event := receiveChatEvents(t, ch, 1)[0]
	if event.Type != "thread_status" || !strings.Contains(string(event.Data), `"releaseReason":"missing_writer_lease"`) {
		t.Fatalf("external orphan reaper event=%+v", event)
	}
}

func TestCodexChatBackendExplicitReleaseRecoversOrphanedSidecarWriter(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	previousLocks := codexFleetWriterSessions
	previousState := codexActiveRolloutTaskState
	previousRestart := restartCodexFleetSidecar
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
		codexFleetWriterSessions = previousLocks
		codexActiveRolloutTaskState = previousState
		restartCodexFleetSidecar = previousRestart
	})
	cfg.CodexMode = "isolated"
	owned := true
	codexThreadWriterProcessOwner = func(string) string {
		if owned {
			return "fleet"
		}
		return ""
	}
	codexFleetWriterSessions = func() []string {
		if owned {
			return []string{"thread-orphan"}
		}
		return nil
	}
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) { return codexRolloutTaskState{}, false }
	restarts := 0
	restartCodexFleetSidecar = func() error { restarts++; owned = false; return nil }
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return newFakeRPCConn(), func() {}, nil
	})
	if err := b.Release(context.Background(), "codex", "thread-orphan"); err != nil {
		t.Fatal(err)
	}
	if restarts != 1 || owned {
		t.Fatalf("restarts=%d owned=%v", restarts, owned)
	}
}

func TestCodexChatBackendReaperDefersSidecarRestartForOtherActiveFleetTurn(t *testing.T) {
	previousCfg := cfg
	previousLocks := codexFleetWriterSessions
	previousState := codexActiveRolloutTaskState
	previousRestart := restartCodexFleetSidecar
	t.Cleanup(func() {
		cfg = previousCfg
		codexFleetWriterSessions = previousLocks
		codexActiveRolloutTaskState = previousState
		restartCodexFleetSidecar = previousRestart
	})
	cfg.CodexMode = "isolated"
	codexFleetWriterSessions = func() []string { return []string{"thread-orphan", "thread-active"} }
	codexActiveRolloutTaskState = func(sessionID string) (codexRolloutTaskState, bool) {
		if sessionID == "thread-active" {
			return codexRolloutTaskState{turnID: "turn-live", terminal: false}, true
		}
		return codexRolloutTaskState{turnID: "turn-done", terminal: true}, true
	}
	restarts := 0
	restartCodexFleetSidecar = func() error { restarts++; return nil }
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return newFakeRPCConn(), func() {}, nil
	})
	b.reapIdleFleetWriters()
	if restarts != 0 {
		t.Fatalf("reaper interrupted another active Fleet turn: restarts=%d", restarts)
	}
}

func TestCodexChatBackendDesktopTurnCannotBeSteered(t *testing.T) {
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.lastTurn["thread-1"] = "turn-desktop"
	b.turnOwners["thread-1"] = "desktop"
	_, err := b.Steer(context.Background(), "codex", "thread-1", "client-1", "change it", nil, nil)
	if !errors.Is(err, errExternalChatTurn) {
		t.Fatalf("desktop steer got %v", err)
	}
	if len(rpc.calls) != 0 {
		t.Fatalf("desktop turn reached Fleet app-server: %v", methods(rpc.calls))
	}
}

func TestCodexChatBackendDesktopTurnBlocksNewFleetTurn(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	t.Cleanup(func() { cfg = previousCfg; codexThreadWriterProcessOwner = previousOwner })
	cfg.CodexMode = "isolated"
	cfg.CodexHome = t.TempDir()
	codexThreadWriterProcessOwner = func(string) string { return "desktop" }
	sessionID := "11111111-2222-4333-8444-555555555555"
	dir := filepath.Join(cfg.CodexHome, "sessions", "2026", "08", "11")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dir, "rollout-2026-08-11T00-00-00-"+sessionID+".jsonl")
	if err := os.WriteFile(rollout, []byte(
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-desktop"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	_, err := b.Input(context.Background(), "codex", sessionID, "do not race", nil, nil, ChatTurnOptions{})
	if !errors.Is(err, errExternalChatTurn) {
		t.Fatalf("new Fleet turn while Desktop runs got %v", err)
	}
	if len(rpc.calls) != 0 {
		t.Fatalf("blocked Fleet input reached sidecar: %v", methods(rpc.calls))
	}
}

func TestCodexChatBackendActiveRolloutWithoutWriterDoesNotBlockNewFleetTurn(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	t.Cleanup(func() { cfg = previousCfg; codexThreadWriterProcessOwner = previousOwner })
	cfg.CodexMode = "isolated"
	cfg.CodexHome = t.TempDir()
	codexThreadWriterProcessOwner = func(string) string { return "" }
	sessionID := "11111111-2222-4333-8444-666666666666"
	dir := filepath.Join(cfg.CodexHome, "sessions", "2026", "08", "13")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dir, "rollout-2026-08-13T00-00-00-"+sessionID+".jsonl")
	if err := os.WriteFile(rollout, []byte(
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-orphan"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rpc := newFakeRPCConn()
	rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"` + sessionID + `"}}`)
	rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-fleet","status":"running"}}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	result, err := b.Input(context.Background(), "codex", sessionID, "continue after crash", nil, nil, ChatTurnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.TurnID != "turn-fleet" || b.writerOwners[sessionID] != "fleet" {
		t.Fatalf("Fleet did not recover orphaned external turn: result=%+v writer=%q", result, b.writerOwners[sessionID])
	}
	if got := methods(rpc.calls); !reflect.DeepEqual(got, []string{"thread/resume", "turn/start"}) {
		t.Fatalf("orphan recovery calls=%v", got)
	}
}

func TestCodexChatBackendActiveFleetLeaseWithoutLockBlocksSecondTurn(t *testing.T) {
	previousCfg := cfg
	previousOwner := codexThreadWriterProcessOwner
	t.Cleanup(func() {
		cfg = previousCfg
		codexThreadWriterProcessOwner = previousOwner
	})
	cfg.CodexMode = "isolated"
	cfg.CodexHome = t.TempDir()
	codexThreadWriterProcessOwner = func(string) string { return "" }
	sessionID := "11111111-2222-4333-8444-999999999999"
	dir := filepath.Join(cfg.CodexHome, "sessions", "2026", "08", "13")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-08-13T00-00-00-"+sessionID+".jsonl"), []byte(
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-fresh"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.loadedThreads[sessionID] = true
	b.lastTurn[sessionID] = "turn-fresh"
	b.turnOwners[sessionID] = "fleet"
	b.writerOwners[sessionID] = "fleet"

	_, err := b.Input(context.Background(), "codex", sessionID, "must queue behind current turn", nil, nil, ChatTurnOptions{})
	if !errors.Is(err, errFleetChatTurnRunning) {
		t.Fatalf("second turn was not blocked by current Fleet lease: %v", err)
	}
	if len(rpc.calls) != 0 {
		t.Fatalf("current Fleet lease triggered a second writer acquisition: %v", methods(rpc.calls))
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
			for _, call := range rpc.calls {
				if call.method == "thread/unsubscribe" {
					t.Fatal("thread unsubscribed while another browser subscriber remained")
				}
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
	last := rpc.calls[len(rpc.calls)-1]
	if rpc.calls[0].method != "thread/resume" || last.method != "response:42" ||
		!reflect.DeepEqual(last.params, map[string]interface{}{"decision": "accept"}) {
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
	last := rpc.calls[len(rpc.calls)-1]
	if rpc.calls[0].method != "thread/resume" || last.method != "response:43" || !reflect.DeepEqual(last.params, want) {
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
			last := rpc.calls[len(rpc.calls)-1]
			if rpc.calls[0].method != "thread/resume" || last.method != "response:"+requestID ||
				!reflect.DeepEqual(last.params, test.want) {
				t.Fatalf("calls got %#v", rpc.calls)
			}
		})
	}
}

func TestCodexChatBackendEventsReloadsThreadAfterRPCReset(t *testing.T) {
	rpc1 := newFakeRPCConn()
	rpc2 := newFakeRPCConn()
	connections := []codexRPCConn{rpc1, rpc2}
	connectCount := 0
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		rpc := connections[connectCount]
		connectCount++
		return rpc, func() {}, nil
	})

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	if _, err := b.Events(firstCtx, "codex", "thread-1"); err != nil {
		t.Fatal(err)
	}
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	if _, err := b.Events(secondCtx, "codex", "thread-1"); err != nil {
		t.Fatal(err)
	}
	if got := methods(rpc1.calls); !reflect.DeepEqual(got, []string{"thread/resume"}) {
		t.Fatalf("same RPC connection should load a thread once, got %v", got)
	}

	b.resetRPC()
	thirdCtx, cancelThird := context.WithCancel(context.Background())
	defer cancelThird()
	if _, err := b.Events(thirdCtx, "codex", "thread-1"); err != nil {
		t.Fatal(err)
	}
	if got := methods(rpc2.calls); !reflect.DeepEqual(got, []string{"thread/resume"}) {
		t.Fatalf("replacement RPC connection should reload the thread, got %v", got)
	}
	cancelFirst()
	cancelSecond()
	cancelThird()
	waitForCodexSyncStop(t, b, "thread-1")
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
	cancelSecond()
	waitForCodexSyncStop(t, b, "thread-1")
}

func TestCodexChatBackendResumeReportsOnlyActionablePendingRequests(t *testing.T) {
	rpc := newFakeRPCConn()
	b := newCodexChatBackend(func(ctx context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	b.pending["request-1"] = pendingCodexRequest{
		id: json.RawMessage(`61`), method: "item/tool/requestUserInput", sessionID: "thread-1",
		params: json.RawMessage(`{"threadId":"thread-1","questions":[{"id":"q1","question":"Continue?"}]}`),
	}

	res, err := b.Resume(context.Background(), "codex", "thread-1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.PendingRequests != 1 {
		t.Fatalf("pendingRequests=%d want 1", res.PendingRequests)
	}
	if len(res.PendingEvents) != 1 || res.PendingEvents[0].Type != "interaction_request" ||
		!strings.Contains(string(res.PendingEvents[0].Data), `"question":"Continue?"`) {
		t.Fatalf("pending event was not recoverable from resume: %+v", res.PendingEvents)
	}

	delete(b.pending, "request-1")
	res, err = b.Resume(context.Background(), "codex", "thread-1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.PendingRequests != 0 || len(res.PendingEvents) != 0 {
		t.Fatalf("stale thread waiting flag leaked as pending request: %+v", res)
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

func waitForCodexSyncStop(t *testing.T, b *codexChatBackend, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		_, running := b.syncers[sessionID]
		b.mu.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("connected sync did not stop after subscriber cancellation")
		}
		time.Sleep(time.Millisecond)
	}
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
