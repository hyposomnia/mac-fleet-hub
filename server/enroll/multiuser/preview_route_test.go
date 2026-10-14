package multiuser

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPreviewRouteServesAuthenticatedDashboard(t *testing.T) {
	server, now := newTestServer(t)
	browser, _, _, _ := registerBrowser(t, server, *now, "preview@example.com")
	for _, path := range []string{"/view?mac=m1&path=%2Fprivate%2Ftmp%2Fexample.log", "/view?mac=m2&path=%2FUsers%2Ftest%2Freport.txt&embed=1"} {
		response := browser.request(http.MethodGet, path, nil)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="preview-page"`) {
			t.Fatalf("preview route %s: got %d, want dashboard preview shell", path, response.Code)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("preview route must not cache private content")
		}
	}
	response := browser.request(http.MethodGet, "/view-missing", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unrelated path got %d, want 404", response.Code)
	}
}

func TestPreviewRouteRequiresFullAuthentication(t *testing.T) {
	server, _ := newTestServer(t)
	browser := &testBrowser{server: server}
	path := "/view?mac=m1&path=%2Fprivate%2Ftmp%2Fexample.log"
	for _, pending := range []bool{false, true} {
		if pending {
			response := browser.request(http.MethodPost, "/api/auth/register", map[string]string{"email": "pending-preview@example.com", "password": "correct horse battery", "confirm_password": "correct horse battery"})
			if response.Code != http.StatusOK {
				t.Fatalf("register: %d", response.Code)
			}
		}
		response := browser.request(http.MethodGet, path, nil)
		target, err := url.Parse(response.Header().Get("Location"))
		if err != nil || response.Code != http.StatusSeeOther || target.Path != "/auth" || target.Query().Get("next") != path {
			t.Fatalf("pending=%v: got %d location %q, want login preserving preview URL", pending, response.Code, response.Header().Get("Location"))
		}
	}
}

func TestPreviewTrailingSlashPreservesQueryAtCanonicalRoute(t *testing.T) {
	server, now := newTestServer(t)
	browser, _, _, _ := registerBrowser(t, server, *now, "slash-preview@example.com")
	path := "/view/?mac=m2&path=%2Fprivate%2Ftmp%2Freport.log&embed=1"
	response := browser.request(http.MethodGet, path, nil)
	if response.Code != http.StatusTemporaryRedirect || response.Header().Get("Location") != strings.Replace(path, "/view/", "/view", 1) {
		t.Fatalf("got %d location %q, want canonical preview URL for relative assets", response.Code, response.Header().Get("Location"))
	}
}
