//go:build preview_uat

package multiuser

import (
	"net/http"
	"net/url"
	"path"
	"testing"
)

// Test-only login is bound to this loopback fixture, never compiled into Fleet.
func TestNetworkPreviewBrowserServer(t *testing.T) {
	server, now := newTestServer(t)
	browser, user, _, _ := registerBrowser(t, server, *now, "network-preview-uat@example.test")
	server.options.Origin = "http://127.0.0.1:7197"
	server.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(1,?,'fixture-node','100.64.0.9','隔离验收主机','active',?)", user.ID, now.Unix())
	if _, _, err := server.issueDeviceCredentials(1); err != nil {
		t.Fatal(err)
	}
	server.options.ProxyURL = func(Device, string) string { return "http://127.0.0.1:7198" }
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/uat/login" {
			http.SetCookie(w, browser.cookie)
			http.Redirect(w, r, "/host-view?mac=m1&url="+url.QueryEscape("http://design.internal:8788/"), 303)
			return
		}
		_, bindingErr := r.Cookie("fleet_preview")
		t.Logf("request=%s %s binding=%v origin=%q", r.Method, path.Base(r.URL.Path), bindingErr == nil, r.Header.Get("Origin"))
		server.ServeHTTP(w, r)
	})
	t.Log("Browser harness: http://127.0.0.1:7197/uat/login (isolated fixture)")
	t.Fatal(http.ListenAndServe("127.0.0.1:7197", handler))
}
