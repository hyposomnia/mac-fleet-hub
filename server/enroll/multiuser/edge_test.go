package multiuser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/pquerna/otp"
)

type readinessNetwork struct {
	testNetwork
	err   error
	users []User
}

type failingRevokeNetwork struct{ testNetwork }

func (network *failingRevokeNetwork) Revoke(context.Context, string) error {
	return errors.New("expire failed")
}

type snapshotNetwork struct {
	failingRevokeNetwork
	nodes  []Node
	cancel context.CancelFunc
}

func (network *snapshotNetwork) Nodes(context.Context) ([]Node, error) { return network.nodes, nil }
func (network *snapshotNetwork) Reconcile(ctx context.Context, devices []Device, users []User) error {
	network.testNetwork.Reconcile(ctx, devices, users)
	if network.cancel != nil {
		network.cancel()
	}
	return nil
}

func TestMissingNodeCannotProxyReassignedIP(t *testing.T) {
	server, now := newTestServer(t)
	_, user, _, _ := registerBrowser(t, server, *now, "missing-node@example.com")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	network := &snapshotNetwork{nodes: []Node{{ID: "2", IP: "100.64.0.10"}}, cancel: cancel}
	server.options.Network = network
	if _, err := server.db.Exec("INSERT INTO devices(user_id,node_id,ip,name,status,created_at) VALUES(?,'1','100.64.0.10','Mac','active',?)", user.ID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	server.Run(ctx)
	if _, err := server.AuthorizedDevice(user.ID, "m1"); err == nil {
		t.Fatal("missing node retains authorization to recycled IP")
	}
	if len(network.policy) != 1 || network.policy[0].Status != "revoked" {
		t.Fatalf("missing node retained policy %+v", network.policy)
	}
}

func TestRevokeWithdrawsPolicyEvenWhenNodeExpiryFails(t *testing.T) {
	server, now := newTestServer(t)
	browser, user, _, _ := registerBrowser(t, server, *now, "expire-failure@example.com")
	network := &failingRevokeNetwork{}
	server.options.Network = network
	if _, err := server.db.Exec("INSERT INTO devices(user_id,node_id,ip,name,status,created_at) VALUES(?,'1','100.64.0.10','Mac','active',?)", user.ID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	response := browser.request("DELETE", "/api/devices/m1", nil)
	if response.Code != 503 || len(network.policy) != 1 || network.policy[0].Status != "revoked" {
		t.Fatalf("failed expiry must not skip policy withdrawal: status=%d policy=%+v", response.Code, network.policy)
	}
}

func (network *readinessNetwork) Reconcile(_ context.Context, _ []Device, users []User) error {
	network.users = users
	return network.err
}

func TestRevokeInstallingDeviceDoesNotExpireEmptyNode(t *testing.T) {
	server, now := newTestServer(t)
	browser, user, _, _ := registerBrowser(t, server, *now, "installing@example.com")
	network := &testNetwork{}
	server.options.Network = network
	if _, err := server.db.Exec("INSERT INTO devices(user_id,name,status,created_at) VALUES(?,'Mac','installing',?)", user.ID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	response := browser.request("DELETE", "/api/devices/m1", nil)
	if response.Code != 200 || len(network.revoked) != 0 {
		t.Fatalf("installing revoke status=%d network expirations=%v", response.Code, network.revoked)
	}
}

func TestReadinessRequiresRecentSuccessfulPolicySync(t *testing.T) {
	server, now := newTestServer(t)
	browser := &testBrowser{server: server}
	network := &readinessNetwork{}
	server.options.Network = network
	if response := browser.request("GET", "/readyz", nil); response.Code != 503 {
		t.Fatalf("unsynchronized readiness %d", response.Code)
	}
	if err := server.syncNetwork(context.Background()); err != nil {
		t.Fatal(err)
	}
	if response := browser.request("GET", "/readyz", nil); response.Code != 200 {
		t.Fatalf("synchronized readiness %d", response.Code)
	}
	network.err = errors.New("Headscale unavailable")
	server.syncNetwork(context.Background())
	if response := browser.request("GET", "/readyz", nil); response.Code != 503 {
		t.Fatalf("failed readiness %d", response.Code)
	}
	network.err = nil
	server.syncNetwork(context.Background())
	*now = now.Add(time.Minute)
	if response := browser.request("GET", "/readyz", nil); response.Code != 503 {
		t.Fatalf("stale readiness %d", response.Code)
	}
}

func TestSetupURIPreservesEmailAndSecret(t *testing.T) {
	server, _ := newTestServer(t)
	browser := &testBrowser{server: server}
	email := "person?&tag@example.com"
	response := browser.request("POST", "/api/auth/register", map[string]string{"email": email, "password": "correct horse battery", "confirm_password": "correct horse battery"})
	var setup struct {
		TOTP struct {
			URI    string `json:"uri"`
			Secret string `json:"secret"`
		} `json:"totp"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &setup) != nil {
		t.Fatalf("setup response %d", response.Code)
	}
	parsed, err := url.Parse(setup.TOTP.URI)
	key, keyErr := otp.NewKeyFromURL(setup.TOTP.URI)
	if err != nil || keyErr != nil || key.AccountName() != email || parsed.Query().Get("secret") != setup.TOTP.Secret {
		t.Fatal("otpauth URI corrupted account identity or secret")
	}
	if response = browser.request("GET", "/api/auth/qr", nil); response.Code != 200 {
		t.Fatalf("QR failed %d", response.Code)
	}
}

func TestRecoveryImmediatelyReconcilesSetupUser(t *testing.T) {
	server, now := newTestServer(t)
	browser, user, _, codes := registerBrowser(t, server, *now, "recovery-policy@example.com")
	network := &readinessNetwork{}
	server.options.Network = network
	response := browser.request("POST", "/api/auth/recover", map[string]string{"email": user.Email, "password": "correct horse battery", "recovery_code": codes[0]})
	if response.Code != 200 || len(network.users) != 1 || network.users[0].Status != "setup" {
		t.Fatalf("recovery must withdraw network ACL immediately: status=%d users=%+v", response.Code, network.users)
	}
}

func TestSuccessfulCredentialsDoNotExhaustFailureBudget(t *testing.T) {
	server, now := newTestServer(t)
	_, user, _, _ := registerBrowser(t, server, *now, "successful@example.com")
	for attempt := 0; attempt < 10; attempt++ {
		browser := &testBrowser{server: server}
		response := browser.request("POST", "/api/auth/login", map[string]string{"email": user.Email, "password": "correct horse battery"})
		if response.Code != 200 {
			t.Fatalf("successful credentials exhausted failure budget at %d: %d", attempt, response.Code)
		}
	}
}

func TestSecurityOperationsRespectPasswordWorkLimit(t *testing.T) {
	server, now := newTestServer(t)
	browser, _, _, _ := registerBrowser(t, server, *now, "work-limit@example.com")
	for capacity := 0; capacity < cap(server.authSlots); capacity++ {
		server.authSlots <- struct{}{}
	}
	defer func() {
		for len(server.authSlots) > 0 {
			<-server.authSlots
		}
	}()
	response := browser.request("POST", "/api/auth/totp/start", map[string]string{"password": "wrong", "code": "000000"})
	if response.Code != 429 {
		t.Fatalf("unbounded security password work: %d", response.Code)
	}
}

func TestAuthenticationLimiterHasBoundedIdentityStorage(t *testing.T) {
	server, _ := newTestServer(t)
	request := httptest.NewRequest("POST", "/api/auth/login", nil)
	for attempt := 0; attempt < 5000; attempt++ {
		server.allowedAuth(request, intString(int64(attempt)))
	}
	if len(server.failures) > 4096 {
		t.Fatalf("unbounded auth identities: %d", len(server.failures))
	}
}
