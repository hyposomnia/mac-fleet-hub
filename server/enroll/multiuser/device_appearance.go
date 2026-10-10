package multiuser

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var deviceIconTextRe = regexp.MustCompile(`^[A-Za-z0-9]{1,4}$`)

type deviceAppearance struct {
	Icon  string `json:"icon"`
	Text  string `json:"text,omitempty"`
	Color string `json:"color"`
}

func (appearance *deviceAppearance) validate() bool {
	switch appearance.Color {
	case "steel", "teal", "green", "amber", "coral", "violet", "rose", "slate":
	default:
		return false
	}
	switch appearance.Icon {
	case "monitor", "laptop", "desktop", "mini", "server", "terminal":
		appearance.Text = ""
		return true
	case "text":
		appearance.Text = strings.TrimSpace(appearance.Text)
		return deviceIconTextRe.MatchString(appearance.Text)
	default:
		return false
	}
}

type dashboardPreferences struct {
	DesktopMaxWindows    int                         `json:"desktopMaxWindows"`
	DesktopScrollback    int                         `json:"desktopScrollback"`
	MobileMaxWindows     int                         `json:"mobileMaxWindows"`
	MobileScrollback     int                         `json:"mobileScrollback"`
	AutoCloseMinutes     int                         `json:"autoCloseMinutes"`
	ChatCacheMaxSessions int                         `json:"chatCacheMaxSessions"`
	DeviceAppearance     map[string]deviceAppearance `json:"deviceAppearance"`
}

// Caller holds server.mu so settings POST and appearance PATCH cannot lose
// each other's fields. Existing integer-only preference rows remain readable.
func (server *Server) loadDashboardPreferences(userID int64) (dashboardPreferences, error) {
	preferences := dashboardPreferences{DesktopMaxWindows: 10, DesktopScrollback: 5000,
		MobileMaxWindows: 4, MobileScrollback: 5000, AutoCloseMinutes: 30, ChatCacheMaxSessions: 6,
		DeviceAppearance: map[string]deviceAppearance{}}
	var raw string
	err := server.db.QueryRow("SELECT value FROM preferences WHERE user_id=?", userID).Scan(&raw)
	if err == sql.ErrNoRows {
		return preferences, nil
	}
	if err != nil {
		return preferences, err
	}
	if err = json.Unmarshal([]byte(raw), &preferences); err != nil {
		return preferences, err
	}
	if preferences.DeviceAppearance == nil {
		preferences.DeviceAppearance = map[string]deviceAppearance{}
	}
	for id, appearance := range preferences.DeviceAppearance {
		if !validDeviceID(id) || !appearance.validate() {
			return preferences, fmt.Errorf("invalid device appearance for %q", id)
		}
		preferences.DeviceAppearance[id] = appearance
	}
	return preferences, nil
}

func validDeviceID(id string) bool {
	if len(id) < 2 || id[0] != 'm' || id[1] < '1' || id[1] > '9' {
		return false
	}
	for _, character := range id[2:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (server *Server) handlePreferences(writer http.ResponseWriter, request *http.Request, user User) {
	if request.Method != "GET" && request.Method != "POST" && request.Method != "PATCH" {
		reject(writer, 405, "不支持的方法")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	preferences, err := server.loadDashboardPreferences(user.ID)
	if err != nil {
		server.failure(writer, err)
		return
	}
	devices, err := server.Devices(user.ID)
	if err != nil {
		server.failure(writer, err)
		return
	}
	owned := map[string]bool{}
	for _, device := range devices {
		if device.Status != "revoked" {
			owned[device.ID] = true
		}
	}
	for id := range preferences.DeviceAppearance {
		if !owned[id] {
			delete(preferences.DeviceAppearance, id)
		}
	}
	if request.Method == "PATCH" {
		var input struct {
			ID         string            `json:"id"`
			Appearance *deviceAppearance `json:"appearance"`
			IfAbsent   bool              `json:"ifAbsent"`
		}
		if !decode(writer, request, &input) {
			return
		}
		if !validDeviceID(input.ID) || input.Appearance == nil || !input.Appearance.validate() {
			reject(writer, 400, "设备外观格式错误")
			return
		}
		if !owned[input.ID] {
			reject(writer, 404, "设备不存在")
			return
		}
		if _, exists := preferences.DeviceAppearance[input.ID]; !exists || !input.IfAbsent {
			preferences.DeviceAppearance[input.ID] = *input.Appearance
		}
	} else if request.Method == "POST" {
		var input map[string]int
		if !decode(writer, request, &input) {
			return
		}
		fields := map[string]*int{"desktopMaxWindows": &preferences.DesktopMaxWindows, "desktopScrollback": &preferences.DesktopScrollback,
			"mobileMaxWindows": &preferences.MobileMaxWindows, "mobileScrollback": &preferences.MobileScrollback,
			"autoCloseMinutes": &preferences.AutoCloseMinutes, "chatCacheMaxSessions": &preferences.ChatCacheMaxSessions}
		limits := map[string][2]int{"desktopMaxWindows": {1, 30}, "desktopScrollback": {200, 100000}, "mobileMaxWindows": {1, 12}, "mobileScrollback": {200, 100000}, "autoCloseMinutes": {1, 1440}, "chatCacheMaxSessions": {1, 20}}
		for key, bounds := range limits {
			if value := input[key]; value != 0 {
				*fields[key] = min(bounds[1], max(bounds[0], value))
			}
		}
	}
	if request.Method != "GET" {
		raw, err := json.Marshal(preferences)
		if err == nil {
			_, err = server.db.Exec("INSERT INTO preferences(user_id,value) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET value=excluded.value", user.ID, string(raw))
		}
		if err != nil {
			server.failure(writer, err)
			return
		}
	}
	respond(writer, 200, preferences)
}
