package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopPairingRequiresMatchingExplicitConfirmation(t *testing.T) {
	var joins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/enrollment/start":
			json.NewEncoder(writer).Encode(map[string]string{"request_id": "request", "claim_token": "private-claim", "code": "ABCD1234", "verification_url": "http://" + request.Host + "/enroll/confirm?code=ABCD1234"})
		case "/api/enrollment/claim":
			json.NewEncoder(writer).Encode(map[string]any{"authKey": "private-key", "loginServer": "https://control.example.test", "index": "12", "device_id": "m12", "owner_email": "owner@example.test", "device_token": strings.Repeat("a", 48), "proxy_token": strings.Repeat("b", 48), "agent_port": 7682, "terminal_port": 7681, "files_port": 8080})
		case "/api/enrollment/complete":
		default:
			writer.WriteHeader(404)
		}
	}))
	defer server.Close()
	path := filepath.Join(desktopTestDirectory(t), "binding.json")
	coordinator := newDesktopPairing(loginOptions{Path: path, Join: func(context.Context, pairingGrant, bool) error { joins.Add(1); return nil }, Setup: func(context.Context, deviceBinding) error { return nil }})
	t.Cleanup(coordinator.Cancel)
	if err := coordinator.Start(server.URL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for coordinator.Snapshot().Phase != "awaiting_confirmation" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	state := coordinator.Snapshot()
	if state.Phase != "awaiting_confirmation" || state.Code != "ABCD1234" || state.OwnerEmail != "owner@example.test" {
		t.Fatalf("%+v", state)
	}
	data, _ := json.Marshal(state)
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "token") {
		t.Fatal("GUI state leaked a credential")
	}
	if joins.Load() != 0 {
		t.Fatal("joined without GUI confirmation")
	}
	wrong := state
	wrong.OwnerEmail = "other@example.test"
	if coordinator.Confirm(wrong) == nil || joins.Load() != 0 {
		t.Fatal("accepted stale or mismatched owner")
	}
	if coordinator.Start(server.URL) == nil {
		t.Fatal("allowed concurrent pairing")
	}
	if err := coordinator.Confirm(state); err != nil {
		t.Fatal(err)
	}
	for coordinator.Snapshot().Phase != "complete" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if coordinator.Snapshot().Phase != "complete" || joins.Load() != 1 {
		t.Fatalf("%+v", coordinator.Snapshot())
	}
	if coordinator.Confirm(state) == nil {
		t.Fatal("replayed consent")
	}
}

func TestDesktopPairingCancelDoesNotApproveJoin(t *testing.T) {
	coordinator := newDesktopPairing(loginOptions{})
	if coordinator.Start("https://fleet.example.test") == nil {
		t.Fatal("pairing began without native network adapters")
	}
}
