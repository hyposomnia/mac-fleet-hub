package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fleet-enroll/multiuser"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

type legacyMigrationNetwork struct {
	nodes []multiuser.Node
}

func (network *legacyMigrationNetwork) Nodes(context.Context) ([]multiuser.Node, error) {
	return network.nodes, nil
}

func (network *legacyMigrationNetwork) Issue(context.Context, int64, int) (multiuser.Grant, error) {
	return multiuser.Grant{}, errors.New("unused")
}

func (network *legacyMigrationNetwork) Discover(context.Context, multiuser.Grant) (multiuser.Node, error) {
	return multiuser.Node{}, errors.New("unused")
}

func (network *legacyMigrationNetwork) Reconcile(context.Context, []multiuser.Device, []multiuser.User) error {
	return nil
}

func (network *legacyMigrationNetwork) Revoke(context.Context, string) error { return nil }

func legacyMigrationOwner(t *testing.T, server *multiuser.Server, email string) multiuser.User {
	t.Helper()
	request := func(path string, body interface{}, cookie *http.Cookie) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		request := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		request.Header.Set("Origin", "http://127.0.0.1:7099")
		request.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	response := request("/api/auth/register", map[string]string{"email": email, "password": "legacy migration password", "confirm_password": "legacy migration password"}, nil)
	var setup struct {
		TOTP struct {
			Secret string `json:"secret"`
		} `json:"totp"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &setup) != nil {
		t.Fatalf("register status=%d", response.Code)
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("missing setup cookie")
	}
	code, err := totp.GenerateCode(setup.TOTP.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	response = request("/api/auth/verify", map[string]string{"code": code}, cookies[0])
	if response.Code != 200 {
		t.Fatalf("verify status=%d", response.Code)
	}
	users, err := server.Users()
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.Email == email && user.Status == "active" {
			return user
		}
	}
	t.Fatal("active owner missing")
	return multiuser.User{}
}

func legacyMigrationFixture(t *testing.T) (*multiuser.Server, multiuser.Options, multiuser.User, string) {
	t.Helper()
	directory := t.TempDir()
	network := &legacyMigrationNetwork{nodes: []multiuser.Node{{ID: "node-1", IP: "100.64.0.3"}, {ID: "node-2", IP: "100.64.0.9"}}}
	options := multiuser.Options{StateDir: filepath.Join(directory, "state"), Origin: "http://127.0.0.1:7099", Key: bytes.Repeat([]byte{7}, 32), Network: network}
	server, err := multiuser.New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	owner := legacyMigrationOwner(t, server, "legacy@example.com")
	source := filepath.Join(directory, "legacy-jobs.json")
	t.Setenv("ENROLL_MESSAGE_JOBS_FILE", source)
	t.Setenv("ENROLL_ACCESS_KEY_FILE", filepath.Join(directory, "legacy-keys.json"))
	t.Setenv("ENROLL_MAC_IPS", "100.64.0.3 100.64.0.9")
	return server, options, owner, source
}

func TestLegacyMigrationPinsQueuedAndRunningJobs(t *testing.T) {
	server, options, owner, source := legacyMigrationFixture(t)
	raw := []byte(`{"version":1,"extra":{"keep":true},"jobs":[{"message_id":"queued","status":"queued","device_id":"m1","device_ip":"100.64.0.3","message":"queued text","extra":{"keep":true}},{"message_id":"running","status":"running","device_id":"m2","device_ip":"100.64.0.9","message":"running text"}]}`)
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacy(server, options, owner.Email); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(options.StateDir, "users", strconv.FormatInt(owner.ID, 10), "message-jobs.json")
	result, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var disk messageStoreDisk
	if err := json.Unmarshal(result, &disk); err != nil {
		t.Fatal(err)
	}
	if len(disk.Jobs) != 2 || disk.Jobs[0].DeviceNodeID != "node-1" || disk.Jobs[1].DeviceNodeID != "node-2" {
		t.Fatalf("verified device identities missing: %#v", disk.Jobs)
	}
	var fields struct {
		Extra map[string]bool `json:"extra"`
		Jobs  []struct {
			Extra map[string]bool `json:"extra"`
		} `json:"jobs"`
	}
	if json.Unmarshal(result, &fields) != nil || !fields.Extra["keep"] || !fields.Jobs[0].Extra["keep"] {
		t.Fatal("migration discarded additional fields")
	}
	if err := migrateLegacy(server, options, owner.Email); err != nil {
		t.Fatalf("retry not idempotent: %v", err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("legacy source was overwritten")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("migrated jobs are not private")
	}
}

func TestLegacyMigrationRefusesUnverifiedJobTargets(t *testing.T) {
	for _, target := range []string{
		`"device_id":"m1","device_ip":"100.64.0.9"`,
		`"device_id":"m99","device_ip":"100.64.0.3"`,
		`"device_id":"","device_ip":"100.64.0.3"`,
		`"device_id":"m1","device_ip":"100.64.0.3","device_node_id":"replacement"`,
	} {
		t.Run(target, func(t *testing.T) {
			server, options, owner, source := legacyMigrationFixture(t)
			raw := []byte(`{"version":1,"jobs":[{"message_id":"pending","status":"queued",` + target + `}]}`)
			if err := os.WriteFile(source, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := migrateLegacy(server, options, owner.Email); err == nil {
				t.Fatal("unverified job was migrated")
			}
			path := filepath.Join(options.StateDir, "users", strconv.FormatInt(owner.ID, 10), "message-jobs.json")
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("failed migration published a job store")
			}
		})
	}
}

func TestLegacyMigrationUpgradesUnmodifiedOldCopy(t *testing.T) {
	server, options, owner, source := legacyMigrationFixture(t)
	raw := []byte(`{"version":1,"jobs":[{"message_id":"pending","status":"queued","device_id":"m1","device_ip":"100.64.0.3"}]}`)
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(options.StateDir, "users", strconv.FormatInt(owner.ID, 10))
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "message-jobs.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacy(server, options, owner.Email); err != nil {
		t.Fatal(err)
	}
	api, err := newMessageAPIAt(directory)
	if err != nil {
		t.Fatal(err)
	}
	if api.jobs["pending"].DeviceNodeID != "node-1" {
		t.Fatal("existing legacy copy was not upgraded")
	}
}

func TestLegacyMigrationRefusesDifferentOwnerRegistry(t *testing.T) {
	server, options, owner, source := legacyMigrationFixture(t)
	other := legacyMigrationOwner(t, server, "other@example.com")
	if err := server.ImportLegacy(other.Email, []multiuser.Device{{Index: 1, NodeID: "node-1", IP: "100.64.0.3"}, {Index: 2, NodeID: "node-2", IP: "100.64.0.9"}}, nil); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"version":1,"jobs":[{"message_id":"pending","status":"queued","device_id":"m1","device_ip":"100.64.0.3"}]}`)
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacy(server, options, owner.Email); err == nil {
		t.Fatal("migration accepted another owner's registered devices")
	}
	path := filepath.Join(options.StateDir, "users", strconv.FormatInt(owner.ID, 10), "message-jobs.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cross-owner migration published jobs")
	}
}

