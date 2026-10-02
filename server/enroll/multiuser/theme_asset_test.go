package multiuser

import "testing"

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
