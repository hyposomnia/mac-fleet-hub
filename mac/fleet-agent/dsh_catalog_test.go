package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func installDSHCatalogTestBackend(t *testing.T, host *dshTestHost) *dshChatBackend {
	t.Helper()
	previousCfg, previousBackend := cfg, agentChatBackend
	cfg.ChatQueueFile = filepath.Join(t.TempDir(), "chat-queue.json")
	cfg.CodexHome = t.TempDir()
	t.Cleanup(func() { cfg, agentChatBackend = previousCfg, previousBackend })
	backend := newTestDSHBackend(t, host)
	agentChatBackend = &routingChatBackend{dsh: backend}
	return backend
}

func newDSHListHost(t *testing.T, baseline any) *dshTestHost {
	t.Helper()
	host := newDSHTestHost(t)
	host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
		if endpoint != "session/list" || !reflect.DeepEqual(args, map[string]any{"_request": map[string]any{}}) {
			t.Errorf("unexpected list request: %s %+v", endpoint, args)
		}
		return map[string]any{"items": []any{
			map[string]any{"sessionId": "session-menu-test", "cwd": "/work", "updatedAt": 20, "projections": map[string]any{"values": map[string]any{"title": "菜单测试"}}},
			map[string]any{"sessionId": "session-archived", "cwd": "/archive", "updatedAt": 30, "projections": map[string]any{"values": map[string]any{"title": "已归档测试"}}},
			map[string]any{"sessionId": "session-latest", "cwd": "/work", "updatedAt": 40},
			map[string]any{"sessionId": "internal-subagent", "cwd": "/work", "updatedAt": 50},
		}}, nil
	}
	host.streamReply = func(endpoint, streamID string, send func(map[string]any)) {
		if endpoint != "workspace/follow" {
			t.Errorf("unexpected stream: %s", endpoint)
		}
		if baseline != nil {
			send(map[string]any{"type": "item", "streamId": streamID, "value": baseline})
		}
	}
	return host
}

