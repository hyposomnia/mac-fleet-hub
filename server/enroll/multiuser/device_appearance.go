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
var paletteColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type deviceAppearance struct {
	Icon  string `json:"icon"`
	Text  string `json:"text,omitempty"`
	Color string `json:"color"`
}

type paletteColors struct {
	Canvas    string `json:"canvas"`
	Accent    string `json:"accent"`
	Highlight string `json:"highlight"`
	Text      string `json:"text"`
}

type themePalette struct {
	Light paletteColors `json:"light"`
	Dark  paletteColors `json:"dark"`
}

func (palette *themePalette) validate() bool {
	for _, colors := range []*paletteColors{&palette.Light, &palette.Dark} {
		for _, color := range []*string{&colors.Canvas, &colors.Accent, &colors.Highlight, &colors.Text} {
			if !paletteColorRe.MatchString(*color) {
				return false
			}
			*color = strings.ToUpper(*color)
		}
	}
	return true
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
	FileView             string                      `json:"fileView,omitempty"`
	DeviceAppearance     map[string]deviceAppearance `json:"deviceAppearance"`
	DeviceOrder          []string                    `json:"deviceOrder"`
	Palette              *themePalette               `json:"palette"`
}

// Caller holds server.mu so settings POST, appearance/file view PATCH and palette PUT
// cannot lose each other's fields. Missing fileView permits one-time browser migration.
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
	seen := map[string]bool{}
	for _, id := range preferences.DeviceOrder {
		if !validDeviceID(id) || seen[id] {
			return preferences, fmt.Errorf("invalid device order")
		}
		seen[id] = true
	}
	if preferences.Palette != nil && !preferences.Palette.validate() {
		return preferences, fmt.Errorf("invalid theme palette")
	}
	if preferences.FileView != "" && !validFileView(preferences.FileView) {
		return preferences, fmt.Errorf("invalid file view")
	}
	return preferences, nil
}

func validFileView(view string) bool {
	return view == "icons" || view == "list" || view == "columns"
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
	if request.Method != "GET" && request.Method != "POST" && request.Method != "PATCH" && request.Method != "PUT" {
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
	if request.Method == "PUT" {
		var input struct {
			Palette  *themePalette `json:"palette"`
			IfAbsent bool          `json:"ifAbsent"`
		}
		if !decode(writer, request, &input) {
			return
		}
		if input.Palette == nil || !input.Palette.validate() {
			reject(writer, 400, "配色须包含浅色和深色的八个 #RRGGBB 颜色")
			return
		}
		if preferences.Palette == nil || !input.IfAbsent {
			preferences.Palette = input.Palette
		}
	} else if request.Method == "PATCH" {
		var input struct {
			ID         string            `json:"id"`
			Appearance *deviceAppearance `json:"appearance"`
			IfAbsent   bool              `json:"ifAbsent"`
			Order      *[]string         `json:"order"`
			FileView   *string           `json:"fileView"`
		}
		if !decode(writer, request, &input) {
			return
		}
		if input.FileView != nil {
			if !validFileView(*input.FileView) {
				reject(writer, 400, "文件查看方式格式错误")
				return
			}
			if preferences.FileView == "" || !input.IfAbsent {
				preferences.FileView = *input.FileView
			}
		} else if input.Order != nil {
			seen := map[string]bool{}
			for _, id := range *input.Order {
				if !validDeviceID(id) || seen[id] || !owned[id] {
					reject(writer, 400, "设备顺序格式错误")
					return
				}
				seen[id] = true
			}
			preferences.DeviceOrder = append([]string(nil), (*input.Order)...)
		} else if !validDeviceID(input.ID) || input.Appearance == nil || !input.Appearance.validate() {
			reject(writer, 400, "设备外观格式错误")
			return
		} else if !owned[input.ID] {
			reject(writer, 404, "设备不存在")
			return
		} else if _, exists := preferences.DeviceAppearance[input.ID]; !exists || !input.IfAbsent {
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
		for key, field := range fields {
			value, provided := input[key]
			if !provided {
				continue
			}
			minimum := 1
			if key == "autoCloseMinutes" {
				minimum = 0
			}
			if value < minimum {
				reject(writer, 400, "设置值不能小于允许的最小值")
				return
			}
			// Scrollback has a per-terminal allocation cost; connection/cache counts
			// are eviction targets, not a limit on how many sessions users can open.
			if key == "desktopScrollback" || key == "mobileScrollback" {
				value = min(100000, max(200, value))
			}
			if key == "autoCloseMinutes" && int64(value) > 2147483647/60 {
				reject(writer, 400, "空闲回收时间过大")
				return
			}
			*field = value
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
