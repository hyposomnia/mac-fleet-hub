package multiuser

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTrustedProxyClientAddress(t *testing.T) {
	s, _ := newTestServer(t)
	for _, tt := range []struct{ peer, forwarded, want string }{
		{"127.0.0.1:123", "198.51.100.8", "198.51.100.8"},
		{"198.51.100.9:123", "198.51.100.8", "198.51.100.9"},
		{"127.0.0.1:123", "192.0.2.99, 198.51.100.8", "198.51.100.8"},
		{"[::1]:123", "bad-input", "::1"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tt.peer
		r.Header.Set("X-Forwarded-For", tt.forwarded)
		if got := s.clientIP(r); got != tt.want {
			t.Fatalf("%s %s got %s", tt.peer, tt.forwarded, got)
		}
	}
}
func TestConfiguredAuthLimitsRetryTiming(t *testing.T) {
	s, now := newTestServer(t)
	s.options.AuthAttempts = 2
	s.options.AuthWindow = time.Minute
	r := httptest.NewRequest("POST", "/", nil)
	if !s.allowedAuth(r, "user") || !s.allowedAuth(r, "user") || s.allowedAuth(r, "user") {
		t.Fatal("configured attempts ignored")
	}
	w := httptest.NewRecorder()
	s.rejectAuthLimit(w, r, "user")
	if w.Code != 429 || w.Header().Get("Retry-After") != "61" {
		t.Fatalf("missing retry timing %v", w)
	}
	*now = now.Add(time.Minute + time.Second)
	if !s.allowedAuth(r, "user") {
		t.Fatal("limit did not reset")
	}
}
func TestConfiguredSessionSlidingRenewal(t *testing.T) {
	s, now := newTestServer(t)
	s.options.SessionLifetime = time.Hour
	s.options.SessionSliding = true
	b, _, _, _ := registerBrowser(t, s, *now, "sliding@example.com")
	if b.cookie.MaxAge != 3600 {
		t.Fatalf("duration ignored %d", b.cookie.MaxAge)
	}
	original := b.cookie.Expires
	*now = now.Add(40 * time.Minute)
	if r := b.request("GET", "/api/auth/me", nil); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if !b.cookie.Expires.After(original) {
		t.Fatal("active session did not renew")
	}
	*now = now.Add(30 * time.Minute)
	if r := b.request("GET", "/api/auth/me", nil); r.Code != 200 {
		t.Fatal("renewed session expired at original deadline")
	}
}
func TestAuthStateRestoresPendingStages(t *testing.T) {
	s, now := newTestServer(t)
	b, _, _, _ := registerBrowser(t, s, *now, "state@example.com")
	r := b.request("POST", "/api/auth/login", map[string]string{"email": "state@example.com", "password": "correct horse battery"})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r = b.request("GET", "/api/auth/state", nil)
	var state map[string]any
	json.Unmarshal(r.Body.Bytes(), &state)
	if r.Code != 200 || state["state"] != "challenge" {
		t.Fatalf("lost pending challenge: %s", r.Body.String())
	}
	if r = b.request("GET", "/api/auth/me", nil); r.Code != 401 {
		t.Fatal("pending session gained full access")
	}
}
