package multiuser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func networkServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer fixture-api-key" {
			t.Errorf("missing authorization")
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler(writer, request)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestHeadscaleIssue(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			var methods []string
			started := time.Now()
			server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) {
				methods = append(methods, request.Method+" "+request.URL.Path)
				switch request.Method + " " + request.URL.Path {
				case "GET /api/v1/user":
					if request.URL.Query().Get("name") != "fleet-u7" {
						t.Errorf("wrong user filter: %s", request.URL.RawQuery)
					}
					if existing {
						fmt.Fprint(writer, `{"users":[{"id":"41","name":"fleet-u7"}]}`)
					} else {
						fmt.Fprint(writer, `{"users":[]}`)
					}
				case "POST /api/v1/user":
					var body struct{ Name string }
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Name != "fleet-u7" {
						t.Errorf("wrong user body: %+v, %v", body, err)
					}
					fmt.Fprint(writer, `{"user":{"id":"41","name":"fleet-u7"}}`)
				case "POST /api/v1/preauthkey":
					var body map[string]any
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body["user"] != "41" || body["reusable"] != false || body["ephemeral"] != false {
						t.Errorf("unsafe key settings: %v", body)
					}
					if _, present := body["aclTags"]; present || body["name"] != nil {
						t.Errorf("tags/hostname must not authorize key: %v", body)
					}
					expires, err := time.Parse(time.RFC3339Nano, fmt.Sprint(body["expiration"]))
					if err != nil || expires.Before(started.Add(10*time.Minute)) || expires.After(time.Now().Add(10*time.Minute)) {
						t.Errorf("wrong expiration: %v, %v", expires, err)
					}
					fmt.Fprint(writer, `{"preAuthKey":{"id":"73","key":"fixture-secret","reusable":false,"user":{"id":"41"}}}`)
				default:
					t.Errorf("unexpected API request: %s", request.URL)
					writer.WriteHeader(404)
				}
			})
			grant, err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1").Issue(context.Background(), 7, 9)
			if err != nil || grant.Key != "fixture-secret" || grant.KeyID != "73" {
				t.Fatalf("grant=%+v err=%v", grant, err)
			}
			want := []string{"GET /api/v1/user", "POST /api/v1/preauthkey"}
			if !existing {
				want = []string{"GET /api/v1/user", "POST /api/v1/user", "POST /api/v1/preauthkey"}
			}
			if !reflect.DeepEqual(methods, want) {
				t.Fatalf("requests=%v want=%v", methods, want)
			}
		})
	}
}

func TestHeadscaleDiscoverUsesKeyID(t *testing.T) {
	server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "GET" || request.URL.Path != "/api/v1/node" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL)
		}
		fmt.Fprint(writer, `{"nodes":[{"id":"1","name":"mac9","ipAddresses":["100.64.0.2"],"preAuthKey":{"id":"72"}},{"id":"2","name":"renamed-host","givenName":"custom-name","ipAddresses":["fd7a:115c:a1e0::2","100.64.0.3"],"preAuthKey":{"id":"73"},"online":true,"lastSeen":"2026-09-30T01:02:03Z"}]}`)
	})
	network := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1")
	node, err := network.Discover(context.Background(), Grant{KeyID: "73", Key: "unused-secret"})
	if err != nil || node.ID != "2" || node.IP != "100.64.0.3" || node.Name != "custom-name" || !node.Online || node.LastSeen != 1790730123 {
		t.Fatalf("node=%+v err=%v", node, err)
	}
	snapshot, ok := network.(interface {
		Nodes(context.Context) ([]Node, error)
	})
	if !ok {
		t.Fatal("missing optional Nodes API")
	}
	nodes, err := snapshot.Nodes(context.Background())
	if err != nil || len(nodes) != 2 || nodes[1].KeyID != "73" {
		t.Fatalf("nodes=%+v err=%v", nodes, err)
	}
}

