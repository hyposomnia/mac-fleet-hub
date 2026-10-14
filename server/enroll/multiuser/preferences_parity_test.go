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
