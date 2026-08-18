package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChatQueuePersistsServerAuthoritativeAccessMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q, err := openChatQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := q.AccessMode("codex", "thread-1"); got != chatAccessReadWrite {
		t.Fatalf("default access = %q", got)
	}
	if _, err := q.SetAccessMode("codex", "thread-1", chatAccessReadOnly); err != nil {
		t.Fatal(err)
	}
	reloaded, err := openChatQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.AccessMode("codex", "thread-1"); got != chatAccessReadOnly {
		t.Fatalf("reloaded access = %q", got)
	}
}

func TestChatQueueReadOnlyImmediatelyProjectsWaitingAccess(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	existing, _ := q.Enqueue(ChatQueueItem{
		ClientMessageID: "existing", Assistant: "codex", SessionID: "thread-1", Text: "one",
	})
	if _, err := q.SetAccessMode("codex", "thread-1", chatAccessReadOnly); err != nil {
		t.Fatal(err)
	}
	blocked, _ := q.get(existing.ID)
	if blocked.Status != chatQueueWaitingAccess {
		t.Fatalf("existing status=%q", blocked.Status)
	}
	created, err := q.Enqueue(ChatQueueItem{
		ClientMessageID: "new", Assistant: "codex", SessionID: "thread-1", Text: "two",
	})
	if err != nil || created.Status != chatQueueWaitingAccess {
		t.Fatalf("new item=%+v err=%v", created, err)
	}
	service := newChatTakeoverService(q, &fakeTakeoverController{}, nil)
	if _, err := service.Decide(created.ID, "retry", "", created.StateVersion); !errors.Is(err, errFleetChatReadOnly) {
		t.Fatalf("read-only decision err=%v", err)
	}
	if _, err := service.Decide(created.ID, "cancel", "", created.StateVersion); err != nil {
		t.Fatalf("read-only cancel err=%v", err)
	}
}

func TestChatQueueAllowedActionsAreServerDerived(t *testing.T) {
	cases := []struct {
		status string
		access string
		want   string
	}{
		{chatQueueQueued, chatAccessReadWrite, "cancel"},
		{chatQueueSteering, chatAccessReadWrite, ""},
		{chatQueueWaitingTurn, chatAccessReadWrite, "steer,cancel"},
		{chatQueueWaitingTurn, chatAccessReadOnly, "cancel"},
		{chatQueueWriterConfirmation, chatAccessReadWrite, "wait,force,cancel"},
		{chatQueueWriterConfirmation, chatAccessReadOnly, "wait,cancel"},
		{chatQueueWaitingWriter, chatAccessReadWrite, "force,cancel"},
		{chatQueueWaitingWriter, chatAccessReadOnly, "cancel"},
		{chatQueueWaitingAccess, chatAccessReadOnly, "enable-write,cancel"},
		{chatQueueTakeoverCheck, chatAccessReadWrite, ""},
		{chatQueueTakeoverConfirmation, chatAccessReadWrite, "confirm-force,wait,cancel"},
		{chatQueueTakeoverConfirmation, chatAccessReadOnly, "wait,cancel"},
		{chatQueueTakingOver, chatAccessReadWrite, ""},
		{chatQueueSending, chatAccessReadWrite, ""},
		{chatQueueRecovering, chatAccessReadWrite, ""},
		{chatQueueUncertain, chatAccessReadWrite, "retry,cancel"},
		{chatQueueFailed, chatAccessReadWrite, "retry,cancel"},
		{chatQueueFailed, chatAccessReadOnly, "cancel"},
		{chatQueueSent, chatAccessReadWrite, ""},
		{chatQueueCancelled, chatAccessReadWrite, ""},
	}
	for _, tc := range cases {
		if got := strings.Join(chatQueueAllowedActions(tc.status, tc.access), ","); got != tc.want {
			t.Errorf("status=%s access=%s actions=%q want=%q", tc.status, tc.access, got, tc.want)
		}
	}
}

