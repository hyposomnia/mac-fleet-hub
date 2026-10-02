package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginOriginPromptsWithoutCollectingAccountCredentials(t *testing.T) {
	var output bytes.Buffer
	origin, err := readLoginOrigin("", bufio.NewReader(strings.NewReader("https://fleet.example.com\n")), &output)
	if err != nil || origin != "https://fleet.example.com" {
		t.Fatalf("origin=%q err=%v", origin, err)
	}
	if !strings.Contains(output.String(), "服务网页地址") || strings.Contains(output.String(), "密码") {
		t.Fatal("prompt must only ask for server address")
	}
	for _, input := range []string{"", "\n", "http://example.com\n", "https://example.com/path\n"} {
		if _, err := readLoginOrigin("", bufio.NewReader(strings.NewReader(input)), io.Discard); err == nil {
			t.Fatalf("accepted invalid address %q", input)
		}
	}
	origin, err = readLoginOrigin("https://fleet.example.com", bufio.NewReader(strings.NewReader("")), io.Discard)
	if err != nil || origin != "https://fleet.example.com" {
		t.Fatal("explicit address must not read stdin", err)
	}
}

func TestTerminalConfirmationShowsOwnerWithoutSecretsAndRejectsDefault(t *testing.T) {
	grant := pairingGrant{deviceBinding: deviceBinding{Origin: "https://fleet.example.com", OwnerEmail: "owner@example.com", DeviceID: "m101", DeviceToken: "DEVICE-SECRET", ProxyToken: "PROXY-SECRET"}, AuthKey: "KEY-SECRET"}
	for _, input := range []string{"\n", "n\n", "", "y\n", "YES\n"} {
		var output bytes.Buffer
		err := confirmDeviceGrant(grant, bufio.NewReader(strings.NewReader(input)), &output)
		accepted := input == "y\n" || input == "YES\n"
		if (err == nil) != accepted {
			t.Fatalf("input=%q err=%v", input, err)
		}
		for _, value := range []string{grant.Origin, grant.OwnerEmail, grant.DeviceID} {
			if !strings.Contains(output.String(), value) {
				t.Fatalf("missing identity %s", value)
			}
		}
		if strings.Contains(output.String(), "SECRET") {
			t.Fatal("terminal confirmation leaked credentials")
		}
	}
}

func TestPairingWaitsForTerminalConsentBeforeBindingOrJoining(t *testing.T) {
	starts, claims, joins, setups, confirmations := 0, 0, 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/enrollment/start":
			starts++
			json.NewEncoder(writer).Encode(map[string]any{"request_id": "request", "claim_token": "private-claim", "code": "ABCD1234", "verification_url": "http://" + request.Host + "/enroll/confirm?code=ABCD1234"})
		case "/api/enrollment/claim":
			claims++
			json.NewEncoder(writer).Encode(map[string]any{"authKey": "private-key", "loginServer": "https://control.example.com", "index": "101", "device_id": "m101", "owner_email": "owner@example.com", "device_token": strings.Repeat("a", 43), "proxy_token": strings.Repeat("b", 43), "agent_port": 7682, "terminal_port": 7681, "files_port": 8080})
		case "/api/enrollment/complete":
			writer.WriteHeader(200)
		default:
			t.Errorf("unexpected endpoint %s", request.URL.Path)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "private", "binding.json")
	accepted := false
	options := loginOptions{Path: path, Output: io.Discard, NoOpen: true,
		Confirm: func(grant pairingGrant) error {
			confirmations++
			if joins != 0 || setups != 0 || grant.OwnerEmail != "owner@example.com" {
				t.Fatal("local mutation before consent or missing owner")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("binding must not be installed before consent")
			}
			if !accepted {
				return errors.New("cancelled")
			}
			return nil
		},
		Join:  func(context.Context, pairingGrant, bool) error { joins++; return nil },
		Setup: func(context.Context, deviceBinding) error { setups++; return nil },
	}
	if err := pairDevice(context.Background(), server.URL, options); err == nil {
		t.Fatal("cancelled confirmation must stop pairing")
	}
	if joins != 0 || setups != 0 {
		t.Fatal("cancelled confirmation changed local services")
	}
	accepted = true
	if err := pairDevice(context.Background(), server.URL, options); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || claims != 1 || joins != 1 || setups != 1 || confirmations != 2 {
		t.Fatalf("unexpected retry: starts=%d claims=%d joins=%d setups=%d confirmations=%d", starts, claims, joins, setups, confirmations)
	}
	if err := pairDevice(context.Background(), server.URL, options); err != nil {
		t.Fatal(err)
	}
	if confirmations != 2 {
		t.Fatal("completed binding must not request a new authorization")
	}
}

