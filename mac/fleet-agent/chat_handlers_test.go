package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeChatBackend struct {
	resumeFn    func(context.Context, string, string, string) (ChatResumeResult, error)
	inputFn     func(context.Context, string, string, string) (ChatInputResult, error)
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

func (f fakeChatBackend) Input(ctx context.Context, assistant, sessionID, text string) (ChatInputResult, error) {
	if f.inputFn != nil {
		return f.inputFn(ctx, assistant, sessionID, text)
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
		inputFn: func(ctx context.Context, assistant, sessionID, text string) (ChatInputResult, error) {
			if assistant != "codex" || sessionID != "s1" || text != "hello" {
				t.Fatalf("input args got assistant=%s sessionID=%s text=%s", assistant, sessionID, text)
			}
			return ChatInputResult{TurnID: "turn-1"}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/input", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","text":"hello"}`))
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
