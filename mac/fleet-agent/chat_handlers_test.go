package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeChatBackend struct {
	resumeFn    func(context.Context, string, string, string) (ChatResumeResult, error)
	historyFn   func(context.Context, string, string, string) (ChatHistoryPage, error)
	inputFn     func(context.Context, string, string, string, []ChatAttachment, ChatTurnOptions) (ChatInputResult, error)
	eventsFn    func(context.Context, string, string) (<-chan ChatEvent, error)
	approveFn   func(context.Context, string, string, string, string) error
	interruptFn func(context.Context, string, string) error
}

func (f fakeChatBackend) Resume(ctx context.Context, assistant, sessionID, mode string) (ChatResumeResult, error) {
	if f.resumeFn != nil {
		return f.resumeFn(ctx, assistant, sessionID, mode)
	}
	return ChatResumeResult{}, nil
}

func (f fakeChatBackend) History(ctx context.Context, assistant, sessionID, cursor string) (ChatHistoryPage, error) {
	if f.historyFn != nil {
		return f.historyFn(ctx, assistant, sessionID, cursor)
	}
	return ChatHistoryPage{}, nil
}

func (f fakeChatBackend) Input(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment, opts ChatTurnOptions) (ChatInputResult, error) {
	if f.inputFn != nil {
		return f.inputFn(ctx, assistant, sessionID, text, images, opts)
	}
	return ChatInputResult{}, nil
}

func (f fakeChatBackend) Events(ctx context.Context, assistant, sessionID string) (<-chan ChatEvent, error) {
	if f.eventsFn != nil {
		return f.eventsFn(ctx, assistant, sessionID)
	}
	ch := make(chan ChatEvent)
	close(ch)
	return ch, nil
}

func (f fakeChatBackend) Approve(ctx context.Context, assistant, sessionID, requestID, decision string) error {
	if f.approveFn != nil {
		return f.approveFn(ctx, assistant, sessionID, requestID, decision)
	}
	return nil
}

func (f fakeChatBackend) Interrupt(ctx context.Context, assistant, sessionID string) error {
	if f.interruptFn != nil {
		return f.interruptFn(ctx, assistant, sessionID)
	}
	return nil
}

func withChatBackend(t *testing.T, b chatBackend) {
	t.Helper()
	prev := agentChatBackend
	agentChatBackend = b
	t.Cleanup(func() { agentChatBackend = prev })
}

func TestChatResumeRejectsClaude(t *testing.T) {
	withChatBackend(t, fakeChatBackend{})
	body := bytes.NewBufferString(`{"assistant":"claude","sessionId":"s1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/resume", body)
	rr := httptest.NewRecorder()

	handleChatResume(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status got %d body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"unsupported_assistant"`) {
		t.Fatalf("body missing unsupported assistant: %s", rr.Body.String())
	}
}

func TestChatResumeUnavailableReturnsStructured503(t *testing.T) {
	withChatBackend(t, unavailableChatBackend{})
	body := bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/resume", body)
	rr := httptest.NewRecorder()

	handleChatResume(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status got %d body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"appserver_unavailable"`) {
		t.Fatalf("body missing appserver unavailable: %s", rr.Body.String())
	}
}

func TestChatResumeBadRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/chat/resume", bytes.NewBufferString(`{"assistant":"codex"}`))
	rr := httptest.NewRecorder()

	handleChatResume(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status got %d body %s", rr.Code, rr.Body.String())
	}
}

func TestChatInputCallsBackend(t *testing.T) {
	withChatBackend(t, fakeChatBackend{
		inputFn: func(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment, opts ChatTurnOptions) (ChatInputResult, error) {
			if assistant != "codex" || sessionID != "s1" || text != "hello" {
				t.Fatalf("input args got assistant=%s sessionID=%s text=%s", assistant, sessionID, text)
			}
			if opts.Model != "gpt-test" || opts.Effort != "high" || opts.ServiceTier == nil || *opts.ServiceTier != "priority" || opts.ApprovalMode != "on-request" {
				t.Fatalf("input opts got %+v", opts)
			}
			return ChatInputResult{TurnID: "turn-1"}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/input", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","text":"hello","model":"gpt-test","effort":"high","serviceTier":"priority","approvalMode":"on-request"}`))
	rr := httptest.NewRecorder()

	handleChatInput(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rr.Code, rr.Body.String())
	}
	var got ChatInputResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.TurnID != "turn-1" {
		t.Fatalf("turn id got %q", got.TurnID)
	}
}

