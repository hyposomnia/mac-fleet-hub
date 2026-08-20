package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeChatBackend struct {
	startFn     func(context.Context, string, string, string) (ChatStartResult, error)
	resumeFn    func(context.Context, string, string, string) (ChatResumeResult, error)
	historyFn   func(context.Context, string, string, string) (ChatHistoryPage, error)
	skillsFn    func(context.Context, string, string) ([]ChatSkill, error)
	inputFn     func(context.Context, string, string, string, []ChatAttachment, []ChatSkill, ChatTurnOptions) (ChatInputResult, error)
	steerFn     func(context.Context, string, string, string, string, []ChatAttachment, []ChatSkill) (ChatInputResult, error)
	eventsFn    func(context.Context, string, string) (<-chan ChatEvent, error)
	respondFn   func(context.Context, string, string, string, json.RawMessage) error
	interruptFn func(context.Context, string, string) error
	releaseFn   func(context.Context, string, string) error
	settingsFn  func(context.Context, string, string, string) error
	controlFn   func(context.Context, string, string) (ChatRuntimeState, error)
}

func (f fakeChatBackend) Control(ctx context.Context, assistant, sessionID string) (ChatRuntimeState, error) {
	if f.controlFn != nil {
		return f.controlFn(ctx, assistant, sessionID)
	}
	return ChatRuntimeState{Status: "idle"}, nil
}

