package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReadyzURLNormalization(t *testing.T) {
	cases := map[string]string{
		"ws://127.0.0.1:47682/rpc": "http://127.0.0.1:47682/readyz",
		"wss://127.0.0.1:47682":    "http://127.0.0.1:47682/readyz",
		"127.0.0.1:47682":          "http://127.0.0.1:47682/readyz",
		"ws://127.0.0.1/rpc":       "http://127.0.0.1:" + sharedEndpointPort() + "/readyz",
		"":                         "",
	}
	for in, want := range cases {
		if got := readyzURL(in); got != want {
			t.Errorf("readyzURL(%q)=%q want %q", in, got, want)
		}
	}
}

func TestProbeReadyz(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if !probeReadyz(ok.URL, time.Second) {
		t.Fatalf("2xx 应判定 ready: %s", ok.URL)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	if probeReadyz(bad.URL, time.Second) {
		t.Fatalf("503 不应判定 ready: %s", bad.URL)
	}

	if probeReadyz("ws://127.0.0.1:1/rpc", 200*time.Millisecond) {
		t.Fatal("无监听端口不应判定 ready")
	}
	if probeReadyz("", time.Second) {
		t.Fatal("空端点不应判定 ready")
	}
}
