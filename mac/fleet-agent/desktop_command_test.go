package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopCommandOnlyReadsBackgroundEvidence(t *testing.T) {
	directory := desktopTestDirectory(t)
	var output bytes.Buffer
	if err := desktopRequest(context.Background(), directory, "status", nil, &output); err == nil {
		t.Fatal("missing daemon must not manufacture status or FDA evidence")
	}
	control, err := startDesktopControl(directory, filepath.Join(directory, "binding.json"), func() desktopDiskAccess {
		return desktopDiskAccess{State: "unknown", Source: "background"}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(control.Close)
	if err := desktopRequest(context.Background(), directory, "status", nil, &output); err != nil {
		t.Fatal(err)
	}
	var status desktopStatus
	if err := json.Unmarshal(output.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.PID != os.Getpid() || status.DiskAccess.State != "unknown" {
		t.Fatalf("%+v", status)
	}
	if err := desktopRequest(context.Background(), directory, "exec", nil, &output); err == nil {
		t.Fatal("accepted unrestricted command")
	}
	if err := desktopRequest(context.Background(), directory, "settings", bytes.NewBufferString(`{"schema":1,"origin":"http://remote.test","auto_start":true}`), &output); err == nil {
		t.Fatal("reported failed save as successful")
	}
}

func TestNativeAgentLeavesInitialDesktopEnvironmentToVerifiedInstaller(t *testing.T) {
	t.Setenv("FLEET_DESKTOP_MANAGED", "1")
	t.Setenv("FLEET_CODEX_APPSERVER_MODE", "shared")
	t.Setenv("FLEET_CODEX_DESKTOP_SHARED_DAEMON", "1")
	if desktopMayConfigureEnvironment() {
		t.Fatal("native Agent must leave initial GUI configuration to the verified shared installer")
	}
	t.Setenv("FLEET_DESKTOP_MANAGED", "")
	if !desktopMayConfigureEnvironment() {
		t.Fatal("changed legacy client behavior")
	}
}
