package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestDSHBackend 直接注入假 host 的客户端，绕过真实端点发现与凭据。
func newTestDSHBackend(t *testing.T, host *dshTestHost) *dshChatBackend {
	t.Helper()
	b := newDSHChatBackend("/nonexistent", "/nonexistent", "")
	b.client = newDSHClient(host.srv.URL, "")
	b.hostSeen = true
	t.Cleanup(b.Close)
	return b
}

// recordingHost 记录每个端点的 args，供断言真实请求形状。
type recordingHost struct {
	host  *dshTestHost
	calls map[string]map[string]any
}

func newRecordingHost(t *testing.T, createValue any) *recordingHost {
	t.Helper()
	r := &recordingHost{calls: map[string]map[string]any{}}
	r.host = newDSHTestHost(t)
	r.host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
		r.calls[endpoint] = args
		switch endpoint {
		case "session/create":
			return createValue, nil
		default:
			return map[string]any{"accepted": true}, nil
		}
	}
	return r
}

func (r *recordingHost) requestArgs(t *testing.T, endpoint string) map[string]any {
	t.Helper()
	args, ok := r.calls[endpoint]
	if !ok {
		t.Fatalf("没有收到 %s 调用；已收到: %v", endpoint, keysOf(r.calls))
	}
	inner, _ := args["request"].(map[string]any)
	if inner == nil {
		t.Fatalf("%s 的 args 缺少 request 包装层: %+v", endpoint, args)
	}
	return inner
}

func keysOf(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestDSHChatStartCreatesSessionWithCwd(t *testing.T) {
	host := newRecordingHost(t, map[string]any{"sessionId": "session-1"})
	b := newTestDSHBackend(t, host.host)

	res, err := b.Start(context.Background(), "dsh", "/tmp/work", "default")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.SessionID != "session-1" || res.Cwd != "/tmp/work" {
		t.Fatalf("res = %+v", res)
	}
	if got := host.requestArgs(t, "session/create")["cwd"]; got != "/tmp/work" {
		t.Fatalf("cwd = %v, want /tmp/work", got)
	}
}

func TestDSHChatStartRejectsMissingSessionID(t *testing.T) {
	host := newRecordingHost(t, map[string]any{})
	b := newTestDSHBackend(t, host.host)

	if _, err := b.Start(context.Background(), "dsh", "/tmp/work", "default"); !errors.Is(err, errDSHProtocolChanged) {
		t.Fatalf("err = %v, want errDSHProtocolChanged", err)
	}
}

func TestDSHChatInputUsesQueueMode(t *testing.T) {
	host := newRecordingHost(t, map[string]any{"sessionId": "session-1"})
	b := newTestDSHBackend(t, host.host)

	// 普通输入一律 queue：空闲时 host 在轮次边界立刻领取，忙碌时排到下一轮。
	// steer 是独立的 Steer 方法，不能在这里混用。
	_, err := b.Input(context.Background(), "dsh", "session-1", "你好", nil, nil,
		ChatTurnOptions{ClientUserMessageID: "client-msg-1"})
	if err != nil {
		t.Fatalf("Input: %v", err)
	}

	req := host.requestArgs(t, "session/prompt")
	if req["mode"] != "queue" {
		t.Fatalf("mode = %v, want queue", req["mode"])
	}
	// requestId 必须原样带上浏览器的 clientMessageId：host 会把它回显成用户事件的
	// rpcId，浏览器据此把乐观气泡换成权威正文。
	if req["requestId"] != "client-msg-1" {
		t.Fatalf("requestId = %v, want client-msg-1", req["requestId"])
	}
	if req["sessionId"] != "session-1" {
		t.Fatalf("sessionId = %v", req["sessionId"])
	}
	content, _ := req["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %+v", req["content"])
	}
	part, _ := content[0].(map[string]any)
	if part["type"] != "text" || part["text"] != "你好" {
		t.Fatalf("content[0] = %+v", part)
	}
}

func TestDSHChatSteerUsesSteerMode(t *testing.T) {
	host := newRecordingHost(t, map[string]any{"sessionId": "session-1"})
	b := newTestDSHBackend(t, host.host)

	if _, err := b.Steer(context.Background(), "dsh", "session-1", "client-1", "插一句", nil, nil); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	req := host.requestArgs(t, "session/prompt")
	if req["mode"] != "steer" {
		t.Fatalf("mode = %v, want steer", req["mode"])
	}
	if req["requestId"] != "client-1" {
		t.Fatalf("requestId = %v", req["requestId"])
	}
}

func TestDSHChatInputRejectsEmptyMessage(t *testing.T) {
	host := newRecordingHost(t, map[string]any{"sessionId": "session-1"})
	b := newTestDSHBackend(t, host.host)

	if _, err := b.Input(context.Background(), "dsh", "session-1", "", nil, nil, ChatTurnOptions{}); err == nil {
		t.Fatal("空文本 + 无图片应当报错，而不是发一条空 prompt")
	}
}

