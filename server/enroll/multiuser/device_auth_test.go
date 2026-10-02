package multiuser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func deviceRequest(server *Server, method, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func TestProxyRequiresCredentialAndInjectsOnlyOwnedSecret(t *testing.T) {
	server, now := newTestServer(t)
	browser, user, _, _ := registerBrowser(t, server, *now, "proxy@example.com")
	server.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(1,?,'node','100.64.0.9','Mac','active',?)", user.ID, now.Unix())
	var expectedProxy string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Fleet-Device-ID") != "m1" || request.Header.Get("X-Fleet-Device-Token") != expectedProxy || request.Header.Get("X-Fleet-Forged") != "" {
			t.Error("missing scoped credential")
		}
		if request.URL.Path != "/m1/files/api/resources" {
			t.Error("wrong loopback proxy path")
		}
		writer.WriteHeader(204)
	}))
	defer upstream.Close()
	server.options.ProxyURL = func(Device, string) string { return upstream.URL }
	if response := browser.request("GET", "/m1/files/api/resources", nil); response.Code != 503 {
		t.Fatalf("legacy device allowed: %d", response.Code)
	}
	_, proxy, err := server.issueDeviceCredentials(1)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/m1/files/api/resources", nil)
	expectedProxy = proxy
	request.AddCookie(browser.cookie)
	request.Header.Set("Origin", server.options.Origin)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("X-Fleet-Device-Token", "spoofed")
	request.Header.Set("X-Fleet-Device-ID", "m999")
	request.Header.Set("X-Fleet-Forged", "spoofed")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatalf("embedded write %d", response.Code)
	}
	if actual, err := server.ProxyCredential(user.ID, "m1"); err != nil || actual != proxy {
		t.Fatal("wrong credential")
	}
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("cross-site write allowed")
	}
}

type failedDeviceNetwork struct{ testNetwork }

func (*failedDeviceNetwork) Reconcile(context.Context, []Device, []User) error {
	return errors.New("offline network")
}

func TestDeviceDisableAndFailedUnbindRemainFailClosed(t *testing.T) {
	server, now := newTestServer(t)
	_, user, _, _ := registerBrowser(t, server, *now, "disable-device@example.com")
	server.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(1,?,'42','100.64.0.42','Mac','active',?)", user.ID, now.Unix())
	token, _, err := server.issueDeviceCredentials(1)
	if err != nil {
		t.Fatal(err)
	}
	server.db.Exec("UPDATE users SET status='disabled' WHERE id=?", user.ID)
	if response := deviceRequest(server, "GET", "/api/device/status", token); response.Code != 403 {
		t.Fatal("disabled device retained lease")
	}
	server.options.Network = &failedDeviceNetwork{}
	response := deviceRequest(server, "DELETE", "/api/device/binding", token)
	if response.Code != 503 || !strings.Contains(response.Body.String(), `"access_revoked":true`) {
		t.Fatal("failed unbind hid irreversible revocation")
	}
	if response = deviceRequest(server, "GET", "/api/device/status", token); response.Code != 410 {
		t.Fatal("failed network revoke restored access")
	}
	if _, err := server.ProxyCredential(user.ID, "m1"); err == nil {
		t.Fatal("revoked proxy token usable")
	}
}

