package multiuser

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func oauthTestParameters() url.Values {
	verifier := strings.Repeat("v", 43)
	challenge := sha256.Sum256([]byte(verifier))
	return url.Values{"client_id": {"fleet-hub"}, "response_type": {"code"}, "scope": {"device:enroll"},
		"redirect_uri": {"http://127.0.0.1:51234/oauth/callback"}, "state": {strings.Repeat("s", 43)},
		"code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "device_name": {"Test Mac"}}
}

func oauthTestConsent(t *testing.T, browser *testBrowser, parameters url.Values) string {
	t.Helper()
	response := browser.request("GET", "/oauth/authorize?"+parameters.Encode(), nil)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("authorize: %d %s", response.Code, response.Body.String())
	}
	location, _ := url.Parse(response.Header().Get("Location"))
	requestID := location.Query().Get("request")
	if location.Path != "/oauth/consent" || requestID == "" {
		t.Fatalf("consent URL: %v", location)
	}
	response = browser.request("POST", "/api/oauth/authorize", map[string]string{"request_id": requestID, "action": "approve"})
	if response.Code != 200 {
		t.Fatalf("consent: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Redirect string `json:"redirect_uri"`
	}
	json.Unmarshal(response.Body.Bytes(), &result)
	callback, _ := url.Parse(result.Redirect)
	if callback.Query().Get("state") != parameters.Get("state") {
		t.Fatal("callback lost state")
	}
	return callback.Query().Get("code")
}

func oauthTestExchange(server *Server, code, verifier, redirect string) *httptest.ResponseRecorder {
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"fleet-hub"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirect}}
	request := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func TestOAuthPKCECodeIsSingleUseAndOwnerComesFromBrowser(t *testing.T) {
	server, now := newTestServer(t)
	browser, owner, _, _ := registerBrowser(t, server, *now, "oauth@example.test")
	parameters := oauthTestParameters()
	code := oauthTestConsent(t, browser, parameters)
	if code == "" {
		t.Fatal("no authorization code")
	}
	if response := oauthTestExchange(server, code, strings.Repeat("x", 43), parameters.Get("redirect_uri")); response.Code != 400 {
		t.Fatalf("wrong PKCE: %d", response.Code)
	}
	if response := oauthTestExchange(server, code, strings.Repeat("v", 43), "http://127.0.0.1:51235/oauth/callback"); response.Code != 400 {
		t.Fatalf("wrong redirect: %d", response.Code)
	}
	response := oauthTestExchange(server, code, strings.Repeat("v", 43), parameters.Get("redirect_uri"))
	if response.Code != 200 {
		t.Fatalf("token: %d %s", response.Code, response.Body.String())
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		EnrollmentID string `json:"enrollment_id"`
		TokenType    string `json:"token_type"`
	}
	json.Unmarshal(response.Body.Bytes(), &token)
	if len(token.AccessToken) < 40 || token.EnrollmentID == "" || token.TokenType != "Bearer" {
		t.Fatal("incomplete enrollment token")
	}
	if response := oauthTestExchange(server, code, strings.Repeat("v", 43), parameters.Get("redirect_uri")); response.Code != 400 {
		t.Fatalf("replayed code: %d", response.Code)
	}
	devices, _ := server.Devices(owner.ID)
	if len(devices) != 1 || devices[0].UserID != owner.ID {
		t.Fatalf("owner devices: %+v", devices)
	}
	var stored string
	server.db.QueryRow("SELECT claim_hash FROM enrollments WHERE id=?", token.EnrollmentID).Scan(&stored)
	if stored != digest(token.AccessToken) {
		t.Fatal("enrollment credential not hashed")
	}
	for _, path := range []string{"/api/devices", "/api/device/status"} {
		request := httptest.NewRequest("GET", path, nil)
		request.Header.Set("Authorization", "Bearer "+token.AccessToken)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != 401 {
			t.Fatalf("enrollment token escaped its scope at %s: %d", path, response.Code)
		}
	}
}

func TestOAuthRejectsUnsafeRedirectsWeakPKCEAndDuplicateParameters(t *testing.T) {
	server, _ := newTestServer(t)
	for _, redirect := range []string{"https://evil.test/cb", "http://localhost:51234/oauth/callback", "http://127.0.0.2:51234/oauth/callback", "http://127.0.0.1/oauth/callback", "http://user@127.0.0.1:51234/oauth/callback", "http://127.0.0.1:51234/other", "http://127.0.0.1:51234/oauth/callback?state=x"} {
		parameters := oauthTestParameters()
		parameters.Set("redirect_uri", redirect)
		response := (&testBrowser{server: server}).request("GET", "/oauth/authorize?"+parameters.Encode(), nil)
		if response.Code != 400 || response.Header().Get("Location") != "" {
			t.Fatalf("unsafe redirect %s: %d", redirect, response.Code)
		}
	}
	for _, change := range []func(url.Values){func(values url.Values) { values.Set("code_challenge_method", "plain") }, func(values url.Values) { values.Set("code_challenge", "bad") }, func(values url.Values) { values.Add("state", "second") }} {
		parameters := oauthTestParameters()
		change(parameters)
		if response := (&testBrowser{server: server}).request("GET", "/oauth/authorize?"+parameters.Encode(), nil); response.Code != 400 {
			t.Fatalf("unsafe parameters: %d", response.Code)
		}
	}
	if response := (&testBrowser{server: server}).request("GET", "/oauth/authorize?"+oauthTestParameters().Encode()+"&bad=%", nil); response.Code != 400 {
		t.Fatalf("malformed authorize query: %d", response.Code)
	}
}