func TestDSHChatInterruptSendsCancel(t *testing.T) {
	host := newRecordingHost(t, map[string]any{"sessionId": "session-1"})
	b := newTestDSHBackend(t, host.host)

	if err := b.Interrupt(context.Background(), "dsh", "session-1"); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if got := host.requestArgs(t, "session/cancel")["sessionId"]; got != "session-1" {
		t.Fatalf("cancel sessionId = %v", got)
	}
}

// Release 对 DSH 是空操作：一个 host 进程内每个会话只有一个 Agent，
// 写入方天然唯一，没有租约可释放。
func TestDSHChatReleaseIsNoop(t *testing.T) {
	host := newRecordingHost(t, map[string]any{"sessionId": "session-1"})
	b := newTestDSHBackend(t, host.host)

	if err := b.Release(context.Background(), "dsh", "session-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if len(host.calls) != 0 {
		t.Fatalf("Release 不该发起任何 RPC，实际: %v", keysOf(host.calls))
	}
}

// 未验证的写入面必须明确报不支持：猜着实现会改动用户的真实 DSH 配置，
// 或让用户以为"这台机器没有技能"。
func TestDSHChatUnsupportedSurfaces(t *testing.T) {
	host := newRecordingHost(t, map[string]any{"sessionId": "session-1"})
	b := newTestDSHBackend(t, host.host)

	if err := b.Settings(context.Background(), "dsh", "session-1", "full-access"); !errors.Is(err, errDSHUnsupported) {
		t.Fatalf("Settings err = %v, want errDSHUnsupported", err)
	}
	if _, err := b.Skills(context.Background(), "dsh", "/tmp"); !errors.Is(err, errDSHUnsupported) {
		t.Fatalf("Skills err = %v, want errDSHUnsupported", err)
	}
	if len(host.calls) != 0 {
		t.Fatalf("不支持的入口不该发起 RPC，实际: %v", keysOf(host.calls))
	}
}

func TestDSHChatApprovalOutcomeTranslation(t *testing.T) {
	cases := []struct {
		name     string
		response string
		wantKind string
		wantVal  any
		wantErr  bool
	}{
		{name: "允许一次", response: `{"decision":"accept"}`, wantKind: "result", wantVal: "allowed-once"},
		{name: "拒绝", response: `{"decision":"decline"}`, wantKind: "result", wantVal: "rejected"},
		{name: "DSH 原生值", response: `{"decision":"rejected"}`, wantKind: "result", wantVal: "rejected"},
		// 会话级授权在 DSH 没有对应 outcome：必须报错，不能悄悄降级成"允许一次"，
		// 那会让用户以为授权范围比实际更大。
		{name: "会话级授权无对应", response: `{"decision":"acceptForSession"}`, wantErr: true},
		{name: "缺 decision", response: `{}`, wantErr: true},
		{name: "坏 JSON", response: `not json`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dshApprovalOutcome(json.RawMessage(tc.response))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，得到 %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got["kind"] != tc.wantKind {
				t.Fatalf("kind = %v, want %v", got["kind"], tc.wantKind)
			}
			if got["value"] != tc.wantVal {
				t.Fatalf("value = %v, want %v", got["value"], tc.wantVal)
			}
		})
	}
}

func TestDSHChatRespondRequiresWaterfall(t *testing.T) {
	host := newRecordingHost(t, map[string]any{"sessionId": "session-1"})
	// 让 $events 流建立后立刻结束：拿不到 clientId 就不能作答。
	host.host.streamReply = func(endpoint, streamID string, send func(map[string]any)) {
		if endpoint == "$events" {
			send(map[string]any{"type": "end", "streamId": streamID})
		}
	}
	b := newTestDSHBackend(t, host.host)

	err := b.Respond(context.Background(), "dsh", "session-1", "req-1", json.RawMessage(`{"decision":"accept"}`))
	if err == nil {
		t.Fatal("没有 clientId 时必须报错，不能发出一个无效的回答案")
	}
}

