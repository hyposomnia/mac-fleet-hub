package multiuser

import (
	"encoding/json"
	"testing"
)

func TestSessionPreferencesKeepExplicitZeroAndLargeEvictionTargets(t *testing.T) {
	server, now := newTestServer(t)
	browser, _, _, _ := registerBrowser(t, server, *now, "preferences-parity@example.com")
	wanted := map[string]int{"desktopMaxWindows": 50, "mobileMaxWindows": 25, "chatCacheMaxSessions": 40, "autoCloseMinutes": 0}
	response := browser.request("POST", "/api/settings", wanted)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	check := func() {
		t.Helper()
		response := browser.request("GET", "/api/settings", nil)
		var values map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &values); err != nil {
			t.Fatal(err)
		}
		for key, expected := range wanted {
			var actual int
			if err := json.Unmarshal(values[key], &actual); err != nil || actual != expected {
				t.Fatalf("%s = %s, want %d", key, values[key], expected)
			}
		}
	}
	check()
	// Partial updates must not restore a disabled timer or reset larger targets.
	if response = browser.request("POST", "/api/settings", map[string]int{"desktopScrollback": 5000}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	check()
	if response = browser.request("POST", "/api/settings", map[string]int{"autoCloseMinutes": -1}); response.Code != 400 {
		t.Fatalf("negative idle accepted: %d", response.Code)
	}
	check()
}

func TestFileViewPreferencePersistsAcrossClientsAndServerRestart(t *testing.T) {
	server, now := newTestServer(t)
	owner, user, _, _ := registerBrowser(t, server, *now, "file-view@example.com")
	other, _, _, _ := registerBrowser(t, server, *now, "other-file-view@example.com")
	readView := func(browser *testBrowser) string {
		t.Helper()
		response := browser.request("GET", "/api/settings", nil)
		var preferences dashboardPreferences
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &preferences) != nil {
			t.Fatalf("read settings: %d %s", response.Code, response.Body.String())
		}
		return preferences.FileView
	}
	if got := readView(owner); got != "" {
		t.Fatalf("new account already has file view %q", got)
	}
	for _, view := range []string{"icons", "list", "columns"} {
		response := owner.request("PATCH", "/api/settings", map[string]any{"fileView": view})
		if response.Code != 200 || readView(owner) != view {
			t.Fatalf("save %s: %d %s", view, response.Code, response.Body.String())
		}
	}
	// Initial migration from another browser must preserve an existing server choice.
	if response := owner.request("PATCH", "/api/settings", map[string]any{"fileView": "icons", "ifAbsent": true}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if response := owner.request("POST", "/api/settings", map[string]int{"chatCacheMaxSessions": 9}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	for _, invalid := range []any{"tiles", "", 1, false} {
		if response := owner.request("PATCH", "/api/settings", map[string]any{"fileView": invalid}); response.Code != 400 {
			t.Fatalf("invalid view %v accepted: %d", invalid, response.Code)
		}
	}
	if readView(owner) != "columns" || readView(other) != "" {
		t.Fatal("file view was overwritten or leaked across accounts")
	}
	var raw string
	if err := server.db.QueryRow("SELECT value FROM preferences WHERE user_id=?", user.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored dashboardPreferences
	if err := json.Unmarshal([]byte(raw), &stored); err != nil || stored.FileView != "columns" || stored.ChatCacheMaxSessions != 9 {
		t.Fatalf("wrong stored preferences: %s", raw)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(server.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	// A new browser client has no local state; the same login reads the saved choice.
	client := &testBrowser{server: restarted, cookie: owner.cookie, csrf: owner.csrf}
	if got := readView(client); got != "columns" {
		t.Fatalf("after restart: %q", got)
	}
}
