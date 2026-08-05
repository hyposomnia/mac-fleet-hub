package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCodexListThreadsUsesDesktopProjectsAndFiltersInternalThreads(t *testing.T) {
	previousCfg := cfg
	cfg.CodexHome = t.TempDir()
	t.Cleanup(func() { cfg = previousCfg })
	state := `{
		"local-projects":{
			"project-jobs":{"id":"project-jobs","name":"get_job_done","rootPaths":["/repos/get_job_done"]}
		},
		"thread-project-assignments":{
			"main":{"projectKind":"local","projectId":"project-jobs","cwd":"/repos/get_job_done"},
			"worktree":{"projectKind":"local","projectId":"project-jobs","cwd":"/codex/worktrees/6dd7/get_job_done"}
		},
		"projectless-thread-ids":["quick","child"]
	}`
	if err := os.WriteFile(filepath.Join(cfg.CodexHome, ".codex-global-state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}

	rpc := newFakeRPCConn()
	rpc.reply["thread/list"] = json.RawMessage(`{
		"data":[
			{"id":"main","cwd":"/repos/get_job_done","preview":"Main","source":"vscode","threadSource":"user","createdAt":1,"updatedAt":2,"recencyAt":3,"status":{"type":"notLoaded"}},
			{"id":"worktree","cwd":"/codex/worktrees/6dd7/get_job_done","preview":"Worktree","source":"vscode","threadSource":"subagent","parentThreadId":"main","createdAt":1,"updatedAt":4,"recencyAt":5,"status":{"type":"notLoaded"}},
			{"id":"quick","cwd":"/Users/test/Documents/Codex/2026-08-05/qu","preview":"Quick chat","source":"vscode","threadSource":"user","createdAt":1,"updatedAt":2},
			{"id":"child","cwd":"/Users/test/Documents/Codex/2026-08-05/child","preview":"Child","source":"vscode","threadSource":"subagent","parentThreadId":"quick","createdAt":1,"updatedAt":2},
			{"id":"object-subagent","cwd":"/repo","preview":"Object child","source":{"subAgent":{"other":"worker"}},"threadSource":"subagent","createdAt":1,"updatedAt":2},
			{"id":"ephemeral","cwd":"/repo","preview":"Ephemeral","ephemeral":true,"createdAt":1,"updatedAt":2},
			{"id":"ambient","cwd":"/repo","preview":"Ambient","threadSource":"ambient_suggestions","createdAt":1,"updatedAt":2}
		],
		"nextCursor":"next"
	}`)
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	page, err := backend.ListThreads(context.Background(), codexThreadListOptions{
		Limit: 50, Search: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 3 || page.NextCursor != "next" {
		t.Fatalf("page got %+v", page)
	}
	byID := map[string]Session{}
	for _, session := range page.Sessions {
		byID[session.SessionID] = session
	}
	for _, id := range []string{"main", "worktree", "quick"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("visible session %q missing from %+v", id, page.Sessions)
		}
	}
	for _, id := range []string{"child", "object-subagent", "ephemeral", "ambient"} {
		if _, ok := byID[id]; ok {
			t.Fatalf("internal session %q leaked into %+v", id, page.Sessions)
		}
	}
	for _, id := range []string{"main", "worktree"} {
		session := byID[id]
		if session.ProjectID != "project-jobs" || session.ProjectName != "get_job_done" || session.ProjectCwd != "/repos/get_job_done" || session.Projectless {
			t.Fatalf("project context for %q got %+v", id, session)
		}
	}
	if quick := byID["quick"]; !quick.Projectless || quick.ProjectID != "" || quick.ProjectCwd != "" {
		t.Fatalf("projectless context got %+v", quick)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "thread/list" {
		t.Fatalf("calls got %+v", rpc.calls)
	}
	got := mapFromParams(t, rpc.calls[0].params)
	want := map[string]interface{}{
		"limit":          float64(50),
		"cursor":         nil,
		"sortKey":        "recency_at",
		"modelProviders": nil,
		"sourceKinds":    []interface{}{},
		"archived":       false,
		"useStateDbOnly": true,
		"searchTerm":     "needle",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("thread/list params got %#v want %#v", got, want)
	}
}

func TestCodexSessionFromThreadMapsDesktopRuntimeStatus(t *testing.T) {
	name := "Active thread"
	active, ok := codexSessionFromThread(codexThreadWire{
		ID:        "thread-active",
		Name:      &name,
		Cwd:       "/repo",
		UpdatedAt: 2,
		Status:    json.RawMessage(`{"type":"active","activeFlags":["waitingOnApproval"]}`),
	})
	if !ok {
		t.Fatal("active Desktop thread should be visible")
	}
	if !active.Live || active.Status != "active" || !active.Waiting {
		t.Fatalf("active Desktop status was not preserved: %+v", active)
	}

	idle, ok := codexSessionFromThread(codexThreadWire{
		ID:        "thread-idle",
		Preview:   "Idle thread",
		UpdatedAt: 2,
		Status:    json.RawMessage(`{"type":"idle"}`),
	})
	if !ok {
		t.Fatal("idle Desktop thread should be visible")
	}
	if idle.Live || idle.Status != "idle" || idle.Waiting {
		t.Fatalf("idle Desktop status was not preserved: %+v", idle)
	}
}

func TestCodexListThreadsTimeoutReconnectsAndRetries(t *testing.T) {
	previousTimeout := codexCatalogCallTimeout
	codexCatalogCallTimeout = 10 * time.Millisecond
	t.Cleanup(func() { codexCatalogCallTimeout = previousTimeout })

	rpc1 := newFakeRPCConn()
	rpc1.block["thread/list"] = true
	rpc2 := newFakeRPCConn()
	rpc2.reply["thread/list"] = json.RawMessage(`{"data":[{"id":"recovered","cwd":"/repo","preview":"Recovered"}]}`)
	connects := 0
	cleaned := 0
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		connects++
		if connects == 1 {
			return rpc1, func() { cleaned++ }, nil
		}
		return rpc2, func() { cleaned++ }, nil
	})

	page, err := backend.ListThreads(context.Background(), codexThreadListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].SessionID != "recovered" {
		t.Fatalf("recovered page got %+v", page)
	}
	if connects != 2 || cleaned != 1 {
		t.Fatalf("connects=%d cleaned=%d, want 2/1", connects, cleaned)
	}
}

func TestCodexRecoveryFailureSchedulesSelfRestartOnce(t *testing.T) {
	previousTimeout := codexCatalogCallTimeout
	codexCatalogCallTimeout = 10 * time.Millisecond
	t.Cleanup(func() { codexCatalogCallTimeout = previousTimeout })

	rpc := newFakeRPCConn()
	rpc.block["thread/list"] = true
	connects := 0
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		connects++
		if connects == 1 {
			return rpc, func() {}, nil
		}
		return nil, nil, errors.New("cannot initialize replacement")
	})
	restarts := make(chan error, 2)
	backend.restart = func(err error) { restarts <- err }

	_, err := backend.ListThreads(context.Background(), codexThreadListOptions{})
	if err == nil {
		t.Fatal("recovery failure should fail the request")
	}
	backend.scheduleSelfRestart(errors.New("duplicate trigger"))
	select {
	case <-restarts:
	case <-time.After(time.Second):
		t.Fatal("self restart was not scheduled")
	}
	select {
	case err := <-restarts:
		t.Fatalf("self restart should be deduplicated, got second trigger: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestCodexInitialConnectionFailureSchedulesSelfRestart(t *testing.T) {
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return nil, nil, errors.New("cannot start app-server")
	})
	restarts := make(chan error, 1)
	backend.restart = func(err error) { restarts <- err }

	_, err := backend.ListThreads(context.Background(), codexThreadListOptions{})
	if !errors.Is(err, errAgentRestarting) {
		t.Fatalf("initial connection error got %v, want agent restarting", err)
	}
	select {
	case <-restarts:
	case <-time.After(time.Second):
		t.Fatal("initial connection failure did not schedule self restart")
	}
}