func TestLegacyMigrationDoesNotOverwriteChangedStore(t *testing.T) {
	server, options, owner, source := legacyMigrationFixture(t)
	raw := []byte(`{"version":1,"jobs":[{"message_id":"pending","status":"queued","device_id":"m1","device_ip":"100.64.0.3","message":"original"}]}`)
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(options.StateDir, "users", strconv.FormatInt(owner.ID, 10))
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "message-jobs.json")
	existing := bytes.Replace(raw, []byte("original"), []byte("changed"), 1)
	if err := os.WriteFile(path, existing, 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacy(server, options, owner.Email); err == nil {
		t.Fatal("changed target was overwritten")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, existing) {
		t.Fatal("failed migration changed the target")
	}
}

func TestLegacyMigrationRefusesAmbiguousHeadscaleSnapshot(t *testing.T) {
	for _, change := range []string{"duplicate-ip", "duplicate-node", "missing-node"} {
		t.Run(change, func(t *testing.T) {
			server, options, owner, source := legacyMigrationFixture(t)
			network := options.Network.(*legacyMigrationNetwork)
			switch change {
			case "duplicate-ip":
				network.nodes = append(network.nodes, multiuser.Node{ID: "other-node", IP: "100.64.0.3"})
			case "duplicate-node":
				network.nodes[1].ID = network.nodes[0].ID
			case "missing-node":
				network.nodes[0].ID = ""
			}
			raw := []byte(`{"version":1,"jobs":[{"message_id":"pending","status":"queued","device_id":"m1","device_ip":"100.64.0.3"}]}`)
			if err := os.WriteFile(source, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := migrateLegacy(server, options, owner.Email); err == nil {
				t.Fatal("unverified Headscale mapping accepted")
			}
			path := filepath.Join(options.StateDir, "users", strconv.FormatInt(owner.ID, 10), "message-jobs.json")
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("ambiguous mapping published jobs")
			}
		})
	}
}
