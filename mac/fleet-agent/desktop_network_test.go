package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testDesktopMesh struct {
	fail   bool
	closed bool
}

func (mesh *testDesktopMesh) Up(context.Context) error {
	if mesh.fail {
		return errors.New("offline")
	}
	return nil
}
func (mesh *testDesktopMesh) Listen(string, string) (net.Listener, error) {
	return nil, errors.New("fixture")
}
func (mesh *testDesktopMesh) Close() error { mesh.closed = true; return nil }

func TestDesktopNetworkUsesPrivateIndependentStateAndNoPersistedAuthKey(t *testing.T) {
	directory := desktopTestDirectory(t)
	var received desktopMeshOptions
	mesh := &testDesktopMesh{}
	network := newDesktopNetwork(directory, func(options desktopMeshOptions) (desktopMesh, error) { received = options; return mesh, nil })
	grant := pairingGrant{deviceBinding: deviceBinding{DeviceID: "m12", Index: "12", Origin: "https://fleet.example.test"}, LoginServer: "https://control.example.test", AuthKey: "secret-one-time-key"}
	if err := network.Join(context.Background(), grant, false); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(received.Directory, directory+string(os.PathSeparator)) || received.Hostname != "mac12" || received.AuthKey != grant.AuthKey {
		t.Fatalf("unexpected mesh config: %+v", received)
	}
	data, err := os.ReadFile(filepath.Join(directory, "network.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "auth_key") {
		t.Fatal("persisted one-time key in network metadata")
	}
	var config desktopNetworkConfig
	if json.Unmarshal(data, &config) != nil || config.DeviceID != "m12" {
		t.Fatal("missing durable device association")
	}
	grant.DeviceID = "m13"
	grant.Index = "13"
	if network.Join(context.Background(), grant, false) == nil {
		t.Fatal("changed owner/network without stopping existing device")
	}
	network.Close()
	if !mesh.closed {
		t.Fatal("network left running")
	}
}

func TestDesktopNetworkFailureIsNotReportedConnected(t *testing.T) {
	network := newDesktopNetwork(desktopTestDirectory(t), func(desktopMeshOptions) (desktopMesh, error) { return &testDesktopMesh{fail: true}, nil })
	grant := pairingGrant{deviceBinding: deviceBinding{DeviceID: "m12", Index: "12", Origin: "https://fleet.example.test"}, LoginServer: "https://control.example.test", AuthKey: "key"}
	if network.Join(context.Background(), grant, false) == nil {
		t.Fatal("hidden network failure")
	}
	if _, err := network.Listen(7682); err == nil {
		t.Fatal("exposed remote services before successful join")
	}
}
