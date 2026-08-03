package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCodexRPCClientAcceptsResponsesLargerThanScannerLimit(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	c := newCodexRPCClient(clientConn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.run(ctx)

	want := bytes.Repeat([]byte("x"), 17*1024*1024)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		s := bufio.NewScanner(serverConn)
		if !s.Scan() {
			t.Errorf("missing request: %v", s.Err())
			return
		}
		response := make([]byte, 0, len(want)+32)
		response = append(response, `{"id":1,"result":{"text":"`...)
		response = append(response, want...)
		response = append(response, `"}}`...)
		response = append(response, '\n')
		if _, err := serverConn.Write(response); err != nil {
			t.Errorf("write response: %v", err)
		}
	}()

	callCtx, cancelCall := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCall()
	res, err := c.call(callCtx, "thread/read", map[string]string{"threadId": "t"})
	if err != nil {
		t.Fatalf("large response: %v", err)
	}
	var decoded struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(res, &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded.Text) != len(want) {
		t.Fatalf("response text length got %d want %d", len(decoded.Text), len(want))
	}
	<-serverDone
}

func TestCodexRPCClientEOFImmediatelyFailsPendingCalls(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	c := newCodexRPCClient(clientConn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.run(ctx)

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		s := bufio.NewScanner(serverConn)
		if s.Scan() {
			_ = serverConn.Close()
		}
	}()

	callCtx, cancelCall := context.WithTimeout(context.Background(), time.Second)
	defer cancelCall()
	started := time.Now()
	_, err := c.call(callCtx, "thread/read", map[string]string{"threadId": "t"})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("call error got %v, want EOF", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("EOF should fail pending calls immediately, took %s", elapsed)
	}
	<-serverDone
}

func TestCodexRPCClientCallRoutesResponsesAndIncrementsIDs(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	c := newCodexRPCClient(clientConn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.run(ctx)

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		s := bufio.NewScanner(serverConn)
		for wantID := int64(1); wantID <= 2; wantID++ {
			if !s.Scan() {
				t.Errorf("server scan: %v", s.Err())
				return
			}
			var req rpcRequest
			if err := json.Unmarshal(s.Bytes(), &req); err != nil {
				t.Errorf("bad request json: %v", err)
				return
			}
			if req.ID != wantID {
				t.Errorf("request id got %d want %d", req.ID, wantID)
				return
			}
			if req.Method != "thread/read" {
				t.Errorf("method got %q", req.Method)
				return
			}
			_, _ = serverConn.Write([]byte(`{"id":` + string(rune('0'+wantID)) + `,"result":{"ok":true}}` + "\n"))
		}
	}()

	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		res, err := c.call(ctx, "thread/read", map[string]string{"threadId": "t"})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if !strings.Contains(string(res), `"ok":true`) {
			t.Fatalf("result got %s", res)
		}
	}
	<-serverDone
}

func TestCodexRPCClientPreservesStructuredErrors(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	c := newCodexRPCClient(clientConn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.run(ctx)

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		s := bufio.NewScanner(serverConn)
		if !s.Scan() {
			t.Errorf("missing request: %v", s.Err())
			return
		}
		_, _ = serverConn.Write([]byte(`{"id":1,"error":{"code":-32001,"message":"no active turn to steer","data":{"turnId":"turn-1"}}}` + "\n"))
	}()

	_, err := c.call(context.Background(), "turn/steer", map[string]string{"threadId": "thread-1"})
	var callErr *rpcCallError
	if !errors.As(err, &callErr) {
		t.Fatalf("call error got %T %v", err, err)
	}
	if callErr.Method != "turn/steer" || callErr.Code != -32001 || callErr.Message != "no active turn to steer" || !strings.Contains(string(callErr.Data), "turn-1") {
		t.Fatalf("structured error got %+v", callErr)
	}
	<-serverDone
}