func TestCompletionAndTemporaryCredentialCleanupAreAtomic(t *testing.T) {
	server, now := newTestServer(t)
	server.options.Network = &testNetwork{}
	browser, _, _, _ := registerBrowser(t, server, *now, "atomic-device@example.com")
	installer := &testBrowser{server: server}
	response := installer.request("POST", "/api/enrollment/start", map[string]string{"name": "Mac"})
	var started map[string]any
	json.Unmarshal(response.Body.Bytes(), &started)
	browser.request("POST", "/api/enrollment/confirm", map[string]any{"code": started["code"]})
	claim := map[string]any{"request_id": started["request_id"], "claim_token": started["claim_token"]}
	installer.request("POST", "/api/enrollment/claim", claim)
	if _, err := server.db.Exec(`CREATE TRIGGER failed_cleanup BEFORE UPDATE ON device_credentials BEGIN SELECT RAISE(FAIL,'cleanup unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if response = installer.request("POST", "/api/enrollment/complete", claim); response.Code != 500 {
		t.Fatal("storage error hidden")
	}
	var state string
	server.db.QueryRow("SELECT state FROM enrollments WHERE id=?", started["request_id"]).Scan(&state)
	if state == "complete" {
		t.Fatal("completion committed without credential cleanup")
	}
	server.db.Exec("DROP TRIGGER failed_cleanup")
	if response = installer.request("POST", "/api/enrollment/complete", claim); response.Code != 200 {
		t.Fatal("completion retry failed")
	}
	var original []byte
	if err := server.db.QueryRow("SELECT claim_token FROM device_credentials").Scan(&original); err != nil || len(original) != 0 {
		t.Fatal("original device token retained after completion")
	}
}

func TestDeviceCredentialsAreScopedPrivateAndRetryable(t *testing.T) {
	server, now := newTestServer(t)
	server.options.Network = &testNetwork{}
	browser, user, _, _ := registerBrowser(t, server, *now, "device-owner@example.com")
	installer := &testBrowser{server: server}
	response := installer.request("POST", "/api/enrollment/start", map[string]string{"name": "Private Mac"})
	var started map[string]any
	json.Unmarshal(response.Body.Bytes(), &started)
	preview := browser.request("GET", "/api/enrollment/preview?code="+started["code"].(string), nil)
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), "Private Mac") {
		t.Fatalf("preview %d", preview.Code)
	}
	browser.request("POST", "/api/enrollment/confirm", map[string]any{"code": started["code"]})
	claim := map[string]any{"request_id": started["request_id"], "claim_token": started["claim_token"]}
	response = installer.request("POST", "/api/enrollment/claim", claim)
	var grant map[string]any
	json.Unmarshal(response.Body.Bytes(), &grant)
	token, tokenOK := grant["device_token"].(string)
	proxyToken, proxyOK := grant["proxy_token"].(string)
	if response.Code != 200 || !tokenOK || !proxyOK || len(token) < 40 || token == proxyToken || grant["owner_email"] != user.Email {
		t.Fatal("claim lacks separate scoped credentials")
	}
	response = installer.request("POST", "/api/enrollment/claim", claim)
	var repeated map[string]any
	json.Unmarshal(response.Body.Bytes(), &repeated)
	if repeated["device_token"] != token || repeated["proxy_token"] != proxyToken {
		t.Fatal("credential retry changed identity")
	}
	if response = deviceRequest(server, "GET", "/api/device/status", proxyToken); response.Code != 401 {
		t.Fatalf("proxy token accepted as device token: %d", response.Code)
	}
	if response = deviceRequest(server, "GET", "/api/device/status", token); response.Code != 202 {
		t.Fatalf("installing status %d", response.Code)
	}
	if response = installer.request("POST", "/api/enrollment/complete", claim); response.Code != 200 {
		t.Fatalf("complete %d", response.Code)
	}
	response = deviceRequest(server, "GET", "/api/device/status", token)
	if response.Code != 200 || !strings.Contains(response.Body.String(), user.Email) {
		t.Fatalf("device status %d", response.Code)
	}
	for _, path := range []string{"/api/devices", "/api/auth/me"} {
		response = browser.request("GET", path, nil)
		if strings.Contains(response.Body.String(), token) || strings.Contains(response.Body.String(), proxyToken) {
			t.Fatal("credential exposed in browser JSON")
		}
	}
	response = deviceRequest(server, "DELETE", "/api/device/binding", token)
	if response.Code != 200 {
		t.Fatalf("unbind %d", response.Code)
	}
	if response = deviceRequest(server, "GET", "/api/device/status", token); response.Code != 410 {
		t.Fatalf("revoked device retained lease %d", response.Code)
	}
}