func TestChatQueueRestartMovesInflightDeliveryToRecovering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q, _ := openChatQueue(path)
	item, _ := q.Enqueue(ChatQueueItem{
		ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1",
		Text: "hello", DeliveryMode: chatDeliveryAuto,
	})
	_, err := q.updateExpected(item.ID, []string{chatQueueQueued}, func(current *ChatQueueItem) error {
		current.Status = chatQueueSteering
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	reloaded, err := openChatQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reloaded.get(item.ID)
	if got.Status != chatQueueRecovering {
		t.Fatalf("inflight restart status = %q", got.Status)
	}
}

func TestChatQueueRestartDoesNotLeaveTakeoverCheckStuck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q, _ := openChatQueue(path)
	item, _ := q.Enqueue(ChatQueueItem{
		ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1", Text: "hello",
	})
	if _, err := q.updateExpected(item.ID, []string{chatQueueQueued}, func(current *ChatQueueItem) error {
		current.Status = chatQueueTakeoverCheck
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := openChatQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reloaded.get(item.ID)
	if got.Status != chatQueueFailed || got.Error == "" {
		t.Fatalf("stuck takeover recovery=%+v", got)
	}
}

func TestChatQueueEnqueueStripsBrowserOwnedState(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	item, err := q.Enqueue(ChatQueueItem{
		ID: "browser-id", ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1", Text: "hello",
		WriterOwner: "desktop", Status: chatQueueSent, StateVersion: 99, Decision: "confirm-force",
		AuditVersion: "forged", Affected: []ChatTakeoverImpact{{SessionID: "victim", Active: true}},
		TurnID: "forged-turn", Error: "forged", CreatedAt: 1, UpdatedAt: 2, AttemptedAt: 3, SentAt: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "browser-id" || item.WriterOwner != "" || item.Status != chatQueueQueued || item.StateVersion != 1 ||
		item.Decision != "" || item.AuditVersion != "" || len(item.Affected) != 0 || item.TurnID != "" || item.Error != "" ||
		item.CreatedAt <= 4 || item.AttemptedAt != 0 || item.SentAt != 0 {
		t.Fatalf("browser state was trusted: %+v", item)
	}
}

func TestChatQueueRejectsEmptyOrInvalidPersistentMessages(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	for _, item := range []ChatQueueItem{
		{ClientMessageID: "empty", Assistant: "codex", SessionID: "thread-1", Text: "  "},
		{ClientMessageID: "bad-image", Assistant: "codex", SessionID: "thread-1", Images: []ChatAttachment{{ID: ""}}},
	} {
		if _, err := q.Enqueue(item); err == nil {
			t.Fatalf("invalid item accepted: %+v", item)
		}
	}
	if got := q.List("codex", "thread-1"); len(got) != 0 {
		t.Fatalf("invalid items were persisted: %+v", got)
	}
}

type blockingChatQueueSender struct {
	entered chan struct{}
	release chan struct{}
}

func (s *blockingChatQueueSender) Deliver(ChatQueueItem) (chatQueueDeliveryResult, error) {
	close(s.entered)
	<-s.release
	return chatQueueDeliveryResult{TurnID: "turn-1", Delivery: chatDeliveryStart}, nil
}
func (*blockingChatQueueSender) Recover(ChatQueueItem) (chatQueueDeliveryResult, bool, error) {
	return chatQueueDeliveryResult{}, false, nil
}

func TestChatQueueAccessTransitionSerializesWithDelivery(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	item, _ := q.Enqueue(ChatQueueItem{
		ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1", Text: "hello", DeliveryMode: chatDeliveryNext,
	})
	sender := &blockingChatQueueSender{entered: make(chan struct{}), release: make(chan struct{})}
	workerDone := make(chan struct{})
	go func() {
		newChatQueueWorker(q, sender).processOne()
		close(workerDone)
	}()
	<-sender.entered
	accessDone := make(chan error, 1)
	go func() {
		_, err := q.SetAccessMode("codex", "thread-1", chatAccessReadOnly)
		accessDone <- err
	}()
	select {
	case err := <-accessDone:
		t.Fatalf("access transition passed in-flight delivery: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(sender.release)
	<-workerDone
	if err := <-accessDone; err != nil {
		t.Fatal(err)
	}
	got, _ := q.get(item.ID)
	if got.Status != chatQueueSent || q.AccessMode("codex", "thread-1") != chatAccessReadOnly {
		t.Fatalf("item=%+v access=%q", got, q.AccessMode("codex", "thread-1"))
	}
}

func TestChatQueueClaimUsesAccessModeAndCAS(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	first, _ := q.Enqueue(ChatQueueItem{
		ClientMessageID: "first", Assistant: "codex", SessionID: "thread-1",
		Text: "one", DeliveryMode: chatDeliveryAuto,
	})
	_, _ = q.SetAccessMode("codex", "thread-1", chatAccessReadOnly)
	if _, ok, err := q.ClaimNext(); err != nil || ok {
		t.Fatalf("read-only claim ok=%v err=%v", ok, err)
	}
	blocked, _ := q.get(first.ID)
	if blocked.Status != chatQueueWaitingAccess {
		t.Fatalf("read-only status = %q", blocked.Status)
	}
	if _, err := q.SetAccessMode("codex", "thread-1", chatAccessReadWrite); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := q.ClaimNext()
	if err != nil || !ok || claimed.Status != chatQueueSteering {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	if _, err := q.updateExpected(first.ID, []string{chatQueueQueued}, func(*ChatQueueItem) error { return nil }); !errors.Is(err, errChatQueueStateConflict) {
		t.Fatalf("stale transition err=%v", err)
	}
}

func TestChatQueueDecisionRejectsCancelOnceDeliveryClaimed(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	item, _ := q.Enqueue(ChatQueueItem{
		ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1",
		Text: "hello", DeliveryMode: chatDeliveryNext,
	})
	claimed, ok, err := q.ClaimNext()
	if err != nil || !ok || claimed.Status != chatQueueSending {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	service := newChatTakeoverService(q, &fakeTakeoverController{}, nil)
	if _, err := service.Decide(item.ID, "cancel", "", item.StateVersion); !errors.Is(err, errChatQueueStateConflict) {
		t.Fatalf("cancel claimed delivery err=%v", err)
	}
}

func TestChatQueueDecisionRejectsStaleBrowserStateVersion(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	item, _ := q.Enqueue(ChatQueueItem{
		ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1", Text: "hello",
	})
	current, err := q.updateExpected(item.ID, []string{chatQueueQueued}, func(current *ChatQueueItem) error {
		current.Status = chatQueueWaitingTurn
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	service := newChatTakeoverService(q, &fakeTakeoverController{}, nil)
	if _, err := service.Decide(item.ID, "cancel", "", item.StateVersion); !errors.Is(err, errChatQueueStateConflict) {
		t.Fatalf("stale version accepted: current=%d stale=%d err=%v", current.StateVersion, item.StateVersion, err)
	}
	got, _ := q.get(item.ID)
	if got.Status != chatQueueWaitingTurn {
		t.Fatalf("stale action mutated item: %+v", got)
	}
}

func TestBackendQueueSenderAutoSteersAndFallsBackToStartOnlyWhenInactive(t *testing.T) {
	steerCalls, inputCalls := 0, 0
	sender := backendChatQueueSender{backend: fakeChatBackend{
		steerFn: func(context.Context, string, string, string, string, []ChatAttachment, []ChatSkill) (ChatInputResult, error) {
			steerCalls++
			return ChatInputResult{TurnID: "turn-live"}, nil
		},
		inputFn: func(context.Context, string, string, string, []ChatAttachment, []ChatSkill, ChatTurnOptions) (ChatInputResult, error) {
			inputCalls++
			return ChatInputResult{TurnID: "turn-next"}, nil
		},
	}}
	result, err := sender.Deliver(ChatQueueItem{
		ClientMessageID: "auto-1", Assistant: "codex", SessionID: "thread-1",
		Text: "guide", DeliveryMode: chatDeliveryAuto,
	})
	if err != nil || result.Delivery != chatDeliverySteer || result.TurnID != "turn-live" || steerCalls != 1 || inputCalls != 0 {
		t.Fatalf("steer result=%+v err=%v calls=%d/%d", result, err, steerCalls, inputCalls)
	}

	sender.backend = fakeChatBackend{
		steerFn: func(context.Context, string, string, string, string, []ChatAttachment, []ChatSkill) (ChatInputResult, error) {
			return ChatInputResult{}, errNoActiveChatTurn
		},
		inputFn: func(context.Context, string, string, string, []ChatAttachment, []ChatSkill, ChatTurnOptions) (ChatInputResult, error) {
			return ChatInputResult{TurnID: "turn-next"}, nil
		},
	}
	result, err = sender.Deliver(ChatQueueItem{
		ClientMessageID: "auto-2", Assistant: "codex", SessionID: "thread-1",
		Text: "next", DeliveryMode: chatDeliveryAuto,
	})
	if err != nil || result.Delivery != chatDeliveryStart || result.TurnID != "turn-next" {
		t.Fatalf("fallback result=%+v err=%v", result, err)
	}
}

func TestBackendQueueSenderAppliesApprovalModeBeforeSteer(t *testing.T) {
	var calls []string
	sender := backendChatQueueSender{backend: fakeChatBackend{
		settingsFn: func(_ context.Context, assistant, sessionID, approvalMode string) error {
			calls = append(calls, "settings:"+approvalMode)
			if assistant != "codex" || sessionID != "thread-1" {
				t.Fatalf("settings identity assistant=%q session=%q", assistant, sessionID)
			}
			return nil
		},
		steerFn: func(context.Context, string, string, string, string, []ChatAttachment, []ChatSkill) (ChatInputResult, error) {
			calls = append(calls, "steer")
			return ChatInputResult{TurnID: "turn-live"}, nil
		},
	}}

	result, err := sender.Deliver(ChatQueueItem{
		ClientMessageID: "auto-approval", Assistant: "codex", SessionID: "thread-1",
		Text: "guide", DeliveryMode: chatDeliveryAuto,
		Options: ChatTurnOptions{ApprovalMode: "on-request"},
	})
	if err != nil || result.Delivery != chatDeliverySteer {
		t.Fatalf("deliver result=%+v err=%v", result, err)
	}
	if got, want := strings.Join(calls, ","), "settings:on-request,steer"; got != want {
		t.Fatalf("call order=%q want=%q", got, want)
	}
}

func TestChatQueuePersistsAndDeduplicatesClientMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q, err := openChatQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	in := ChatQueueItem{ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1", Text: "hello", WriterOwner: "fleet"}
	one, err := q.Enqueue(in)
	if err != nil {
		t.Fatal(err)
	}
	two, err := q.Enqueue(in)
	if err != nil {
		t.Fatal(err)
	}
	if one.ID != two.ID {
		t.Fatalf("duplicate item: %q != %q", one.ID, two.ID)
	}
	reloaded, err := openChatQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	items := reloaded.List("codex", "thread-1")
	if len(items) != 1 || items[0].Text != "hello" || items[0].WriterOwner != "" {
		t.Fatalf("reloaded=%+v", items)
	}
}

type fakeChatQueueSender struct {
	calls      int
	err        error
	recoverErr error
	last       ChatQueueItem
}

func (f *fakeChatQueueSender) Deliver(item ChatQueueItem) (chatQueueDeliveryResult, error) {
	f.calls++
	f.last = item
	if f.err != nil {
		return chatQueueDeliveryResult{}, f.err
	}
	return chatQueueDeliveryResult{TurnID: "turn-1", Delivery: chatDeliveryStart}, nil
}
func (f *fakeChatQueueSender) Recover(ChatQueueItem) (chatQueueDeliveryResult, bool, error) {
	return chatQueueDeliveryResult{}, false, f.recoverErr
}

func TestChatQueueRecoveryErrorRemainsUncertainInsteadOfBlindRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q, _ := openChatQueue(path)
	item, _ := q.Enqueue(ChatQueueItem{
		ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1", Text: "hello",
	})
	if _, err := q.updateExpected(item.ID, []string{chatQueueQueued}, func(current *ChatQueueItem) error {
		current.Status = chatQueueSending
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	q, _ = openChatQueue(path)
	newChatQueueWorker(q, &fakeChatQueueSender{recoverErr: errors.New("history unavailable")}).processOne()
	got, _ := q.get(item.ID)
	if got.Status != chatQueueUncertain || !strings.Contains(got.Error, "history unavailable") {
		t.Fatalf("recovery error was treated as safe failure: %+v", got)
	}
}

func TestChatQueueForcedDeliveryFailsInsteadOfReturningToWait(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	item, _ := q.Enqueue(ChatQueueItem{ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1", Text: "hello"})
	item, _ = q.updateExpected(item.ID, []string{chatQueueQueued}, func(item *ChatQueueItem) error {
		item.Decision = "force"
		return nil
	})
	sender := &fakeChatQueueSender{err: errExternalChatTurn}
	newChatQueueWorker(q, sender).processOne()
	got, _ := q.get(item.ID)
	if got.Status != chatQueueFailed || got.Error == "" {
		t.Fatalf("forced item silently returned to waiting: %+v", got)
	}
}

func TestChatQueueWorkerPersistsWaitingAndSendsWithoutBrowser(t *testing.T) {
	q, err := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := q.Enqueue(ChatQueueItem{ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	sender := &fakeChatQueueSender{err: errExternalChatTurn}
	w := newChatQueueWorker(q, sender)
	w.processOne()
	got, _ := q.get(item.ID)
	if got.Status != chatQueueWriterConfirmation || got.WriterOwner != "desktop" {
		t.Fatalf("status=%q", got.Status)
	}
	service := newChatTakeoverService(q, &fakeTakeoverController{}, w.Wake)
	if _, err := service.Decide(item.ID, "wait", "", got.StateVersion); err != nil {
		t.Fatal(err)
	}
	sender.err = nil
	w.processOne()
	got, _ = q.get(item.ID)
	if got.Status != chatQueueSent || got.TurnID != "turn-1" || sender.calls != 2 {
		t.Fatalf("item=%+v calls=%d", got, sender.calls)
	}
	reloaded, err := openChatQueue(q.path)
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := reloaded.get(item.ID)
	if persisted.Status != chatQueueSent {
		t.Fatalf("persisted=%+v", persisted)
	}
	_ = time.Second
}

func TestChatQueueWaitsForFleetTurnWithoutOfferingTakeoverState(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	item, _ := q.Enqueue(ChatQueueItem{ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1", Text: "hello"})
	sender := &fakeChatQueueSender{err: errFleetChatTurnRunning}
	worker := newChatQueueWorker(q, sender)
	worker.processOne()
	got, _ := q.get(item.ID)
	if got.Status != chatQueueWaitingTurn || got.WriterOwner != "fleet" {
		t.Fatalf("Fleet-owned turn became external writer state: %+v", got)
	}
	sender.err = nil
	worker.processOne()
	got, _ = q.get(item.ID)
	if got.Status != chatQueueSent {
		t.Fatalf("queued follow-up was not sent after Fleet turn: %+v", got)
	}
}

func TestChatQueueHandlersPersistBeyondRequest(t *testing.T) {
	previous := agentChatQueue
	q, err := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	agentChatQueue = q
	t.Cleanup(func() { agentChatQueue = previous })
	body := bytes.NewBufferString(`{"assistant":"codex","sessionId":"thread-1","clientMessageId":"client-1","text":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/queue", body)
	rr := httptest.NewRecorder()
	handleChatQueue(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var item ChatQueueItem
	if json.Unmarshal(rr.Body.Bytes(), &item) != nil || item.Status != chatQueueQueued {
		t.Fatalf("item=%+v", item)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/chat/queue?assistant=codex&sessionId=thread-1", nil)
	rr = httptest.NewRecorder()
	handleChatQueue(rr, req)
	if rr.Code != http.StatusOK || !bytes.Contains(rr.Body.Bytes(), []byte(`"clientMessageId":"client-1"`)) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatQueueVisibleListExcludesTerminalAuditRecords(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	queued, _ := q.Enqueue(ChatQueueItem{ClientMessageID: "queued", Assistant: "codex", SessionID: "thread-1", Text: "queued"})
	sent, _ := q.Enqueue(ChatQueueItem{ClientMessageID: "sent", Assistant: "codex", SessionID: "thread-1", Text: "sent"})
	cancelled, _ := q.Enqueue(ChatQueueItem{ClientMessageID: "cancelled", Assistant: "codex", SessionID: "thread-1", Text: "cancelled"})
	_, _ = q.update(sent.ID, func(item *ChatQueueItem) error { item.Status = chatQueueSent; return nil })
	_, _ = q.update(cancelled.ID, func(item *ChatQueueItem) error { item.Status = chatQueueCancelled; return nil })
	visible := q.ListVisible("codex", "thread-1")
	if len(visible) != 1 || visible[0].ID != queued.ID {
		t.Fatalf("visible=%+v", visible)
	}
	if audit := q.List("codex", "thread-1"); len(audit) != 3 {
		t.Fatalf("audit records lost: %+v", audit)
	}
}

func TestChatQueueForceRequiresMatchingAuditConfirmation(t *testing.T) {
	q, err := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := q.Enqueue(ChatQueueItem{ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	item, err = q.updateExpected(item.ID, []string{chatQueueQueued}, func(item *ChatQueueItem) error {
		item.Status = chatQueueTakeoverCheck
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	affected := []ChatTakeoverImpact{{SessionID: "other", Title: "Other", Active: true}}
	item, err = q.RequireTakeoverConfirmation(item.ID, "audit-1", affected)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != chatQueueTakeoverConfirmation {
		t.Fatalf("status=%q", item.Status)
	}
	if _, err := q.ConfirmTakeover(item.ID, "stale"); err == nil {
		t.Fatal("stale audit accepted")
	}
	item, err = q.ConfirmTakeover(item.ID, "audit-1")
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != chatQueueTakingOver {
		t.Fatalf("status=%q", item.Status)
	}
}

type fakeTakeoverController struct {
	impacts  []ChatTakeoverImpact
	version  string
	restarts int
	err      error
}

func (f *fakeTakeoverController) Audit() ([]ChatTakeoverImpact, string, error) {
	return f.impacts, f.version, nil
}
func (f *fakeTakeoverController) Restart() error { f.restarts++; return f.err }

func TestChatQueueForceAuditsBeforeRestartAndRequiresConfirmation(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	item, _ := q.Enqueue(ChatQueueItem{ClientMessageID: "c", Assistant: "codex", SessionID: "s", Text: "x"})
	item, _ = q.updateExpected(item.ID, []string{chatQueueQueued}, func(item *ChatQueueItem) error {
		item.Status = chatQueueWriterConfirmation
		return nil
	})
	controller := &fakeTakeoverController{version: "v1", impacts: []ChatTakeoverImpact{{SessionID: "active", Active: true}}}
	service := newChatTakeoverService(q, controller, nil)
	got, err := service.Decide(item.ID, "force", "", item.StateVersion)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != chatQueueTakeoverConfirmation || controller.restarts != 0 {
		t.Fatalf("item=%+v restarts=%d", got, controller.restarts)
	}
	got, err = service.Decide(item.ID, "confirm-force", "v1", got.StateVersion)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != chatQueueQueued || controller.restarts != 1 {
		t.Fatalf("item=%+v restarts=%d", got, controller.restarts)
	}
}

func TestChatQueuePersistsFailedTakeover(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	item, _ := q.Enqueue(ChatQueueItem{ClientMessageID: "c", Assistant: "codex", SessionID: "s", Text: "x"})
	item, _ = q.updateExpected(item.ID, []string{chatQueueQueued}, func(item *ChatQueueItem) error {
		item.Status = chatQueueWriterConfirmation
		return nil
	})
	controller := &fakeTakeoverController{version: "v1", err: errors.New("restart unavailable")}
	service := newChatTakeoverService(q, controller, nil)
	if _, err := service.Decide(item.ID, "force", "", item.StateVersion); err == nil {
		t.Fatal("restart failure was ignored")
	}
	got, _ := q.get(item.ID)
	if got.Status != chatQueueFailed || got.Error == "" {
		t.Fatalf("item=%+v", got)
	}
}

func TestSystemTakeoverControllerAuditsAndStopsOnlyExternalWriters(t *testing.T) {
	previousExternal := codexExternalWriterSessions
	previousFleet := codexFleetWriterSessions
	previousState := codexActiveRolloutTaskState
	previousRestart := stopCodexExternalWriters
	t.Cleanup(func() {
		codexExternalWriterSessions = previousExternal
		codexFleetWriterSessions = previousFleet
		codexActiveRolloutTaskState = previousState
		stopCodexExternalWriters = previousRestart
	})
	codexExternalWriterSessions = func() []string { return []string{"thread-a", "thread-b"} }
	codexFleetWriterSessions = func() []string { return []string{"thread-fleet-active"} }
	codexActiveRolloutTaskState = func(sessionID string) (codexRolloutTaskState, bool) {
		return codexRolloutTaskState{turnID: "turn-" + sessionID, terminal: sessionID == "thread-b"}, true
	}
	restarted := 0
	stopCodexExternalWriters = func() error { restarted++; return nil }
	controller := systemTakeoverController{}
	impacts, version, err := controller.Audit()
	if err != nil || version == "" || len(impacts) != 2 || impacts[0].SessionID != "thread-a" || !impacts[0].Active || impacts[1].SessionID != "thread-b" || impacts[1].Active {
		t.Fatalf("impacts=%+v version=%q err=%v", impacts, version, err)
	}
	for _, impact := range impacts {
		if impact.SessionID == "thread-fleet-active" {
			t.Fatalf("Fleet-owned task entered takeover impact list: %+v", impacts)
		}
	}
	if err := controller.Restart(); err != nil || restarted != 1 {
		t.Fatalf("restart count=%d err=%v", restarted, err)
	}
}
