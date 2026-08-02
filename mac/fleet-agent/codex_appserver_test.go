package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
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

func installCodexLauncherFakes(t *testing.T, mode, socket string) (*[]string, *[]string) {
	t.Helper()
	previousCfg := cfg
	previousStart := startManagedCodexDaemon
	previousConnect := connectCodexProcess
	t.Cleanup(func() {
		cfg = previousCfg
		startManagedCodexDaemon = previousStart
		connectCodexProcess = previousConnect
	})

	cfg.CodexBin = "/usr/local/bin/codex-test"
	cfg.CodexMode = mode
	cfg.CodexSock = socket
	started := []string{}
	connected := []string{}
	startManagedCodexDaemon = func(_ context.Context, codexBin string) error {
		started = append(started, codexBin)
		return nil
	}
	connectCodexProcess = func(_ context.Context, codexBin string, args ...string) (codexRPCConn, func(), error) {
		connected = append(connected, append([]string{codexBin}, args...)...)
		return newFakeRPCConn(), func() {}, nil
	}
	return &started, &connected
}

func TestCodexAppServerAutoPrefersManagedDaemonProxy(t *testing.T) {
	started, connected := installCodexLauncherFakes(t, "auto", "")
	_, cleanup, err := connectCodexAppServer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if !reflect.DeepEqual(*started, []string{"/usr/local/bin/codex-test"}) {
		t.Fatalf("daemon starts got %v", *started)
	}
	want := []string{"/usr/local/bin/codex-test", "app-server", "proxy"}
	if !reflect.DeepEqual(*connected, want) {
		t.Fatalf("process args got %v want %v", *connected, want)
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

func TestCodexAppServerProxyUsesConfiguredSocket(t *testing.T) {
	_, connected := installCodexLauncherFakes(t, "daemon", "/tmp/codex.sock")
	_, cleanup, err := connectCodexAppServer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	want := []string{"/usr/local/bin/codex-test", "app-server", "proxy", "--sock", "/tmp/codex.sock"}
	if !reflect.DeepEqual(*connected, want) {
		t.Fatalf("process args got %v want %v", *connected, want)
	}
}

func TestCodexAppServerAutoFallsBackToStdio(t *testing.T) {
	for _, test := range []struct {
		name       string
		startError error
		proxyError error
	}{
		{name: "daemon start failure", startError: errors.New("daemon unavailable")},
		{name: "proxy failure", proxyError: errors.New("socket unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			started, connected := installCodexLauncherFakes(t, "auto", "")
			startManagedCodexDaemon = func(_ context.Context, codexBin string) error {
				*started = append(*started, codexBin)
				return test.startError
			}
			connectCodexProcess = func(_ context.Context, codexBin string, args ...string) (codexRPCConn, func(), error) {
				*connected = append(*connected, strings.Join(append([]string{codexBin}, args...), " "))
				if len(args) > 1 && args[1] == "proxy" && test.proxyError != nil {
					return nil, nil, test.proxyError
				}
				return newFakeRPCConn(), func() {}, nil
			}

			_, cleanup, err := connectCodexAppServer(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			cleanup()
			if got := (*connected)[len(*connected)-1]; got != "/usr/local/bin/codex-test app-server --stdio" {
				t.Fatalf("fallback process got %q", got)
			}
		})
	}
}

func TestCodexAppServerDaemonModeDoesNotFallBack(t *testing.T) {
	_, connected := installCodexLauncherFakes(t, "daemon", "")
	connectCodexProcess = func(_ context.Context, codexBin string, args ...string) (codexRPCConn, func(), error) {
		*connected = append(*connected, strings.Join(append([]string{codexBin}, args...), " "))
		return nil, nil, errors.New("socket unavailable")
	}
	if _, _, err := connectCodexAppServer(context.Background()); err == nil || !strings.Contains(err.Error(), "socket unavailable") {
		t.Fatalf("strict daemon mode error got %v", err)
	}
	if len(*connected) != 1 || (*connected)[0] != "/usr/local/bin/codex-test app-server proxy" {
		t.Fatalf("strict daemon mode unexpectedly fell back: %v", *connected)
	}
}

func TestCodexAppServerStdioModeSkipsDaemon(t *testing.T) {
	started, connected := installCodexLauncherFakes(t, "stdio", "")
	_, cleanup, err := connectCodexAppServer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if len(*started) != 0 {
		t.Fatalf("stdio mode started daemon: %v", *started)
	}
	want := []string{"/usr/local/bin/codex-test", "app-server", "--stdio"}
	if !reflect.DeepEqual(*connected, want) {
		t.Fatalf("process args got %v want %v", *connected, want)
	}
}

func TestEnvWithOverrideReplacesDuplicateValues(t *testing.T) {
	got := envWithOverride([]string{"PATH=/bin", "CODEX_HOME=/old", "CODEX_HOME=/also-old"}, "CODEX_HOME", "/new")
	want := []string{"PATH=/bin", "CODEX_HOME=/new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment got %v want %v", got, want)
	}
}
