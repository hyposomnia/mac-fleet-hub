package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dshTestHost 是一个假 DSH host：一元 RPC 走 HTTP，流走 /api/remote.mux。
// 用它把线协议契约固定下来，不需要真实 Desktop。
type dshTestHost struct {
	srv         *httptest.Server
	upgrader    websocket.Upgrader
	mu          sync.Mutex
	lastRequest dshRequestEnvelope
	lastPath    string
	lastHost    string
	lastOrigin  string
	lastCookies string
	lastCT      string
	cancels     chan string

	// callResult 由用例设置：返回 (value, remoteError)；remoteError 非空表示 ok:false。
	callResult func(endpoint string, args map[string]any) (any, *dshRemoteError)
	// streamReply 由用例设置：对每个 open 决定回什么帧。
	streamReply func(endpoint string, streamID string, send func(map[string]any))
}

func newDSHTestHost(t *testing.T) *dshTestHost {
	t.Helper()
	h := &dshTestHost{
		upgrader: websocket.Upgrader{},
		cancels:  make(chan string, 8),
	}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != dshRemoteMuxPath:
			h.handleCall(t, w, r)
		case r.URL.Path == dshRemoteMuxPath:
			h.handleMux(t, w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *dshTestHost) handleCall(t *testing.T, w http.ResponseWriter, r *http.Request) {
	var req dshRequestEnvelope
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Errorf("请求体不是合法 envelope: %v", err)
	}
	h.mu.Lock()
	h.lastRequest = req
	h.lastPath = r.URL.Path
	h.lastHost = r.Host
	h.lastOrigin = r.Header.Get("Origin")
	h.lastCookies = r.Header.Get("Cookie")
	h.lastCT = r.Header.Get("Content-Type")
	h.mu.Unlock()

	endpoint := strings.TrimPrefix(r.URL.Path, "/api/")
	var (
		value any
		rerr  *dshRemoteError
	)
	if h.callResult != nil {
		value, rerr = h.callResult(endpoint, req.Payload.Args)
	}
	result := map[string]any{"ok": rerr == nil}
	if rerr != nil {
		result["error"] = rerr
	} else {
		result["value"] = value
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "server-response", "rpcId": req.RPCID, "result": result,
	})
}

func (h *dshTestHost) handleMux(t *testing.T, w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		t.Errorf("upgrade 失败: %v", err)
		return
	}
	defer conn.Close()
	var writeMu sync.Mutex
	send := func(frame map[string]any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.WriteJSON(frame)
	}
	for {
		var frame map[string]any
		if err := conn.ReadJSON(&frame); err != nil {
			return
		}
		kind, _ := frame["type"].(string)
		streamID, _ := frame["streamId"].(string)
		switch kind {
		case "open":
			endpoint, _ := frame["endpoint"].(string)
			if h.streamReply != nil {
				h.streamReply(endpoint, streamID, send)
			}
		case "cancel":
			select {
			case h.cancels <- streamID:
			default:
			}
		}
	}
}

func (h *dshTestHost) snapshot() (dshRequestEnvelope, string, string, string, string, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastRequest, h.lastPath, h.lastHost, h.lastOrigin, h.lastCookies, h.lastCT
}

func TestDSHClientCallEnvelope(t *testing.T) {
	host := newDSHTestHost(t)
	host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
		return map[string]any{"items": []any{}}, nil
	}

	c := newDSHClient(host.srv.URL, "dsh-auth-test=cookie")
	value, err := c.call(context.Background(), "session/list", map[string]any{
		"_request": map[string]any{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(value), "items") {
		t.Fatalf("value = %s, want items", value)
	}

	req, path, gotHost, gotOrigin, gotCookies, gotCT := host.snapshot()
	if path != "/api/session/list" {
		t.Fatalf("path = %q, want /api/session/list", path)
	}
	if req.Type != "client-request" {
		t.Fatalf("type = %q, want client-request", req.Type)
	}
	if req.Method != "session/list" {
		t.Fatalf("method = %q, want session/list", req.Method)
	}
	if req.RPCID == "" {
		t.Fatal("rpcId 不能为空：host 用它回关响应")
	}
	if _, ok := req.Payload.Args["_request"]; !ok {
		t.Fatalf("args = %+v, want _request（session/list 的参数名就是这个）", req.Payload.Args)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Fatalf("content-type = %q", gotCT)
	}
	if gotCookies != "dsh-auth-test=cookie" {
		t.Fatalf("cookie = %q", gotCookies)
	}
	// 信任栅栏先于认证：Host 必须是 loopback 权威，Origin 必须缺省或等于 Host。
	// 这两条断言就是"请求约束"契约的守卫。
	if gotHost != strings.TrimPrefix(host.srv.URL, "http://") {
		t.Fatalf("Host = %q, want loopback authority", gotHost)
	}
	if gotOrigin != "" {
		t.Fatalf("Origin = %q, want empty（非空时必须等于 Host，不如不发）", gotOrigin)
	}
}

func TestDSHClientCallErrorTranslation(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{name: "401 未认证", status: http.StatusUnauthorized, wantErr: errDSHAuthFailed},
		{name: "403 信任栅栏", status: http.StatusForbidden, wantErr: errDSHAuthFailed},
		{name: "404 未认领端点", status: http.StatusNotFound, body: "not found", wantErr: errDSHProtocolChanged},
		{
			name:   "session/not-found",
			status: http.StatusOK,
			body: `{"type":"server-response","rpcId":"x","result":{"ok":false,"error":` +
				`{"code":"session/not-found","message":"gone","details":{"sessionId":"s"}}}}`,
			wantErr: errDSHSessionNotFound,
		},
		{
			name:   "gateway/signature-invalid 说明协议变了",
			status: http.StatusOK,
			body: `{"type":"server-response","rpcId":"x","result":{"ok":false,"error":` +
				`{"code":"gateway/signature-invalid","message":"stream endpoint","details":{}}}}`,
			wantErr: errDSHProtocolChanged,
		},
		{
			name:   "响应不是 envelope",
			status: http.StatusOK,
			body:   `{"nonsense":true}`,
			wantErr: errDSHProtocolChanged,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != http.StatusOK {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := newDSHClient(srv.URL, "")
			_, err := c.call(context.Background(), "session/list", nil)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want errors.Is(%v)", err, tc.wantErr)
			}
		})
	}
}

func TestDSHClientCallHostUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := newDSHClient(url, "")
	if _, err := c.call(context.Background(), "session/list", nil); !errors.Is(err, errDSHHostUnavailable) {
		t.Fatalf("err = %v, want errDSHHostUnavailable", err)
	}
}

func TestDSHClientStreamMuxNoCrossTalk(t *testing.T) {
	host := newDSHTestHost(t)
	host.streamReply = func(endpoint, streamID string, send func(map[string]any)) {
		send(map[string]any{
			"type": "item", "streamId": streamID,
			"value": map[string]any{"endpoint": endpoint, "streamId": streamID},
		})
		send(map[string]any{"type": "end", "streamId": streamID})
	}

	c := newDSHClient(host.srv.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, err := c.openStream(ctx, "$events", nil)
	if err != nil {
		t.Fatalf("open $events: %v", err)
	}
	follow, err := c.openStream(ctx, "session/follow", map[string]any{"sessionId": "s1"})
	if err != nil {
		t.Fatalf("open session/follow: %v", err)
	}
	if events.id == follow.id {
		t.Fatal("两条流的 streamId 必须不同")
	}

	// 同一条 WS 上两条逻辑流：各自必须只收到自己的帧。
	for _, tc := range []struct {
		name   string
		stream *dshStream
		want   string
	}{{"$events", events, "$events"}, {"session/follow", follow, "session/follow"}} {
		item, err := tc.stream.Recv(ctx)
		if err != nil {
			t.Fatalf("%s Recv: %v", tc.name, err)
		}
		var got struct {
			Endpoint string `json:"endpoint"`
		}
		if err := json.Unmarshal(item, &got); err != nil {
			t.Fatalf("%s 帧解析失败: %v", tc.name, err)
		}
		if got.Endpoint != tc.want {
			t.Fatalf("%s 收到串流的帧: endpoint=%q want %q", tc.name, got.Endpoint, tc.want)
		}
	}

	// end 之后 Recv 必须先吐完已缓冲的帧，再返回 io.EOF。
	for _, s := range []*dshStream{events, follow} {
		if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("end 之后 Recv err = %v, want io.EOF", err)
		}
	}
}

func TestDSHClientStreamErrorFrame(t *testing.T) {
	host := newDSHTestHost(t)
	host.streamReply = func(endpoint, streamID string, send func(map[string]any)) {
		send(map[string]any{
			"type": "error", "streamId": streamID,
			"error": map[string]any{"code": "gateway/internal", "message": "boom", "details": map[string]any{}},
		})
	}

	c := newDSHClient(host.srv.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := c.openStream(ctx, "$events", nil)
	if err != nil {
		t.Fatalf("openStream: %v", err)
	}
	_, err = stream.Recv(ctx)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want 携带 host 的 message", err)
	}
}

func TestDSHClientStreamCloseSendsCancel(t *testing.T) {
	host := newDSHTestHost(t)
	host.streamReply = func(endpoint, streamID string, send func(map[string]any)) {
		// 保持流打开，等客户端主动取消。
	}

	c := newDSHClient(host.srv.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := c.openStream(ctx, "session/control", nil)
	if err != nil {
		t.Fatalf("openStream: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case id := <-host.cancels:
		if id != stream.id {
			t.Fatalf("cancel streamId = %q, want %q", id, stream.id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close 之后 host 没有收到 cancel 帧")
	}

	// 关闭后的 Recv 必须立刻返回，不能挂住。
	if _, err := stream.Recv(ctx); err == nil {
		t.Fatal("关闭后的 Recv 应当报错")
	}
}
