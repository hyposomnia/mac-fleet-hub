package multiuser

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
)

type appearanceSettingsResponse struct {
	ChatCacheMaxSessions int                          `json:"chatCacheMaxSessions"`
	DeviceAppearance     map[string]map[string]string `json:"deviceAppearance"`
}

func readAppearanceSettings(t *testing.T, response *httptest.ResponseRecorder) appearanceSettingsResponse {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("settings status=%d body=%s", response.Code, response.Body.String())
	}
	var result appearanceSettingsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.DeviceAppearance == nil {
		t.Fatalf("appearance response missing or invalid: %v %s", err, response.Body.String())
	}
	return result
}

func addAppearanceDevice(t *testing.T, server *Server, user User, index int, status string) {
	t.Helper()
	_, err := server.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(?,?,?,?,?,?,?)",
		index, user.ID, fmt.Sprint(index), fmt.Sprintf("100.64.0.%d", index), "Test Mac", status, server.now().Unix())
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeviceAppearanceIsOwnerScopedAndRequiresAnOwnedDevice(t *testing.T) {
	server, now := newTestServer(t)
	owner, user, _, _ := registerBrowser(t, server, *now, "appearance-owner@example.com")
	other, admin, _, _ := registerBrowser(t, server, *now, "appearance-other@example.com")
	addAppearanceDevice(t, server, user, 1, "active")
	addAppearanceDevice(t, server, user, 2, "revoked")
	update := map[string]any{"id": "m1", "appearance": map[string]string{"icon": "text", "text": "aB04", "color": "violet"}}
	readAppearanceSettings(t, owner.request("PATCH", "/api/settings", update))
	if result := readAppearanceSettings(t, owner.request("GET", "/api/settings", nil)); result.DeviceAppearance["m1"]["text"] != "aB04" {
		t.Fatalf("saved appearance lost: %+v", result)
	}
	if result := readAppearanceSettings(t, other.request("GET", "/api/settings", nil)); len(result.DeviceAppearance) != 0 {
		t.Fatalf("cross-user appearance leak: %+v", result)
	}
	if err := server.SetAdmin(admin.Email); err != nil {
		t.Fatal(err)
	}
	if response := other.request("PATCH", "/api/settings", update); response.Code != 404 {
		t.Fatalf("admin changed another user's appearance: %d", response.Code)
	}
	for _, id := range []string{"m2", "m999"} {
		update["id"] = id
		if response := owner.request("PATCH", "/api/settings", update); response.Code != 404 {
			t.Fatalf("unavailable device %s accepted: %d", id, response.Code)
		}
	}
	if _, err := server.db.Exec("UPDATE devices SET status='revoked' WHERE device_index=1"); err != nil {
		t.Fatal(err)
	}
	if result := readAppearanceSettings(t, owner.request("GET", "/api/settings", nil)); len(result.DeviceAppearance) != 0 {
		t.Fatalf("revoked device appearance remains visible: %+v", result)
	}
}

func TestDeviceAppearancePersistsWithoutReplacingChatSettingsOrOtherDevices(t *testing.T) {
	server, now := newTestServer(t)
	owner, user, _, _ := registerBrowser(t, server, *now, "appearance-settings@example.com")
	addAppearanceDevice(t, server, user, 1, "active")
	addAppearanceDevice(t, server, user, 2, "active")
	if response := owner.request("POST", "/api/settings", map[string]int{"chatCacheMaxSessions": 9}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	patch := func(id, icon, text, color string, ifAbsent bool) appearanceSettingsResponse {
		return readAppearanceSettings(t, owner.request("PATCH", "/api/settings", map[string]any{"id": id,
			"appearance": map[string]string{"icon": icon, "text": text, "color": color}, "ifAbsent": ifAbsent}))
	}
	patch("m1", "text", "aB04", "violet", false)
	patch("m2", "laptop", "ignored", "teal", false)
	result := patch("m1", "mini", "", "coral", true)
	if result.ChatCacheMaxSessions != 9 || result.DeviceAppearance["m1"]["text"] != "aB04" || result.DeviceAppearance["m2"]["text"] != "" {
		t.Fatalf("PATCH lost settings, overwrote migration, or retained SVG text: %+v", result)
	}
	result = readAppearanceSettings(t, owner.request("POST", "/api/settings", map[string]int{"chatCacheMaxSessions": 3}))
	if result.ChatCacheMaxSessions != 3 || len(result.DeviceAppearance) != 2 {
		t.Fatalf("POST replaced appearances: %+v", result)
	}
	result = patch("m1", "monitor", "", "steel", false)
	if result.DeviceAppearance["m1"]["icon"] != "monitor" || result.DeviceAppearance["m1"]["text"] != "" {
		t.Fatalf("reset did not clear text: %+v", result)
	}
	var raw string
	if err := server.db.QueryRow("SELECT value FROM preferences WHERE user_id=?", user.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var persisted appearanceSettingsResponse
	if err := json.Unmarshal([]byte(raw), &persisted); err != nil || persisted.DeviceAppearance["m2"]["color"] != "teal" {
		t.Fatalf("appearance not stored in account preferences: %v %s", err, raw)
	}
}

func TestDeviceAppearanceRejectsInvalidInputAndCorruptStoredPreferences(t *testing.T) {
	server, now := newTestServer(t)
	owner, user, _, _ := registerBrowser(t, server, *now, "appearance-invalid@example.com")
	addAppearanceDevice(t, server, user, 1, "active")
	for _, input := range []map[string]any{
		{"id": "__proto__", "appearance": map[string]string{"icon": "monitor", "color": "steel"}},
		{"id": "m1"},
		{"id": "m1", "appearance": map[string]string{"icon": "text", "text": "ABCDE", "color": "steel"}},
		{"id": "m1", "appearance": map[string]string{"icon": "monitor", "color": "url(secret)"}},
	} {
		if response := owner.request("PATCH", "/api/settings", input); response.Code != 400 {
			t.Fatalf("invalid appearance accepted: %d", response.Code)
		}
	}
	if _, err := server.db.Exec("INSERT INTO preferences(user_id,value) VALUES(?,?)", user.ID, "broken-json"); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST", "PATCH"} {
		body := any(map[string]int{"chatCacheMaxSessions": 2})
		if method == "PATCH" {
			body = map[string]any{"id": "m1", "appearance": map[string]string{"icon": "monitor", "color": "steel"}}
		}
		if response := owner.request(method, "/api/settings", body); response.Code != 500 {
			t.Fatalf("corrupt preferences silently replaced by %s: %d", method, response.Code)
		}
	}
}