func TestHandleSessionsMissingCodexExecutableReturnsEmptyWithoutRestart(t *testing.T) {
	previousCfg := cfg
	previousBackend := agentChatBackend
	cfg.CodexBin = filepath.Join(t.TempDir(), "missing-codex")
	cfg.CodexHome = t.TempDir()
	t.Cleanup(func() {
		cfg = previousCfg
		agentChatBackend = previousBackend
	})

	connects := 0
	restarts := make(chan error, 1)
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		connects++
		return nil, nil, errors.New("connector must not run")
	})
	backend.restart = func(err error) { restarts <- err }
	agentChatBackend = backend

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?assistant=codex&archived=false", nil)
	rr := httptest.NewRecorder()
	handleSessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Sessions []Session `json:"sessions"`
		Total    int       `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response should be JSON, got %q", rr.Body.String())
	}
	if body.Sessions == nil || len(body.Sessions) != 0 || body.Total != 0 {
		t.Fatalf("missing Codex should return an empty list, got %+v", body)
	}
	if connects != 0 {
		t.Fatalf("missing Codex started app-server %d times", connects)
	}
	select {
	case err := <-restarts:
		t.Fatalf("missing Codex scheduled fleet-agent restart: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestHandleSessionsMissingCodexExecutableReusesControlSocket(t *testing.T) {
	previousCfg := cfg
	previousBackend := agentChatBackend
	codexHome := t.TempDir()
	cfg.CodexBin = filepath.Join(t.TempDir(), "missing-codex")
	cfg.CodexHome = codexHome
	socketDir, err := os.MkdirTemp("/tmp", "mfh-codex-socket-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "control.sock")
	cfg.CodexSock = socketPath
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		listener.Close()
		cfg = previousCfg
		agentChatBackend = previousBackend
	})

	rpc := newFakeRPCConn()
	rpc.reply["thread/list"] = json.RawMessage(`{"data":[{"id":"thread-1","cwd":"/repo","preview":"Visible"}]}`)
	connects := 0
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		connects++
		return rpc, func() {}, nil
	})
	agentChatBackend = backend

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?assistant=codex&archived=false", nil)
	rr := httptest.NewRecorder()
	handleSessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Sessions []Session `json:"sessions"`
		Total    int       `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response should be JSON, got %q", rr.Body.String())
	}
	if len(body.Sessions) != 1 || body.Sessions[0].SessionID != "thread-1" || body.Total != 1 {
		t.Fatalf("existing control socket should expose threads, got %+v", body)
	}
	if connects != 1 {
		t.Fatalf("existing control socket backend connect count got %d, want 1", connects)
	}
}

