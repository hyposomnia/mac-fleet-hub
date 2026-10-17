package multiuser

import (
	"encoding/base64"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestNetworkPreviewOwnershipSandboxAndRevocation(t *testing.T) {
	server, now := newTestServer(t)
	browser, user, _, _ := registerBrowser(t, server, *now, "preview-owner@example.test")
	other, _, _, _ := registerBrowser(t, server, *now, "other-preview@example.test")
	_, err := server.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(1,?,'node','100.64.0.9','Host','active',?)", user.ID, now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	_, credential, err := server.issueDeviceCredentials(1)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-CSRF-Token") != "" || r.Header.Get("X-Fleet-Device-Token") != credential {
			t.Error("credentials crossed preview boundary")
		}
		if r.Header.Get("X-Macfleet-Preview-Origin") != "http://vpn.internal:8080" || !strings.HasPrefix(r.Header.Get("X-Macfleet-Preview-Prefix"), networkPreviewPrefix) {
			t.Error("missing trusted preview context")
		}
		if !strings.Contains(r.URL.EscapedPath(), "/a%2Fb") {
			t.Errorf("escaped path lost: %s", r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Set-Cookie", "attack=x; Path=/")
		w.Header().Set("Content-Security-Policy", "default-src *")
		io.WriteString(w, "preview")
	}))
	defer upstream.Close()
	server.options.ProxyURL = func(Device, string) string { return upstream.URL }
	launch := "/host-view?mac=m1&url=" + url.QueryEscape("http://vpn.internal:8080/a%2Fb?q=x%2Fy#section")
	if result := other.request("GET", launch, nil); result.Code != 404 {
		t.Fatalf("cross-owner launch %d", result.Code)
	}
	loginHash := digest(browser.cookie.Value)
	result := browser.request("GET", launch, nil)
	if result.Code != 200 {
		t.Fatalf("launch %d %s", result.Code, result.Body.String())
	}
	match := regexp.MustCompile(`<iframe[^>]+src="([^"]+)"`).FindStringSubmatch(result.Body.String())
	if len(match) != 2 {
		t.Fatal(result.Body.String())
	}
	var binding *http.Cookie
	for _, cookie := range result.Result().Cookies() {
		if cookie.Name == "fleet_preview" {
			binding = cookie
		}
	}
	if binding == nil || !binding.HttpOnly || !binding.Secure {
		t.Fatal("missing secure browser binding")
	}
	path := html.UnescapeString(match[1])
	path = strings.Split(path, "#")[0]
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Origin", "null")
		r.AddCookie(binding)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	missing := httptest.NewRecorder()
	server.ServeHTTP(missing, httptest.NewRequest("GET", path, nil))
	if missing.Code != 401 {
		t.Fatalf("unbound capability usable: %d", missing.Code)
	}
	result = request(path)
	if result.Code != 200 || result.Body.String() != "preview" {
		t.Fatalf("capability access: %d %s", result.Code, result.Body.String())
	}
	csp := result.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox allow-scripts") || strings.Contains(csp, "allow-same-origin") {
		t.Fatalf("unsafe document CSP: %s", csp)
	}
	if len(result.Result().Cookies()) != 0 || result.Header().Get("Cache-Control") != "no-store" || result.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("cookie/cache/referrer leak: cookies=%v cache=%v referrer=%v", result.Result().Cookies(), result.Header().Values("Cache-Control"), result.Header().Values("Referrer-Policy"))
	}
	if result = request(strings.Replace(path, networkPreviewPrefix, networkPreviewPrefix+"invalid", 1)); result.Code != 401 {
		t.Fatalf("forged capability %d", result.Code)
	}
	// Cross-origin redirects may navigate a document, but cannot enlarge the
	// old page's fetch permissions. A document gets a distinct capability.
	segments := strings.SplitN(strings.TrimPrefix(path, networkPreviewPrefix), "/", 3)
	nextPath := networkPreviewPrefix + segments[0] + "/" + base64.RawURLEncoding.EncodeToString([]byte("https://other.internal")) + "/next"
	navigation := httptest.NewRequest("GET", nextPath, nil)
	navigation.AddCookie(binding)
	navigation.Header.Set("Sec-Fetch-Dest", "iframe")
	nav := httptest.NewRecorder()
	server.ServeHTTP(nav, navigation)
	if nav.Code != 303 || strings.Contains(nav.Header().Get("Location"), segments[0]) {
		t.Fatalf("document origin was not isolated: %d", nav.Code)
	}
	var originalOrigin string
	server.db.QueryRow("SELECT origin FROM network_previews WHERE token_hash=?", digest(segments[0])).Scan(&originalOrigin)
	if originalOrigin != "http://vpn.internal:8080" {
		t.Fatal("navigation broadened existing origin")
	}
	server.db.Exec("UPDATE devices SET node_id='replacement' WHERE device_index=1")
	if result = request(path); result.Code != 404 {
		t.Fatalf("node replacement left preview usable: %d", result.Code)
	}
	server.db.Exec("UPDATE devices SET node_id='node' WHERE device_index=1")
	server.db.Exec("UPDATE users SET status='disabled' WHERE id=?", user.ID)
	if result = request(path); result.Code != 401 {
		t.Fatalf("disabled user retained preview access: %d", result.Code)
	}
	server.db.Exec("UPDATE users SET status='active' WHERE id=?", user.ID)
	// These requests carry no account cookie, but remain tied to the original login.
	server.db.Exec("DELETE FROM sessions WHERE token_hash=?", loginHash)
	if result = request(path); result.Code != 401 {
		t.Fatalf("logout left preview usable: %d", result.Code)
	}
}

func TestNetworkPreviewRejectsUnsafeLaunchAddresses(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "javascript:alert(1)", "http://user:password@host/", "ftp://internal/", "//internal/path", "http://host\\evil/", "http://"} {
		if _, err := previewURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"http://[::1]:8788/", "http://[fd00::123]/", "http://10.7.8.9:3000/", "https://vpn.example.test/"} {
		if _, err := previewURL(raw); err != nil {
			t.Errorf("rejected %q: %v", raw, err)
		}
	}
}
