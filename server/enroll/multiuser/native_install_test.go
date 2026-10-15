package multiuser

import (
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestNativeInstallPublicCanonicalOrigin(t *testing.T) {
	server, _ := newTestServer(t)
	server.options.Origin = "https://fleet.example.test:9443"
	request := httptest.NewRequest("GET", "https://attacker.test/install.sh", nil)
	request.Header.Set("X-Forwarded-Host", "attacker.test")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("Content-Type") != "text/x-shellscript; charset=utf-8" {
		t.Fatalf("public install: %d %s", response.Code, response.Body.String())
	}
	script := response.Body.String()
	if !strings.Contains(script, "FLEET_INSTALL_ORIGIN='https://fleet.example.test:9443'") || strings.Contains(script, "attacker.test") {
		t.Fatal("installer did not use configured canonical origin")
	}
	command := exec.Command("bash", "-n")
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bash syntax: %v %s", err, output)
	}
	for _, method := range []string{"HEAD", "POST", "PUT", "DELETE"} {
		response = httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(method, "/install.sh", nil))
		if response.Code != 405 || response.Header().Get("Allow") != "GET" {
			t.Fatalf("%s: %d", method, response.Code)
		}
	}
}