func TestBrowserPairingResumesWithoutRejoiningOrChangingOwner(t *testing.T) {
	confirmed := false
	starts, joins, setups := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/enrollment/start":
			starts++
			json.NewEncoder(writer).Encode(map[string]any{"request_id": "request", "claim_token": "private-claim", "code": "ABCD1234", "verification_url": "http://" + request.Host + "/enroll/confirm?code=ABCD1234"})
		case "/api/enrollment/claim":
			if !confirmed {
				writer.WriteHeader(202)
				return
			}
			json.NewEncoder(writer).Encode(map[string]any{"authKey": "private-key", "loginServer": "https://control.example.com", "index": "101", "device_id": "m101", "owner_email": "owner@example.com", "device_token": strings.Repeat("a", 43), "proxy_token": strings.Repeat("b", 43), "agent_port": 7682, "terminal_port": 7681, "files_port": 8080})
		case "/api/enrollment/complete":
			writer.WriteHeader(200)
		default:
			t.Errorf("unexpected endpoint %s", request.URL.Path)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "private", "binding.json")
	options := loginOptions{Path: path, Output: io.Discard, Open: func(link string) error {
		if !strings.HasPrefix(link, server.URL+"/enroll/confirm?") {
			t.Fatal("unsafe browser destination")
		}
		confirmed = true
		return nil
	}, Join: func(context.Context, pairingGrant, bool) error { joins++; return nil }, Setup: func(context.Context, deviceBinding) error {
		setups++
		if setups == 1 {
			return errors.New("setup interrupted")
		}
		return nil
	}}
	if err := pairDevice(context.Background(), server.URL, options); err == nil {
		t.Fatal("setup failure hidden")
	}
	if _, err := readDeviceBinding(path); err != nil {
		t.Fatal("credentials not durably saved", err)
	}
	if err := pairDevice(context.Background(), server.URL, options); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || joins != 1 || setups != 2 {
		t.Fatalf("unsafe retry starts=%d joins=%d setups=%d", starts, joins, setups)
	}
	options.Reconfigure = true
	if err := pairDevice(context.Background(), server.URL, options); err != nil {
		t.Fatal(err)
	}
	if setups != 3 || joins != 1 || starts != 1 {
		t.Fatal("reinstall must configure without rebinding")
	}
	if err := pairDevice(context.Background(), "https://other.example.com", options); err == nil {
		t.Fatal("silently changed owner/server")
	}
}

func TestLogoutLocksBeforeContactingServerAndRetries(t *testing.T) {
	status := 503
	path := filepath.Join(t.TempDir(), "private", "binding.json")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		binding, err := readDeviceBinding(path)
		if err != nil || !binding.Locked {
			t.Error("remote revoke started before local lock")
		}
		writer.WriteHeader(status)
	}))
	defer server.Close()
	binding := deviceBinding{Origin: server.URL, DeviceID: "m101", DeviceToken: strings.Repeat("a", 43), ProxyToken: strings.Repeat("b", 43)}
	writePrivateJSON(path, binding)
	if err := logoutDevice(context.Background(), path); err == nil {
		t.Fatal("remote failure hidden")
	}
	if binding, err := readDeviceBinding(path); err != nil || !binding.Locked {
		t.Fatal("lost retry credentials or lock")
	}
	status = 200
	if err := logoutDevice(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("credentials retained")
	}
}

func TestClientRejectsUnsafeOriginsAndCommandsAdvertiseProtocol(t *testing.T) {
	for _, origin := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/api", "https://example.com?secret=x"} {
		if _, err := validateFleetOrigin(origin); err == nil {
			t.Errorf("accepted %s", origin)
		}
	}
	if parseCommand("login") == actUnknown || parseCommand("logout") == actUnknown || parseCommand("capabilities") == actUnknown {
		t.Fatal("missing pairing commands")
	}
}

func TestDeviceCommandsSerializePrivateState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "binding.json")
	lock, err := lockDeviceState(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if second, err := lockDeviceState(path); err == nil {
		second.Close()
		t.Fatal("concurrent commands can overwrite device identity")
	}
}

func TestUpdatesCannotRemoveDeviceAuthorization(t *testing.T) {
	for _, value := range []string{`{}`, `{"device_authorization":1}`, `{"device_authorization":0,"browser_pairing":1,"agent_proxy":1}`} {
		if supportsDeviceAuthorization([]byte(value)) {
			t.Fatal("accepted unsupported agent")
		}
	}
	if !supportsDeviceAuthorization([]byte(`{"device_authorization":1,"browser_pairing":1,"agent_proxy":1}`)) {
		t.Fatal("rejected complete protocol")
	}
}

func TestPairingRecoversLostMeshJoinResponse(t *testing.T) {
	joined := false
	joins := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/enrollment/complete" {
			t.Error("retry should use persisted grant")
		}
		if !joined {
			writer.WriteHeader(202)
		} else {
			writer.WriteHeader(200)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "private", "binding.json")
	grant := pairingGrant{deviceBinding: deviceBinding{Origin: server.URL, Index: "1", DeviceID: "m1", OwnerEmail: "owner@example.com", DeviceToken: strings.Repeat("a", 43), ProxyToken: strings.Repeat("b", 43)}, AuthKey: "one-use-key", LoginServer: "https://control.example.com"}
	writePrivateJSON(filepath.Join(filepath.Dir(path), "pairing.json"), pairingPending{Origin: server.URL, ID: "request", Token: "claim", URL: server.URL + "/enroll/confirm?code=code", Grant: &grant})
	options := loginOptions{Path: path, Output: io.Discard, Join: func(context.Context, pairingGrant, bool) error {
		joins++
		joined = true
		return errors.New("join response lost")
	}, Setup: func(context.Context, deviceBinding) error { return nil }}
	if err := pairDevice(context.Background(), server.URL, options); err == nil {
		t.Fatal("join error hidden")
	}
	if err := pairDevice(context.Background(), server.URL, options); err != nil {
		t.Fatal(err)
	}
	if joins != 1 {
		t.Fatal("reused consumed one-use key")
	}
}
