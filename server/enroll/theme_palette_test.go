package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const paletteTestA = `{"light":{"canvas":"#eeeeee","accent":"#145da0","highlight":"#ffee11","text":"#18211f"},"dark":{"canvas":"#222222","accent":"#78b59d","highlight":"#e0b84f","text":"#f4f7f6"}}`
const paletteTestB = `{"light":{"canvas":"#ffffff","accent":"#246810","highlight":"#aabbcc","text":"#112233"},"dark":{"canvas":"#000000","accent":"#123456","highlight":"#abcdef","text":"#ffffff"}}`

func paletteRead(t *testing.T, method, body string) json.RawMessage {
	t.Helper()
	r := appearanceRequest(method, body)
	if r.Code != 200 {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(r.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data["palette"] == nil {
		t.Fatal("response missing palette")
	}
	return data["palette"]
}

func TestThemePaletteRoundTripAndIndependentSettings(t *testing.T) {
	appearanceTestFiles(t)
	if got := string(paletteRead(t, http.MethodGet, "")); got != "null" {
		t.Fatalf("unset=%s", got)
	}
	first := paletteRead(t, http.MethodPut, `{"palette":`+paletteTestA+`}`)
	if !strings.Contains(string(first), "#EEEEEE") || !strings.Contains(string(first), "#145DA0") {
		t.Fatalf("normalization=%s", first)
	}
	if _, err := os.ReadFile(themePaletteFile); err != nil {
		t.Fatal(err)
	}
	appearanceResponse(t, appearanceRequest(http.MethodPatch, `{"id":"m1","appearance":{"icon":"mini","color":"teal"}}`))
	appearanceRequest(http.MethodPost, `{"chatCacheMaxSessions":9}`)
	if got := paletteRead(t, http.MethodGet, ""); string(got) != string(first) {
		t.Fatalf("other settings replaced palette: %s", got)
	}
	if got := paletteRead(t, http.MethodPut, `{"palette":`+paletteTestB+`,"ifAbsent":true}`); string(got) != string(first) {
		t.Fatal("legacy cache replaced saved palette")
	}
	if got := paletteRead(t, http.MethodPut, `{"palette":`+paletteTestB+`}`); string(got) == string(first) {
		t.Fatal("explicit save was ignored")
	}
}

func TestThemePaletteRejectsBadInputAndStorageFailures(t *testing.T) {
	appearanceTestFiles(t)
	for _, body := range []string{`{}`, `{"palette":null}`, `{"palette":{"light":{"canvas":"#123456"}}}`, `{"palette":` + strings.Replace(paletteTestA, "#eeeeee", "url(private)", 1) + `}`} {
		if r := appearanceRequest(http.MethodPut, body); r.Code != 400 {
			t.Errorf("invalid input status=%d", r.Code)
		}
	}
	if err := os.WriteFile(themePaletteFile, []byte("bad json"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := appearanceRequest(http.MethodGet, ""); r.Code != 500 {
		t.Errorf("corrupt read status=%d", r.Code)
	}
	if r := appearanceRequest(http.MethodPut, `{"palette":`+paletteTestA+`}`); r.Code != 500 {
		t.Errorf("corrupt save status=%d", r.Code)
	}
	themePaletteFile = filepath.Join(t.TempDir(), "absent", "palette.json")
	if r := appearanceRequest(http.MethodPut, `{"palette":`+paletteTestA+`}`); r.Code != 500 {
		t.Errorf("write failure status=%d", r.Code)
	}
}

func TestThemePaletteConcurrentMigrationKeepsOneCompletePalette(t *testing.T) {
	appearanceTestFiles(t)
	var wg sync.WaitGroup
	for _, p := range []string{paletteTestA, paletteTestB} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			if r := appearanceRequest(http.MethodPut, `{"palette":`+p+`,"ifAbsent":true}`); r.Code != 200 {
				t.Errorf("migration status=%d", r.Code)
			}
		}(p)
	}
	wg.Wait()
	got := string(paletteRead(t, http.MethodGet, ""))
	var actual map[string]map[string]string
	if err := json.Unmarshal([]byte(got), &actual); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{paletteTestA, paletteTestB} {
		var expected map[string]map[string]string
		json.Unmarshal([]byte(candidate), &expected)
		for _, colors := range expected {
			for key, value := range colors {
				colors[key] = strings.ToUpper(value)
			}
		}
		if reflect.DeepEqual(actual, expected) {
			return
		}
	}
	t.Fatalf("migration mixed palette values: %s", got)
}
