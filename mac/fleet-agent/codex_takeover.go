package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
)

type chatTakeoverController interface {
	Audit() ([]ChatTakeoverImpact, string, error)
	Restart() error
}
type chatTakeoverService struct {
	queue      *chatQueue
	controller chatTakeoverController
	wake       func()
	mu         sync.Mutex
}

func newChatTakeoverService(q *chatQueue, c chatTakeoverController, w func()) *chatTakeoverService {
	return &chatTakeoverService{queue: q, controller: c, wake: w}
}
func (s *chatTakeoverService) Decide(id, action, audit string, expectedVersion int64) (ChatQueueItem, error) {
	current, ok := s.queue.get(id)
	if !ok {
		return ChatQueueItem{}, errChatQueueStateConflict
	}
	if expectedVersion > 0 && current.StateVersion != expectedVersion {
		return ChatQueueItem{}, errChatQueueStateConflict
	}
	operation := s.queue.sessionOperation(current.Assistant, current.SessionID)
	operation.Lock()
	defer operation.Unlock()
	current, ok = s.queue.get(id)
	if !ok {
		return ChatQueueItem{}, errChatQueueStateConflict
	}
	if s.queue.AccessMode(current.Assistant, current.SessionID) == chatAccessReadOnly && action != "cancel" && action != "wait" {
		return ChatQueueItem{}, errFleetChatReadOnly
	}
	if action == "force" || action == "confirm-force" {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	switch action {
	case "wait":
		item, err := s.queue.updateExpected(id, []string{chatQueueWriterConfirmation, chatQueueWaitingWriter, chatQueueTakeoverConfirmation}, func(i *ChatQueueItem) error {
			i.Status, i.Decision, i.Affected, i.AuditVersion = chatQueueWaitingWriter, "wait", nil, ""
			return nil
		})
		if err == nil && s.wake != nil {
			s.wake()
		}
		return item, err
	case "cancel":
		return s.queue.updateExpected(id, []string{
			chatQueueQueued, chatQueueWaitingWriter, chatQueueWriterConfirmation, chatQueueWaitingTurn,
			chatQueueWaitingAccess, chatQueueTakeoverConfirmation, chatQueueUncertain, chatQueueFailed,
		}, func(i *ChatQueueItem) error { i.Status = chatQueueCancelled; return nil })
	case "retry":
		item, err := s.queue.updateExpected(id, []string{chatQueueFailed, chatQueueUncertain}, func(i *ChatQueueItem) error {
			i.Status, i.Error, i.Decision = chatQueueQueued, "", ""
			return nil
		})
		if err == nil && s.wake != nil {
			s.wake()
		}
		return item, err
	case "steer":
		item, err := s.queue.updateExpected(id, []string{chatQueueWaitingTurn}, func(i *ChatQueueItem) error {
			i.Status, i.DeliveryMode, i.Error = chatQueueQueued, chatDeliveryAuto, ""
			return nil
		})
		if err == nil && s.wake != nil {
			s.wake()
		}
		return item, err
	case "force":
		if _, err := s.queue.updateExpected(id, []string{chatQueueWriterConfirmation, chatQueueWaitingWriter}, func(i *ChatQueueItem) error {
			i.Status, i.Decision = chatQueueTakeoverCheck, "force"
			return nil
		}); err != nil {
			return ChatQueueItem{}, err
		}
		impacts, v, err := s.controller.Audit()
		if err != nil {
			return s.takeoverFailed(id, err)
		}
		active := false
		for _, x := range impacts {
			active = active || x.Active
		}
		if active {
			return s.queue.RequireTakeoverConfirmation(id, v, impacts)
		}
		if err = s.controller.Restart(); err != nil {
			return s.takeoverFailed(id, err)
		}
	case "confirm-force":
		_, v, err := s.controller.Audit()
		if err != nil {
			return ChatQueueItem{}, err
		}
		if v != audit {
			return ChatQueueItem{}, errChatQueueStateConflict
		}
		if _, err = s.queue.ConfirmTakeover(id, audit); err != nil {
			return ChatQueueItem{}, err
		}
		if err = s.controller.Restart(); err != nil {
			return s.takeoverFailed(id, err)
		}
	default:
		return ChatQueueItem{}, errChatQueueStateConflict
	}
	item, err := s.queue.update(id, func(i *ChatQueueItem) error {
		expected := chatQueueTakeoverCheck
		if action == "confirm-force" {
			expected = chatQueueTakingOver
		}
		if i.Status != expected {
			return errChatQueueStateConflict
		}
		i.Status = chatQueueQueued
		i.Decision = action
		i.Affected = nil
		return nil
	})
	if err == nil && s.wake != nil {
		s.wake()
	}
	return item, err
}

func (s *chatTakeoverService) takeoverFailed(id string, cause error) (ChatQueueItem, error) {
	item, saveErr := s.queue.update(id, func(i *ChatQueueItem) error {
		i.Status, i.Error = chatQueueFailed, "强制接管失败："+cause.Error()
		return nil
	})
	if saveErr != nil {
		return ChatQueueItem{}, fmt.Errorf("%v; persist takeover failure: %w", cause, saveErr)
	}
	return item, cause
}

type systemTakeoverController struct{}

func (systemTakeoverController) Audit() ([]ChatTakeoverImpact, string, error) {
	var impacts []ChatTakeoverImpact
	var audit strings.Builder
	titles := make(map[string]string)
	for _, session := range scanCodexSessions() {
		titles[session.SessionID] = session.Title
	}
	for _, id := range codexFleetWriterSessions() {
		st, ok := codexActiveRolloutTaskState(id)
		impact := ChatTakeoverImpact{SessionID: id, Title: titles[id], TurnID: st.turnID, Active: ok && !st.terminal}
		impacts = append(impacts, impact)
		fmt.Fprintf(&audit, "%s\x00%s\x00%t\n", impact.SessionID, impact.TurnID, impact.Active)
	}
	h := sha256.Sum256([]byte(audit.String()))
	return impacts, hex.EncodeToString(h[:8]), nil
}
func (systemTakeoverController) Restart() error {
	// All affected active turns were explicitly confirmed by the user. Restart
	// the launchd-owned isolated sidecar instead of guessing process command
	// lines; the lock scan above is the source of truth for affected sessions.
	return restartCodexFleetSidecar()
}