func TestCodexCanceledRequestDoesNotRecoverOrRestart(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.block["thread/list"] = true
	connects := 0
	cleaned := 0
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		connects++
		return rpc, func() { cleaned++ }, nil
	})
	restarts := make(chan error, 1)
	backend.restart = func(err error) { restarts <- err }

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := backend.ListThreads(ctx, codexThreadListOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request error got %v", err)
	}
	if connects != 1 || cleaned != 0 {
		t.Fatalf("canceled request should keep connection, connects=%d cleaned=%d", connects, cleaned)
	}
	select {
	case err := <-restarts:
		t.Fatalf("canceled request scheduled restart: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestCodexMutationRecoversConnectionWithoutReplayingWrite(t *testing.T) {
	previousTimeout := codexRPCCallTimeout
	codexRPCCallTimeout = 10 * time.Millisecond
	t.Cleanup(func() { codexRPCCallTimeout = previousTimeout })

	rpc1 := newFakeRPCConn()
	rpc1.block["thread/archive"] = true
	rpc2 := newFakeRPCConn()
	connects := 0
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		connects++
		if connects == 1 {
			return rpc1, func() {}, nil
		}
		return rpc2, func() {}, nil
	})

	err := backend.MutateThread(context.Background(), "thread-1", "archive", "")
	if !errors.Is(err, errAppServerRecovered) {
		t.Fatalf("mutation error got %v, want recovered connection error", err)
	}
	if connects != 2 {
		t.Fatalf("connect count got %d, want 2", connects)
	}
	if len(rpc2.calls) != 0 {
		t.Fatalf("unsafe mutation was replayed on replacement RPC: %+v", rpc2.calls)
	}
}

