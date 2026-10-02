package multiuser

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestPreferencesAreUserScoped(t *testing.T) {
	server, now := newTestServer(t)
	first, _, _, _ := registerBrowser(t, server, *now, "prefs-one@example.com")
	second, _, _, _ := registerBrowser(t, server, *now, "prefs-two@example.com")
	response := first.request("POST", "/api/settings", map[string]int{"chatCacheMaxSessions": 2})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response = second.request("GET", "/api/settings", nil)
	var settings map[string]int
	json.Unmarshal(response.Body.Bytes(), &settings)
	if settings["chatCacheMaxSessions"] != 6 {
		t.Fatalf("leaked preference %+v", settings)
	}
}

func TestPasswordChangeRevokesAllSessions(t *testing.T) {
	server, now := newTestServer(t)
	first, _, secret, _ := registerBrowser(t, server, *now, "password@example.com")
	*now = now.Add(time.Minute)
	code, _ := totp.GenerateCode(secret, *now)
	response := first.request("POST", "/api/auth/password", map[string]string{"current_password": "correct horse battery", "password": "different strong password", "confirm_password": "different strong password", "code": code})
	if response.Code != 200 {
		t.Fatalf("password change %d %s", response.Code, response.Body.String())
	}
	if response = first.request("GET", "/api/auth/me", nil); response.Code != 401 {
		t.Fatalf("old session %d", response.Code)
	}
	response = first.request("POST", "/api/auth/login", map[string]string{"email": "password@example.com", "password": "correct horse battery"})
	if response.Code != 401 {
		t.Fatalf("old password %d", response.Code)
	}
}

func TestAuthRejectsForeignOrigin(t *testing.T) {
	server, _ := newTestServer(t)
	request := httptest.NewRequest("POST", "/api/auth/register", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-site register %d", response.Code)
	}
}

func TestImportLegacyIsExplicitAndIdempotent(t *testing.T) {
	server, now := newTestServer(t)
	_, owner, _, _ := registerBrowser(t, server, *now, "legacy@example.com")
	devices := []Device{{Index: 7, NodeID: "legacy-7", IP: "100.64.0.7", Name: "Old Mac", Status: "active"}}
	if err := server.ImportLegacy(owner.Email, devices, json.RawMessage(`{"chatCacheMaxSessions":3}`)); err != nil {
		t.Fatal(err)
	}
	if err := server.ImportLegacy(owner.Email, devices, nil); err != nil {
		t.Fatal(err)
	}
	result, err := server.Devices(owner.ID)
	if err != nil || len(result) != 1 || result[0].ID != "m7" {
		t.Fatalf("import %+v %v", result, err)
	}
	if err := server.ImportLegacy("unknown@example.com", devices, nil); err == nil {
		t.Fatal("must not invent legacy owner")
	}
}

func TestAccountResetRevokesAndRequiresNewBinding(t *testing.T) {
	server, now := newTestServer(t)
	browser, owner, _, _ := registerBrowser(t, server, *now, "reset@example.com")
	if err := server.ResetPassword(owner.Email, "replacement strong password"); err != nil {
		t.Fatal(err)
	}
	if response := browser.request("GET", "/api/auth/me", nil); response.Code != 401 {
		t.Fatalf("reset retained session %d", response.Code)
	}
	response := browser.request("POST", "/api/auth/login", map[string]string{"email": owner.Email, "password": "replacement strong password"})
	var result map[string]any
	json.Unmarshal(response.Body.Bytes(), &result)
	if response.Code != 200 || result["state"] != "setup" {
		t.Fatalf("reset skips binding %d %s", response.Code, response.Body.String())
	}
}
