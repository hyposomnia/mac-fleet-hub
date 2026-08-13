package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
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
func (s *chatTakeoverService) Decide(id, action, audit string) (ChatQueueItem, error) {
	if action == "force" || action == "confirm-force" {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	switch action {
	case "wait":
		return s.queue.update(id, func(i *ChatQueueItem) error { i.Status = chatQueueWaitingWriter; i.Decision = "wait"; return nil })
	case "cancel":
		return s.queue.update(id, func(i *ChatQueueItem) error { i.Status = chatQueueCancelled; return nil })
	case "retry":
		return s.queue.update(id, func(i *ChatQueueItem) error { i.Status = chatQueueQueued; i.Error = ""; return nil })
	case "force":
		impacts, v, err := s.controller.Audit()
		if err != nil {
			return ChatQueueItem{}, err
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
	out, err := exec.Command("sh", "-c", `p=$(pgrep -f 'codex app-server --remote-control --listen unix://$'|head -1); test -n "$p" || exit 0; lsof -p "$p" 2>/dev/null|sed -n 's#.*thread-writer-locks/\([^ ]*\)\.lock#\1#p'`).Output()
	if err != nil && len(out) == 0 {
		return nil, "", err
	}
	var impacts []ChatTakeoverImpact
	var audit strings.Builder
	titles := make(map[string]string)
	for _, session := range scanCodexSessions() {
		titles[session.SessionID] = session.Title
	}
	for _, id := range strings.Fields(string(out)) {
		st, ok := codexCurrentRolloutTaskState(id)
		impact := ChatTakeoverImpact{SessionID: id, Title: titles[id], TurnID: st.turnID, Active: ok && !st.terminal}
		impacts = append(impacts, impact)
		fmt.Fprintf(&audit, "%s\x00%s\x00%t\n", impact.SessionID, impact.TurnID, impact.Active)
	}
	h := sha256.Sum256([]byte(audit.String()))
	return impacts, hex.EncodeToString(h[:8]), nil
}
func (systemTakeoverController) Restart() error {
	// All affected active turns were explicitly confirmed by the user.
	if err := exec.Command("sh", "-c", `p=$(pgrep -f 'codex app-server --remote-control --listen unix://$'|head -1); s=$(pgrep -f 'codex app-server daemon pid-update-loop'|head -1); test -z "$p" || kill -TERM "$p"; test -z "$s" || kill -TERM "$s"; for n in 1 2 3 4 5; do test -z "$p" || ! kill -0 "$p" 2>/dev/null || sleep 0.2; done; test -z "$p" || ! kill -0 "$p" 2>/dev/null || kill -KILL "$p"; test -z "$s" || ! kill -0 "$s" 2>/dev/null || kill -KILL "$s"`).Run(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, cfg.CodexBin, "app-server", "daemon", "bootstrap", "--remote-control").Run()
}

var _ = errors.Is
