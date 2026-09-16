package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// dshRemoteMuxPath 是 host 承载全部 Typert Remote 流的唯一 WebSocket 路由。
const dshRemoteMuxPath = "/api/remote.mux"

// maxDSHResponseBytes 给一元响应一个上界：history 分页可能很大，但不能无上限读。
const maxDSHResponseBytes = 64 << 20

// 稳定错误码：沿用 writeChatErr 的做法，判据是错误身份而不是字符串匹配。
var (
	errDSHHostUnavailable = errors.New("dsh_host_unavailable")
	errDSHAuthFailed      = errors.New("dsh_auth_failed")
	errDSHProtocolChanged = errors.New("dsh_protocol_changed")
	errDSHSessionNotFound = errors.New("dsh_session_not_found")
)

// dshRequestEnvelope 是一元调用的请求信封。
//
// 注意 payload 必须恰好只有 args 一个字段，且它必须是普通对象：
// host 侧校验不通过会回 gateway/internal "Remote payload must contain exactly
// one plain-object args field"。
type dshRequestEnvelope struct {
	Type    string            `json:"type"`
	RPCID   string            `json:"rpcId"`
	Method  string            `json:"method"`
	Payload dshRequestPayload `json:"payload"`
}

type dshRequestPayload struct {
	Args map[string]any `json:"args"`
}

type dshResponseEnvelope struct {
	Type   string          `json:"type"`
	RPCID  string          `json:"rpcId"`
	Result json.RawMessage `json:"result"`
}

type dshCallResult struct {
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value"`
	Error *dshRemoteError `json:"error"`
}

// dshRemoteError 是 host 返回的稳定失败码与结构化细节。
type dshRemoteError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details"`
}

func (e *dshRemoteError) Error() string {
	if e == nil {
		return "DSH remote error"
	}
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// translateDSHRemoteError 把 host 的码翻译成本包内的稳定哨兵错误。
//
// 只翻译调用方需要分支处理的码；其余原样返回，避免把未知码硬塞进错误分类里。
func translateDSHRemoteError(e *dshRemoteError) error {
	if e == nil {
		return nil
	}
	switch e.Code {
	case "session/not-found":
		return fmt.Errorf("%w: %s", errDSHSessionNotFound, e.Error())
	case "gateway/signature-invalid", "gateway/bad-request", "gateway/arguments-invalid", "gateway/result-invalid":
		// 这些码意味着我们按旧契约发的请求/端点不再被承认 —— 协议变了。
		return fmt.Errorf("%w: %s", errDSHProtocolChanged, e.Error())
	}
	return e
}

// dshClient 是 DSH host 的 loopback 客户端：一元走 HTTP POST，流走一条多路复用 WebSocket。
//
// 它刻意不做自动重连与流恢复：host 重启会换端口与 token，恢复语义属于上层
// （重新发现端点、重新打开 session/follow 并按 cursor/seq 去重），在这里偷偷重试
// 只会把"连接已失效"这件事藏起来。
type dshClient struct {
	baseURL string
	cookie  string
	http    *http.Client
	dialer  *websocket.Dialer

	mu      sync.Mutex
	conn    *websocket.Conn
	streams map[string]*dshStream
	closed  bool

	dialMu  sync.Mutex // 串行化建连，避免并发 openStream 重复拨号
	writeMu sync.Mutex // gorilla 不允许多协程并发写同一连接
}

func newDSHClient(baseURL, cookie string) *dshClient {
	return &dshClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		cookie:  cookie,
		http:    &http.Client{Timeout: 30 * time.Second},
		dialer:  &websocket.Dialer{HandshakeTimeout: 10 * time.Second},
		streams: map[string]*dshStream{},
	}
}

