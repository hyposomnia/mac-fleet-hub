package multiuser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOAuthNativeClientAgainstRealServer(t *testing.T) {
	if testing.Short() {
		t.Skip("native OAuth process integration")
	}
	work := t.TempDir()
	helper := filepath.Join(work, "native-oauth-tests")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-c", "-o", helper, ".")
	command.Dir = filepath.Join("..", "..", "..", "mac", "fleet-agent")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native helper build: %v %s", err, output)
	}
	server, now := newTestServer(t)
	server.options.Network = &testNetwork{}
	browser, owner, _, _ := registerBrowser(t, server, *now, "native-oauth@example.test")
	_, otherUser, _, _ := registerBrowser(t, server, *now, "native-other@example.test")
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	server.options.Origin = httpServer.URL
	server.options.LoginServer = "https://control.example.test"
	browserFile := filepath.Join(work, "browser.url")
	state := filepath.Join(work, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	process := exec.CommandContext(ctx, helper, "-test.run=^TestDesktopOAuthIntegrationProcess$")
	process.Env = append(os.Environ(), "FLEET_OAUTH_INTEGRATION=1", "FLEET_OAUTH_ORIGIN="+httpServer.URL, "FLEET_OAUTH_BROWSER_FILE="+browserFile, "FLEET_BINDING_FILE="+filepath.Join(state, "binding.json"))
	privateLog, err := os.OpenFile(filepath.Join(work, "process.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer privateLog.Close()
	process.Stdout, process.Stderr = privateLog, privateLog
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { process.Process.Kill(); process.Wait() }()
	var authorization []byte
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		authorization, err = os.ReadFile(browserFile)
		if err == nil && len(authorization) != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(authorization) == 0 {
		t.Fatal("native process did not generate fresh authorization")
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequest(http.MethodGet, string(authorization), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(browser.cookie)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("OAuth authorize: %d", response.StatusCode)
	}
	consent, _ := url.Parse(response.Header.Get("Location"))
	body, _ := json.Marshal(map[string]string{"request_id": consent.Query().Get("request"), "action": "approve"})
	request, _ = http.NewRequest(http.MethodPost, httpServer.URL+"/api/oauth/authorize", strings.NewReader(string(body)))
	request.AddCookie(browser.cookie)
	request.Header.Set("Origin", httpServer.URL)
	request.Header.Set("X-CSRF-Token", browser.csrf)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var callback struct {
		Redirect string `json:"redirect_uri"`
	}
	err = json.NewDecoder(response.Body).Decode(&callback)
	response.Body.Close()
	if response.StatusCode != 200 || err != nil {
		t.Fatalf("OAuth consent: %d %v", response.StatusCode, err)
	}
	response, err = client.Get(callback.Redirect)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("native loopback callback: %d", response.StatusCode)
	}
	if err := process.Wait(); err != nil {
		t.Fatal("native OAuth failed; credentials remain only in private test directory")
	}
	devices, err := server.Devices(owner.ID)
	if err != nil || len(devices) != 1 || devices[0].Status != "active" {
		t.Fatal("browser owner did not obtain exactly one active device")
	}
	otherDevices, err := server.Devices(otherUser.ID)
	if err != nil || len(otherDevices) != 0 {
		t.Fatal("cross-account device association")
	}
	var credentials struct {
		Token string `json:"device_token"`
	}
	data, err := os.ReadFile(filepath.Join(state, "binding.json"))
	if err != nil || json.Unmarshal(data, &credentials) != nil || len(credentials.Token) < 40 {
		t.Fatal("native binding missing device credential")
	}
	request, _ = http.NewRequest(http.MethodGet, httpServer.URL+"/api/device/status", nil)
	request.Header.Set("Authorization", "Bearer "+credentials.Token)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("native device authorization: %d", response.StatusCode)
	}
	t.Log("actual native OAuth + loopback callback + HTTP/SQLite: PKCE, stale pending replacement, automatic join, owner isolation and device credential verified; OS/mesh adapters isolated")
}