func TestChatUploadAndImageOnlyInput(t *testing.T) {
	tmp := t.TempDir()
	prevRoot := chatUploadRoot
	chatUploadRoot = func() string { return tmp }
	t.Cleanup(func() { chatUploadRoot = prevRoot })

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("assistant", "codex")
	_ = mw.WriteField("sessionId", "s1")
	fw, err := mw.CreateFormFile("file", "shot.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0})
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chat/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()

	handleChatUpload(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("upload status got %d body %s", rr.Code, rr.Body.String())
	}
	var upload ChatAttachment
	if err := json.Unmarshal(rr.Body.Bytes(), &upload); err != nil {
		t.Fatal(err)
	}
	if upload.ID == "" || upload.URL == "" || upload.Path != "" {
		t.Fatalf("bad upload response: %+v", upload)
	}

	withChatBackend(t, fakeChatBackend{
		inputFn: func(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment, opts ChatTurnOptions) (ChatInputResult, error) {
			if text != "" || len(images) != 1 {
				t.Fatalf("input got text=%q images=%+v", text, images)
			}
			if _, err := os.Stat(images[0].Path); err != nil {
				t.Fatalf("image path not resolved: %v", err)
			}
			if filepath.Base(images[0].Path) != upload.ID {
				t.Fatalf("image path got %s want basename %s", images[0].Path, upload.ID)
			}
			return ChatInputResult{TurnID: "turn-img"}, nil
		},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/chat/input", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","images":[{"id":"`+upload.ID+`"}]}`))
	rr = httptest.NewRecorder()
	handleChatInput(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("input status got %d body %s", rr.Code, rr.Body.String())
	}
}

func TestChatHistoryCallsBackendWithCursor(t *testing.T) {
	withChatBackend(t, fakeChatBackend{
		historyFn: func(ctx context.Context, assistant, sessionID, cursor string) (ChatHistoryPage, error) {
			if assistant != "codex" || sessionID != "s1" || cursor != "older-1" {
				t.Fatalf("history args got assistant=%s sessionID=%s cursor=%s", assistant, sessionID, cursor)
			}
			return ChatHistoryPage{Events: []ChatEvent{newChatEvent("assistant_done", "codex", "s1", "t1", "a1", map[string]string{"text": "old"})}}, nil
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/chat/history?assistant=codex&sessionId=s1&cursor=older-1", nil)
	rr := httptest.NewRecorder()
	handleChatHistory(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"assistant_done"`) {
		t.Fatalf("history response status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatInputRejectsUnknownApprovalMode(t *testing.T) {
	withChatBackend(t, fakeChatBackend{})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/input", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","text":"hello","approvalMode":"always-trust"}`))
	rr := httptest.NewRecorder()
	handleChatInput(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"bad_chat_options"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatUploadRejectsNonImage(t *testing.T) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("assistant", "codex")
	_ = mw.WriteField("sessionId", "s1")
	fw, err := mw.CreateFormFile("file", "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(fw, "not an image")
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chat/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()

	handleChatUpload(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status got %d body %s", rr.Code, rr.Body.String())
	}
}

func TestChatEventsStreamsSSE(t *testing.T) {
	withChatBackend(t, fakeChatBackend{
		eventsFn: func(ctx context.Context, assistant, sessionID string) (<-chan ChatEvent, error) {
			ch := make(chan ChatEvent, 1)
			ch <- newChatEvent("assistant_delta", "codex", sessionID, "turn-1", "item-1", map[string]string{"delta": "ok"})
			close(ch)
			return ch, nil
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/chat/events?assistant=codex&sessionId=s1", nil)
	rr := httptest.NewRecorder()

	handleChatEvents(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type got %q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "data: ") || !strings.Contains(body, `"assistant_delta"`) || !strings.Contains(body, `"delta":"ok"`) {
		t.Fatalf("bad sse body: %s", body)
	}
}

func TestChatApproveCallsBackend(t *testing.T) {
	withChatBackend(t, fakeChatBackend{
		approveFn: func(ctx context.Context, assistant, sessionID, requestID, decision string) error {
			if assistant != "codex" || sessionID != "s1" || requestID != "42" || decision != "approved" {
				t.Fatalf("approve args got assistant=%s sessionID=%s requestID=%s decision=%s", assistant, sessionID, requestID, decision)
			}
			return nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/approve", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","requestId":"42","decision":"approved"}`))
	rr := httptest.NewRecorder()

	handleChatApprove(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rr.Code, rr.Body.String())
	}
}