// call 发一元 RPC 并返回 result.value。
func (c *dshClient) call(ctx context.Context, endpoint string, args map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(dshRequestEnvelope{
		Type:    "client-request",
		RPCID:   newDSHID(),
		Method:  endpoint,
		Payload: dshRequestPayload{Args: dshArgs(args)},
	})
	if err != nil {
		return nil, fmt.Errorf("序列化 DSH 请求: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/"+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造 DSH 请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	// 刻意不设 Origin 与 Sec-Fetch-Site：host 的信任栅栏要求 Host 是 loopback 权威，
	// 且 Origin 缺省或等于 Host；不发比发错安全。

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", errDSHHostUnavailable, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: HTTP %d", errDSHAuthFailed, resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: HTTP 404（host 未认领该端点）", errDSHProtocolChanged)
	default:
		return nil, fmt.Errorf("%w: HTTP %d", errDSHProtocolChanged, resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDSHResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: 读取响应失败: %v", errDSHHostUnavailable, err)
	}
	var envelope dshResponseEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%w: 响应不是 server-response 信封", errDSHProtocolChanged)
	}
	if envelope.Type != "server-response" {
		return nil, fmt.Errorf("%w: envelope.type=%q", errDSHProtocolChanged, envelope.Type)
	}
	var result dshCallResult
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		return nil, fmt.Errorf("%w: result 解析失败", errDSHProtocolChanged)
	}
	if !result.OK {
		return nil, translateDSHRemoteError(result.Error)
	}
	return result.Value, nil
}

// openStream 在 remote.mux 上开一条逻辑流。
func (c *dshClient) openStream(ctx context.Context, endpoint string, args map[string]any) (*dshStream, error) {
	conn, err := c.ensureConn(ctx)
	if err != nil {
		return nil, err
	}
	stream := newDSHStream(c, newDSHID(), endpoint)

	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: 连接在打开流之前失效", errDSHHostUnavailable)
	}
	c.streams[stream.id] = stream
	c.mu.Unlock()

	frame := map[string]any{
		"type":     "open",
		"streamId": stream.id,
		"endpoint": endpoint,
		"payload":  map[string]any{"args": dshArgs(args)},
	}
	if err := c.writeFrame(conn, frame); err != nil {
		c.removeStream(stream.id, false)
		c.invalidateConn(conn, err)
		return nil, fmt.Errorf("%w: 打开流失败: %v", errDSHHostUnavailable, err)
	}
	return stream, nil
}

// Close 关闭整条连接与全部在途流。
func (c *dshClient) Close() error {
	c.mu.Lock()
	c.closed = true
	conn := c.conn
	c.conn = nil
	streams := c.takeStreamsLocked()
	c.mu.Unlock()

	for _, s := range streams {
		s.finish(errDSHHostUnavailable)
	}
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func (c *dshClient) ensureConn(ctx context.Context) (*websocket.Conn, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errDSHHostUnavailable
	}
	if c.conn != nil {
		conn := c.conn
		c.mu.Unlock()
		return conn, nil
	}
	c.mu.Unlock()

	c.dialMu.Lock()
	defer c.dialMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errDSHHostUnavailable
	}
	if c.conn != nil {
		conn := c.conn
		c.mu.Unlock()
		return conn, nil
	}
	c.mu.Unlock()

	wsURL := "ws" + strings.TrimPrefix(c.baseURL, "http") + dshRemoteMuxPath
	header := http.Header{}
	if c.cookie != "" {
		header.Set("Cookie", c.cookie)
	}
	conn, resp, err := c.dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				return nil, fmt.Errorf("%w: WS 握手 HTTP %d", errDSHAuthFailed, resp.StatusCode)
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: WS 握手失败: %v", errDSHHostUnavailable, err)
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return nil, errDSHHostUnavailable
	}
	c.conn = conn
	c.mu.Unlock()

	go c.readLoop(conn)
	return conn, nil
}

func (c *dshClient) readLoop(conn *websocket.Conn) {
	for {
		var msg dshStreamServerMessage
		if err := conn.ReadJSON(&msg); err != nil {
			c.invalidateConn(conn, err)
			return
		}
		c.dispatch(msg)
	}
}

