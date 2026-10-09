package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func appearanceTestFiles(t *testing.T) {
	t.Helper()
	previousAppearance, previousSettings := deviceAppearanceFile, settingsFile
	dir := t.TempDir()
	deviceAppearanceFile = filepath.Join(dir, "device-appearance.json")
	settingsFile = filepath.Join(dir, "settings.json")
	t.Cleanup(func() { deviceAppearanceFile, settingsFile = previousAppearance, previousSettings })
}

func appearanceRequest(method, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	handleSettings(rr, httptest.NewRequest(method, "/settings", strings.NewReader(body)))
	return rr
}

func appearanceResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]map[string]string {
	t.Helper()
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var response struct {
		DeviceAppearance map[string]map[string]string `json:"deviceAppearance"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.DeviceAppearance == nil {
		t.Fatal("settings response must include deviceAppearance")
	}
	return response.DeviceAppearance
}

func TestDeviceAppearanceSettingsPersistsAndPreservesOtherSettings(t *testing.T) {
	appearanceTestFiles(t)
	if got := appearanceResponse(t, appearanceRequest(http.MethodGet, "")); len(got) != 0 {
		t.Fatalf("empty store: %v", got)
	}
	appearanceResponse(t, appearanceRequest(http.MethodPatch, `{"id":"m1","appearance":{"icon":"text","text":"aB04","color":"violet"}}`))
	appearanceResponse(t, appearanceRequest(http.MethodPatch, `{"id":"m2","appearance":{"icon":"laptop","color":"teal"}}`))
	got := appearanceResponse(t, appearanceRequest(http.MethodGet, ""))
	if got["m1"]["text"] != "aB04" || got["m1"]["color"] != "violet" || got["m2"]["icon"] != "laptop" {
		t.Fatalf("round trip: %v", got)
	}
	if data, err := os.ReadFile(deviceAppearanceFile); err != nil || !json.Valid(data) {
		t.Fatalf("persisted JSON: %s %v", data, err)
	}
	if rr := appearanceRequest(http.MethodPost, `{"chatCacheMaxSessions":9}`); rr.Code != 200 {
		t.Fatalf("legacy settings save: %d %s", rr.Code, rr.Body.String())
	}
	got = appearanceResponse(t, appearanceRequest(http.MethodGet, ""))
	if got["m1"]["text"] != "aB04" || loadSettings().ChatCacheMaxSessions != 9 {
		t.Fatalf("legacy POST must preserve device appearance: %v", got)
	}
	appearanceResponse(t, appearanceRequest(http.MethodPatch, `{"id":"m1","appearance":{"icon":"monitor","color":"steel"}}`))
	got = appearanceResponse(t, appearanceRequest(http.MethodGet, ""))
	if got["m1"]["icon"] != "monitor" || got["m1"]["text"] != "" {
		t.Fatalf("restore default must clear text: %v", got)
	}
}

func TestDeviceAppearanceMigrationNeverOverwritesServerPreference(t *testing.T) {
	appearanceTestFiles(t)
	appearanceResponse(t, appearanceRequest(http.MethodPatch, `{"id":"m1","appearance":{"icon":"text","text":"A","color":"violet"}}`))
	got := appearanceResponse(t, appearanceRequest(http.MethodPatch, `{"id":"m1","appearance":{"icon":"mini","color":"coral"},"ifAbsent":true}`))
	if got["m1"]["text"] != "A" {
		t.Fatalf("stale local preference replaced server value: %v", got)
	}
	got = appearanceResponse(t, appearanceRequest(http.MethodPatch, `{"id":"m2","appearance":{"icon":"text","text":"0009","color":"teal"},"ifAbsent":true}`))
	if got["m2"]["text"] != "0009" {
		t.Fatalf("missing preference was not imported: %v", got)
	}
}

func TestDeviceAppearanceRejectsInvalidInputAndWriteFailure(t *testing.T) {
	appearanceTestFiles(t)
	for _, body := range []string{
		`{"id":"m0","appearance":{"icon":"mini","color":"teal"}}`,
		`{"id":"m1"}`,
		`{"id":"m1","appearance":{"icon":"html","color":"teal"}}`,
		`{"id":"m1","appearance":{"icon":"mini","color":"url(secret)"}}`,
		`{"id":"m1","appearance":{"icon":"text","text":"","color":"teal"}}`,
		`{"id":"m1","appearance":{"icon":"text","text":"ABCDE","color":"teal"}}`,
		`{"id":"m1","appearance":{"icon":"text","text":"中","color":"teal"}}`,
		`{"id":"m1","appearance":{"icon":"text","text":"A 1","color":"teal"}}`,
	} {
		if rr := appearanceRequest(http.MethodPatch, body); rr.Code != 400 {
			t.Fatalf("invalid input %s: %d %s", body, rr.Code, rr.Body.String())
		}
	}
	deviceAppearanceFile = filepath.Join(t.TempDir(), "missing", "appearance.json")
	if rr := appearanceRequest(http.MethodPatch, `{"id":"m1","appearance":{"icon":"mini","color":"teal"}}`); rr.Code != 500 {
		t.Fatalf("write failure must not report success: %d %s", rr.Code, rr.Body.String())
	}
}

func TestDeviceAppearanceConcurrentUpdatesDoNotLoseOtherDevices(t *testing.T) {
	appearanceTestFiles(t)
	var wg sync.WaitGroup
	for _, id := range []string{"m1", "m2", "m3", "m4", "m5"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			body := `{"id":"` + id + `","appearance":{"icon":"text","text":"` + id + `","color":"teal"}}`
			if rr := appearanceRequest(http.MethodPatch, body); rr.Code != 200 {
				t.Errorf("concurrent update: %d %s", rr.Code, rr.Body.String())
			}
		}(id)
	}
	wg.Wait()
	if got := appearanceResponse(t, appearanceRequest(http.MethodGet, "")); len(got) != 5 {
		t.Fatalf("lost updates: %v", got)
	}
}
