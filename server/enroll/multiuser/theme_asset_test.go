package multiuser

import (
	"strings"
	"testing"
)

func TestThemeBootstrapIsPublicButAppStaysPrivate(t *testing.T) {
	server, _ := newTestServer(t)
	browser := &testBrowser{server: server}
	response := browser.request("GET", "/theme.js?v=155", nil)
	if response.Code != 200 {
		t.Fatalf("theme bootstrap: got %d, want 200", response.Code)
	}
	if response = browser.request("GET", "/app.js?v=155", nil); response.Code != 303 {
		t.Fatalf("private app: got %d, want 303", response.Code)
	}
}

func TestLoginEffectsArePublicWithoutExposingSettingsScripts(testContext *testing.T) {
	server, _ := newTestServer(testContext)
	browser := &testBrowser{server: server}
	response := browser.request("GET", "/auth_effects.js?v=185", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "FleetAuthEffects") {
		testContext.Fatalf("public login effects: got %d, want JavaScript with status 200", response.Code)
	}
	for _, path := range []string{"/app.js?v=185", "/settings_dialog.js?v=185", "/sw.js"} {
		if response = browser.request("GET", path, nil); response.Code != 303 {
			testContext.Fatalf("private script %s: got %d, want 303", path, response.Code)
		}
	}
}
