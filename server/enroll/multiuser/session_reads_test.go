package multiuser

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestSessionReadStatePersistsAcrossClientsAndRestart(t *testing.T) {
	server, now := newTestServer(t)
	owner, user, _, _ := registerBrowser(t, server, *now, "read-owner@example.com")
	other, otherUser, _, _ := registerBrowser(t, server, *now, "read-other@example.com")
	if err := server.SetAdmin(otherUser.Email); err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{1, 2} {
		if _, err := server.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(?,?,?,'100.64.0.42','Mac','active',?)", index, user.ID, intString(int64(index)), now.Unix()); err != nil {
			t.Fatal(err)
		}
		if _, _, err := server.issueDeviceCredentials(int64(index)); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var native []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sessions":
			respond(w, 200, map[string]any{"sessions": []map[string]any{{"sessionId": "thread-1", "assistant": r.URL.Query().Get("assistant"), "mtime": now.UnixMilli(), "desktopUnread": false, "readAt": now.UnixMilli()}}, "nextCursor": "next"})
		case "/api/sessions/read-state":
			if r.Header.Get("X-Fleet-Device-Token") == "" || r.Header.Get("Cookie") != "" {
				t.Error("invalid native sync credentials")
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			native = append(native, body)
			mu.Unlock()
			respond(w, 200, map[string]bool{"nativeSynced": true})
		default:
			t.Errorf("unexpected upstream %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	server.options.ProxyURL = func(Device, string) string { return upstream.URL }
	mark := func(browser *testBrowser, device, assistant string, unread bool, version int64) sessionReadState {
		t.Helper()
		response := browser.request("POST", "/"+device+"/api/sessions/read", map[string]any{"assistant": assistant, "sessionId": "thread-1", "unread": unread, "activityAt": now.UnixMilli(), "expectedVersion": version, "explicit": !unread})
		if response.Code != 200 {
			t.Fatalf("mark %d %s", response.Code, response.Body.String())
		}
		var state sessionReadState
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	list := func(browser *testBrowser, device, assistant string) map[string]any {
		t.Helper()
		response := browser.request("GET", "/"+device+"/api/sessions?assistant="+assistant, nil)
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("list %d %s", response.Code, response.Body.String())
		}
		var result struct {
			Sessions   []map[string]any `json:"sessions"`
			NextCursor string           `json:"nextCursor"`
		}
		if json.Unmarshal(response.Body.Bytes(), &result) != nil || result.NextCursor != "next" || len(result.Sessions) != 1 {
			t.Fatal(response.Body.String())
		}
		return result.Sessions[0]
	}
	for _, assistant := range []string{"codex", "dsh"} {
		state := mark(owner, "m1", assistant, true, 0)
		if !state.MarkedUnread || state.Version != 1 {
			t.Fatalf("missing mark %+v", state)
		}
		// A second client and a fresh server process receive the durable mark.
		second := &testBrowser{server: server, cookie: owner.cookie, csrf: owner.csrf}
		if list(second, "m1", assistant)["markedUnread"] != true {
			t.Fatal("mark was client-local")
		}
		if list(second, "m2", assistant)["markedUnread"] == true {
			t.Fatal("mark leaked to another device")
		}
		// Delayed legacy/automatic reads cannot cancel a newer explicit unread.
		stale := mark(second, "m1", assistant, false, 0)
		if !stale.MarkedUnread || stale.Version != 1 {
			t.Fatalf("stale read erased mark %+v", stale)
		}
		automatic := second.request("POST", "/m1/api/sessions/read", map[string]any{"assistant": assistant, "sessionId": "thread-1", "activityAt": now.UnixMilli(), "expectedVersion": state.Version})
		if automatic.Code != 200 || json.Unmarshal(automatic.Body.Bytes(), &stale) != nil || !stale.MarkedUnread || stale.Version != 1 {
			t.Fatalf("automatic read erased mark: %d %s", automatic.Code, automatic.Body.String())
		}
		read := mark(second, "m1", assistant, false, state.Version)
		if read.MarkedUnread || read.ReadAt != now.UnixMilli() {
			t.Fatalf("explicit open did not clear %+v", read)
		}
		mark(owner, "m1", assistant, true, read.Version)
	}
	mu.Lock()
	if len(native) != 3 || native[0]["read"] != false || native[1]["read"] != true || native[2]["read"] != false {
		t.Errorf("native sync not scoped to accepted Codex changes: %+v", native)
	}
	mu.Unlock()
	for _, method := range []string{"GET", "POST"} {
		path := "/m1/api/sessions?assistant=codex"
		var body any
		if method == "POST" {
			path = "/m1/api/sessions/read"
			body = map[string]any{"assistant": "codex", "sessionId": "thread-1", "unread": true}
		}
		if response := other.request(method, path, body); response.Code != 404 {
			t.Fatalf("admin cross-owner %s: %d", method, response.Code)
		}
	}
	server.Close()
	restarted, err := New(server.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	owner.server = restarted
	for _, assistant := range []string{"codex", "dsh"} {
		if list(owner, "m1", assistant)["markedUnread"] != true {
			t.Fatal("mark did not survive gateway restart")
		}
	}
}

func TestSessionReadStateReconcilesOnlyConfirmedLaterNativeRead(t *testing.T) {
	server, now := newTestServer(t)
	owner, user, _, _ := registerBrowser(t, server, *now, "native-read@example.com")
	if _, err := server.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(1,?,'1','100.64.0.42','Mac','active',?)", user.ID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.issueDeviceCredentials(1); err != nil {
		t.Fatal(err)
	}
	nativeAt := now.UnixMilli()
	desktopAt, receipt := nativeAt-1, nativeAt
	desktopUnread := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sessions/read-state" {
			respond(w, 200, map[string]any{"nativeSynced": true, "nativeReadStateAt": receipt})
			return
		}
		respond(w, 200, map[string]any{"sessions": []map[string]any{{"assistant": "codex", "sessionId": "native-thread", "mtime": nativeAt, "desktopUnread": desktopUnread, "desktopReadStateAt": desktopAt}}})
	}))
	defer upstream.Close()
	server.options.ProxyURL = func(Device, string) string { return upstream.URL }
	mark := func() {
		t.Helper()
		if response := owner.request("POST", "/m1/api/sessions/read", map[string]any{"assistant": "codex", "sessionId": "native-thread", "unread": true}); response.Code != 200 {
			t.Fatal(response.Body.String())
		}
	}
	list := func() sessionReadState {
		t.Helper()
		response := owner.request("GET", "/m1/api/sessions?assistant=codex", nil)
		var result struct {
			Sessions []sessionReadState `json:"sessions"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Sessions) != 1 {
			t.Fatal(response.Body.String())
		}
		return result.Sessions[0]
	}
	mark()
	for _, stamp := range []int64{nativeAt - 1, nativeAt} {
		desktopAt = stamp
		if state := list(); !state.MarkedUnread || state.Version != 1 {
			t.Fatalf("stale native read erased mark %+v", state)
		}
	}
	desktopAt, desktopUnread = nativeAt+1, true
	if state := list(); !state.MarkedUnread {
		t.Fatal("native unread erased gateway mark")
	}
	desktopUnread = false
	if state := list(); state.MarkedUnread || state.ReadAt != nativeAt || state.Version != 2 {
		t.Fatalf("later native read not applied %+v", state)
	}
	if state := list(); state.Version != 2 {
		t.Fatalf("read reconciliation is not idempotent %+v", state)
	}
	// Without a confirmed native write, a later unrelated native snapshot cannot
	// erase the durable fallback (for example, Desktop is closed or outdated).
	receipt = 0
	mark()
	desktopAt = nativeAt + 100
	if state := list(); !state.MarkedUnread || state.Version != 3 {
		t.Fatalf("unconfirmed native state erased fallback %+v", state)
	}
}

func TestSessionReadStateValidationAndNativeFallback(t *testing.T) {
	server, now := newTestServer(t)
	owner, user, _, _ := registerBrowser(t, server, *now, "fallback@example.com")
	server.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(1,?,'1','100.64.0.42','Mac','active',?)", user.ID, now.Unix())
	server.issueDeviceCredentials(1)
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	server.options.ProxyURL = func(Device, string) string { return upstream.URL }
	for _, input := range []map[string]any{
		{"assistant": "other", "sessionId": "thread"}, {"assistant": "codex", "sessionId": "bad\nthread"},
		{"assistant": "dsh", "sessionId": ""}, {"assistant": "codex", "sessionId": "thread", "expectedVersion": -1},
		{"assistant": "codex", "sessionId": "thread", "activityAt": now.Add(6 * time.Minute).UnixMilli()},
	} {
		if response := owner.request("POST", "/m1/api/sessions/read", input); response.Code != 400 {
			t.Fatalf("invalid input accepted: %d %s", response.Code, response.Body.String())
		}
	}
	if response := owner.request("GET", "/m1/api/sessions/read", nil); response.Code != 405 {
		t.Fatalf("method %d", response.Code)
	}
	response := owner.request("POST", "/m1/api/sessions/read", map[string]any{"assistant": "codex", "sessionId": "thread", "unread": true})
	var result struct {
		sessionReadState
		NativeSynced bool `json:"nativeSynced"`
	}
	json.Unmarshal(response.Body.Bytes(), &result)
	if response.Code != 200 || !result.MarkedUnread || result.NativeSynced {
		t.Fatalf("old agent fallback %d %s", response.Code, response.Body.String())
	}
	request := httptest.NewRequest("POST", "/m1/api/sessions/read", nil)
	request.AddCookie(owner.cookie)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatalf("missing CSRF accepted: %d", response.Code)
	}
	request = httptest.NewRequest("POST", "/m1/api/sessions/read", nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatalf("unauthenticated mark accepted: %d", response.Code)
	}
}
