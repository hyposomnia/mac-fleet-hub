package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// rpcRequest / rpcResponse / rpcNotification model the line-oriented JSON-RPC-ish
// protocol used by `codex app-server proxy`. The wire omits "jsonrpc":"2.0",
// but otherwise behaves like request/response plus notifications.
type rpcRequest struct {
	ID     int64       `json:"id"`
	Method string      `json:"method"`
	Params interface{} `json:"params,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcNotification struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type codexRPCClient struct {
	rw        io.ReadWriter
	processID int

	nextID  atomic.Int64
	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[int64]chan rpcResponse

	notes chan rpcNotification

	initMu      sync.Mutex
	initialized bool
}

type codexRPCConn interface {
	call(ctx context.Context, method string, params interface{}) (json.RawMessage, error)
	notify(method string, params interface{}) error
	respond(id json.RawMessage, result interface{}) error
	notifications() <-chan rpcNotification
}

type stdioReadWriter struct {
	io.Reader
	io.Writer
}

func newCodexRPCClient(rw io.ReadWriter) *codexRPCClient {
	c := &codexRPCClient{
		rw:      rw,
		pending: map[int64]chan rpcResponse{},
		notes:   make(chan rpcNotification, 128),
	}
	c.nextID.Store(0)
	return c
}

func (c *codexRPCClient) notifications() <-chan rpcNotification { return c.notes }

func (c *codexRPCClient) terminateCommandDescendants() {
	if c.processID <= 0 {
		return
	}
	pairs, err := processParentPairs()
	if err != nil {
		return
	}
	for _, pid := range descendantProcessIDs(c.processID, pairs) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func processParentPairs() ([][2]int, error) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return nil, err
	}
	pairs := make([][2]int, 0, 64)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		var pid, parentPID int
		if _, err := fmt.Sscan(scanner.Text(), &pid, &parentPID); err == nil && pid > 0 {
			pairs = append(pairs, [2]int{pid, parentPID})
		}
	}
	return pairs, scanner.Err()
}

func descendantProcessIDs(rootPID int, pairs [][2]int) []int {
	children := make(map[int][]int)
	for _, pair := range pairs {
		children[pair[1]] = append(children[pair[1]], pair[0])
	}
	queue := []int{rootPID}
	seen := map[int]bool{rootPID: true}
	var descendants []int
	for len(queue) > 0 {
		parentPID := queue[0]
		queue = queue[1:]
		for _, pid := range children[parentPID] {
			if seen[pid] {
				continue
			}
			seen[pid] = true
			descendants = append(descendants, pid)
			queue = append(queue, pid)
		}
	}
	for left, right := 0, len(descendants)-1; left < right; left, right = left+1, right-1 {
		descendants[left], descendants[right] = descendants[right], descendants[left]
	}
	return descendants
}

func (c *codexRPCClient) run(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(c.rw)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			c.handleLine(sc.Bytes())
		}
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

func (c *codexRPCClient) handleLine(b []byte) {
	var probe struct {
		ID     json.RawMessage `json:"id,omitempty"`
		Method string          `json:"method,omitempty"`
		Params json.RawMessage `json:"params,omitempty"`
		Result json.RawMessage `json:"result,omitempty"`
		Error  *rpcError       `json:"error,omitempty"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		// app-server occasionally sends warnings/log-like malformed lines while
		// tooling evolves. They must not kill the client; callers just miss that
		// one line.
		return
	}
	hasID := len(probe.ID) > 0 && string(probe.ID) != "null"
	if hasID && probe.Method == "" {
		id, ok := rpcIDAsInt64(probe.ID)
		if !ok {
			return
		}
		resp := rpcResponse{ID: id, Result: probe.Result, Error: probe.Error}
		c.pendingMu.Lock()
		ch := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.pendingMu.Unlock()
		if ch != nil {
			ch <- resp
			close(ch)
		}
		return
	}
	if probe.Method != "" {
		n := rpcNotification{ID: append(json.RawMessage(nil), probe.ID...), Method: probe.Method, Params: probe.Params}
		select {
		case c.notes <- n:
		default:
			// Keep app-server reader non-blocking. Dropping is preferable to
			// deadlocking the control channel; higher layers can reconnect.
		}
	}
}

func rpcIDAsInt64(raw json.RawMessage) (int64, bool) {
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		var parsed int64
		if _, err := fmt.Sscan(s, &parsed); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func (c *codexRPCClient) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan rpcResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	req := rpcRequest{ID: id, Method: method, Params: params}
	if err := c.writeJSON(req); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("codex app-server %s failed: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *codexRPCClient) notify(method string, params interface{}) error {
	msg := struct {
		Method string      `json:"method"`
		Params interface{} `json:"params,omitempty"`
	}{Method: method, Params: params}
	return c.writeJSON(msg)
}

func (c *codexRPCClient) respond(id json.RawMessage, result interface{}) error {
	msg := struct {
		ID     json.RawMessage `json:"id"`
		Result interface{}     `json:"result"`
	}{ID: id, Result: result}
	return c.writeJSON(msg)
}

func (c *codexRPCClient) writeJSON(v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.rw.Write(b)
	return err
}

func (c *codexRPCClient) initialize(ctx context.Context, version string) error {
	c.initMu.Lock()
	if c.initialized {
		c.initMu.Unlock()
		return errors.New("codex app-server already initialized")
	}
	c.initMu.Unlock()

	params := map[string]interface{}{
		"clientInfo": map[string]string{
			"name":    "mac_fleet_hub",
			"title":   "mac-fleet-hub",
			"version": version,
		},
		"capabilities": map[string]interface{}{
			"experimentalApi": true,
		},
	}
	if _, err := c.call(ctx, "initialize", params); err != nil {
		return err
	}
	if err := c.notify("initialized", nil); err != nil {
		return err
	}
	c.initMu.Lock()
	c.initialized = true
	c.initMu.Unlock()
	return nil
}

func connectCodexAppServerStdio(ctx context.Context) (codexRPCConn, func(), error) {
	codexBin := cfg.CodexBin
	if codexBin == "" {
		codexBin = "codex"
	}
	cmd := exec.CommandContext(context.Background(), codexBin, "app-server", "--stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = log.Writer()
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	client := newCodexRPCClient(stdioReadWriter{Reader: stdout, Writer: stdin})
	client.processID = cmd.Process.Pid
	go client.run(runCtx)

	initCtx, cancelInit := context.WithTimeout(ctx, 15*time.Second)
	err = client.initialize(initCtx, version)
	cancelInit()
	if err != nil {
		cancelRun()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, err
	}

	cleanup := func() {
		cancelRun()
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
	return client, cleanup, nil
}