func TestOAuthExpiryAndConsentCSRF(t *testing.T) {
	server, now := newTestServer(t)
	browser, _, _, _ := registerBrowser(t, server, *now, "expiry@example.test")
	parameters := oauthTestParameters()
	response := browser.request("GET", "/oauth/authorize?"+parameters.Encode(), nil)
	location, _ := url.Parse(response.Header().Get("Location"))
	requestID := location.Query().Get("request")
	validCSRF := browser.csrf
	browser.csrf = "invalid"
	if response := browser.request("POST", "/api/oauth/authorize", map[string]string{"request_id": requestID, "action": "approve"}); response.Code != 403 {
		t.Fatalf("CSRF: %d", response.Code)
	}
	browser.csrf = validCSRF
	response = browser.request("POST", "/api/oauth/authorize", map[string]string{"request_id": requestID, "action": "approve"})
	var result struct {
		Redirect string `json:"redirect_uri"`
	}
	json.Unmarshal(response.Body.Bytes(), &result)
	callback, _ := url.Parse(result.Redirect)
	*now = now.Add(121 * time.Second)
	if response := oauthTestExchange(server, callback.Query().Get("code"), strings.Repeat("v", 43), parameters.Get("redirect_uri")); response.Code != 400 {
		t.Fatalf("expired code: %d", response.Code)
	}
}

func TestOAuthDeniedAndDisabledAccountsCannotCreateDevices(t *testing.T) {
	for _, deny := range []bool{true, false} {
		server, now := newTestServer(t)
		browser, owner, _, _ := registerBrowser(t, server, *now, "denial@example.test")
		parameters := oauthTestParameters()
		if deny {
			response := browser.request("GET", "/oauth/authorize?"+parameters.Encode(), nil)
			location, _ := url.Parse(response.Header().Get("Location"))
			id := location.Query().Get("request")
			response = browser.request("POST", "/api/oauth/authorize", map[string]string{"request_id": id, "action": "deny"})
			var callback struct {
				Redirect string `json:"redirect_uri"`
			}
			json.Unmarshal(response.Body.Bytes(), &callback)
			redirect, _ := url.Parse(callback.Redirect)
			if response.Code != 200 || redirect.Query().Get("error") != "access_denied" || redirect.Query().Get("code") != "" {
				t.Fatal("denial granted credentials")
			}
			if response := browser.request("POST", "/api/oauth/authorize", map[string]string{"request_id": id, "action": "approve"}); response.Code != 410 {
				t.Fatal("denied request was approved later")
			}
		} else {
			code := oauthTestConsent(t, browser, parameters)
			if _, err := server.db.Exec("UPDATE users SET status='disabled' WHERE id=?", owner.ID); err != nil {
				t.Fatal(err)
			}
			if response := oauthTestExchange(server, code, strings.Repeat("v", 43), parameters.Get("redirect_uri")); response.Code != 400 {
				t.Fatal("disabled owner obtained enrollment")
			}
		}
		devices, err := server.Devices(owner.ID)
		if err != nil || len(devices) != 0 {
			t.Fatalf("unapproved devices: %d %v", len(devices), err)
		}
	}
}

func TestOAuthConcurrentExchangesCreateOnlyOneEnrollment(t *testing.T) {
	server, now := newTestServer(t)
	browser, owner, _, _ := registerBrowser(t, server, *now, "concurrent@example.test")
	parameters := oauthTestParameters()
	code := oauthTestConsent(t, browser, parameters)
	var group sync.WaitGroup
	statuses := make(chan int, 2)
	for attempt := 0; attempt < 2; attempt++ {
		group.Add(1)
		go func() {
			defer group.Done()
			statuses <- oauthTestExchange(server, code, strings.Repeat("v", 43), parameters.Get("redirect_uri")).Code
		}()
	}
	group.Wait()
	first, second := <-statuses, <-statuses
	if first+second != 600 {
		t.Fatalf("concurrent exchange: %d %d", first, second)
	}
	devices, _ := server.Devices(owner.ID)
	if len(devices) != 1 {
		t.Fatalf("duplicated devices: %d", len(devices))
	}
}

func TestOAuthPendingExpiryRequiresFreshAuthorization(t *testing.T) {
	server, now := newTestServer(t)
	browser, _, _, _ := registerBrowser(t, server, *now, "fresh@example.test")
	parameters := oauthTestParameters()
	response := browser.request("GET", "/oauth/authorize?"+parameters.Encode(), nil)
	location, _ := url.Parse(response.Header().Get("Location"))
	*now = now.Add(601 * time.Second)
	if response = browser.request("GET", "/api/oauth/preview?request="+location.Query().Get("request"), nil); response.Code != 410 {
		t.Fatal("expired request remained usable")
	}
	if response = browser.request("GET", "/oauth/authorize?"+parameters.Encode(), nil); response.Code != 409 {
		t.Fatal("reopened stale authorization")
	}
	challenge := sha256.Sum256([]byte(strings.Repeat("n", 43)))
	parameters.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	parameters.Set("state", strings.Repeat("n", 43))
	if response = browser.request("GET", "/oauth/authorize?"+parameters.Encode(), nil); response.Code != 303 {
		t.Fatalf("fresh retry: %d", response.Code)
	}
}