func (c *dshClient) dispatch(msg dshStreamServerMessage) {
	c.mu.Lock()
	stream := c.streams[msg.StreamID]
	c.mu.Unlock()
	if stream == nil {
		// 已取消或已终结的流的迟到帧：丢弃。
		return
	}
	switch msg.Type {
	case "item":
		stream.deliver(msg.Value)
	case "end":
		stream.finish(nil)
	case "error":
		stream.finish(translateDSHRemoteError(msg.Error))
	}
}

// invalidateConn 在连接失效时终结全部在途流。
//
// 不在这里重连：调用方需要知道连接断了才能重新发现端点并重建订阅。
func (c *dshClient) invalidateConn(conn *websocket.Conn, cause error) {
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return
	}
	c.conn = nil
	streams := c.takeStreamsLocked()
	c.mu.Unlock()

	err := fmt.Errorf("%w: %v", errDSHHostUnavailable, cause)
	for _, s := range streams {
		s.finish(err)
	}
	_ = conn.Close()
}

func (c *dshClient) takeStreamsLocked() []*dshStream {
	out := make([]*dshStream, 0, len(c.streams))
	for _, s := range c.streams {
		out = append(out, s)
	}
	c.streams = map[string]*dshStream{}
	return out
}

// removeStream 从路由表摘掉一条流，并尽力通知 host 取消（notify 为 false 时只摘表）。
func (c *dshClient) removeStream(id string, notify bool) {
	c.mu.Lock()
	stream := c.streams[id]
	conn := c.conn
	delete(c.streams, id)
	c.mu.Unlock()

	if stream == nil || !notify || conn == nil {
		return
	}
	_ = c.writeFrame(conn, map[string]any{"type": "cancel", "streamId": id})
}

func (c *dshClient) writeFrame(conn *websocket.Conn, frame map[string]any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return conn.WriteJSON(frame)
}

// dshStreamServerMessage 是 remote.mux 的服务端帧。
type dshStreamServerMessage struct {
	Type     string          `json:"type"`
	StreamID string          `json:"streamId"`
	Value    json.RawMessage `json:"value"`
	Error    *dshRemoteError `json:"error"`
}

// dshStream 是 remote.mux 上的一条逻辑流。
//
// 刻意不 close(items)：帧投递与终态可能并发发生，关通道会引入"向已关闭通道发送"的竞态。
// 终态由 done 表达，Recv 在 done 后先把已缓冲的帧吐完再报终态。
type dshStream struct {
	client   *dshClient
	id       string
	endpoint string

	items chan json.RawMessage
	done  chan struct{}
	once  sync.Once

	mu  sync.Mutex
	err error
}

func newDSHStream(client *dshClient, id, endpoint string) *dshStream {
	return &dshStream{
		client:   client,
		id:       id,
		endpoint: endpoint,
		items:    make(chan json.RawMessage, 64),
		done:     make(chan struct{}),
	}
}

func (s *dshStream) deliver(value json.RawMessage) {
	if len(value) == 0 {
		value = json.RawMessage("null")
	}
	select {
	case s.items <- value:
	case <-s.done:
	}
}

func (s *dshStream) finish(err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.done)
	})
}

func (s *dshStream) terminalError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Recv 取下一条帧；host 正常结束返回 io.EOF，连接失效返回 errDSHHostUnavailable。
func (s *dshStream) Recv(ctx context.Context) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case item := <-s.items:
		return item, nil
	case <-s.done:
		// host 可能把 item 与 end 连在一起发，先吐完已缓冲的帧再报终态。
		select {
		case item := <-s.items:
			return item, nil
		default:
		}
		if err := s.terminalError(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
}

// Close 取消这条流并终结它。
func (s *dshStream) Close() error {
	s.client.removeStream(s.id, true)
	s.finish(nil)
	return nil
}

func dshArgs(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	return args
}

// newDSHID 生成 rpcId / streamId。
//
// 与邻居 newChatControlEpoch 同样的写法：优先随机，失败退回时间戳，永不返回空串
// （host 会用 rpcId 回关响应，streamId 重复会直接抛 duplicate Remote stream id）。
func newDSHID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
