package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestDSHNativeProxyRewritesRootUIAndKeepsCredentialsServerSide(t *testing.T) {
	var seenPath, seenCookie, seenOrigin, seenHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath, seenCookie, seenOrigin, seenHost = r.URL.RequestURI(), r.Header.Get("Cookie"), r.Header.Get("Origin"), r.Host
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Add("Set-Cookie", "dsh-auth-upstream=must-not-leak; Path=/; HttpOnly")
		_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><link rel="preload" href="/plugins/??one"><script>globalThis.__DSH_BOOT__={url:"/plugins/??two"}</script><link rel="icon" href="/dsh-desktop-logo.png"><script src="./assets/app.js"></script></head><body></body></html>`)
	}))
	defer upstream.Close()

	proxy := httptest.NewServer(newDSHNativeProxy("7", func(context.Context) (dshNativeTarget, error) {
		return dshNativeTarget{BaseURL: upstream.URL, Cookie: "dsh-auth-upstream=server-secret"}, nil
	}, nil))
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+dshNativeInternalPrefix, nil)
	req.Header.Set("Cookie", "authelia_session=browser-cookie")
	req.Header.Set("Origin", "https://fleet.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)

	if seenPath != "/" {
		t.Fatalf("upstream path=%q want /", seenPath)
	}
	if seenCookie != "dsh-auth-upstream=server-secret" {
		t.Fatalf("upstream cookie=%q", seenCookie)
	}
	if seenOrigin != upstream.URL || seenHost != strings.TrimPrefix(upstream.URL, "http://") {
		t.Fatalf("upstream trust headers origin=%q host=%q", seenOrigin, seenHost)
	}
	if len(resp.Cookies()) != 0 {
		t.Fatalf("upstream Set-Cookie leaked to browser: %#v", resp.Cookies())
	}
	for _, want := range []string{
		`<base href="/m7/dsh/">`,
		`href="/m7/dsh/plugins/??one"`,
		`url:"/m7/dsh/plugins/??two"`,
		`href="/m7/dsh/dsh-desktop-logo.png"`,
		`__MACFLEET_DSH_PREFIX__`,
		`window.fetch`,
		`window.WebSocket`,
		`u.protocol==="wss:"?"https:"`,
		`window.XMLHttpRequest`,
		`__DSH_FILE_UPLOAD__`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rewritten HTML missing %q\n%s", want, text)
		}
	}
	legacy := string(rewriteDSHNativeHTML([]byte(`<html><head><base href="/"></head></html>`), "/m7/dsh"))
	if strings.Count(legacy, `<base href="/m7/dsh/">`) != 1 {
		t.Fatalf("legacy root base was not replaced exactly once: %s", legacy)
	}
}

func TestDSHNativeProxyRewritesManifestAndPassesAPIRoutes(t *testing.T) {
	var seen string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.RequestURI()
		if r.URL.Path == "/manifest.webmanifest" {
			w.Header().Set("Content-Type", "application/manifest+json")
			_, _ = io.WriteString(w, `{"id":"/","start_url":"/","scope":"/","icons":[{"src":"/dsh-desktop-logo.png"}]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"path":%q}`, r.URL.RequestURI())
	}))
	defer upstream.Close()

	h := newDSHNativeProxy("2", func(context.Context) (dshNativeTarget, error) {
		return dshNativeTarget{BaseURL: upstream.URL, Cookie: "dsh-auth=x"}, nil
	}, nil)
	proxy := httptest.NewServer(h)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + dshNativeInternalPrefix + "manifest.webmanifest")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	for _, want := range []string{`"id":"/m2/dsh/"`, `"start_url":"/m2/dsh/"`, `"scope":"/m2/dsh/"`, `"src":"/m2/dsh/dsh-desktop-logo.png"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("manifest missing %s: %s", want, body)
		}
	}

	resp, err = http.Get(proxy.URL + dshNativeInternalPrefix + "api/session/list?limit=2")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if seen != "/api/session/list?limit=2" {
		t.Fatalf("upstream API route=%q", seen)
	}
}

func TestDSHNativeProxyCarriesWebSocketAtSubpath(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != dshRemoteMuxPath || r.Header.Get("Cookie") != "dsh-auth=ws-secret" {
			http.Error(w, "bad websocket target", http.StatusBadRequest)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		kind, payload, err := conn.ReadMessage()
		if err == nil {
			_ = conn.WriteMessage(kind, payload)
		}
	}))
	defer upstream.Close()

	proxy := httptest.NewServer(newDSHNativeProxy("3", func(context.Context) (dshNativeTarget, error) {
		return dshNativeTarget{BaseURL: upstream.URL, Cookie: "dsh-auth=ws-secret"}, nil
	}, nil))
	defer proxy.Close()

	wsURL := "ws" + strings.TrimPrefix(proxy.URL, "http") + dshNativeInternalPrefix + "api/remote.mux"
	header := http.Header{"Origin": []string{"https://fleet.example.com"}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if resp != nil {
			t.Fatalf("websocket dial: %v (HTTP %d)", err, resp.StatusCode)
		}
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil || string(payload) != "hello" {
		t.Fatalf("websocket echo payload=%q err=%v", payload, err)
	}
}

func TestDSHNativeProxyUnavailableAndRejectsWrongInternalPath(t *testing.T) {
	h := newDSHNativeProxy("1", func(context.Context) (dshNativeTarget, error) {
		return dshNativeTarget{}, errors.New("desktop stopped")
	}, nil)

	for _, tc := range []struct {
		path string
		want int
	}{
		{path: dshNativeInternalPrefix, want: http.StatusServiceUnavailable},
		{path: "/api/info", want: http.StatusNotFound},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.want {
			t.Fatalf("%s status=%d want %d", tc.path, w.Code, tc.want)
		}
	}
}