func (f fakeChatBackend) Start(ctx context.Context, assistant, cwd, mode string) (ChatStartResult, error) {
	if f.startFn != nil {
		return f.startFn(ctx, assistant, cwd, mode)
	}
	return ChatStartResult{}, nil
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

func (f fakeChatBackend) Skills(ctx context.Context, assistant, cwd string) ([]ChatSkill, error) {
	if f.skillsFn != nil {
		return f.skillsFn(ctx, assistant, cwd)
	}
	return nil, nil
}

func (f fakeChatBackend) Input(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment, skills []ChatSkill, opts ChatTurnOptions) (ChatInputResult, error) {
	if f.inputFn != nil {
		return f.inputFn(ctx, assistant, sessionID, text, images, skills, opts)
	}
	return ChatInputResult{}, nil
}

func (f fakeChatBackend) Steer(ctx context.Context, assistant, sessionID, clientMessageID, text string, images []ChatAttachment, skills []ChatSkill) (ChatInputResult, error) {
	if f.steerFn != nil {
		return f.steerFn(ctx, assistant, sessionID, clientMessageID, text, images, skills)
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

func (f fakeChatBackend) Respond(ctx context.Context, assistant, sessionID, requestID string, response json.RawMessage) error {
	if f.respondFn != nil {
		return f.respondFn(ctx, assistant, sessionID, requestID, response)
	}
	return nil
}

func (f fakeChatBackend) Interrupt(ctx context.Context, assistant, sessionID string) error {
	if f.interruptFn != nil {
		return f.interruptFn(ctx, assistant, sessionID)
	}
	return nil
}

func (f fakeChatBackend) Release(ctx context.Context, assistant, sessionID string) error {
	if f.releaseFn != nil {
		return f.releaseFn(ctx, assistant, sessionID)
	}
	return nil
}

func (f fakeChatBackend) Settings(ctx context.Context, assistant, sessionID, approvalMode string) error {
	if f.settingsFn != nil {
		return f.settingsFn(ctx, assistant, sessionID, approvalMode)
	}
	return nil
}

func withChatBackend(t *testing.T, b chatBackend) {
	t.Helper()
	prev := agentChatBackend
	prevQueue := agentChatQueue
	queue, err := openChatQueue(filepath.Join(t.TempDir(), "chat-queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	agentChatBackend = b
	agentChatQueue = queue
	t.Cleanup(func() {
		agentChatBackend = prev
		agentChatQueue = prevQueue
	})
}

func TestChatStartCallsBackend(t *testing.T) {
	withChatBackend(t, fakeChatBackend{
		startFn: func(ctx context.Context, assistant, cwd, mode string) (ChatStartResult, error) {
			if assistant != "codex" || cwd != "/repo" || mode != "default" {
				t.Fatalf("start args got assistant=%s cwd=%s mode=%s", assistant, cwd, mode)
			}
			return ChatStartResult{
				SessionID: "thread-new", Cwd: "/repo", Model: "gpt-new", Effort: "high",
				Models: []ChatModelOption{{Value: "gpt-new", DisplayName: "GPT New", IsDefault: true}},
			}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/start", bytes.NewBufferString(`{"assistant":"codex","cwd":"/repo","mode":"default"}`))
	rr := httptest.NewRecorder()

	handleChatStart(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rr.Code, rr.Body.String())
	}
	var got ChatStartResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "thread-new" || got.Cwd != "/repo" {
		t.Fatalf("bad start result: %+v", got)
	}
	if got.Model != "gpt-new" || got.Effort != "high" || len(got.Models) != 1 || got.Models[0].Value != "gpt-new" {
		t.Fatalf("start options missing: %+v", got)
	}
	if got.Control == nil || got.Control.ServerEpoch == "" || got.Control.SnapshotVersion == 0 || got.Control.TurnPhase != "idle" || got.Control.AccessMode != chatAccessReadWrite {
		t.Fatalf("start control snapshot missing: %+v", got.Control)
	}
}

func TestChatStartRejectsClaude(t *testing.T) {
	withChatBackend(t, fakeChatBackend{})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/start", bytes.NewBufferString(`{"assistant":"claude","cwd":"/repo"}`))
	rr := httptest.NewRecorder()

	handleChatStart(rr, req)

	if rr.Code != http.StatusNotImplemented || !strings.Contains(rr.Body.String(), `"unsupported_assistant"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
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

func TestChatResumeProjectsServerAccessMode(t *testing.T) {
	withChatBackend(t, fakeChatBackend{resumeFn: func(context.Context, string, string, string) (ChatResumeResult, error) {
		return ChatResumeResult{SessionID: "s1", ThreadID: "s1"}, nil
	}})
	if _, err := agentChatQueue.SetAccessMode("codex", "s1", chatAccessReadOnly); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chat/resume", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1"}`))
	rr := httptest.NewRecorder()
	handleChatResume(rr, req)
	var got ChatResumeResult
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &got) != nil || got.AccessMode != chatAccessReadOnly || got.Control == nil || got.Control.AccessMode != chatAccessReadOnly {
		t.Fatalf("status=%d result=%+v body=%s", rr.Code, got, rr.Body.String())
	}
}

func TestChatQueueGetReturnsVersionedAuthoritativeControlSnapshot(t *testing.T) {
	withChatBackend(t, fakeChatBackend{controlFn: func(_ context.Context, assistant, sessionID string) (ChatRuntimeState, error) {
		if assistant != "codex" || sessionID != "s1" {
			t.Fatalf("control args assistant=%q session=%q", assistant, sessionID)
		}
		return ChatRuntimeState{
			Status: "running", ActiveTurnID: "turn-1", TurnOwner: "desktop", WriterOwner: "desktop",
			ApprovalMode: "full-access", PendingRequests: 2,
		}, nil
	}})
	queued, err := agentChatQueue.Enqueue(ChatQueueItem{ClientMessageID: "queued", Assistant: "codex", SessionID: "s1", Text: "later"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentChatQueue.SetAccessMode("codex", "s1", chatAccessReadOnly); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/chat/queue?assistant=codex&sessionId=s1", nil)
	rr := httptest.NewRecorder()
	handleChatQueue(rr, req)
	var got ChatSessionControlSnapshot
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &got) != nil {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got.ServerEpoch == "" || got.SnapshotVersion == 0 || got.AccessMode != chatAccessReadOnly ||
		got.WriterOwner != "desktop" || got.TurnOwner != "desktop" || got.TurnPhase != "running" || got.ActiveTurnID != "turn-1" ||
		got.ApprovalMode != "full-access" || got.PendingRequests != 2 || len(got.Items) != 1 || got.Items[0].ID != queued.ID ||
		strings.Join(got.Items[0].AllowedActions, ",") != "enable-write,cancel" {
		t.Fatalf("bad control snapshot: %+v", got)
	}
}

func TestChatQueueGetDefaultsInvalidBackendApprovalMode(t *testing.T) {
	withChatBackend(t, fakeChatBackend{controlFn: func(context.Context, string, string) (ChatRuntimeState, error) {
		return ChatRuntimeState{Status: "idle", ApprovalMode: "invalid-mode"}, nil
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/chat/queue?assistant=codex&sessionId=s1", nil)
	rr := httptest.NewRecorder()
	handleChatQueue(rr, req)
	var got ChatSessionControlSnapshot
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &got) != nil || got.ApprovalMode != "on-request" {
		t.Fatalf("status=%d control=%+v body=%s", rr.Code, got, rr.Body.String())
	}
}

func TestChatUploadRejectsReadOnlySessionBeforeWritingFile(t *testing.T) {
	withChatBackend(t, fakeChatBackend{})
	if _, err := agentChatQueue.SetAccessMode("codex", "s1", chatAccessReadOnly); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("assistant", "codex")
	_ = mw.WriteField("sessionId", "s1")
	fw, err := mw.CreateFormFile("file", "pixel.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chat/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	handleChatUpload(rr, req)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"fleet_read_only"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
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
		inputFn: func(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment, skills []ChatSkill, opts ChatTurnOptions) (ChatInputResult, error) {
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

func TestChatSettingsUpdatesApprovalMode(t *testing.T) {
	withChatBackend(t, fakeChatBackend{
		settingsFn: func(ctx context.Context, assistant, sessionID, approvalMode string) error {
			if assistant != "codex" || sessionID != "s1" || approvalMode != "full-access" {
				t.Fatalf("settings args got assistant=%s sessionID=%s approvalMode=%s", assistant, sessionID, approvalMode)
			}
			return nil
		},
		controlFn: func(context.Context, string, string) (ChatRuntimeState, error) {
			return ChatRuntimeState{Status: "idle", ApprovalMode: "full-access"}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/settings", bytes.NewBufferString(
		`{"assistant":"codex","sessionId":"s1","approvalMode":"full-access"}`,
	))
	rr := httptest.NewRecorder()

	handleChatSettings(rr, req)

	var control ChatSessionControlSnapshot
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &control) != nil || control.ApprovalMode != "full-access" ||
		control.ServerEpoch == "" || control.SnapshotVersion == 0 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatQueueDecisionReturnsFreshControlSnapshot(t *testing.T) {
	withChatBackend(t, fakeChatBackend{controlFn: func(context.Context, string, string) (ChatRuntimeState, error) {
		return ChatRuntimeState{Status: "idle", ApprovalMode: "on-request"}, nil
	}})
	previousTakeover := agentChatTakeover
	agentChatTakeover = newChatTakeoverService(agentChatQueue, &fakeTakeoverController{}, nil)
	t.Cleanup(func() { agentChatTakeover = previousTakeover })
	item, err := agentChatQueue.Enqueue(ChatQueueItem{
		ClientMessageID: "client-1", Assistant: "codex", SessionID: "s1", Text: "later", DeliveryMode: chatDeliveryNext,
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err = agentChatQueue.updateExpected(item.ID, []string{chatQueueQueued}, func(current *ChatQueueItem) error {
		current.Status = chatQueueWaitingTurn
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chat/queue/decision", bytes.NewBufferString(fmt.Sprintf(
		`{"id":%q,"action":"steer","stateVersion":%d}`, item.ID, item.StateVersion,
	)))
	rr := httptest.NewRecorder()
	handleChatQueueDecision(rr, req)
	var control ChatSessionControlSnapshot
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &control) != nil || control.ServerEpoch == "" || len(control.Items) != 1 ||
		control.Items[0].ID != item.ID || control.Items[0].StateVersion <= item.StateVersion {
		t.Fatalf("status=%d control=%+v body=%s", rr.Code, control, rr.Body.String())
	}
}

func TestChatSettingsRejectsUnknownApprovalMode(t *testing.T) {
	withChatBackend(t, fakeChatBackend{})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/settings", bytes.NewBufferString(
		`{"assistant":"codex","sessionId":"s1","approvalMode":"always-trust"}`,
	))
	rr := httptest.NewRecorder()

	handleChatSettings(rr, req)

	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"bad_chat_options"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatSkillsCallsBackendWithoutExposingPath(t *testing.T) {
	withChatBackend(t, fakeChatBackend{
		skillsFn: func(ctx context.Context, assistant, cwd string) ([]ChatSkill, error) {
			if assistant != "codex" || cwd != "/repo" {
				t.Fatalf("skills args assistant=%s cwd=%s", assistant, cwd)
			}
			return []ChatSkill{{
				ID: "skill-dev", Name: "dev", Description: "Develop",
				Path: "/repo/.agents/skills/dev/SKILL.md", Scope: "repo",
			}}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/skills", bytes.NewBufferString(`{"assistant":"codex","cwd":"/repo"}`))
	rr := httptest.NewRecorder()

	handleChatSkills(rr, req)

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"id":"skill-dev"`) ||
		!strings.Contains(rr.Body.String(), `"name":"dev"`) ||
		!strings.Contains(rr.Body.String(), `"description":"Develop"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "SKILL.md") {
		t.Fatalf("skill path must stay server-side: %s", rr.Body.String())
	}
}

func TestChatInputResolvesSkillNameBeforeCallingBackend(t *testing.T) {
	const skillPath = "/repo/.agents/skills/dev/SKILL.md"
	const skillID = "skill-dev"
	withChatBackend(t, fakeChatBackend{
		skillsFn: func(ctx context.Context, assistant, cwd string) ([]ChatSkill, error) {
			return []ChatSkill{{ID: skillID, Name: "dev", Path: skillPath}}, nil
		},
		inputFn: func(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment, skills []ChatSkill, opts ChatTurnOptions) (ChatInputResult, error) {
			if text != "fix it" || len(skills) != 1 || skills[0].ID != skillID ||
				skills[0].Name != "dev" || skills[0].Path != skillPath {
				t.Fatalf("resolved input text=%q skills=%+v", text, skills)
			}
			return ChatInputResult{TurnID: "turn-skill"}, nil
		},
	})
	body := `{"assistant":"codex","sessionId":"s1","cwd":"/repo","text":"fix it","skills":[{"id":"skill-dev","name":"dev"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/input", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()

	handleChatInput(rr, req)

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"turnId":"turn-skill"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatInputUsesFirstAppServerSkillForLegacyNameOnlyRequest(t *testing.T) {
	const firstPath = "/Users/test/.agents/skills/tavily/SKILL.md"
	withChatBackend(t, fakeChatBackend{
		skillsFn: func(context.Context, string, string) ([]ChatSkill, error) {
			return []ChatSkill{
				{ID: "first", Name: "tavily", Path: firstPath},
				{ID: "second", Name: "tavily", Path: "/Users/test/.codex/skills/tavily/SKILL.md"},
			}, nil
		},
		inputFn: func(_ context.Context, _, _, _ string, _ []ChatAttachment, skills []ChatSkill, _ ChatTurnOptions) (ChatInputResult, error) {
			if len(skills) != 1 || skills[0].ID != "first" || skills[0].Path != firstPath {
				t.Fatalf("legacy name must resolve to first app-server result: %+v", skills)
			}
			return ChatInputResult{TurnID: "turn-first"}, nil
		},
	})
	body := `{"assistant":"codex","sessionId":"s1","cwd":"/repo","skills":[{"name":"tavily"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/input", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()

	handleChatInput(rr, req)

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"turnId":"turn-first"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatInputRejectsUnknownSkillBeforeCallingBackend(t *testing.T) {
	inputCalled := false
	withChatBackend(t, fakeChatBackend{
		skillsFn: func(context.Context, string, string) ([]ChatSkill, error) {
			return []ChatSkill{{Name: "dev", Path: "/repo/dev/SKILL.md"}}, nil
		},
		inputFn: func(context.Context, string, string, string, []ChatAttachment, []ChatSkill, ChatTurnOptions) (ChatInputResult, error) {
			inputCalled = true
			return ChatInputResult{}, nil
		},
	})
	body := `{"assistant":"codex","sessionId":"s1","cwd":"/repo","skills":[{"name":"missing"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/input", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()

	handleChatInput(rr, req)

	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"bad_skill"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if inputCalled {
		t.Fatal("unknown skill must not reach turn/start")
	}
}

func TestChatSteerCallsBackend(t *testing.T) {
	withChatBackend(t, fakeChatBackend{
		steerFn: func(ctx context.Context, assistant, sessionID, clientMessageID, text string, images []ChatAttachment, skills []ChatSkill) (ChatInputResult, error) {
			if assistant != "codex" || sessionID != "s1" || clientMessageID != "follow-1" || text != "guide this" || len(images) != 0 {
				t.Fatalf("steer args got assistant=%s sessionID=%s clientMessageID=%s text=%s images=%+v", assistant, sessionID, clientMessageID, text, images)
			}
			return ChatInputResult{TurnID: "turn-1"}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/steer", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","clientMessageId":"follow-1","text":"guide this"}`))
	rr := httptest.NewRecorder()

	handleChatSteer(rr, req)

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"turnId":"turn-1"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatSteerWithoutActiveTurnReturnsConflict(t *testing.T) {
	withChatBackend(t, fakeChatBackend{
		steerFn: func(context.Context, string, string, string, string, []ChatAttachment, []ChatSkill) (ChatInputResult, error) {
			return ChatInputResult{}, errNoActiveChatTurn
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/steer", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","text":"guide this"}`))
	rr := httptest.NewRecorder()

	handleChatSteer(rr, req)

	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"no_active_turn"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatInterruptWithoutActiveTurnReturnsConflict(t *testing.T) {
	withChatBackend(t, fakeChatBackend{
		interruptFn: func(context.Context, string, string) error {
			return errNoActiveChatTurn
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/interrupt", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1"}`))
	rr := httptest.NewRecorder()

	handleChatInterrupt(rr, req)

	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"no_active_turn"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatReleaseCallsBackend(t *testing.T) {
	called := false
	withChatBackend(t, fakeChatBackend{
		releaseFn: func(_ context.Context, assistant, sessionID string) error {
			called = assistant == "codex" && sessionID == "s1"
			return nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/release", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1"}`))
	rr := httptest.NewRecorder()
	handleChatRelease(rr, req)
	if rr.Code != http.StatusOK || !called {
		t.Fatalf("status=%d called=%v body=%s", rr.Code, called, rr.Body.String())
	}
}

func TestChatAccessReleasePersistsReadOnlyBeforeBackendRelease(t *testing.T) {
	withChatBackend(t, fakeChatBackend{releaseFn: func(context.Context, string, string) error {
		if got := agentChatQueue.AccessMode("codex", "s1"); got != chatAccessReadOnly {
			t.Fatalf("backend release observed accessMode=%q", got)
		}
		return nil
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/access", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","action":"release"}`))
	rr := httptest.NewRecorder()
	handleChatAccess(rr, req)
	var control ChatSessionControlSnapshot
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &control) != nil || agentChatQueue.AccessMode("codex", "s1") != chatAccessReadOnly ||
		control.ServerEpoch == "" || control.SnapshotVersion == 0 || control.AccessMode != chatAccessReadOnly || control.TurnPhase != "idle" {
		t.Fatalf("status=%d mode=%q body=%s", rr.Code, agentChatQueue.AccessMode("codex", "s1"), rr.Body.String())
	}
}

func TestChatAccessReleaseFailureRemainsReadOnlyAndBlocksDirectMutation(t *testing.T) {
	withChatBackend(t, fakeChatBackend{releaseFn: func(context.Context, string, string) error {
		return errors.New("unsubscribe failed")
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/access", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","action":"release"}`))
	rr := httptest.NewRecorder()
	handleChatAccess(rr, req)
	if rr.Code != http.StatusInternalServerError || agentChatQueue.AccessMode("codex", "s1") != chatAccessReadOnly {
		t.Fatalf("status=%d mode=%q body=%s", rr.Code, agentChatQueue.AccessMode("codex", "s1"), rr.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/chat/input", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","text":"must not send"}`))
	rr = httptest.NewRecorder()
	handleChatInput(rr, req)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"fleet_read_only"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatAccessEnableWriteRequeuesWaitingAccess(t *testing.T) {
	withChatBackend(t, fakeChatBackend{})
	if _, err := agentChatQueue.SetAccessMode("codex", "s1", chatAccessReadOnly); err != nil {
		t.Fatal(err)
	}
	item, err := agentChatQueue.Enqueue(ChatQueueItem{ClientMessageID: "queued", Assistant: "codex", SessionID: "s1", Text: "later"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := agentChatQueue.ClaimNext(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chat/access", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","action":"enable-write"}`))
	rr := httptest.NewRecorder()
	handleChatAccess(rr, req)
	got, _ := agentChatQueue.get(item.ID)
	if rr.Code != http.StatusOK || got.Status != chatQueueQueued || agentChatQueue.AccessMode("codex", "s1") != chatAccessReadWrite {
		t.Fatalf("status=%d item=%+v body=%s", rr.Code, got, rr.Body.String())
	}
}

func TestChatAccessReleaseSerializesWithDirectWriteEndpoint(t *testing.T) {
	inputEntered := make(chan struct{})
	finishInput := make(chan struct{})
	releaseEntered := make(chan struct{})
	withChatBackend(t, fakeChatBackend{
		inputFn: func(context.Context, string, string, string, []ChatAttachment, []ChatSkill, ChatTurnOptions) (ChatInputResult, error) {
			close(inputEntered)
			<-finishInput
			return ChatInputResult{TurnID: "turn-1"}, nil
		},
		releaseFn: func(context.Context, string, string) error {
			close(releaseEntered)
			return nil
		},
	})
	inputReq := httptest.NewRequest(http.MethodPost, "/api/chat/input", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","text":"hello"}`))
	inputRR := httptest.NewRecorder()
	inputDone := make(chan struct{})
	go func() {
		handleChatInput(inputRR, inputReq)
		close(inputDone)
	}()
	<-inputEntered
	accessReq := httptest.NewRequest(http.MethodPost, "/api/chat/access", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","action":"release"}`))
	accessRR := httptest.NewRecorder()
	accessDone := make(chan struct{})
	go func() {
		handleChatAccess(accessRR, accessReq)
		close(accessDone)
	}()
	select {
	case <-releaseEntered:
		t.Fatal("release crossed an in-flight write operation")
	case <-time.After(20 * time.Millisecond):
	}
	close(finishInput)
	<-inputDone
	<-accessDone
	if inputRR.Code != http.StatusOK || accessRR.Code != http.StatusOK || agentChatQueue.AccessMode("codex", "s1") != chatAccessReadOnly {
		t.Fatalf("input=%d access=%d mode=%q", inputRR.Code, accessRR.Code, agentChatQueue.AccessMode("codex", "s1"))
	}
}

func TestChatAccessTransitionsSerializeThroughBackendRelease(t *testing.T) {
	releaseEntered := make(chan struct{})
	finishRelease := make(chan struct{})
	withChatBackend(t, fakeChatBackend{releaseFn: func(context.Context, string, string) error {
		close(releaseEntered)
		<-finishRelease
		return nil
	}})
	releaseReq := httptest.NewRequest(http.MethodPost, "/api/chat/access", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","action":"release"}`))
	releaseRR := httptest.NewRecorder()
	releaseDone := make(chan struct{})
	go func() {
		handleChatAccess(releaseRR, releaseReq)
		close(releaseDone)
	}()
	<-releaseEntered

	enableReq := httptest.NewRequest(http.MethodPost, "/api/chat/access", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","action":"enable-write"}`))
	enableRR := httptest.NewRecorder()
	enableDone := make(chan struct{})
	go func() {
		handleChatAccess(enableRR, enableReq)
		close(enableDone)
	}()
	select {
	case <-enableDone:
		t.Fatal("enable-write crossed an in-flight backend release")
	case <-time.After(20 * time.Millisecond):
	}
	if got := agentChatQueue.AccessMode("codex", "s1"); got != chatAccessReadOnly {
		t.Fatalf("access mode changed during release: %q", got)
	}

	close(finishRelease)
	<-releaseDone
	<-enableDone
	if releaseRR.Code != http.StatusOK || enableRR.Code != http.StatusOK || agentChatQueue.AccessMode("codex", "s1") != chatAccessReadWrite {
		t.Fatalf("release=%d enable=%d mode=%q", releaseRR.Code, enableRR.Code, agentChatQueue.AccessMode("codex", "s1"))
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
		inputFn: func(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment, skills []ChatSkill, opts ChatTurnOptions) (ChatInputResult, error) {
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
	if buffering := rr.Header().Get("X-Accel-Buffering"); buffering != "no" {
		t.Fatalf("X-Accel-Buffering got %q", buffering)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "data: ") || !strings.Contains(body, `"assistant_delta"`) || !strings.Contains(body, `"delta":"ok"`) {
		t.Fatalf("bad sse body: %s", body)
	}
}

func TestChatRespondCallsBackend(t *testing.T) {
	withChatBackend(t, fakeChatBackend{
		respondFn: func(ctx context.Context, assistant, sessionID, requestID string, response json.RawMessage) error {
			if assistant != "codex" || sessionID != "s1" || requestID != "42" || string(response) != `{"decision":"accept"}` {
				t.Fatalf("respond args got assistant=%s sessionID=%s requestID=%s response=%s", assistant, sessionID, requestID, response)
			}
			return nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/respond", bytes.NewBufferString(`{"assistant":"codex","sessionId":"s1","requestId":"42","response":{"decision":"accept"}}`))
	rr := httptest.NewRecorder()

	handleChatRespond(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status got %d body %s", rr.Code, rr.Body.String())
	}
}

func TestChatMediaServesOnlyRegularPreviewMedia(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "pixel.png")
	// PNG signature plus IHDR marker is enough for net/http content sniffing.
	if err := os.WriteFile(imagePath, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0600); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/chat/media?path="+urlQueryEscape(imagePath), nil)
	rr := httptest.NewRecorder()
	handleChatMedia(rr, req)
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("image response status=%d content-type=%q body=%q", rr.Code, rr.Header().Get("Content-Type"), rr.Body.String())
	}

	audioPath := filepath.Join(dir, "voice.wav")
	if err := os.WriteFile(audioPath, []byte("RIFF\x24\x00\x00\x00WAVEfmt "), 0600); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/chat/media?path="+urlQueryEscape(audioPath), nil)
	req.Header.Set("Range", "bytes=0-3")
	rr = httptest.NewRecorder()
	handleChatMedia(rr, req)
	if rr.Code != http.StatusPartialContent || rr.Header().Get("Content-Type") != "audio/wav" || rr.Body.String() != "RIFF" {
		t.Fatalf("audio response status=%d content-type=%q body=%q", rr.Code, rr.Header().Get("Content-Type"), rr.Body.String())
	}
	if rr.Header().Get("Accept-Ranges") != "bytes" || rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("audio response headers=%v", rr.Header())
	}

	textPath := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(textPath, []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/chat/media?path="+urlQueryEscape(textPath), nil)
	rr = httptest.NewRecorder()
	handleChatMedia(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text response status got %d", rr.Code)
	}
}
