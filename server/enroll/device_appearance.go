package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var (
	deviceAppearanceFile = envOr("ENROLL_DEVICE_APPEARANCE_FILE", "/var/lib/fleet-enroll/device-appearance.json")
	deviceAppearanceMu   sync.Mutex
	deviceIconTextRe     = regexp.MustCompile(`^[A-Za-z0-9]{1,4}$`)
)

type deviceAppearance struct {
	Icon  string `json:"icon"`
	Text  string `json:"text,omitempty"`
	Color string `json:"color"`
}

func (a *deviceAppearance) validate() bool {
	switch a.Color {
	case "steel", "teal", "green", "amber", "coral", "violet", "rose", "slate":
	default:
		return false
	}
	switch a.Icon {
	case "monitor", "laptop", "desktop", "mini", "server", "terminal":
		a.Text = ""
		return true
	case "text":
		a.Text = strings.TrimSpace(a.Text)
		return deviceIconTextRe.MatchString(a.Text)
	default:
		return false
	}
}

// Caller holds deviceAppearanceMu. Only a missing file is an empty store;
// unreadable or corrupt files must not be silently replaced during a save.
func readDeviceAppearanceLocked() (map[string]deviceAppearance, error) {
	data, err := os.ReadFile(deviceAppearanceFile)
	if os.IsNotExist(err) {
		return map[string]deviceAppearance{}, nil
	}
	if err != nil {
		return nil, err
	}
	var preferences map[string]deviceAppearance
	if err := json.Unmarshal(data, &preferences); err != nil {
		return nil, err
	}
	if preferences == nil {
		preferences = map[string]deviceAppearance{}
	}
	for id, appearance := range preferences {
		if !validMacID(id) || !appearance.validate() {
			return nil, fmt.Errorf("invalid device appearance for %q", id)
		}
		preferences[id] = appearance
	}
	return preferences, nil
}

func loadDeviceAppearance() (map[string]deviceAppearance, error) {
	deviceAppearanceMu.Lock()
	defer deviceAppearanceMu.Unlock()
	return readDeviceAppearanceLocked()
}

func saveDeviceAppearanceLocked(preferences map[string]deviceAppearance) error {
	data, err := json.Marshal(preferences)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(deviceAppearanceFile), ".device-appearance-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), deviceAppearanceFile)
}

// PATCH /settings writes only one device. Terminal settings POSTs remain
// independent, and ifAbsent imports legacy browser preferences without
// overwriting an appearance already saved by another browser.
func handleDeviceAppearance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID         string            `json:"id"`
		Appearance *deviceAppearance `json:"appearance"`
		IfAbsent   bool              `json:"ifAbsent"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req) != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if !validMacID(req.ID) || req.Appearance == nil || !req.Appearance.validate() {
		writeErr(w, 400, "设备外观格式错误")
		return
	}
	deviceAppearanceMu.Lock()
	defer deviceAppearanceMu.Unlock()
	preferences, err := readDeviceAppearanceLocked()
	if err == nil {
		if _, exists := preferences[req.ID]; !exists || !req.IfAbsent {
			preferences[req.ID] = *req.Appearance
			err = saveDeviceAppearanceLocked(preferences)
		}
	}
	if err != nil {
		log.Printf("保存设备外观失败: %v", err)
		writeErr(w, 500, "设备外观保存失败")
		return
	}
	writeJSON(w, 200, map[string]any{"deviceAppearance": preferences})
}
