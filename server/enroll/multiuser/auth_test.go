package multiuser

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

type testBrowser struct {
	server *Server
	cookie *http.Cookie
	csrf   string
}

func (browser *testBrowser) request(method, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.Header.Set("Origin", "http://127.0.0.1:7099")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", browser.csrf)
	if browser.cookie != nil {
		request.AddCookie(browser.cookie)
	}
	response := httptest.NewRecorder()
	browser.server.ServeHTTP(response, request)
	for _, cookie := range response.Result().Cookies() {
		browser.cookie = cookie
	}
	return response
}

func newTestServer(t *testing.T) (*Server, *time.Time) {
	t.Helper()
	now := time.Unix(1800000000, 0)
	server, err := New(Options{StateDir: t.TempDir(), Origin: "http://127.0.0.1:7099", Key: bytes.Repeat([]byte{9}, 32), Now: func() time.Time { return now }, StaticDir: filepath.Join("..", "..", "dashboard")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return server, &now
}

func registerBrowser(t *testing.T, server *Server, now time.Time, email string) (*testBrowser, User, string, []string) {
	t.Helper()
	browser := &testBrowser{server: server}
	response := browser.request("POST", "/api/auth/register", map[string]string{"email": email, "password": "correct horse battery", "confirm_password": "correct horse battery"})
	if response.Code != 200 {
		t.Fatalf("register: %d %s", response.Code, response.Body.String())
	}
	var setup struct {
		User User `json:"user"`
		TOTP struct {
			Secret string `json:"secret"`
		} `json:"totp"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(setup.TOTP.Secret, now)
	if err != nil {
		t.Fatal(err)
	}
	response = browser.request("POST", "/api/auth/verify", map[string]string{"code": code})
	if response.Code != 200 {
		t.Fatalf("verify: %d %s", response.Code, response.Body.String())
	}
	var verified struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	json.Unmarshal(response.Body.Bytes(), &verified)
	response = browser.request("GET", "/api/auth/me", nil)
	var me struct {
		CSRF string `json:"csrf_token"`
		User User   `json:"user"`
	}
	json.Unmarshal(response.Body.Bytes(), &me)
	browser.csrf = me.CSRF
	return browser, me.User, setup.TOTP.Secret, verified.RecoveryCodes
}

func TestRegistrationRequiresTOTP(t *testing.T) {
	server, _ := newTestServer(t)
	browser := &testBrowser{server: server}
	response := browser.request("POST", "/api/auth/register", map[string]string{"email": " Person@Example.com ", "password": "correct horse battery", "confirm_password": "correct horse battery"})
	if response.Code != 200 {
		t.Fatalf("register %d %s", response.Code, response.Body.String())
	}
	if response = browser.request("GET", "/api/auth/me", nil); response.Code != 401 {
		t.Fatalf("pending session accessed private data: %d", response.Code)
	}
	other := &testBrowser{server: server}
	if response = other.request("POST", "/api/auth/register", map[string]string{"email": "person@example.com", "password": "correct horse battery", "confirm_password": "correct horse battery"}); response.Code != 409 {
		t.Fatalf("duplicate email %d", response.Code)
	}
	if response = other.request("POST", "/api/auth/register", map[string]string{"email": "another@example.com", "password": "correct horse battery", "confirm_password": "different"}); response.Code != 400 {
		t.Fatalf("confirmation %d", response.Code)
	}
}

func TestAuthReplayExpiryAndCSRF(t *testing.T) {
	server, now := newTestServer(t)
	browser, _, secret, _ := registerBrowser(t, server, *now, "first@example.com")
	login := &testBrowser{server: server}
	response := login.request("POST", "/api/auth/login", map[string]string{"email": "first@example.com", "password": "correct horse battery"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	code, _ := totp.GenerateCode(secret, *now)
	if response = login.request("POST", "/api/auth/verify", map[string]string{"code": code}); response.Code != 401 {
		t.Fatalf("TOTP replay %d", response.Code)
	}
	browser.csrf = "wrong"
	if response = browser.request("POST", "/api/auth/logout", nil); response.Code != 403 {
		t.Fatalf("CSRF %d", response.Code)
	}
	*now = now.Add(30 * 24 * time.Hour)
	if response = browser.request("GET", "/api/auth/me", nil); response.Code != 401 {
		t.Fatalf("absolute expiry %d", response.Code)
	}
}

func TestRecoveryCodesAreSingleUse(t *testing.T) {
	server, now := newTestServer(t)
	browser, _, _, codes := registerBrowser(t, server, *now, "recover@example.com")
	if len(codes) != 10 {
		t.Fatalf("recovery count %d", len(codes))
	}
	recovery := &testBrowser{server: server}
	response := recovery.request("POST", "/api/auth/recover", map[string]string{"email": "recover@example.com", "password": "correct horse battery", "recovery_code": codes[0]})
	if response.Code != 200 {
		t.Fatalf("recover %d %s", response.Code, response.Body.String())
	}
	if response = browser.request("GET", "/api/auth/me", nil); response.Code != 401 {
		t.Fatalf("old session survives recovery %d", response.Code)
	}
	if response = recovery.request("POST", "/api/auth/recover", map[string]string{"email": "recover@example.com", "password": "correct horse battery", "recovery_code": codes[0]}); response.Code != 401 {
		t.Fatalf("reused recovery %d", response.Code)
	}
}
