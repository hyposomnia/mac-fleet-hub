package multiuser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type testNetwork struct {
	issues  int
	revoked []string
	policy  []Device
}

func (network *testNetwork) Issue(context.Context, int64, int) (Grant, error) {
	network.issues++
	return Grant{Key: "hskey-test", KeyID: "key-1"}, nil
}
func (network *testNetwork) Discover(context.Context, Grant) (Node, error) {
	return Node{ID: "1", IP: "100.64.0.10", Name: "mac1", Online: true}, nil
}
func (network *testNetwork) Reconcile(_ context.Context, devices []Device, _ []User) error {
	network.policy = devices
	return nil
}
func (network *testNetwork) Revoke(_ context.Context, id string) error {
	network.revoked = append(network.revoked, id)
	return nil
}

func TestEnrollmentOwnerAndSingleGrant(t *testing.T) {
	server, now := newTestServer(t)
	network := &testNetwork{}
	server.options.Network = network
	owner, user, _, _ := registerBrowser(t, server, *now, "owner@example.com")
	other, _, _, _ := registerBrowser(t, server, *now, "other@example.com")
	installer := &testBrowser{server: server}
	response := installer.request("POST", "/api/enrollment/start", map[string]string{"name": "My Mac"})
	if response.Code != 200 {
		t.Fatalf("start %d %s", response.Code, response.Body.String())
	}
	var start map[string]any
	json.Unmarshal(response.Body.Bytes(), &start)
	response = owner.request("POST", "/api/enrollment/confirm", map[string]any{"code": start["code"]})
	if response.Code != 200 {
		t.Fatalf("confirm %d %s", response.Code, response.Body.String())
	}
	if response = other.request("POST", "/api/enrollment/confirm", map[string]any{"code": start["code"]}); response.Code != 409 {
		t.Fatalf("second owner %d", response.Code)
	}
	claim := map[string]any{"request_id": start["request_id"], "claim_token": start["claim_token"]}
	response = installer.request("POST", "/api/enrollment/claim", claim)
	if response.Code != 200 {
		t.Fatalf("claim %d %s", response.Code, response.Body.String())
	}
	if response = installer.request("POST", "/api/enrollment/claim", claim); response.Code != 200 || network.issues != 1 {
		t.Fatalf("grant not idempotent %d issues=%d", response.Code, network.issues)
	}
	if response = installer.request("POST", "/api/enrollment/complete", claim); response.Code != 200 {
		t.Fatalf("complete %d %s", response.Code, response.Body.String())
	}
	devices, err := server.Devices(user.ID)
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices %v %v", devices, err)
	}
	if response = other.request("GET", "/m1/api/info", nil); response.Code != 404 {
		t.Fatalf("cross-owner proxy %d", response.Code)
	}
	if response = other.request("DELETE", "/api/devices/m1", nil); response.Code != 404 {
		t.Fatalf("cross-owner revoke %d", response.Code)
	}
	if response = other.request("POST", "/api/names", map[string]string{"id": "m1", "name": "stolen"}); response.Code != 404 {
		t.Fatalf("cross-owner name %d", response.Code)
	}
}

func TestAdminMetadataDoesNotGrantDeviceAccess(t *testing.T) {
	server, now := newTestServer(t)
	owner, user, _, _ := registerBrowser(t, server, *now, "member@example.com")
	admin, adminUser, _, _ := registerBrowser(t, server, *now, "admin@example.com")
	if response := owner.request("GET", "/api/admin/users", nil); response.Code != 403 {
		t.Fatalf("ordinary admin access %d", response.Code)
	}
	if err := server.SetAdmin(adminUser.Email); err != nil {
		t.Fatal(err)
	}
	server.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(1,?, '42','100.64.0.42','Private Mac','active',?)", user.ID, now.Unix())
	if response := admin.request("GET", "/api/admin/users", nil); response.Code != 200 {
		t.Fatalf("admin listing %d %s", response.Code, response.Body.String())
	}
	if response := admin.request("GET", "/m1/api/info", nil); response.Code != 404 {
		t.Fatalf("admin accessed private device %d", response.Code)
	}
	response := admin.request("PATCH", "/api/admin/users/"+intString(user.ID), map[string]string{"status": "disabled"})
	if response.Code != 200 {
		t.Fatalf("disable %d %s", response.Code, response.Body.String())
	}
	if response = owner.request("GET", "/api/auth/me", nil); response.Code != 401 {
		t.Fatalf("disabled session %d", response.Code)
	}
}

func TestProxyStripsCredentialsAndUsesOwner(t *testing.T) {
	server, now := newTestServer(t)
	browser, user, _, _ := registerBrowser(t, server, *now, "proxy@example.com")
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Cookie") != "" || request.Header.Get("Authorization") != "" || request.Header.Get("X-Remote-User") != "" {
			t.Error("forwarded browser credentials")
		}
		if request.URL.Path != "/api/info" {
			t.Errorf("path %s", request.URL.Path)
		}
		writer.Write([]byte("private response"))
	}))
	defer upstream.Close()
	server.options.ProxyURL = func(Device, string) string { return upstream.URL }
	server.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(1,?, '42','100.64.0.42','Mac','active',?)", user.ID, now.Unix())
	if _, _, err := server.issueDeviceCredentials(1); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/m1/api/info", nil)
	request.AddCookie(browser.cookie)
	request.Header.Set("Authorization", "Bearer browser-secret")
	request.Header.Set("X-Remote-User", "spoofed")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 200 || response.Body.String() != "private response" {
		t.Fatalf("proxy %d %s", response.Code, response.Body.String())
	}
}
