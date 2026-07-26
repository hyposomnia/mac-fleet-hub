package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

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
