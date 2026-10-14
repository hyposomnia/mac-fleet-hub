package multiuser

import (
	"encoding/json"
	"github.com/pquerna/otp/totp"
	"testing"
	"time"
)

func TestSecurityMistakeKeepsSession(t *testing.T) {
	s, now := newTestServer(t)
	b, _, _, _ := registerBrowser(t, s, *now, "mistake@example.com")
	r := b.request("POST", "/api/auth/password", map[string]string{"current_password": "wrong", "password": "new strong password", "confirm_password": "new strong password"})
	if r.Code != 422 {
		t.Fatalf("credential mistake must be inline 422, got %d", r.Code)
	}
	if r = b.request("GET", "/api/auth/me", nil); r.Code != 200 {
		t.Fatal("session lost")
	}
}
func TestPasswordCountsCharacters(t *testing.T) {
	if validPassword("一二三四") {
		t.Fatal("four characters accepted as twelve")
	}
	if !validPassword("一二三四五六七八九十十一") {
		t.Fatal("twelve characters rejected")
	}
}
func TestTOTPReplacementKeepsRotatedCurrentSession(t *testing.T) {
	s, now := newTestServer(t)
	b, _, secret, _ := registerBrowser(t, s, *now, "rotate@example.com")
	old := *b.cookie
	*now = now.Add(time.Minute)
	code, _ := totp.GenerateCode(secret, *now)
	r := b.request("POST", "/api/auth/totp/start", map[string]string{"password": "correct horse battery", "code": code})
	var setup struct {
		Secret string `json:"secret"`
	}
	json.Unmarshal(r.Body.Bytes(), &setup)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	code, _ = totp.GenerateCode(setup.Secret, *now)
	r = b.request("POST", "/api/auth/totp/confirm", map[string]string{"code": code})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if b.cookie.Value == old.Value {
		t.Fatal("cookie not rotated")
	}
	if r = b.request("GET", "/api/auth/me", nil); r.Code != 200 {
		t.Fatal("current session revoked")
	}
	other := &testBrowser{server: s, cookie: &old}
	if r = other.request("GET", "/api/auth/me", nil); r.Code != 401 {
		t.Fatal("old session retained")
	}
}

func TestAuditHistoryPagination(t *testing.T) {
	s, now := newTestServer(t)
	b, u, _, _ := registerBrowser(t, s, *now, "audit-pages@example.com")
	s.SetAdmin(u.Email)
	for i := 0; i < 55; i++ {
		s.audit(u.ID, u.ID, "test_event")
	}
	r := b.request("GET", "/api/admin/users/"+intString(u.ID)+"?event_page=2", nil)
	var body struct {
		Events []map[string]any `json:"events"`
		Page   int              `json:"event_page"`
		Total  int              `json:"event_total"`
	}
	json.Unmarshal(r.Body.Bytes(), &body)
	if body.Page != 2 || len(body.Events) == 0 || len(body.Events) >= 50 || body.Total < 55 {
		t.Fatalf("pagination missing: %s", r.Body.String())
	}
}
