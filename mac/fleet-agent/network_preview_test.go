package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const previewTestPrefix = "/network-preview/abcdefghijklmnopqrstuvwxyz0123456789/"

func previewTestRequest(method, target, root string, body io.Reader) *http.Request {
	u, _ := url.Parse(target)
	r := httptest.NewRequest(method, networkPreviewAPI+base64.RawURLEncoding.EncodeToString([]byte(previewOriginString(u)))+u.EscapedPath()+"?"+u.RawQuery, body)
	r.Header.Set("X-Macfleet-Preview-Origin", root)
	r.Header.Set("X-Macfleet-Preview-Gateway", "https://fleet.example.test")
	r.Header.Set("X-Macfleet-Preview-Prefix", previewTestPrefix)
	r.Header.Set("X-Macfleet-Preview-ID", strings.Repeat("a", 64))
	return r
}

func TestNetworkPreviewUsesHostDNSAndRoutes(t *testing.T) {
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Host+r.URL.RequestURI())
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Fleet-Device-Token") != "" || r.Header.Get("X-Macfleet-Preview-ID") != "" || r.Header.Get("X-Forwarded-For") != "" {
			t.Error("Fleet credentials or headers leaked")
		}
		if r.Header.Get("Authorization") != "Bearer app-session" {
			t.Error("application authorization lost")
		}
		if r.Header.Get("X-CSRF-Token") != "app-csrf" {
			t.Error("application CSRF header lost")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"reachable":true}`)
	}))
	defer upstream.Close()
	address := upstream.Listener.Addr().String()
	p := newNetworkPreviewProxy().(*networkPreviewProxy)
	p.transport.Proxy = nil
	p.transport.DialContext = func(ctx context.Context, network, destination string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	for _, origin := range []string{"http://127.2.3.4:8788", "http://localhost:8788", "http://192.168.33.7:8080", "http://10.44.5.6", "http://172.31.4.2", "http://[fd12::7]:8080", "http://buildbox.local:3000", "http://intranet.corp", "http://vpn-only.example.test"} {
		r := previewTestRequest("GET", origin+"/a%2Fb?q=x%2Fy", origin, nil)
		r.Header.Set("Cookie", "fleet_session=secret")
		r.Header.Set("X-Fleet-Device-Token", "private")
		r.Header.Set("Authorization", "Bearer app-session")
		r.Header.Set("X-CSRF-Token", "app-csrf")
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != `{"reachable":true}` {
			t.Fatalf("%s: %d %s", origin, w.Code, w.Body.String())
		}
		if got := seen[len(seen)-1]; !strings.Contains(got, "/a%2Fb?q=x%2Fy") {
			t.Fatalf("escaped path changed: %s", got)
		}
	}
}

func TestNetworkPreviewCookiesCORSRedirectsAndWrites(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "app", Value: "signed-in", Path: "/"})
			w.Header().Set("Location", "/dashboard?q=1")
			w.WriteHeader(303)
		case "/dashboard":
			c, e := r.Cookie("app")
			if e != nil || c.Value != "signed-in" {
				t.Error("upstream cookie lost")
			}
			io.WriteString(w, "signed in")
		case "/write":
			data, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || string(data) != "payload" {
				t.Error("method/body lost")
			}
			w.WriteHeader(201)
		case "/cors":
			w.Header().Set("Access-Control-Allow-Origin", "http://root.internal")
			io.WriteString(w, "allowed anonymously")
		default:
			io.WriteString(w, "private")
		}
	}))
	defer upstream.Close()
	p := newNetworkPreviewProxy().(*networkPreviewProxy)
	for _, path := range []string{"/login", "/dashboard", "/write"} {
		method := "GET"
		if path == "/write" {
			method = "POST"
		}
		w := httptest.NewRecorder()
		p.ServeHTTP(w, previewTestRequest(method, upstream.URL+path, upstream.URL, strings.NewReader("payload")))
		if w.Code >= 400 {
			t.Fatalf("%s: %d", path, w.Code)
		}
		if len(w.Result().Cookies()) != 0 {
			t.Error("upstream cookies escaped to Fleet origin")
		}
		if path == "/login" && !strings.HasPrefix(w.Header().Get("Location"), previewTestPrefix) {
			t.Error("redirect escaped host proxy")
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "null" {
			t.Error("opaque frame cannot read virtual same-origin response")
		}
	}
	for _, tc := range []struct{ path, credentials, want string }{{"/private", "", ""}, {"/cors", "", "null"}, {"/cors", "include", ""}} {
		r := previewTestRequest("GET", upstream.URL+tc.path, "http://root.internal", nil)
		r.Header.Set("X-Macfleet-Preview-Credentials", tc.credentials)
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != tc.want {
			t.Fatalf("cross-origin %s/%s CORS=%q want %q", tc.path, tc.credentials, got, tc.want)
		}
	}
	// A separate launch never inherits the first preview's app login.
	r := previewTestRequest("GET", upstream.URL+"/private", upstream.URL, nil)
	r.Header.Set("X-Macfleet-Preview-ID", strings.Repeat("b", 64))
	if len(p.jar(strings.Repeat("b", 64)).Cookies(mustPreviewURL(t, upstream.URL))) != 0 {
		t.Error("cookie jars shared across previews")
	}
}

func mustPreviewURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, e := url.Parse(raw)
	if e != nil {
		t.Fatal(e)
	}
	return u
}

func TestNetworkPreviewRewritesDocumentAndResources(t *testing.T) {
	base := mustPreviewURL(t, "http://host.internal:8788/docs/index.html")
	gate := mustPreviewURL(t, "https://fleet.example.test")
	body := []byte(`<!doctype html><html><head><base href="/project/"><meta http-equiv="Content-Security-Policy" content="default-src 'none'"><link rel="stylesheet" href="/styles/app.css" integrity="bad"></head><body><img src="../image.png"><form action="/save"></form><a href="http://lan.internal/report" target="_blank">LAN</a><script type="module">import '/src/main.js'; fetch('/api/info')</script><style>.hero{background:url('../hero.png')}</style></body></html>`)
	got, err := rewriteNetworkHTML(body, base, base, gate, previewTestPrefix)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"networkPreview", previewTestPrefix, "/src/main.js", "/styles/app.css", "/host-view?mac=", "target=\"_top\""} {
		if want == "networkPreview" {
			continue
		}
		if !strings.Contains(string(got), want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	if strings.Contains(string(got), `href="/project/"`) || strings.Contains(string(got), `integrity="bad"`) {
		t.Error("base or integrity blocks rewritten resources")
	}
	if strings.Contains(string(got), `target="_blank"`) || strings.Count(string(got), `target="_top"`) != 1 {
		t.Error("cross-origin navigation has conflicting targets")
	}
	if strings.Index(string(got), "const gate=") > strings.Index(string(got), "/src/main.js") {
		t.Error("application executes before adapter")
	}
	css := rewriteNetworkCSS(`@import "./other.css";div{background:url('/img.png')}`, func(raw string) string { return previewRewriteURL(raw, base, previewTestPrefix) })
	if strings.Count(css, previewTestPrefix) != 2 {
		t.Fatalf("CSS resources not routed: %s", css)
	}
}

func TestNetworkPreviewWebSocketAndSSE(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return strings.HasPrefix(r.Header.Get("Origin"), "http://") }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: streamed\n\n")
			w.(http.Flusher).Flush()
			return
		}
		c, e := upgrader.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		kind, data, e := c.ReadMessage()
		if e == nil {
			c.WriteMessage(kind, data)
		}
	}))
	defer upstream.Close()
	p := newNetworkPreviewProxy()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := upstream.URL + r.URL.Path
		r2 := previewTestRequest(r.Method, target, upstream.URL, nil)
		r2.Header = r.Header.Clone()
		for key, values := range previewTestRequest("GET", target, upstream.URL, nil).Header {
			r2.Header[key] = values
		}
		p.ServeHTTP(w, r2)
	}))
	defer proxy.Close()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxy.URL, "http")+"/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	c.WriteMessage(websocket.TextMessage, []byte("live"))
	_, data, err := c.ReadMessage()
	if err != nil || string(data) != "live" {
		t.Fatalf("websocket: %q %v", data, err)
	}
	resp, err := http.Get(proxy.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ = io.ReadAll(resp.Body)
	if string(data) != "data: streamed\n\n" {
		t.Fatalf("SSE: %s", data)
	}
}