func TestWriteChatErrMapsRecoveryStates(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "timeout", err: errAppServerTimeout, status: http.StatusGatewayTimeout, code: "appserver_timeout"},
		{name: "recovered", err: errAppServerRecovered, status: http.StatusServiceUnavailable, code: "appserver_recovered"},
		{name: "restarting", err: errAgentRestarting, status: http.StatusServiceUnavailable, code: "agent_restarting"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeChatErr(rr, test.err)
			if rr.Code != test.status {
				t.Fatalf("status got %d, want %d", rr.Code, test.status)
			}
			var body map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("response should be JSON, got %q", rr.Body.String())
			}
			if body["error"] != test.code {
				t.Fatalf("error code got %v, want %s", body["error"], test.code)
			}
		})
	}
}

func TestCodexThreadCwdUsesThreadReadWithoutTurns(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/read"] = json.RawMessage(`{"thread":{"id":"thread-1","cwd":"/repo"}}`)
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	cwd, err := backend.ThreadCwd(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if cwd != "/repo" {
		t.Fatalf("cwd got %q", cwd)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "thread/read" {
		t.Fatalf("calls got %+v", rpc.calls)
	}
	want := map[string]interface{}{"threadId": "thread-1", "includeTurns": false}
	if got := mapFromParams(t, rpc.calls[0].params); !reflect.DeepEqual(got, want) {
		t.Fatalf("params got %#v want %#v", got, want)
	}
}

func TestCodexMutateThreadUsesAppServerMethods(t *testing.T) {
	tests := []struct {
		action string
		value  string
		method string
		params map[string]interface{}
	}{
		{action: "rename", value: "New name", method: "thread/name/set", params: map[string]interface{}{"threadId": "thread-1", "name": "New name"}},
		{action: "archive", method: "thread/archive", params: map[string]interface{}{"threadId": "thread-1"}},
		{action: "unarchive", method: "thread/unarchive", params: map[string]interface{}{"threadId": "thread-1"}},
	}
	for _, test := range tests {
		t.Run(test.action, func(t *testing.T) {
			rpc := newFakeRPCConn()
			backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
				return rpc, func() {}, nil
			})
			if err := backend.MutateThread(context.Background(), "thread-1", test.action, test.value); err != nil {
				t.Fatal(err)
			}
			if len(rpc.calls) != 1 || rpc.calls[0].method != test.method {
				t.Fatalf("calls got %+v", rpc.calls)
			}
			if got := mapFromParams(t, rpc.calls[0].params); !reflect.DeepEqual(got, test.params) {
				t.Fatalf("params got %#v want %#v", got, test.params)
			}
		})
	}
}

func TestCodexPinStatePersistsOutsideAppServer(t *testing.T) {
	previousCfg := cfg
	cfg.CodexHome = t.TempDir()
	t.Cleanup(func() { cfg = previousCfg })
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return newFakeRPCConn(), func() {}, nil
	})

	if err := backend.MutateThread(context.Background(), "thread-1", "pin", ""); err != nil {
		t.Fatal(err)
	}
	if !readCodexThreadPins()["thread-1"] {
		t.Fatal("thread pin was not persisted")
	}
	info, err := os.Stat(filepath.Join(cfg.CodexHome, "fleet-thread-pins.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("pin file mode got %o", info.Mode().Perm())
	}
	if err := backend.MutateThread(context.Background(), "thread-1", "unpin", ""); err != nil {
		t.Fatal(err)
	}
	if readCodexThreadPins()["thread-1"] {
		t.Fatal("thread pin was not removed")
	}
}