func TestHeadscaleDiscoverRejectsUnattributedNodes(t *testing.T) {
	for name, response := range map[string]string{
		"not enrolled": `{"nodes":[{"id":"1","name":"mac9","ipAddresses":["100.64.0.2"]}]}`,
		"wrong key":    `{"nodes":[{"id":"1","name":"mac9","ipAddresses":["100.64.0.2"],"preAuthKey":{"id":"72"}}]}`,
		"ambiguous":    `{"nodes":[{"id":"1","ipAddresses":["100.64.0.2"],"preAuthKey":{"id":"73"}},{"id":"2","ipAddresses":["100.64.0.3"],"preAuthKey":{"id":"73"}}]}`,
		"public IP":    `{"nodes":[{"id":"1","ipAddresses":["8.8.8.8"],"preAuthKey":{"id":"73"}}]}`,
		"malformed":    `{"nodes":[{"id":"../2","ipAddresses":["100.64.0.2"],"preAuthKey":{"id":"73"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) { fmt.Fprint(writer, response) })
			if node, err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1").Discover(context.Background(), Grant{KeyID: "73"}); err == nil {
				t.Fatalf("accepted node=%+v", node)
			}
		})
	}
}

type networkPolicy struct {
	ACLs []struct {
		Action string   `json:"action"`
		Src    []string `json:"src"`
		Dst    []string `json:"dst"`
	} `json:"acls"`
}

func (policy networkPolicy) allows(source, destination, port string) bool {
	for _, rule := range policy.ACLs {
		for _, allowedSource := range rule.Src {
			for _, allowedDestination := range rule.Dst {
				if rule.Action == "accept" && allowedSource == source && (allowedDestination == destination+":*" || allowedDestination == destination+":"+port) {
					return true
				}
			}
		}
	}
	return false
}

func TestHeadscaleReconcileIsolation(t *testing.T) {
	var captured networkPolicy
	server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "PUT" || request.URL.Path != "/api/v1/policy" {
			t.Errorf("unexpected policy API: %s %s", request.Method, request.URL)
		}
		var body struct{ Policy string }
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(body.Policy), &captured); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body.Policy, "fleet-mac") || strings.Contains(body.Policy, "autogroup") {
			t.Errorf("global group leaked into policy: %s", body.Policy)
		}
		fmt.Fprint(writer, `{}`)
	})
	users := []User{{ID: 1, Status: "active"}, {ID: 2, Status: "active"}, {ID: 3, Status: "disabled"}, {ID: 4, Role: "admin", Status: "active"}}
	devices := []Device{
		{ID: "a", UserID: 1, NodeID: "1", IP: "100.64.0.2", Status: "active"},
		{ID: "b", UserID: 1, NodeID: "2", IP: "100.64.0.3", Status: "active"},
		{ID: "c", UserID: 2, NodeID: "3", IP: "100.64.0.4", Status: "active"},
		{ID: "d", UserID: 3, NodeID: "4", IP: "100.64.0.5", Status: "active"},
		{ID: "e", UserID: 1, NodeID: "5", IP: "100.64.0.6", Status: "revoked"},
		{ID: "f", UserID: 1, IP: "100.64.0.7", Status: "active"},
		{ID: "g", UserID: 99, NodeID: "6", IP: "100.64.0.8", Status: "active"},
		{ID: "h", UserID: 4, NodeID: "7", IP: "100.64.0.9", Status: "active"},
	}
	if err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1").Reconcile(context.Background(), devices, users); err != nil {
		t.Fatal(err)
	}
	addresses := []string{"100.64.0.1", "100.64.0.2", "100.64.0.3", "100.64.0.4", "100.64.0.5", "100.64.0.6", "100.64.0.7", "100.64.0.8", "100.64.0.9", "100.64.0.10"}
	for _, source := range addresses {
		for _, destination := range addresses {
			for _, port := range []string{"7681", "8080", "7682", "22", "5900", "443"} {
				active := destination == "100.64.0.2" || destination == "100.64.0.3" || destination == "100.64.0.4" || destination == "100.64.0.9"
				gateway := source == "100.64.0.1" && active && (port == "7681" || port == "8080" || port == "7682")
				sameUser := (source == "100.64.0.2" || source == "100.64.0.3") && (destination == "100.64.0.2" || destination == "100.64.0.3")
				sameUser = sameUser || source == destination && (source == "100.64.0.4" || source == "100.64.0.9")
				if got := captured.allows(source, destination, port); got != (gateway || sameUser) {
					t.Errorf("allow %s -> %s:%s = %t", source, destination, port, got)
				}
			}
		}
	}
	if err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1").Reconcile(context.Background(), nil, nil); err != nil || captured.ACLs == nil || len(captured.ACLs) != 0 {
		t.Fatalf("empty inventory must install explicit deny-all: %+v %v", captured, err)
	}
}

func TestHeadscaleReconcileRejectsUnsafeInventory(t *testing.T) {
	for name, devices := range map[string][]Device{
		"nonmesh":        {{UserID: 1, NodeID: "1", IP: "192.168.1.1", Status: "active"}},
		"injection":      {{UserID: 1, NodeID: "1", IP: "100.64.0.2:*", Status: "active"}},
		"gateway":        {{UserID: 1, NodeID: "1", IP: "100.64.0.1", Status: "active"}},
		"duplicate IP":   {{UserID: 1, NodeID: "1", IP: "100.64.0.2", Status: "active"}, {UserID: 2, NodeID: "2", IP: "100.64.0.2", Status: "active"}},
		"duplicate node": {{UserID: 1, NodeID: "1", IP: "100.64.0.2", Status: "active"}, {UserID: 2, NodeID: "1", IP: "100.64.0.3", Status: "active"}},
	} {
		t.Run(name, func(t *testing.T) {
			server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) { t.Error("unsafe inventory reached API") })
			if err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1").Reconcile(context.Background(), devices, []User{{ID: 1, Status: "active"}, {ID: 2, Status: "active"}}); err == nil {
				t.Fatal("unsafe inventory accepted")
			}
		})
	}
}

func TestHeadscaleReconcileConfiguredServicePorts(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		ports   []int
		allowed []int
	}{
		{name: "defaults", allowed: []int{7681, 8080, 7682}},
		{name: "custom", ports: []int{17682, 17681, 18080}, allowed: []int{17682, 17681, 18080}},
		{name: "boundaries", ports: []int{1, 65535}, allowed: []int{1, 65535}},
		{name: "single", ports: []int{443}, allowed: []int{443}},
		{name: "duplicate", ports: []int{443, 443}, allowed: []int{443}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var policy networkPolicy
			server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != "PUT" || request.URL.Path != "/api/v1/policy" {
					t.Errorf("unexpected API: %s %s", request.Method, request.URL)
				}
				var body struct{ Policy string }
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(body.Policy), &policy); err != nil {
					t.Fatal(err)
				}
				fmt.Fprint(writer, `{}`)
			})
			ports := append([]int(nil), testCase.ports...)
			network := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1", ports...)
			if len(ports) > 0 {
				ports[0] = 22
			}
			devices := []Device{{UserID: 1, NodeID: "1", IP: "100.64.0.2", Status: "active"}, {UserID: 2, NodeID: "2", IP: "100.64.0.3", Status: "active"}}
			users := []User{{ID: 1, Status: "active"}, {ID: 2, Status: "active"}}
			if err := network.Reconcile(context.Background(), devices, users); err != nil {
				t.Fatal(err)
			}
			if len(policy.ACLs) == 0 || len(policy.ACLs[0].Dst) != 2*len(testCase.allowed) {
				t.Fatalf("wrong gateway destinations: %+v", policy)
			}
			for _, port := range []int{1, 22, 443, 7681, 7682, 8080, 17681, 17682, 18080, 65535} {
				want := false
				for _, allowed := range testCase.allowed {
					want = want || port == allowed
				}
				for _, destination := range []string{"100.64.0.2", "100.64.0.3"} {
					if got := policy.allows("100.64.0.1", destination, fmt.Sprint(port)); got != want {
						t.Errorf("gateway -> %s:%d = %t want %t", destination, port, got, want)
					}
				}
				if policy.allows("100.64.0.2", "100.64.0.3", fmt.Sprint(port)) || !policy.allows("100.64.0.2", "100.64.0.2", fmt.Sprint(port)) {
					t.Errorf("custom ports changed user isolation at port %d", port)
				}
			}
		})
	}
}

func TestHeadscaleRejectsInvalidServicePorts(t *testing.T) {
	server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) { t.Error("invalid service ports reached API") })
	for _, ports := range [][]int{{0}, {-1}, {65536}, {17682, 0, 18080}, {7681, 8080, -1}} {
		if err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1", ports...).Reconcile(context.Background(), nil, nil); err == nil {
			t.Errorf("invalid ports accepted: %v", ports)
		}
	}
}

func TestHeadscaleRevokeExpiresNodeIdempotently(t *testing.T) {
	var requests []string
	server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if len(requests) == 1 {
			fmt.Fprint(writer, `{"node":{"id":"42"}}`)
		} else {
			writer.WriteHeader(404)
		}
	})
	network := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1")
	for attempt := 0; attempt < 2; attempt++ {
		if err := network.Revoke(context.Background(), "42"); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(requests, []string{"POST /api/v1/node/42/expire", "POST /api/v1/node/42/expire"}) {
		t.Fatalf("must expire, not delete: %v", requests)
	}
}

func TestHeadscaleRevokeMissingNodeUsesInventory(t *testing.T) {
	server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == "POST" {
			writer.WriteHeader(500)
			fmt.Fprint(writer, `{"code":2,"message":"record not found"}`)
			return
		}
		if request.Method != "GET" || request.URL.Path != "/api/v1/node" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL)
		}
		fmt.Fprint(writer, `{"nodes":[]}`)
	})
	if err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1").Revoke(context.Background(), "42"); err != nil {
		t.Fatalf("0.26.1 missing node must be idempotent: %v", err)
	}
}

func TestHeadscaleLocalIntegration(t *testing.T) {
	endpoint := os.Getenv("HEADSCALE_UAT_URL")
	if endpoint == "" {
		t.Skip("set HEADSCALE_UAT_URL and HEADSCALE_UAT_KEY_FILE for isolated fixture")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() != "127.0.0.1" || parsed.Scheme != "http" {
		t.Fatal("integration test only accepts the isolated loopback fixture")
	}
	key, err := os.ReadFile(os.Getenv("HEADSCALE_UAT_KEY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	network := NewHeadscale(endpoint, strings.TrimSpace(string(key)), "100.64.0.1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first, err := network.Issue(ctx, 900001, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := network.Issue(ctx, 900001, 2)
	if err != nil || first.KeyID == second.KeyID || first.Key == second.Key {
		t.Fatalf("issue existing user failed: %v", err)
	}
	if _, err := network.Discover(ctx, first); err == nil {
		t.Fatal("unregistered key discovered a node")
	}
	if err := network.Reconcile(ctx, nil, nil); err != nil {
		t.Fatalf("empty deny-all policy rejected: %v", err)
	}
	devices := []Device{{UserID: 900001, NodeID: "900001", IP: "100.64.0.2", Status: "active"}, {UserID: 900002, NodeID: "900002", IP: "100.64.0.3", Status: "active"}}
	if err := network.Reconcile(ctx, devices, []User{{ID: 900001, Status: "active"}, {ID: 900002, Status: "active"}}); err != nil {
		t.Fatalf("isolation policy rejected: %v", err)
	}
	customNetwork := NewHeadscale(endpoint, strings.TrimSpace(string(key)), "100.64.0.1", 17682, 17681, 18080).(*headscaleNetwork)
	if err := customNetwork.Reconcile(ctx, devices, []User{{ID: 900001, Status: "active"}, {ID: 900002, Status: "active"}}); err != nil {
		t.Fatalf("custom service port policy rejected: %v", err)
	}
	var stored struct{ Policy string }
	if err := customNetwork.request(ctx, http.MethodGet, "/api/v1/policy", nil, &stored); err != nil {
		t.Fatal(err)
	}
	var policy networkPolicy
	if err := json.Unmarshal([]byte(stored.Policy), &policy); err != nil {
		t.Fatal(err)
	}
	for _, port := range []string{"17682", "17681", "18080"} {
		if !policy.allows("100.64.0.1", "100.64.0.2", port) {
			t.Errorf("stored policy omits custom service port %s", port)
		}
	}
	for _, port := range []string{"7681", "8080", "7682"} {
		if policy.allows("100.64.0.1", "100.64.0.2", port) {
			t.Errorf("stored policy leaks default port %s", port)
		}
	}
	if err := network.Revoke(ctx, "900000000"); err != nil {
		t.Fatalf("missing node revoke failed: %v", err)
	}
	t.Log("real Headscale 0.26.1: user reuse, distinct keys, no hostname attribution, deny-all/isolation/custom-port policy PUT/readback, missing-node revoke passed")
}

func TestHeadscaleLiveKeyAttribution(t *testing.T) {
	endpoint := os.Getenv("HEADSCALE_UAT_URL")
	clientBinary := os.Getenv("HEADSCALE_UAT_TAILSCALE")
	daemonBinary := os.Getenv("HEADSCALE_UAT_TAILSCALED")
	if endpoint == "" || clientBinary == "" || daemonBinary == "" {
		t.Skip("set isolated fixture and explicit Tailscale binaries for live enrollment")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() != "127.0.0.1" || parsed.Scheme != "http" {
		t.Fatal("live test only accepts the isolated loopback fixture")
	}
	key, err := os.ReadFile(os.Getenv("HEADSCALE_UAT_KEY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	stateDir, err := os.MkdirTemp("/private/tmp", "macfleet-tailscale-uat.")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateDir)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	socket := filepath.Join(stateDir, "tailscaled.sock")
	daemon := exec.CommandContext(ctx, daemonBinary, "--tun=userspace-networking", "--state="+filepath.Join(stateDir, "state"), "--socket="+socket, "--port=0")
	daemon.Env = append(os.Environ(), "TS_NO_LOGS_NO_SUPPORT=true", "TS_LOGS_DIR="+stateDir)
	if err := daemon.Start(); err != nil {
		t.Fatalf("start isolated Tailscale: %v", err)
	}
	defer func() { daemon.Process.Kill(); daemon.Wait() }()
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("isolated Tailscale socket did not start")
		case <-time.After(20 * time.Millisecond):
		}
	}
	network := NewHeadscale(endpoint, strings.TrimSpace(string(key)), "100.64.0.254").(*headscaleNetwork)
	grant, err := network.Issue(ctx, 900003, 101)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(stateDir, "preauth.key")
	if err := os.WriteFile(keyPath, []byte(grant.Key), 0600); err != nil {
		t.Fatal(err)
	}
	join := exec.CommandContext(ctx, clientBinary, "--socket="+socket, "up", "--force-reauth", "--login-server="+endpoint, "--auth-key=file:"+keyPath, "--hostname=mac101", "--accept-dns=false", "--accept-routes=false")
	join.Env = daemon.Env
	if err := join.Run(); err != nil {
		t.Fatalf("isolated Tailscale enrollment failed: %v", err)
	}
	node, err := network.Discover(ctx, grant)
	if err != nil || node.KeyID != grant.KeyID || node.ID == "" || node.IP == "" {
		t.Fatalf("live key attribution failed: node=%+v err=%v", node, err)
	}
	renamedName := "renamed-uat-" + node.ID
	if err := network.request(ctx, http.MethodPost, "/api/v1/node/"+node.ID+"/rename/"+renamedName, nil, nil); err != nil {
		t.Fatal(err)
	}
	renamed, err := network.Discover(ctx, Grant{KeyID: grant.KeyID})
	if err != nil || renamed.ID != node.ID || renamed.Name != renamedName {
		t.Fatalf("renamed live node lost attribution: %+v %v", renamed, err)
	}
	if err := network.Reconcile(ctx, []Device{{UserID: 900003, NodeID: node.ID, IP: node.IP, Status: "active"}}, []User{{ID: 900003, Status: "active"}}); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := network.Revoke(ctx, node.ID); err != nil {
			t.Fatalf("live node expiry failed: %v", err)
		}
	}
	if err := network.Reconcile(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	t.Logf("live isolated userspace node %s: preAuthKey.id attribution survives hostname rename; policy accepted and repeated expiry passed; private socket %s", node.ID, socket)
}

func TestHeadscaleBackendFailuresVisible(t *testing.T) {
	for _, status := range []int{401, 403, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(status)
				fmt.Fprint(writer, `fixture-secret fixture-api-key`)
			})
			network := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1")
			_, issueErr := network.Issue(context.Background(), 1, 1)
			_, discoverErr := network.Discover(context.Background(), Grant{KeyID: "1"})
			policyErr := network.Reconcile(context.Background(), nil, nil)
			revokeErr := network.Revoke(context.Background(), "1")
			for _, err := range []error{issueErr, discoverErr, policyErr, revokeErr} {
				if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "fixture-api-key") {
					t.Errorf("failure hidden or secret exposed: %v", err)
				}
			}
		})
	}
}

func TestHeadscaleRequestsBoundedAndNoRedirect(t *testing.T) {
	t.Run("context cancellation", func(t *testing.T) {
		server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) { <-request.Context().Done() })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		if _, err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1").Discover(ctx, Grant{KeyID: "1"}); err == nil {
			t.Fatal("timeout hidden")
		}
	})
	t.Run("redirect", func(t *testing.T) {
		target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { t.Error("redirect followed") }))
		defer target.Close()
		server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, target.URL, 302)
		})
		if _, err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1").Discover(context.Background(), Grant{KeyID: "1"}); err == nil {
			t.Fatal("redirect accepted")
		}
	})
	t.Run("response bound", func(t *testing.T) {
		server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) {
			fmt.Fprint(writer, strings.Repeat(" ", 5<<20))
		})
		if _, err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1").Discover(context.Background(), Grant{KeyID: "1"}); err == nil {
			t.Fatal("oversized response accepted")
		}
	})
}

func TestHeadscaleConfigurationAndIDsValidated(t *testing.T) {
	for _, endpoint := range []string{"", "file:///tmp/hs", "http://example.com", "https://user:pass@example.com", "https://example.com/?target=x", "https://example.com/#fragment"} {
		if _, err := NewHeadscale(endpoint, "fixture-api-key", "100.64.0.1").Issue(context.Background(), 1, 1); err == nil {
			t.Errorf("unsafe endpoint accepted: %q", endpoint)
		}
	}
	server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) { t.Error("invalid input reached API") })
	for _, config := range [][2]string{{"", "100.64.0.1"}, {"key\n", "100.64.0.1"}, {"fixture-api-key", "8.8.8.8"}} {
		if err := NewHeadscale(server.URL, config[0], config[1]).Reconcile(context.Background(), nil, nil); err == nil {
			t.Errorf("unsafe config accepted: %v", config)
		}
	}
	network := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1")
	for _, id := range []string{"", "0", "../1", "1?x=y", "-1"} {
		if err := network.Revoke(context.Background(), id); err == nil {
			t.Errorf("unsafe node ID accepted: %q", id)
		}
		if _, err := network.Discover(context.Background(), Grant{KeyID: id}); err == nil {
			t.Errorf("unsafe key ID accepted: %q", id)
		}
	}
	for _, input := range [][2]int{{0, 1}, {1, 0}, {-1, 1}} {
		if _, err := network.Issue(context.Background(), int64(input[0]), input[1]); err == nil {
			t.Errorf("invalid issue input accepted: %v", input)
		}
	}
}

func TestHeadscaleConcurrentIssueCreatesOneUser(t *testing.T) {
	var lock sync.Mutex
	created := false
	server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) {
		lock.Lock()
		defer lock.Unlock()
		switch request.Method + " " + request.URL.Path {
		case "GET /api/v1/user":
			if created {
				fmt.Fprint(writer, `{"users":[{"id":"41","name":"fleet-u7"}]}`)
			} else {
				fmt.Fprint(writer, `{"users":[]}`)
			}
		case "POST /api/v1/user":
			if created {
				t.Error("user created twice")
			}
			created = true
			fmt.Fprint(writer, `{"user":{"id":"41","name":"fleet-u7"}}`)
		case "POST /api/v1/preauthkey":
			fmt.Fprint(writer, `{"preAuthKey":{"id":"73","key":"fixture-secret","user":{"id":"41"}}}`)
		default:
			t.Errorf("unexpected API: %s", request.URL)
		}
	})
	network := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1")
	var workers sync.WaitGroup
	for index := 1; index <= 8; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			if _, err := network.Issue(context.Background(), 7, index); err != nil {
				t.Error(err)
			}
		}(index)
	}
	workers.Wait()
}

func TestHeadscaleIssueRejectsUnsafeKeys(t *testing.T) {
	for name, key := range map[string]string{
		"missing ID":   `{"key":"secret","user":{"id":"41"}}`,
		"missing key":  `{"id":"73","user":{"id":"41"}}`,
		"wrong user":   `{"id":"73","key":"secret","user":{"id":"42"}}`,
		"reusable":     `{"id":"73","key":"secret","user":{"id":"41"},"reusable":true}`,
		"ephemeral":    `{"id":"73","key":"secret","user":{"id":"41"},"ephemeral":true}`,
		"already used": `{"id":"73","key":"secret","user":{"id":"41"},"used":true}`,
		"global tag":   `{"id":"73","key":"secret","user":{"id":"41"},"aclTags":["tag:fleet-mac"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method == "GET" {
					fmt.Fprint(writer, `{"users":[{"id":"41","name":"fleet-u7"}]}`)
				} else {
					fmt.Fprintf(writer, `{"preAuthKey":%s}`, key)
				}
			})
			if _, err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1").Issue(context.Background(), 7, 1); err == nil {
				t.Fatal("unsafe key accepted")
			}
		})
	}
}

