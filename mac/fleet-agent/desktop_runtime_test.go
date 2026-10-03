package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func desktopTestDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "fd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	return directory
}

func TestDesktopSocketUsesActualBackgroundProbe(t *testing.T) {
	directory := desktopTestDirectory(t)
	probes := 0
	runtime, err := startDesktopControl(directory, filepath.Join(directory, "binding.json"), func() desktopDiskAccess {
		probes++
		return desktopDiskAccess{State: "restricted", Source: "background"}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	info, _ := os.Lstat(filepath.Join(directory, "control.sock"))
	if info.Mode().Perm() != 0600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatal("management endpoint must be a private Unix socket")
	}
	client := desktopTestClient(directory)
	response, err := client.Get("http://local/status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var status desktopStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.PID != os.Getpid() || status.DiskAccess.Source != "background" || status.DiskAccess.State != "restricted" || status.Binding != nil || probes != 1 {
		t.Fatalf("unexpected background evidence: %+v; probes=%d", status, probes)
	}
	response, err = client.Post("http://local/disk/recheck", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || probes != 2 {
		t.Fatal("recheck did not execute in background")
	}
	if _, err := startDesktopControl(directory, "unused", desktopDiskStatus); err == nil {
		t.Fatal("replaced an active control socket")
	}
}

func desktopTestClient(directory string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(directory, "control.sock"))
	}}}
}

func TestDesktopMaintenanceBlocksConfigurationAndPairing(t *testing.T) {
	directory := desktopTestDirectory(t)
	control, err := startDesktopControl(directory, filepath.Join(directory, "binding.json"), desktopDiskStatus)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(control.Close)
	if err := desktopMaintenance.Prepare(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(desktopMaintenance.Resume)
	client := desktopTestClient(directory)
	for _, fixture := range []struct{ method, path string }{
		{"PUT", "/settings"}, {"POST", "/pair/start"}, {"POST", "/pair/confirm"},
	} {
		request, _ := http.NewRequest(fixture.method, "http://local"+fixture.path, strings.NewReader(`{"schema":1,"origin":"https://fleet.example.test"}`))
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusConflict {
			t.Errorf("%s during maintenance: got %d, want 409", fixture.path, response.StatusCode)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("maintenance request changed settings")
	}
}

func TestDesktopSocketRejectsUnsafeRequestsAndBindingChanges(t *testing.T) {
	directory := desktopTestDirectory(t)
	bindingFile := filepath.Join(directory, "binding.json")
	binding := deviceBinding{Origin: "https://fleet.example.test", DeviceID: "m12", OwnerEmail: "owner@example.test", DeviceToken: strings.Repeat("a", 48), ProxyToken: strings.Repeat("b", 48), Complete: true}
	if err := writePrivateJSON(bindingFile, binding); err != nil {
		t.Fatal(err)
	}
	runtime, err := startDesktopControl(directory, bindingFile, func() desktopDiskAccess { return desktopDiskAccess{State: "unknown", Source: "background"} })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	client := desktopTestClient(directory)
	for _, fixture := range []struct {
		method, endpoint, body string
		expected               int
	}{
		{"PUT", "/settings", `{"schema":1,"origin":"https://other.example.test","auto_start":true}`, 409},
		{"PUT", "/settings", `{"schema":1,"origin":"https://fleet.example.test","owner":"intruder"}`, 400},
		{"POST", "/status", `{}`, 405},
		{"POST", "/exec", `{"command":"sh"}`, 404},
		{"PUT", "/settings", strings.Repeat("x", 40000), 400},
		{"PUT", "/settings", `{"schema":1,"origin":"https://fleet.example.test","auto_start":false}`, 200},
	} {
		request, _ := http.NewRequest(fixture.method, "http://local"+fixture.endpoint, strings.NewReader(fixture.body))
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != fixture.expected {
			t.Fatalf("%s: %d %s", fixture.endpoint, response.StatusCode, data)
		}
		if strings.Contains(string(data), binding.DeviceToken) || strings.Contains(string(data), binding.ProxyToken) {
			t.Fatal("leaked binding secrets")
		}
	}
	request, _ := http.NewRequest("PUT", "http://local/settings", strings.NewReader(`{}`))
	request.Header.Set("Origin", "https://attacker.example.test")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("accepted browser-origin management request")
	}
	settings, err := loadDesktopSettings(filepath.Join(directory, "settings.json"))
	if err != nil || settings.AutoStart || settings.Origin != binding.Origin {
		t.Fatalf("%+v %v", settings, err)
	}
}

func TestDesktopSocketRefusesSymlinkAndPublicDirectory(t *testing.T) {
	directory := desktopTestDirectory(t)
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if runtime, err := startDesktopControl(directory, "unused", desktopDiskStatus); err == nil {
		runtime.Close()
		t.Fatal("accepted public state directory")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "target")
	os.WriteFile(target, []byte("preserve"), 0600)
	os.Symlink(target, filepath.Join(directory, "control.sock"))
	if runtime, err := startDesktopControl(directory, "unused", desktopDiskStatus); err == nil {
		runtime.Close()
		t.Fatal("replaced symlink socket")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "preserve" {
		t.Fatal("modified unrelated file")
	}
}
