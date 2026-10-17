package main

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestChatQueueBusySessionDoesNotStarveOtherSession(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	first, _ := q.Enqueue(ChatQueueItem{ClientMessageID: "a", Assistant: "codex", SessionID: "busy", Text: "a"})
	q.update(first.ID, func(i *ChatQueueItem) error { i.CreatedAt = 1; return nil })
	second, _ := q.Enqueue(ChatQueueItem{ClientMessageID: "b", Assistant: "codex", SessionID: "idle", Text: "b"})
	sender := &fakeChatQueueSender{err: errFleetChatTurnRunning}
	worker := newChatQueueWorker(q, sender)
	worker.processOne()
	if sender.last.ID != first.ID {
		t.Fatal("oldest message must be attempted first")
	}
	sender.err = nil
	worker.processOne()
	got, _ := q.get(second.ID)
	if got.Status != chatQueueSent {
		t.Fatalf("idle session starved: %+v", got)
	}
	firstState, _ := q.get(first.ID)
	if firstState.Status != chatQueueWaitingTurn {
		t.Fatalf("busy session lost: %+v", firstState)
	}
	worker.processOne()
	firstState, _ = q.get(first.ID)
	if firstState.Status != chatQueueSent {
		t.Fatal("busy session never retried")
	}
}

func TestChatQueueExplicitSteerPassesPendingButNotInFlight(t *testing.T) {
	for _, status := range []string{chatQueueWaitingTurn, chatQueueSending, chatQueueUncertain} {
		t.Run(status, func(t *testing.T) {
			q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
			first, _ := q.Enqueue(ChatQueueItem{ClientMessageID: "a", Assistant: "codex", SessionID: "thread", Text: "a"})
			q.update(first.ID, func(i *ChatQueueItem) error { i.CreatedAt = 1; i.Status = status; return nil })
			second, _ := q.Enqueue(ChatQueueItem{ClientMessageID: "b", Assistant: "codex", SessionID: "thread", Text: "b"})
			wakes := 0
			service := newChatTakeoverService(q, &fakeTakeoverController{}, func() { wakes++ })
			steered, err := service.Decide(second.ID, "steer", "", second.StateVersion)
			if err != nil || steered.DeliveryMode != chatDeliveryAuto || wakes != 1 {
				t.Fatalf("steer=%+v err=%v wakes=%d", steered, err, wakes)
			}
			claimed, ok, err := q.ClaimNext()
			if err != nil {
				t.Fatal(err)
			}
			if status == chatQueueWaitingTurn {
				if !ok || claimed.ID != second.ID || claimed.Status != chatQueueSteering {
					t.Fatalf("steer was blocked: %+v", claimed)
				}
				if again, ok, err := q.ClaimNext(); err != nil || ok {
					t.Fatalf("concurrent delivery while steer in flight: %+v %v", again, err)
				}
				if _, err := service.Decide(second.ID, "steer", "", steered.StateVersion); !errors.Is(err, errChatQueueStateConflict) {
					t.Fatalf("in-flight steer accepted: %v", err)
				}
			} else if ok {
				t.Fatalf("unsafe delivery passed %s: %+v", status, claimed)
			}
		})
	}
}

func TestChatQueueReadOnlyQueuedCannotSteer(t *testing.T) {
	if got := chatQueueAllowedActions(chatQueueQueued, chatAccessReadOnly); len(got) != 1 || got[0] != "cancel" {
		t.Fatalf("actions=%v", got)
	}
}