func dshListRequest(query string) (*httptest.ResponseRecorder, []Session) {
	rr := httptest.NewRecorder()
	handleSessions(rr, httptest.NewRequest(http.MethodGet, "/api/sessions?assistant=dsh&"+query, nil))
	var body struct {
		Sessions []Session `json:"sessions"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return rr, body.Sessions
}

func TestDSHSessionListNativeArchiveFilter(t *testing.T) {
	host := newDSHListHost(t, map[string]any{"type": "baseline", "value": map[string]any{"archivedSessionIds": []string{"session-archived"}}})
	backend := installDSHCatalogTestBackend(t, host)
	for _, tc := range []struct {
		query string
		ids   []string
	}{
		{"archived=false", []string{"session-latest", "session-menu-test"}},
		{"archived=true", []string{"session-archived"}},
		{"archived=false&search=菜单", []string{"session-menu-test"}},
		{"archived=true&search=missing", []string{}},
	} {
		rr, sessions := dshListRequest(tc.query)
		ids := []string{}
		for _, session := range sessions {
			ids = append(ids, session.SessionID)
		}
		if rr.Code != http.StatusOK || !reflect.DeepEqual(ids, tc.ids) {
			t.Fatalf("%s: status=%d ids=%v want=%v body=%s", tc.query, rr.Code, ids, tc.ids, rr.Body)
		}
		backend.client.mu.Lock()
		streams := len(backend.client.streams)
		backend.client.mu.Unlock()
		if streams != 0 {
			t.Fatalf("archive snapshot left %d streams", streams)
		}
	}
}

func TestDSHSessionListRequiresArchiveSnapshot(t *testing.T) {
	for _, frame := range []any{
		map[string]any{"type": "upsert"},
		map[string]any{"type": "baseline", "value": map[string]any{}},
	} {
		t.Run("invalid_baseline", func(t *testing.T) {
			host := newDSHListHost(t, frame)
			installDSHCatalogTestBackend(t, host)
			rr, _ := dshListRequest("archived=false")
			if rr.Code == http.StatusOK {
				t.Fatal("unknown archive state must not appear as current sessions")
			}
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		host := newDSHListHost(t, nil)
		backend := installDSHCatalogTestBackend(t, host)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		rr := httptest.NewRecorder()
		handleSessions(rr, httptest.NewRequest(http.MethodGet, "/api/sessions?assistant=dsh", nil).WithContext(ctx))
		if rr.Code == http.StatusOK {
			t.Fatal("missing archive snapshot must not succeed")
		}
		backend.client.mu.Lock()
		streams := len(backend.client.streams)
		backend.client.mu.Unlock()
		if streams != 0 {
			t.Fatalf("cancel left %d streams", streams)
		}
	})
}

func TestDSHSessionPinsPersistAndStaySeparateFromCodex(t *testing.T) {
	host := newDSHListHost(t, map[string]any{"type": "baseline", "value": map[string]any{"archivedSessionIds": []string{}}})
	installDSHCatalogTestBackend(t, host)
	if rr := dshActionRequest("pin", ""); rr.Code != http.StatusOK {
		t.Fatalf("pin: %d %s", rr.Code, rr.Body)
	}
	// A new backend reads the durable pin rather than keeping it in a connection-local map.
	agentChatBackend = &routingChatBackend{dsh: newTestDSHBackend(t, host)}
	_, sessions := dshListRequest("archived=false")
	if len(sessions) == 0 || sessions[0].SessionID != "session-menu-test" || !sessions[0].Pinned {
		t.Fatalf("pin not restored: %+v", sessions)
	}
	if len(readCodexThreadPins()) != 0 {
		t.Fatal("DSH pin leaked into Codex")
	}
	path := filepath.Join(filepath.Dir(cfg.ChatQueueFile), "dsh-thread-pins.json")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("pin file mode: %v %v", info, err)
	}
	if rr := dshActionRequest("unpin", ""); rr.Code != http.StatusOK {
		t.Fatalf("unpin: %d %s", rr.Code, rr.Body)
	}
	_, sessions = dshListRequest("archived=false")
	for _, s := range sessions {
		if s.Pinned {
			t.Fatal("unpin was not persisted")
		}
	}
}

func dshActionRequest(action, value string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{
		"assistant": "dsh", "sessionId": "session-menu-test", "action": action, "value": value,
	})
	rr := httptest.NewRecorder()
	handleSessionAction(rr, httptest.NewRequest(http.MethodPost, "/api/sessions/action", strings.NewReader(string(body))))
	return rr
}

func TestDSHSessionActionNativeRouting(t *testing.T) {
	for _, tc := range []struct {
		action, value, endpoint string
		request, result         map[string]any
	}{
		{"rename", "  新标题  ", "session/rename", map[string]any{"sessionId": "session-menu-test", "title": "新标题"}, map[string]any{"title": "新标题", "seq": 3}},
		{"archive", "", "workspace/archiveSession", map[string]any{"sessionId": "session-menu-test"}, map[string]any{"archivedSessionIds": []string{"session-menu-test"}}},
		{"delete", "", "session/delete", map[string]any{"sessionId": "session-menu-test"}, map[string]any{"deleted": true}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			host := newDSHTestHost(t)
			calls := 0
			host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
				calls++
				if endpoint != tc.endpoint || !reflect.DeepEqual(args, map[string]any{"request": tc.request}) {
					t.Errorf("got %s %+v; want %s %+v", endpoint, args, tc.endpoint, tc.request)
				}
				return tc.result, nil
			}
			installDSHCatalogTestBackend(t, host)
			rr := dshActionRequest(tc.action, tc.value)
			if rr.Code != http.StatusOK || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", rr.Code, calls, rr.Body)
			}
		})
	}
}

func TestDSHSessionActionRejectsUnsupportedRestoreAndInvalidName(t *testing.T) {
	host := newDSHTestHost(t)
	host.callResult = func(endpoint string, _ map[string]any) (any, *dshRemoteError) {
		t.Errorf("invalid action must not call host: %s", endpoint)
		return nil, nil
	}
	installDSHCatalogTestBackend(t, host)
	for _, tc := range []struct {
		action, value string
		status        int
	}{
		{"unarchive", "", http.StatusNotImplemented},
		{"rename", "   ", http.StatusBadRequest},
		{"rename", strings.Repeat("x", 501), http.StatusBadRequest},
		{"invalid", "", http.StatusNotImplemented},
	} {
		rr := dshActionRequest(tc.action, tc.value)
		if rr.Code != tc.status {
			t.Errorf("%s: status=%d want=%d body=%s", tc.action, rr.Code, tc.status, rr.Body)
		}
	}
}

func TestDSHSessionActionPropagatesHostErrorWithoutRetry(t *testing.T) {
	host := newDSHTestHost(t)
	calls := 0
	host.callResult = func(_ string, _ map[string]any) (any, *dshRemoteError) {
		calls++
		return nil, &dshRemoteError{Code: "session/not-found", Message: "missing"}
	}
	installDSHCatalogTestBackend(t, host)
	if rr := dshActionRequest("pin", ""); rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	rr := dshActionRequest("delete", "")
	if rr.Code == http.StatusOK || calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", rr.Code, calls, rr.Body)
	}
	if !readThreadPins(dshPinsPath())["session-menu-test"] {
		t.Fatal("failed delete discarded pin")
	}
	host.callResult = func(_ string, _ map[string]any) (any, *dshRemoteError) { return map[string]any{"deleted": true}, nil }
	rr = dshActionRequest("delete", "")
	if rr.Code != http.StatusOK || readThreadPins(dshPinsPath())["session-menu-test"] {
		t.Fatalf("successful delete retained pin: %s", rr.Body)
	}
}

// Opt-in only: native end-to-end verification against the already-running Desktop host.
// Creates and deletes only its own empty session; never prompts the model or changes an existing session.
func TestDSHSessionMenuLive(t *testing.T) {
	if os.Getenv("FLEET_DSH_MENU_UAT") != "1" {
		t.Skip("set FLEET_DSH_MENU_UAT=1 for native host UAT")
	}
	realCfg := loadConfig()
	previousCfg := cfg
	cfg.ChatQueueFile = filepath.Join(t.TempDir(), "chat-queue.json")
	t.Cleanup(func() { cfg = previousCfg })
	b := newDSHChatBackend(realCfg.DSHHome, realCfg.DSHLog, realCfg.DSHEndpoint)
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := b.ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := client.call(ctx, "session/create", map[string]any{"request": map[string]any{"cwd": cwd}})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(raw, &created) != nil || !dshVisibleSessionID(created.SessionID) {
		t.Fatalf("invalid created session: %s", raw)
	}
	deleted := false
	defer func() {
		if !deleted {
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			if err := b.mutateSession(cleanup, created.SessionID, "delete", ""); err != nil {
				t.Errorf("cleanup %s: %v", created.SessionID, err)
			}
		}
	}()
	title := "Fleet menu UAT " + time.Now().Format("20060102-150405")
	for _, action := range []string{"rename", "pin", "archive"} {
		if err := b.mutateSession(ctx, created.SessionID, action, title); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		t.Log(action + ": ok")
	}
	current, err := b.listSessions(ctx, false, title)
	if err != nil {
		t.Fatal(err)
	}
	if len(current) != 0 {
		t.Fatal("archived session still in current list")
	}
	archived, err := b.listSessions(ctx, true, title)
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 1 || archived[0].SessionID != created.SessionID || !archived[0].Pinned || archived[0].Title != title {
		t.Fatalf("native archive projection mismatch: %+v", archived)
	}
	t.Log("native archive list + renamed title + Fleet pin: ok")
	if err := b.mutateSession(ctx, created.SessionID, "unarchive", ""); !errors.Is(err, errDSHUnsupported) {
		t.Fatalf("unarchive must remain unsupported: %v", err)
	}
	if err := b.mutateSession(ctx, created.SessionID, "delete", ""); err != nil {
		t.Fatal(err)
	}
	deleted = true
	archived, err = b.listSessions(ctx, true, title)
	if err != nil || len(archived) != 0 || readThreadPins(dshPinsPath())[created.SessionID] {
		t.Fatalf("deleted session retained: %+v %v", archived, err)
	}
	t.Log("delete + cleanup: ok")
}
