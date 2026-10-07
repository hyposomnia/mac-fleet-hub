package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type desktopSettings struct {
	Schema    int    `json:"schema"`
	Origin    string `json:"origin"`
	AutoStart bool   `json:"auto_start"`
}

type desktopDiskAccess struct {
	State           string   `json:"state"`
	Source          string   `json:"source"`
	CheckedAt       int64    `json:"checked_at"`
	VerifiedTargets int      `json:"verified_targets"`
	DeniedTargets   []string `json:"denied_targets,omitempty"`
}

type desktopBindingStatus struct {
	DeviceID   string `json:"device_id"`
	OwnerEmail string `json:"owner_email"`
	Origin     string `json:"origin"`
	Complete   bool   `json:"complete"`
	Locked     bool   `json:"locked"`
}

func desktopStateDirectory() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".macfleet", "desktop")
}

func loadDesktopSettings(path string) (desktopSettings, error) {
	settings := desktopSettings{Schema: 1, AutoStart: true}
	if err := readPrivateJSON(path, &settings); err != nil {
		if os.IsNotExist(err) {
			return settings, nil
		}
		return settings, err
	}
	if settings.Schema != 1 {
		return settings, errors.New("不支持的本机设置版本")
	}
	if settings.Origin != "" {
		origin, err := validateFleetOrigin(settings.Origin)
		if err != nil {
			return settings, err
		}
		settings.Origin = origin
	}
	return settings, nil
}

func saveDesktopSettings(path string, settings desktopSettings) error {
	if settings.Schema != 1 {
		return errors.New("不支持的本机设置版本")
	}
	origin, err := validateFleetOrigin(strings.TrimSpace(settings.Origin))
	if err != nil {
		return err
	}
	settings.Origin = origin
	return writePrivateJSON(path, settings)
}

func desktopPublicBinding(binding deviceBinding) desktopBindingStatus {
	return desktopBindingStatus{DeviceID: binding.DeviceID, OwnerEmail: binding.OwnerEmail, Origin: binding.Origin, Complete: binding.Complete, Locked: binding.Locked}
}

func probeDesktopDiskAccess(targets []string, probe func(string) error) desktopDiskAccess {
	result := desktopDiskAccess{State: "unknown", Source: "background", CheckedAt: time.Now().Unix()}
	unexpected := false
	for _, target := range targets {
		err := probe(target)
		switch {
		case err == nil:
			result.VerifiedTargets++
		case errors.Is(err, os.ErrPermission):
			result.State = "restricted"
			result.DeniedTargets = append(result.DeniedTargets, target)
		case os.IsNotExist(err):
		default:
			unexpected = true
		}
	}
	if result.State != "restricted" && !unexpected && result.VerifiedTargets > 0 {
		result.State = "verified"
	}
	return result
}

func desktopDiskProbe(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		_, err = file.Readdirnames(1)
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
	return err
}

func probeDesktopFullDiskAccess(protected string, diagnostics []string, probe func(string) error) desktopDiskAccess {
	protectedError := probe(protected)
	result := probeDesktopDiskAccess([]string{protected}, func(string) error { return protectedError })
	if result.State == "restricted" && !errors.Is(protectedError, syscall.EPERM) {
		result.State = "unknown"
	}
	for _, path := range diagnostics {
		if errors.Is(probe(path), os.ErrPermission) {
			result.DeniedTargets = append(result.DeniedTargets, path)
		}
	}
	return result
}

func desktopIndependentAgentPath(executable string) bool {
	if !strings.HasSuffix(filepath.Clean(executable), "/Fleet Agent.app/Contents/MacOS/fleet-agent") {
		return false
	}
	application := filepath.Dir(filepath.Dir(filepath.Dir(executable)))
	for parent := filepath.Dir(application); parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		if strings.HasSuffix(strings.ToLower(parent), ".app") {
			return false
		}
	}
	return true
}

func desktopDiskStatus() desktopDiskAccess {
	executable, err := os.Executable()
	if err != nil || runtime.GOOS != "darwin" || os.Getppid() != 1 || !desktopIndependentAgentPath(executable) {
		return desktopDiskAccess{State: "unknown", Source: "unverified-process", CheckedAt: time.Now().Unix()}
	}
	home, _ := os.UserHomeDir()
	result := probeDesktopFullDiskAccess(filepath.Join(home, "Library", "Application Support", "com.apple.TCC", "TCC.db"), []string{filepath.Join(home, "Library", "Messages", "chat.db"), filepath.Join(home, "Library", "Safari", "History.db"), filepath.Join(home, "Library", "Mail")}, desktopDiskProbe)
	for index, path := range result.DeniedTargets {
		result.DeniedTargets[index], _ = filepath.Rel(home, path)
	}
	return result
}
