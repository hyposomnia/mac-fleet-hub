package multiuser

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClientCLIAndAgentAgainstRealHTTPServer(t *testing.T) {
	if testing.Short() {
		t.Skip("native process integration")
	}
	work := t.TempDir()
	agentDir, err := filepath.Abs("../../../mac/fleet-agent")
	if err != nil {
		t.Fatal(err)
	}
	cli, helper := filepath.Join(work, "fleet-agent"), filepath.Join(work, "agent-tests")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, arguments := range [][]string{{"build", "-o", cli, "."}, {"test", "-c", "-o", helper, "."}} {
		command := exec.CommandContext(ctx, "go", arguments...)
		command.Dir = agentDir
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build %v: %s", err, output)
		}
	}
	server, _ := newTestServer(t)
	server.options.Now = time.Now
	server.options.Network = &testNetwork{}
	owner, _, _, _ := registerBrowser(t, server, time.Now(), "cli-owner@example.com")
	other, _, _, _ := registerBrowser(t, server, time.Now(), "cli-other@example.com")
	child := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Fleet-Device-Token") != "" {
			t.Error("secret escaped into child service")
		}
		fmt.Fprint(writer, "loopback-child:"+request.URL.Path)
	}))
	defer child.Close()
	_, childPort, _ := net.SplitHostPort(strings.TrimPrefix(child.URL, "http://"))
	server.options.FilesPort, _ = strconv.Atoi(childPort)
	server.options.TerminalPort = server.options.FilesPort
	agentListener, _ := net.Listen("tcp", "127.0.0.1:0")
	agentAddress := agentListener.Addr().String()
	agentListener.Close()
	_, agentPort, _ := net.SplitHostPort(agentAddress)
	server.options.AgentPort, _ = strconv.Atoi(agentPort)
	server.options.ProxyURL = func(Device, string) string { return "http://" + agentAddress }
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	server.options.Origin = "http://" + listener.Addr().String()
	server.options.LoginServer = "http://127.0.0.1:9999"
	httpServer := &http.Server{Handler: server}
	go httpServer.Serve(listener)
	defer httpServer.Close()
	home := filepath.Join(work, "home")
	support := filepath.Join(home, ".macfleet", "support")
	os.MkdirAll(support, 0700)
	for _, name := range []string{"join-device.sh", "setup-mac.sh"} {
		if err := os.WriteFile(filepath.Join(support, name), []byte("#!/bin/bash\nset -eu\n[[ -n \"$MAC_INDEX\" ]]\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if endpoint := os.Getenv("HEADSCALE_UAT_URL"); endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
			t.Fatal("live CLI integration only permits a loopback fixture")
		}
		clientBinary, daemonBinary := os.Getenv("HEADSCALE_UAT_TAILSCALE"), os.Getenv("HEADSCALE_UAT_TAILSCALED")
		if clientBinary == "" || daemonBinary == "" {
			t.Fatal("live CLI integration requires explicit isolated Tailscale binaries")
		}
		key, err := os.ReadFile(os.Getenv("HEADSCALE_UAT_KEY_FILE"))
		if err != nil {
			t.Fatal(err)
		}
		network := NewHeadscale(endpoint, strings.TrimSpace(string(key)), "100.64.0.254", server.options.AgentPort, server.options.FilesPort)
		server.options.Network = network
		server.options.LoginServer = endpoint
		defer network.Reconcile(context.Background(), nil, nil)
		meshWork, err := os.MkdirTemp("/private/tmp", "mf-cli-mesh.")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(meshWork)
		socket := filepath.Join(meshWork, "tailscaled.sock")
		daemon := exec.CommandContext(ctx, daemonBinary, "--tun=userspace-networking", "--state="+filepath.Join(meshWork, "tailscaled.state"), "--socket="+socket, "--port=0")
		daemon.Env = append(os.Environ(), "TS_NO_LOGS_NO_SUPPORT=true", "TS_LOGS_DIR="+meshWork)
		if err := daemon.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { daemon.Process.Kill(); daemon.Wait() }()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(socket); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("isolated socket unavailable")
			}
			time.Sleep(20 * time.Millisecond)
		}
		join := fmt.Sprintf("#!/bin/bash\nset -euo pipefail\n%q --socket=%q up --force-reauth --login-server=\"$LOGIN_SERVER\" --auth-key=\"file:$FLEET_AUTHKEY_FILE\" --hostname=\"mac${MAC_INDEX}\" --accept-dns=false --accept-routes=false\n", clientBinary, socket)
		if err := os.WriteFile(filepath.Join(support, "join-device.sh"), []byte(join), 0700); err != nil {
			t.Fatal(err)
		}
		t.Log("live mode: real Headscale and private userspace Tailscale join; no system daemon or sudo")
	}
	environment := append(os.Environ(), "HOME="+home, "FLEET_BINDING_FILE="+filepath.Join(home, ".macfleet", "binding.json"))
	log, err := os.Create(filepath.Join(work, "login.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	command := exec.CommandContext(ctx, cli, "login", "--no-open", server.options.Origin)
	command.Env = environment
	command.Stdin = strings.NewReader("y\n")
	command.Stdout = log
	command.Stderr = log
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	var code string
	deadline := time.Now().Add(8 * time.Second)
	for code == "" && time.Now().Before(deadline) {
		server.db.QueryRow("SELECT code FROM enrollments LIMIT 1").Scan(&code)
		time.Sleep(20 * time.Millisecond)
	}
	if code == "" {
		t.Fatal("CLI did not start pairing")
	}
	raw := strings.NewReader(fmt.Sprintf(`{"code":%q}`, code))
	confirm, _ := http.NewRequest("POST", server.options.Origin+"/api/enrollment/confirm", raw)
	confirm.Header.Set("Origin", server.options.Origin)
	confirm.Header.Set("X-CSRF-Token", owner.csrf)
	confirm.AddCookie(owner.cookie)
	response, err := http.DefaultClient.Do(confirm)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("confirm", response.StatusCode)
	}
	if err = command.Wait(); err != nil {
		t.Fatal("CLI login failed; private log retained only in test temp directory")
	}
	launch := func() *exec.Cmd {
		process := exec.CommandContext(ctx, helper, "-test.run=^TestDeviceIntegrationProcess$")
		process.Env = append(environment, "FLEET_INTEGRATION_PROCESS=1", "FLEET_INTEGRATION_LISTEN="+agentAddress)
		process.Stdout = log
		process.Stderr = log
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			if response, err := http.Get("http://" + agentAddress + "/api/health"); err == nil {
				response.Body.Close()
				return process
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatal("agent did not start")
		return nil
	}
	agent := launch()
	defer func() { agent.Process.Kill(); agent.Wait() }()
	get := func(path string, browser *testBrowser) (int, string) {
		request, _ := http.NewRequest("GET", server.options.Origin+path, nil)
		request.AddCookie(browser.cookie)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(data)
	}
	for _, path := range []string{"/m1/api/info", "/m1/files/api/resources", "/m1/term/"} {
		if status, _ := get(path, owner); status != 200 {
			t.Fatalf("owned proxy %s: %d", path, status)
		}
		if status, _ := get(path, other); status != 404 {
			t.Fatalf("cross-user proxy %s: %d", path, status)
		}
	}
	response, err = http.Get("http://" + agentAddress + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("direct access allowed")
	}
	agent.Process.Kill()
	agent.Wait()
	agent = launch()
	if status, _ := get("/m1/api/info", owner); status != 200 {
		t.Fatal("restart lost binding")
	}
	request, _ := http.NewRequest("GET", server.options.Origin+"/m1/api/events", nil)
	request.AddCookie(owner.cookie)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if line, err := bufio.NewReader(response.Body).ReadString('\n'); err != nil || line != "data: connected\n" {
		t.Fatal("stream failed")
	}
	logout := exec.CommandContext(ctx, cli, "logout")
	logout.Env = environment
	if output, err := logout.CombinedOutput(); err != nil {
		t.Fatalf("logout %v %s", err, output)
	}
	closed := make(chan struct{})
	go func() { io.Copy(io.Discard, response.Body); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("logout did not close stream")
	}
	if status, _ := get("/m1/api/info", owner); status != 404 {
		t.Fatal("revoked proxy retained access")
	}
	t.Log("real HTTP/SQLite + native CLI + native agent: pairing, loopback proxies, ownership, restart and stream revocation passed; OS setup adapters isolated")
}
