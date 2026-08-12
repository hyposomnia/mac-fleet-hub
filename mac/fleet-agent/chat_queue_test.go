package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestChatQueuePersistsAndDeduplicatesClientMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q, err := openChatQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	in := ChatQueueItem{ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1", Text: "hello"}
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
	if len(items) != 1 || items[0].Text != "hello" {
		t.Fatalf("reloaded=%+v", items)
	}
}

type fakeChatQueueSender struct {
	calls int
	err   error
	last  ChatQueueItem
}

func (f *fakeChatQueueSender) Send(item ChatQueueItem) (string, error) {
	f.calls++
	f.last = item
	if f.err != nil {
		return "", f.err
	}
	return "turn-1", nil
}

func TestChatQueueForcedDeliveryFailsInsteadOfReturningToWait(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	item, _ := q.Enqueue(ChatQueueItem{ClientMessageID: "client-1", Assistant: "codex", SessionID: "thread-1", Text: "hello", Decision: "force"})
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
	if got.Status != chatQueueWaitingWriter {
		t.Fatalf("status=%q", got.Status)
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
	if got.Status != chatQueueWaitingTurn {
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
	controller := &fakeTakeoverController{version: "v1", impacts: []ChatTakeoverImpact{{SessionID: "active", Active: true}}}
	service := newChatTakeoverService(q, controller, nil)
	got, err := service.Decide(item.ID, "force", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != chatQueueTakeoverConfirmation || controller.restarts != 0 {
		t.Fatalf("item=%+v restarts=%d", got, controller.restarts)
	}
	got, err = service.Decide(item.ID, "confirm-force", "v1")
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
	controller := &fakeTakeoverController{version: "v1", err: errors.New("restart unavailable")}
	service := newChatTakeoverService(q, controller, nil)
	if _, err := service.Decide(item.ID, "force", ""); err == nil {
		t.Fatal("restart failure was ignored")
	}
	got, _ := q.get(item.ID)
	if got.Status != chatQueueFailed || got.Error == "" {
		t.Fatalf("item=%+v", got)
	}
}