func TestHeadscaleCreateUserRaceRecovers(t *testing.T) {
	listed := false
	server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /api/v1/user":
			if listed {
				fmt.Fprint(writer, `{"users":[{"id":"41","name":"fleet-u7"}]}`)
			} else {
				listed = true
				fmt.Fprint(writer, `{"users":[]}`)
			}
		case "POST /api/v1/user":
			writer.WriteHeader(500)
		case "POST /api/v1/preauthkey":
			fmt.Fprint(writer, `{"preAuthKey":{"id":"73","key":"secret","user":{"id":"41"}}}`)
		default:
			t.Errorf("unexpected API: %s", request.URL)
		}
	})
	if _, err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1").Issue(context.Background(), 7, 1); err != nil {
		t.Fatalf("concurrent process created matching user: %v", err)
	}
}

func TestHeadscaleInvalidJSONVisible(t *testing.T) {
	for _, response := range []string{`not-json`, `{"nodes":[]} trailing`, `{"nodes":[{"id":1}]}`, `{"nodes":[{"id":"1","lastSeen":"yesterday"}]}`} {
		server := networkServer(t, func(writer http.ResponseWriter, request *http.Request) { fmt.Fprint(writer, response) })
		if _, err := NewHeadscale(server.URL, "fixture-api-key", "100.64.0.1").Discover(context.Background(), Grant{KeyID: "1"}); err == nil {
			t.Errorf("invalid schema accepted: %q", response)
		}
	}
}