func TestDescendantProcessIDsAreLeafFirstAndScoped(t *testing.T) {
	pairs := [][2]int{
		{100, 1},
		{101, 100},
		{102, 101},
		{103, 100},
		{200, 1},
		{201, 200},
	}
	got := descendantProcessIDs(100, pairs)
	if len(got) != 3 {
		t.Fatalf("descendants got %v", got)
	}
	position := map[int]int{}
	for i, pid := range got {
		position[pid] = i
	}
	if _, ok := position[103]; !ok {
		t.Fatalf("direct child missing from %v", got)
	}
	if position[102] >= position[101] {
		t.Fatalf("leaf must precede its parent in %v", got)
	}
	if _, ok := position[200]; ok {
		t.Fatalf("unrelated process included in %v", got)
	}
}

func TestCodexRPCClientDeliversNotificationsAndSkipsMalformedJSON(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	c := newCodexRPCClient(clientConn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.run(ctx)

	_, _ = serverConn.Write([]byte("not json\n"))
	_, _ = serverConn.Write([]byte(`{"method":"item/agentMessage/delta","params":{"delta":"hi"}}` + "\n"))

	select {
	case n := <-c.notifications():
		if n.Method != "item/agentMessage/delta" {
			t.Fatalf("method got %q", n.Method)
		}
		if !strings.Contains(string(n.Params), "hi") {
			t.Fatalf("params got %s", n.Params)
		}
	case <-time.After(time.Second):
		t.Fatal("notification timeout")
	}
}

func TestCodexRPCClientDeliversServerRequestsWithIDs(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	c := newCodexRPCClient(clientConn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.run(ctx)

	_, _ = serverConn.Write([]byte(`{"id":99,"method":"item/commandExecution/requestApproval","params":{"threadId":"t1","turnId":"turn1","itemId":"cmd1"}}` + "\n"))

	select {
	case n := <-c.notifications():
		if string(n.ID) != "99" {
			t.Fatalf("request id got %v", n.ID)
		}
		if n.Method != "item/commandExecution/requestApproval" {
			t.Fatalf("method got %q", n.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("server request timeout")
	}
}

func TestCodexRPCClientInitializeSendsInitializedNotification(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	c := newCodexRPCClient(clientConn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.run(ctx)

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		s := bufio.NewScanner(serverConn)
		if !s.Scan() {
			t.Errorf("missing initialize")
			return
		}
		var initReq rpcRequest
		if err := json.Unmarshal(s.Bytes(), &initReq); err != nil {
			t.Errorf("bad init: %v", err)
			return
		}
		if initReq.Method != "initialize" {
			t.Errorf("method got %q", initReq.Method)
			return
		}
		b, _ := json.Marshal(initReq.Params)
		if !strings.Contains(string(b), "mac_fleet_hub") {
			t.Errorf("clientInfo missing: %s", b)
			return
		}
		_, _ = serverConn.Write([]byte(`{"id":1,"result":{"codexHome":"/tmp/codex","platformFamily":"unix","platformOs":"macos","userAgent":"test"}}` + "\n"))
		if !s.Scan() {
			t.Errorf("missing initialized notification")
			return
		}
		var note rpcNotification
		if err := json.Unmarshal(s.Bytes(), &note); err != nil {
			t.Errorf("bad note: %v", err)
			return
		}
		if note.Method != "initialized" {
			t.Errorf("note method got %q", note.Method)
			return
		}
	}()

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := c.initialize(ctx2, "0.0-test"); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	<-serverDone
}

func TestConnectCodexAppServerSocketUsesWebSocketFrames(t *testing.T) {
	tempDir, err := os.MkdirTemp("/tmp", "mfh-ws-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })
	socketPath := filepath.Join(tempDir, "app-server.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request rpcRequest
			if err := json.Unmarshal(message, &request); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}
			switch request.Method {
			case "initialize":
				err = conn.WriteJSON(map[string]interface{}{
					"id":     request.ID,
					"result": map[string]string{"userAgent": "test-server"},
				})
			case "thread/list":
				err = conn.WriteJSON(map[string]interface{}{
					"id":     request.ID,
					"result": map[string]interface{}{"data": []interface{}{}},
				})
			}
			if err != nil {
				return
			}
		}
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rpc, cleanup, err := connectCodexAppServerSocket(ctx, socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	raw, err := rpc.call(ctx, "thread/list", map[string]int{"limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"data":[]}` {
		t.Fatalf("thread/list result got %s", raw)
	}
}

func TestConnectCodexAppServerSocketLive(t *testing.T) {
	socketPath := os.Getenv("FLEET_TEST_CODEX_SOCKET")
	if socketPath == "" {
		t.Skip("set FLEET_TEST_CODEX_SOCKET to run against a managed daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rpc, cleanup, err := connectCodexAppServerSocket(ctx, socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	raw, err := rpc.call(ctx, "thread/list", map[string]int{"limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("thread/list returned invalid JSON: %q", raw)
	}
}

func installCodexLauncherFakes(t *testing.T, mode, socket string) (*[]string, *[]string, *[]string) {
	t.Helper()
	previousCfg := cfg
	previousStart := startManagedCodexDaemon
	previousConnect := connectCodexProcess
	previousSocket := connectCodexSocket
	t.Cleanup(func() {
		cfg = previousCfg
		startManagedCodexDaemon = previousStart
		connectCodexProcess = previousConnect
		connectCodexSocket = previousSocket
	})

	cfg.CodexBin = "/usr/local/bin/codex-test"
	cfg.CodexHome = "/tmp/codex-home"
	cfg.CodexMode = mode
	cfg.CodexSock = socket
	started := []string{}
	sockets := []string{}
	processes := []string{}
	startManagedCodexDaemon = func(_ context.Context, codexBin string) error {
		started = append(started, codexBin)
		return nil
	}
	connectCodexSocket = func(_ context.Context, socketPath string) (codexRPCConn, func(), error) {
		sockets = append(sockets, socketPath)
		return newFakeRPCConn(), func() {}, nil
	}
	connectCodexProcess = func(_ context.Context, codexBin string, args ...string) (codexRPCConn, func(), error) {
		processes = append(processes, strings.Join(append([]string{codexBin}, args...), " "))
		return newFakeRPCConn(), func() {}, nil
	}
	return &started, &sockets, &processes
}

func TestCodexAppServerAutoReusesExistingSocket(t *testing.T) {
	started, sockets, processes := installCodexLauncherFakes(t, "auto", "")
	_, cleanup, err := connectCodexAppServer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if len(*started) != 0 {
		t.Fatalf("existing socket unexpectedly started daemon: %v", *started)
	}
	want := []string{"/tmp/codex-home/app-server-control/app-server-control.sock"}
	if !reflect.DeepEqual(*sockets, want) {
		t.Fatalf("socket paths got %v want %v", *sockets, want)
	}
	if len(*processes) != 0 {
		t.Fatalf("managed socket started child process: %v", *processes)
	}
}

func TestCodexAppServerStartsManagedDaemonAfterSocketFailure(t *testing.T) {
	started, sockets, processes := installCodexLauncherFakes(t, "auto", "")
	attempts := 0
	connectCodexSocket = func(_ context.Context, socketPath string) (codexRPCConn, func(), error) {
		*sockets = append(*sockets, socketPath)
		attempts++
		if attempts == 1 {
			return nil, nil, errors.New("socket unavailable")
		}
		return newFakeRPCConn(), func() {}, nil
	}

	_, cleanup, err := connectCodexAppServer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if !reflect.DeepEqual(*started, []string{"/usr/local/bin/codex-test"}) {
		t.Fatalf("daemon starts got %v", *started)
	}
	wantSockets := []string{
		"/tmp/codex-home/app-server-control/app-server-control.sock",
		"/tmp/codex-home/app-server-control/app-server-control.sock",
	}
	if !reflect.DeepEqual(*sockets, wantSockets) {
		t.Fatalf("socket attempts got %v want %v", *sockets, wantSockets)
	}
	if len(*processes) != 0 {
		t.Fatalf("managed socket started child process: %v", *processes)
	}
}

func TestStartCodexAppServerDaemonEnablesRemoteControl(t *testing.T) {
	previousRun := runCodexDaemonCommand
	t.Cleanup(func() { runCodexDaemonCommand = previousRun })
	commands := []string{}
	runCodexDaemonCommand = func(_ context.Context, codexBin string, args ...string) ([]byte, error) {
		commands = append(commands, strings.Join(append([]string{codexBin}, args...), " "))
		return nil, nil
	}

	if err := startCodexAppServerDaemon(context.Background(), "/usr/local/bin/codex-test"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/usr/local/bin/codex-test app-server daemon start",
		"/usr/local/bin/codex-test app-server daemon enable-remote-control",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("daemon commands got %v want %v", commands, want)
	}
}

func TestCodexAppServerUsesConfiguredSocket(t *testing.T) {
	_, sockets, processes := installCodexLauncherFakes(t, "daemon", "/tmp/codex.sock")
	_, cleanup, err := connectCodexAppServer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	want := []string{"/tmp/codex.sock"}
	if !reflect.DeepEqual(*sockets, want) {
		t.Fatalf("socket paths got %v want %v", *sockets, want)
	}
	if len(*processes) != 0 {
		t.Fatalf("configured socket started child process: %v", *processes)
	}
}

func TestCodexAppServerAutoFallsBackToStdio(t *testing.T) {
	for _, test := range []struct {
		name        string
		startError  error
		socketError error
	}{
		{name: "daemon start failure", startError: errors.New("daemon unavailable")},
		{name: "socket failure", socketError: errors.New("socket unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			started, sockets, processes := installCodexLauncherFakes(t, "auto", "")
			startManagedCodexDaemon = func(_ context.Context, codexBin string) error {
				*started = append(*started, codexBin)
				return test.startError
			}
			connectCodexSocket = func(_ context.Context, socketPath string) (codexRPCConn, func(), error) {
				*sockets = append(*sockets, socketPath)
				if test.socketError != nil {
					return nil, nil, test.socketError
				}
				return nil, nil, errors.New("socket unavailable before daemon start")
			}
			connectCodexProcess = func(_ context.Context, codexBin string, args ...string) (codexRPCConn, func(), error) {
				*processes = append(*processes, strings.Join(append([]string{codexBin}, args...), " "))
				return newFakeRPCConn(), func() {}, nil
			}

			_, cleanup, err := connectCodexAppServer(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			cleanup()
			if got := (*processes)[len(*processes)-1]; got != "/usr/local/bin/codex-test app-server --stdio" {
				t.Fatalf("fallback process got %q", got)
			}
		})
	}
}

func TestCodexAppServerDaemonModeDoesNotFallBack(t *testing.T) {
	_, sockets, processes := installCodexLauncherFakes(t, "daemon", "")
	connectCodexSocket = func(_ context.Context, socketPath string) (codexRPCConn, func(), error) {
		*sockets = append(*sockets, socketPath)
		return nil, nil, errors.New("socket unavailable")
	}
	if _, _, err := connectCodexAppServer(context.Background()); err == nil || !strings.Contains(err.Error(), "socket unavailable") {
		t.Fatalf("strict daemon mode error got %v", err)
	}
	if len(*sockets) != 2 || len(*processes) != 0 {
		t.Fatalf("strict daemon mode unexpectedly fell back: sockets=%v processes=%v", *sockets, *processes)
	}
}

func TestCodexAppServerStdioModeSkipsDaemon(t *testing.T) {
	started, sockets, processes := installCodexLauncherFakes(t, "stdio", "")
	_, cleanup, err := connectCodexAppServer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if len(*started) != 0 {
		t.Fatalf("stdio mode started daemon: %v", *started)
	}
	if len(*sockets) != 0 {
		t.Fatalf("stdio mode connected socket: %v", *sockets)
	}
	want := []string{"/usr/local/bin/codex-test app-server --stdio"}
	if !reflect.DeepEqual(*processes, want) {
		t.Fatalf("process args got %v want %v", *processes, want)
	}
}

func TestEnvWithOverrideReplacesDuplicateValues(t *testing.T) {
	got := envWithOverride([]string{"PATH=/bin", "CODEX_HOME=/old", "CODEX_HOME=/also-old"}, "CODEX_HOME", "/new")
	want := []string{"PATH=/bin", "CODEX_HOME=/new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment got %v want %v", got, want)
	}
}
