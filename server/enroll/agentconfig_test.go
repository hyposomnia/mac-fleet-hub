package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentConfigReturnsIdleSec(t *testing.T) {
	// settingsFile 不存在 → loadSettings 回退默认 AutoCloseMinutes=30 → idleSec=1800
	settingsFile = t.TempDir() + "/none.json"
	r := httptest.NewRequest(http.MethodGet, "/agent-config", nil)
	w := httptest.NewRecorder()
	handleAgentConfig(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d want 200", w.Code)
	}
	var got struct {
		IdleSec int64 `json:"idleSec"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got.IdleSec != 1800 {
		t.Fatalf("idleSec=%d want 1800（默认 30min）", got.IdleSec)
	}
}

func TestAgentConfigRejectsNonGet(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/agent-config", nil)
	w := httptest.NewRecorder()
	handleAgentConfig(w, r)
	if w.Code != 405 {
		t.Fatalf("POST status=%d want 405", w.Code)
	}
}
