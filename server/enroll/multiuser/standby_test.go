package multiuser

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStandbyOwnershipCoverageAndRevocableReadOnlyCapability(t *testing.T) {
	s, now := newTestServer(t)
	owner, user, _, _ := registerBrowser(t, s, *now, "widget-owner@example.com")
	other, otherUser, _, _ := registerBrowser(t, s, *now, "widget-other@example.com")
	if err := s.SetAdmin(otherUser.Email); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		uid := user.ID
		if index == 3 {
			uid = otherUser.ID
		}
		_, err := s.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(?,?,?,'100.64.0.42','Mac','active',?)", index, uid, intString(int64(index)), now.Unix())
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.issueDeviceCredentials(int64(index)); err != nil {
			t.Fatal(err)
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Fleet-Device-Token") == "" {
			t.Error("browser/widget credential forwarded to device")
		}
		if r.Header.Get("X-Fleet-Device-ID") == "m3" {
			t.Error("queried another owner's device")
		}
		if r.URL.Path == "/api/usage/daily" {
			if r.Header.Get("X-Fleet-Device-ID") == "m2" {
				http.NotFound(w, r)
				return
			}
			respond(w, 200, standbyUsage{Date: now.UTC().Format("2006-01-02"), Timezone: "UTC", Models: []standbyModel{{Assistant: "codex", Model: "gpt-5", InputTokens: 100, OutputTokens: 20, TotalTokens: 120}}})
			return
		}
		if r.URL.Path == "/api/sessions" {
			respond(w, 200, map[string]any{"sessions": []map[string]any{{"sessionId": "session", "title": "Test title", "cwd": "/private/project", "status": "active", "mtime": now.UnixMilli()}}})
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	s.options.ProxyURL = func(Device, string) string { return upstream.URL }
	response := owner.request("GET", "/api/standby?timezone=UTC", nil)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(response.Code, response.Body.String())
	}
	var snapshot standbySnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Hosts) != 2 || snapshot.ReportingHosts != 1 || snapshot.UsageComplete || snapshot.TotalTokens != 120 || len(snapshot.Sessions) != 6 {
		t.Fatalf("bad coverage %+v", snapshot)
	}
	if snapshot.Hosts[1].UsageStatus != "upgrade-required" || strings.Contains(response.Body.String(), "/private/") {
		t.Fatal(response.Body.String())
	}
	response = owner.request("POST", "/api/standby/token", map[string]any{})
	var token struct {
		Token string `json:"token"`
	}
	json.Unmarshal(response.Body.Bytes(), &token)
	if response.Code != 200 || token.Token == "" {
		t.Fatal(response.Code, response.Body.String())
	}
	capability := func(method, route, value string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, route, nil)
		r.Header.Set("Authorization", "Bearer "+value)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if response := capability("GET", "/api/standby?timezone=UTC", token.Token); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, route := range []string{"/api/devices", "/api/auth/state", "/m1/api/sessions", "/api/standby/token"} {
		if response := capability("GET", route, token.Token); response.Code != 401 {
			t.Fatalf("capability escaped scope %s: %d", route, response.Code)
		}
	}
	if response := capability("POST", "/api/standby", token.Token); response.Code != 405 {
		t.Fatal("capability permitted writes", response.Code)
	}
	if response := capability("GET", "/api/standby", token.Token+"x"); response.Code != 401 {
		t.Fatal("tamper accepted")
	}
	if response := owner.request("GET", "/api/standby?timezone=not-a-zone", nil); response.Code != 400 {
		t.Fatal("bad zone accepted")
	}
	adminUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fleet-Device-ID") != "m3" {
			t.Error("admin queried another owner's device")
		}
		if r.URL.Path == "/api/usage/daily" {
			respond(w, 200, standbyUsage{Date: now.UTC().Format("2006-01-02"), Timezone: "UTC", Models: []standbyModel{}})
			return
		}
		respond(w, 200, map[string]any{"sessions": []any{}})
	}))
	defer adminUpstream.Close()
	s.options.ProxyURL = func(Device, string) string { return adminUpstream.URL }
	response = other.request("GET", "/api/standby?timezone=UTC", nil)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil || len(snapshot.Hosts) != 1 || snapshot.Hosts[0].ID != "m3" {
		t.Fatal("admin aggregation escaped owner scope", response.Body.String())
	}
	owner.request("POST", "/api/auth/logout", map[string]any{})
	if response := capability("GET", "/api/standby", token.Token); response.Code != 401 {
		t.Fatalf("logout did not revoke: %d %s", response.Code, response.Body.String())
	}
}

func TestStandbyEmptyAccountAndRejectsAnonymous(t *testing.T) {
	s, now := newTestServer(t)
	owner, _, _, _ := registerBrowser(t, s, *now, "widget-empty@example.com")
	response := owner.request("GET", "/api/standby", nil)
	var snapshot standbySnapshot
	json.Unmarshal(response.Body.Bytes(), &snapshot)
	if response.Code != 200 || len(snapshot.Hosts) != 0 || snapshot.TotalTokens != 0 {
		t.Fatal(response.Body.String())
	}
	anonymous := &testBrowser{server: s}
	for _, route := range []string{"/api/standby", "/api/standby/token"} {
		if response := anonymous.request("GET", route, nil); response.Code != 401 {
			t.Fatal("anonymous accepted", route, response.Code)
		}
	}
}

func TestStandbyCapabilityExpiryAndDisabledOwner(t *testing.T) {
	s, now := newTestServer(t)
	browser, user, _, _ := registerBrowser(t, s, *now, "widget-expiry@example.com")
	response := browser.request("POST", "/api/standby/token", map[string]any{})
	var grant struct {
		Token string `json:"token"`
	}
	json.Unmarshal(response.Body.Bytes(), &grant)
	if _, err := s.standbyUser(grant.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE users SET status='disabled' WHERE id=?", user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.standbyUser(grant.Token); err == nil {
		t.Fatal("disabled owner accepted")
	}
	if _, err := s.db.Exec("UPDATE users SET status='active' WHERE id=?", user.ID); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(31 * 24 * time.Hour)
	if _, err := s.standbyUser(grant.Token); err == nil {
		t.Fatal("expired capability accepted")
	}
}

func TestStandbyDiscardsDeviceResponseAfterRebinding(t *testing.T) {
	s, now := newTestServer(t)
	browser, user, _, _ := registerBrowser(t, s, *now, "widget-rebinding@example.com")
	_, err := s.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(1,?,'old-node','100.64.0.42','Mac','active',?)", user.ID, now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.issueDeviceCredentials(1); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/usage/daily" {
			if _, err := s.db.Exec("UPDATE devices SET node_id='new-node' WHERE device_index=1"); err != nil {
				t.Error(err)
			}
			respond(w, 200, standbyUsage{Date: now.UTC().Format("2006-01-02"), Timezone: "UTC", Models: []standbyModel{{Assistant: "codex", Model: "private-old-device", InputTokens: 100, TotalTokens: 100}}})
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	s.options.ProxyURL = func(Device, string) string { return upstream.URL }
	response := browser.request("GET", "/api/standby?timezone=UTC", nil)
	if response.Code != 200 || strings.Contains(response.Body.String(), "private-old-device") {
		t.Fatal("stale binding response leaked", response.Code, response.Body.String())
	}
}