func TestDSHChatRespondPostsOutcomeWithClientID(t *testing.T) {
	host := newRecordingHost(t, map[string]any{"sessionId": "session-1"})
	host.host.streamReply = func(endpoint, streamID string, send func(map[string]any)) {
		if endpoint == "$events" {
			send(map[string]any{
				"type": "item", "streamId": streamID,
				"value": map[string]any{"type": "ready", "clientId": "client-abc", "host": map[string]any{}},
			})
		}
	}
	b := newTestDSHBackend(t, host.host)

	if err := b.Respond(context.Background(), "dsh", "session-1", "req-1", json.RawMessage(`{"decision":"accept"}`)); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	args := host.calls["$events/result"]
	if args == nil {
		t.Fatal("没有发出 $events/result")
	}
	if args["clientId"] != "client-abc" {
		t.Fatalf("clientId = %v, want client-abc（host 靠它定位事件流）", args["clientId"])
	}
	if args["eventId"] != "req-1" {
		t.Fatalf("eventId = %v", args["eventId"])
	}
	outcome, _ := args["outcome"].(map[string]any)
	if outcome["kind"] != "result" || outcome["value"] != "allowed-once" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestDSHChatEventsDeliversLiveFrames(t *testing.T) {
	host := newRecordingHost(t, map[string]any{"sessionId": "session-1"})
	host.host.streamReply = func(endpoint, streamID string, send func(map[string]any)) {
		if endpoint != "session/follow" {
			return
		}
		// 首帧是快照，随后跟一条真实形状的 text-delta。
		send(map[string]any{
			"type": "item", "streamId": streamID,
			"value": map[string]any{"type": "snapshot", "cursor": 10, "records": []any{}, "hasMore": false},
		})
		send(map[string]any{
			"type": "item", "streamId": streamID,
			"value": map[string]any{
				"type": "event",
				"event": map[string]any{
					"type": "assistant/chunk", "seq": 11, "time": 1,
					"data": map[string]any{
						"turn": 1, "step": 1,
						"chunk": map[string]any{"type": "text-delta", "index": 0, "text": "你好"},
					},
				},
			},
		})
	}
	b := newTestDSHBackend(t, host.host)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := b.Events(ctx, "dsh", "session-1")
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	deadline := time.After(4 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("订阅通道被关闭")
			}
			if ev.Type == "assistant_delta" {
				var data map[string]any
				_ = json.Unmarshal(ev.Data, &data)
				if data["delta"] != "你好" {
					t.Fatalf("delta = %v", data["delta"])
				}
				if ev.ItemID != "turn-1-step-1-block-0" {
					t.Fatalf("itemId = %q", ev.ItemID)
				}
				return
			}
		case <-deadline:
			t.Fatal("4 秒内没有收到 assistant_delta")
		}
	}
}

func TestDSHChatControlReflectsTurnState(t *testing.T) {
	host := newRecordingHost(t, map[string]any{"sessionId": "session-1"})
	b := newTestDSHBackend(t, host.host)

	state, err := b.Control(context.Background(), "dsh", "session-1")
	if err != nil {
		t.Fatalf("Control: %v", err)
	}
	if state.Status != "idle" {
		t.Fatalf("status = %q, want idle", state.Status)
	}
	// 写入方在 host 侧；TurnOwner 留空表示没有排他归属，浏览器 steer 是安全的。
	if state.WriterOwner != "dsh-host" || state.TurnOwner != "" {
		t.Fatalf("state = %+v", state)
	}

	b.trackTurnState(b.session("session-1"), []ChatEvent{
		{Type: "turn_started", TurnID: "turn-2"},
	})
	state, _ = b.Control(context.Background(), "dsh", "session-1")
	if state.Status != "active" || state.ActiveTurnID != "turn-2" {
		t.Fatalf("state = %+v", state)
	}
	b.trackTurnState(b.session("session-1"), []ChatEvent{{Type: "turn_done", TurnID: "turn-2"}})
	state, _ = b.Control(context.Background(), "dsh", "session-1")
	if state.Status != "idle" || state.ActiveTurnID != "" {
		t.Fatalf("turn 结束后 state = %+v", state)
	}
}

func TestDSHImagePartOnlyAcceptsHostMediaTypes(t *testing.T) {
	// host 只接受这四种 MIME；其它类型必须被跳过而不是硬塞一个假 mediaType。
	for _, mime := range []string{"application/pdf", "image/tiff", ""} {
		part, err := dshImagePart(ChatAttachment{MIME: mime, Path: "/tmp/whatever"})
		if err != nil || part != nil {
			t.Fatalf("mime=%q 应被跳过，得到 part=%+v err=%v", mime, part, err)
		}
	}
	// 图片文件的读取失败要冒泡，不能静默丢附件。
	if _, err := dshImagePart(ChatAttachment{MIME: "image/png", Path: "/nonexistent/x.png"}); err == nil {
		t.Fatal("读不到文件时应报错")
	}
	// 非绝对路径拒绝：Path 是 agent 本地路径，相对路径含义不明确。
	if _, err := dshImagePart(ChatAttachment{MIME: "image/png", Path: "rel.png"}); err == nil {
		t.Fatal("相对路径应被拒绝")
	}
}

func TestDSHAttachmentPartUsesACPResourceLinkForAnyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("pdf-ish"), 0o600); err != nil {
		t.Fatal(err)
	}
	part, err := dshAttachmentPart(ChatAttachment{Name: "季度 报告.pdf", MIME: "application/pdf", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if part["type"] != "resource_link" || part["name"] != "季度 报告.pdf" || part["mimeType"] != "application/pdf" {
		t.Fatalf("resource link = %#v", part)
	}
	if uri, _ := part["uri"].(string); !strings.HasPrefix(uri, "file://") || !strings.Contains(uri, "report.pdf") {
		t.Fatalf("resource URI = %q", uri)
	}
}

func TestDSHFirstLine(t *testing.T) {
	if got := dshFirstLine("{\"a\":1}\nsecond"); got != `{"a":1}` {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("x", 300)
	if got := dshFirstLine(long); len(got) > 210 {
		t.Fatalf("超长参数应被截断，长度 %d", len(got))
	}
}
