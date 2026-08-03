package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// rpcRequest / rpcResponse / rpcNotification model the line-oriented JSON-RPC-ish
// protocol used by app-server. The wire omits "jsonrpc":"2.0", but otherwise
// behaves like request/response plus notifications.
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

type rpcCallError struct {
	Method  string
	Code    int
	Message string
	Data    json.RawMessage
}

func (e *rpcCallError) Error() string {
	return fmt.Sprintf("codex app-server %s failed: %s", e.Method, e.Message)
}

type rpcResponse struct {
	ID           int64           `json:"id"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        *rpcError       `json:"error,omitempty"`
	transportErr error
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
	serverInfo  codexServerInfo
}

type codexServerInfo struct {
	UserAgent      string
	PlatformFamily string
	PlatformOS     string
}

const (
	codexAppServerModeAuto   = "auto"
	codexAppServerModeDaemon = "daemon"
	codexAppServerModeStdio  = "stdio"
)

type codexProcessConnector func(context.Context, string, ...string) (codexRPCConn, func(), error)
type codexSocketConnector func(context.Context, string) (codexRPCConn, func(), error)

var (
	startManagedCodexDaemon                       = startCodexAppServerDaemon
	connectCodexProcess     codexProcessConnector = connectCodexAppServerProcess
	connectCodexSocket      codexSocketConnector  = connectCodexAppServerSocket
	runCodexDaemonCommand                         = func(ctx context.Context, codexBin string, args ...string) ([]byte, error) {
		return codexCommand(ctx, codexBin, args...).CombinedOutput()
	}
)

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

// websocketLineReadWriter adapts app-server's one-JSON-object-per-WebSocket-
// message transport to the line-oriented reader used by codexRPCClient.
type websocketLineReadWriter struct {
	conn    *websocket.Conn
	pending []byte
}

func (rw *websocketLineReadWriter) Read(p []byte) (int, error) {
	for len(rw.pending) == 0 {
		messageType, message, err := rw.conn.ReadMessage()
		if err != nil {
			return 0, err
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}
		rw.pending = append(message, '\n')
	}
	n := copy(p, rw.pending)
	rw.pending = rw.pending[n:]
	return n, nil
}

func (rw *websocketLineReadWriter) Write(p []byte) (int, error) {
	message := p
	if len(message) > 0 && message[len(message)-1] == '\n' {
		message = message[:len(message)-1]
	}
	if err := rw.conn.WriteMessage(websocket.TextMessage, message); err != nil {
		return 0, err
	}
	return len(p), nil
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
	r := bufio.NewReaderSize(c.rw, 64*1024)
	var err error
	for {
		var line []byte
		line, err = r.ReadBytes('\n')
		if len(line) > 0 {
			c.handleLine(line)
		}
		if err != nil {
			break
		}
	}
	if errors.Is(err, io.EOF) && ctx.Err() != nil {
		err = ctx.Err()
	}
	c.failPending(err)
	close(c.notes)
}

func (c *codexRPCClient) failPending(err error) {
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = map[int64]chan rpcResponse{}
	c.pendingMu.Unlock()
	for id, ch := range pending {
		ch <- rpcResponse{ID: id, transportErr: err}
		close(ch)
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
		// App-server notifications are state transitions, not telemetry. Losing
		// one can strand an approval, duplicate history, or leave a turn running
		// forever in the UI. The backend dispatcher is deliberately lightweight,
		// so apply backpressure here instead of silently dropping protocol state.
		c.notes <- n
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
		if resp.transportErr != nil {
			return nil, resp.transportErr
		}
		if resp.Error != nil {
			return nil, &rpcCallError{
				Method: method, Code: resp.Error.Code, Message: resp.Error.Message,
				Data: append(json.RawMessage(nil), resp.Error.Data...),
			}
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
	raw, err := c.call(ctx, "initialize", params)
	if err != nil {
		return err
	}
	var info struct {
		UserAgent      string `json:"userAgent"`
		PlatformFamily string `json:"platformFamily"`
		PlatformOS     string `json:"platformOs"`
	}
	if json.Unmarshal(raw, &info) == nil {
		c.serverInfo = codexServerInfo{
			UserAgent: info.UserAgent, PlatformFamily: info.PlatformFamily, PlatformOS: info.PlatformOS,
		}
	}
	if err := c.notify("initialized", nil); err != nil {
		return err
	}
	c.initMu.Lock()
	c.initialized = true
	c.initMu.Unlock()
	return nil
}

func normalizeCodexAppServerMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case codexAppServerModeDaemon:
		return codexAppServerModeDaemon
	case codexAppServerModeStdio:
		return codexAppServerModeStdio
	default:
		return codexAppServerModeAuto
	}
}

func codexCommand(ctx context.Context, codexBin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, codexBin, args...)
	if home := strings.TrimSpace(cfg.CodexHome); home != "" {
		cmd.Env = envWithOverride(os.Environ(), "CODEX_HOME", home)
	}
	return cmd
}

func envWithOverride(env []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}

func startCodexAppServerDaemon(ctx context.Context, codexBin string) error {
	startCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for _, action := range []string{"start", "enable-remote-control"} {
		out, err := runCodexDaemonCommand(startCtx, codexBin, "app-server", "daemon", action)
		if err == nil {
			continue
		}
		detail := strings.TrimSpace(string(out))
		if len(detail) > 512 {
			detail = detail[:512]
		}
		if detail == "" {
			return fmt.Errorf("Codex app-server daemon %s: %w", action, err)
		}
		return fmt.Errorf("Codex app-server daemon %s: %w: %s", action, err, detail)
	}
	return nil
}

func connectCodexAppServer(ctx context.Context) (codexRPCConn, func(), error) {
	codexBin := cfg.CodexBin
	if codexBin == "" {
		codexBin = "codex"
	}
	mode := normalizeCodexAppServerMode(cfg.CodexMode)
	if mode != codexAppServerModeStdio {
		socketPath, socketErr := codexAppServerSocketPath()
		if socketErr == nil {
			if rpc, cleanup, err := connectCodexSocket(ctx, socketPath); err == nil {
				return rpc, cleanup, nil
			} else {
				socketErr = err
			}
		}

		startErr := startManagedCodexDaemon(ctx, codexBin)
		if startErr == nil {
			socketPath, socketErr = codexAppServerSocketPath()
			if socketErr == nil {
				if rpc, cleanup, err := connectCodexSocket(ctx, socketPath); err == nil {
					return rpc, cleanup, nil
				} else {
					socketErr = err
				}
			}
			if mode == codexAppServerModeDaemon {
				return nil, nil, fmt.Errorf("connect managed Codex app-server: %w", socketErr)
			}
			log.Printf("managed Codex app-server socket unavailable; falling back to stdio: %v", socketErr)
		} else {
			if mode == codexAppServerModeDaemon {
				return nil, nil, startErr
			}
			log.Printf("Codex app-server socket and managed daemon unavailable; falling back to stdio: %v", startErr)
		}
	}
	return connectCodexProcess(ctx, codexBin, "app-server", "--stdio")
}

func codexAppServerSocketPath() (string, error) {
	if socketPath := strings.TrimSpace(cfg.CodexSock); socketPath != "" {
		return socketPath, nil
	}
	codexHome := strings.TrimSpace(cfg.CodexHome)
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		codexHome = filepath.Join(home, ".codex")
	}
	return filepath.Join(codexHome, "app-server-control", "app-server-control.sock"), nil
}

func connectCodexAppServerSocket(ctx context.Context, socketPath string) (codexRPCConn, func(), error) {
	dialer := websocket.Dialer{
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	conn, response, err := dialer.DialContext(ctx, "ws://localhost/", nil)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, nil, err
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	client := newCodexRPCClient(&websocketLineReadWriter{conn: conn})
	go client.run(runCtx)

	initCtx, cancelInit := context.WithTimeout(ctx, 15*time.Second)
	err = client.initialize(initCtx, version)
	cancelInit()
	if err != nil {
		cancelRun()
		_ = conn.Close()
		return nil, nil, err
	}

	cleanup := func() {
		cancelRun()
		_ = conn.Close()
	}
	return client, cleanup, nil
}

func connectCodexAppServerProcess(ctx context.Context, codexBin string, args ...string) (codexRPCConn, func(), error) {
	cmd := codexCommand(context.Background(), codexBin, args...)
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
