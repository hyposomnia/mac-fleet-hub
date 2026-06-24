package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidMacID(t *testing.T) {
	ok := []string{"m1", "m2", "m9", "m10", "m99"}
	bad := []string{"", "m0", "m100", "mac1", "x", "m1;rm -rf", "M1", "m01", "m1 "}
	for _, s := range ok {
		if !validMacID(s) {
			t.Errorf("expected valid: %q", s)
		}
	}
	for _, s := range bad {
		if validMacID(s) {
			t.Errorf("expected invalid: %q", s)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	if got := sanitizeName("  客厅 Mac  "); got != "客厅 Mac" {
		t.Errorf("trim: %q", got)
	}
	if got := sanitizeName("a\nb\tc"); got != "abc" {
		t.Errorf("ctrl strip: %q", got)
	}
	if got := sanitizeName(strings.Repeat("名", 50)); len([]rune(got)) != 40 {
		t.Errorf("truncate to 40 runes, got %d", len([]rune(got)))
	}
	if got := sanitizeName("   "); got != "" {
		t.Errorf("blank → empty, got %q", got)
	}
}

func TestNamesHandler(t *testing.T) {
	namesFile = filepath.Join(t.TempDir(), "names.json")

	// GET 空 → {}
	rr := httptest.NewRecorder()
	handleNames(rr, httptest.NewRequest(http.MethodGet, "/names", nil))
	if rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != "{}" {
		t.Fatalf("空 GET: code=%d body=%q", rr.Code, rr.Body.String())
	}

	// POST 设置 m2
	rr = httptest.NewRecorder()
	handleNames(rr, httptest.NewRequest(http.MethodPost, "/names", strings.NewReader(`{"id":"m2","name":"卧室 Mac"}`)))
	if rr.Code != 200 {
		t.Fatalf("POST 设置: code=%d body=%q", rr.Code, rr.Body.String())
	}

	// GET 取回（并验证落盘）
	rr = httptest.NewRecorder()
	handleNames(rr, httptest.NewRequest(http.MethodGet, "/names", nil))
	var m map[string]string
	json.Unmarshal(rr.Body.Bytes(), &m)
	if m["m2"] != "卧室 Mac" {
		t.Fatalf("期望 m2 已设置，得 %v", m)
	}

	// POST 空名 → 删除该 override
	rr = httptest.NewRecorder()
	handleNames(rr, httptest.NewRequest(http.MethodPost, "/names", strings.NewReader(`{"id":"m2","name":"  "}`)))
	m = nil
	json.Unmarshal(rr.Body.Bytes(), &m)
	if _, ok := m["m2"]; ok {
		t.Fatalf("期望 m2 被删除，得 %v", m)
	}

	// POST 非法 id → 400
	rr = httptest.NewRecorder()
	handleNames(rr, httptest.NewRequest(http.MethodPost, "/names", strings.NewReader(`{"id":"mac1","name":"x"}`)))
	if rr.Code != 400 {
		t.Fatalf("非法 id 期望 400，得 %d", rr.Code)
	}
}
