package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfigURLDerivation(t *testing.T) {
	t.Setenv("FLEET_CONFIG_URL", "https://gw.example.com/enroll/agent-config")
	if got := configURL(); got != "https://gw.example.com/enroll/agent-config" {
		t.Fatalf("configURL()=%q", got)
	}
	// 未配置 → 空（configSync 关闭，沿用本地默认）
	t.Setenv("FLEET_CONFIG_URL", "  ")
	if got := configURL(); got != "" {
		t.Fatalf("未配置应空，得到 %q", got)
	}
}

func TestFetchIdleSec(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"idleSec": 600}`))
	}))
	defer srv.Close()
	got, err := fetchIdleSec(srv.URL)
	if err != nil || got != 600 {
		t.Fatalf("fetchIdleSec=%d err=%v want 600", got, err)
	}
}

func TestFetchIdleSecRejectsBadValues(t *testing.T) {
	// idleSec<=0：非法 → 报错，调用方保留旧值
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"idleSec": 0}`))
	}))
	defer srv.Close()
	if _, err := fetchIdleSec(srv.URL); err == nil {
		t.Fatalf("idleSec<=0 应报错")
	}

	// 非 200 → 报错
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", 404)
	}))
	defer srv2.Close()
	if _, err := fetchIdleSec(srv2.URL); err == nil {
		t.Fatalf("HTTP 404 应报错")
	}
}
