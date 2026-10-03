package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopSettingsValidateAndPersist(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "settings.json")
	settings := desktopSettings{Schema: 1, Origin: " https://fleet.example.test:7443/ ", AutoStart: true}
	if err := saveDesktopSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadDesktopSettings(path)
	if err != nil || loaded.Origin != "https://fleet.example.test:7443" || !loaded.AutoStart {
		t.Fatalf("%+v %v", loaded, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("settings must be private")
	}
	for _, origin := range []string{"http://fleet.example.test", "https://fleet.example.test/path", "https://owner:secret@fleet.example.test", "https://fleet.example.test?token=secret"} {
		settings.Origin = origin
		if saveDesktopSettings(path, settings) == nil {
			t.Fatalf("accepted %s", origin)
		}
		retained, _ := loadDesktopSettings(path)
		if retained != loaded {
			t.Fatal("invalid draft replaced saved settings")
		}
	}
	settings = loaded
	settings.Schema = 9
	if saveDesktopSettings(path, settings) == nil {
		t.Fatal("accepted unsupported schema")
	}
}

func TestDesktopSettingsDoNotFollowSymlinks(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "settings.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if saveDesktopSettings(path, desktopSettings{Schema: 1, Origin: "https://fleet.example.test"}) == nil {
		t.Fatal("followed settings symlink")
	}
	bytes, _ := os.ReadFile(target)
	if string(bytes) != "original" {
		t.Fatal("modified symlink target")
	}
}

func TestDesktopDiskPermissionProbeClassifiesEvidence(t *testing.T) {
	for _, fixture := range []struct {
		name, expected string
		results        []error
	}{
		{"absent is not authorization", "unknown", []error{os.ErrNotExist, os.ErrNotExist}},
		{"permission denied", "restricted", []error{nil, os.ErrPermission}},
		{"protected access verified", "verified", []error{nil, os.ErrNotExist}},
		{"unexpected error", "unknown", []error{errors.New("probe unavailable")}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			index := 0
			probe := func(path string) error { result := fixture.results[index]; index++; return result }
			targets := make([]string, len(fixture.results))
			result := probeDesktopDiskAccess(targets, probe)
			if result.State != fixture.expected || result.Source != "background" {
				t.Fatalf("%+v", result)
			}
		})
	}
}

func TestDesktopStatusDoesNotExposeDeviceSecrets(t *testing.T) {
	binding := deviceBinding{DeviceID: "m12", OwnerEmail: "owner@example.test", DeviceToken: strings.Repeat("device-private", 5), ProxyToken: strings.Repeat("proxy-private", 5), Origin: "https://fleet.example.test", Complete: true}
	status := desktopPublicBinding(binding)
	bytes, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), "private") || strings.Contains(string(bytes), "token") {
		t.Fatalf("leaked credentials: %s", bytes)
	}
	if status.DeviceID != "m12" || status.OwnerEmail != binding.OwnerEmail {
		t.Fatal("missing public ownership metadata")
	}
}

func TestDesktopDiskProbeDoesNotClaimLaunchdEvidenceInUICaller(t *testing.T) {
	if os.Getppid() == 1 {
		t.Skip("test process was adopted by launchd")
	}
	status := desktopDiskStatus()
	if status.State != "unknown" || status.Source == "background" {
		t.Fatalf("non-launchd process claimed background FDA: %+v", status)
	}
}
