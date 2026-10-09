package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

func TestDesktopSettingsPersistAutoStartWithoutServer(testContext *testing.T) {
	directory := testContext.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		testContext.Fatal(err)
	}
	settingsFile := filepath.Join(directory, "settings.json")
	settings := desktopSettings{Schema: 1, AutoStart: false}
	if err := saveDesktopSettings(settingsFile, settings); err != nil {
		testContext.Fatal(err)
	}
	loaded, err := loadDesktopSettings(settingsFile)
	if err != nil || loaded != settings {
		testContext.Fatalf("%+v %v", loaded, err)
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

func TestDesktopFDARequiresStandaloneAgentNotHubNestedPayload(t *testing.T) {
	for _, fixture := range []struct {
		path    string
		allowed bool
	}{
		{"/Users/fixture/.macfleet/desktop/runtime/Fleet Agent.app/Contents/MacOS/fleet-agent", true},
		{"/Applications/Fleet Hub.app/Contents/Library/LoginItems/Fleet Agent.app/Contents/MacOS/fleet-agent", false},
		{"/tmp/Other.APP/Fleet Agent.app/Contents/MacOS/fleet-agent", false},
		{"/Users/fixture/.local/bin/fleet-agent", false},
	} {
		if result := desktopIndependentAgentPath(fixture.path); result != fixture.allowed {
			t.Fatalf("path %s: %t, want %t", fixture.path, result, fixture.allowed)
		}
	}
}

func TestDesktopDiskProbeExplainsWhichBackgroundTargetsAreRestricted(t *testing.T) {
	result := probeDesktopDiskAccess([]string{"Library/Messages/chat.db", "Library/Mail"}, func(path string) error {
		if path == "Library/Mail" {
			return os.ErrPermission
		}
		return nil
	})
	data, _ := json.Marshal(result)
	if !strings.Contains(string(data), `"denied_targets":["Library/Mail"]`) || result.State != "restricted" || result.VerifiedTargets != 1 {
		t.Fatalf("missing real evidence: %s", data)
	}
}

func TestDesktopFullDiskAccessRequiresProtectedDatabaseNotOtherFilePermissions(t *testing.T) {
	for _, fixture := range []struct {
		name, expected string
		protected      error
		other          error
	}{
		{"FDA granted, other Unix permissions denied", "verified", nil, os.ErrPermission},
		{"other directory grants do not prove FDA", "unknown", os.ErrNotExist, nil},
		{"protected database denied by TCC", "restricted", syscall.EPERM, nil},
		{"Unix permissions do not prove a TCC denial", "unknown", syscall.EACCES, nil},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			result := probeDesktopFullDiskAccess("TCC.db", []string{"Mail"}, func(path string) error {
				if path == "TCC.db" {
					return fixture.protected
				}
				return fixture.other
			})
			if result.State != fixture.expected || result.Source != "background" {
				t.Fatalf("FDA evidence: %+v", result)
			}
			if fixture.other != nil && (len(result.DeniedTargets) == 0 || result.DeniedTargets[len(result.DeniedTargets)-1] != "Mail") {
				t.Fatal("file diagnostic was lost")
			}
		})
	}
}

func TestDesktopStatusDoesNotExposeDeviceSecrets(t *testing.T) {
	binding := deviceBinding{DeviceID: "m12", DeviceName: "Office Mac", OwnerEmail: "owner@example.test", DeviceToken: strings.Repeat("device-private", 5), ProxyToken: strings.Repeat("proxy-private", 5), Origin: "https://fleet.example.test", Complete: true}
	status := desktopPublicBinding(binding)
	bytes, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), "private") || strings.Contains(string(bytes), "token") {
		t.Fatalf("leaked credentials: %s", bytes)
	}
	if status.DeviceID != "m12" || status.OwnerEmail != binding.OwnerEmail || status.DeviceName != binding.DeviceName {
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
