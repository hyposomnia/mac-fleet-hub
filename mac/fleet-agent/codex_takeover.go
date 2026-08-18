package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	for _, id := range codexExternalWriterSessions() {
		st, ok := codexActiveRolloutTaskState(id)
		impact := ChatTakeoverImpact{SessionID: id, Title: titles[id], TurnID: st.turnID, Active: ok && !st.terminal}
		impacts = append(impacts, impact)
		fmt.Fprintf(&audit, "%s\x00%s\x00%t\n", impact.SessionID, impact.TurnID, impact.Active)
	}
	h := sha256.Sum256([]byte(audit.String()))
	return impacts, hex.EncodeToString(h[:8]), nil
}
func (systemTakeoverController) Restart() error {
	// All affected active turns were explicitly confirmed by the user. Stop the
	// actual external lock/rollout holders; restarting Fleet's isolated sidecar
	// cannot release a writer owned by Desktop or another Codex CLI.
	return stopCodexExternalWriters()
}

func systemStopCodexExternalWriters() error {
	pids := make(map[int]string)
	sessions := systemCodexExternalWriterSessions()
	for _, sessionID := range sessions {
		paths := []string{filepath.Join(cfg.CodexHome, "thread-writer-locks", sessionID+".lock")}
		if rolloutPath := jsonlPathFor("codex", sessionID); rolloutPath != "" {
			paths = append(paths, rolloutPath)
		}
		for _, path := range paths {
			holders, err := codexExternalHolderPIDs(path)
			if err != nil {
				return fmt.Errorf("inspect Codex writer for %s: %w", sessionID, err)
			}
			for pid, command := range holders {
				pids[pid] = command
			}
		}
	}
	if len(pids) == 0 {
		return nil
	}
	ordered := make([]int, 0, len(pids))
	for pid := range pids {
		ordered = append(ordered, pid)
	}
	sort.Ints(ordered)
	for _, pid := range ordered {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("stop external Codex writer pid %d: %w", pid, err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !codexSessionsHaveExternalWriter(sessions) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, pid := range ordered {
		if err := syscall.Kill(pid, 0); err == nil || errors.Is(err, syscall.EPERM) {
			if killErr := syscall.Kill(pid, syscall.SIGKILL); killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
				return fmt.Errorf("force-stop external Codex writer pid %d: %w", pid, killErr)
			}
		}
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !codexSessionsHaveExternalWriter(sessions) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("external Codex writer remained after forced takeover")
}

func codexSessionsHaveExternalWriter(sessionIDs []string) bool {
	for _, sessionID := range sessionIDs {
		if systemCodexThreadWriterProcessOwner(sessionID) == "desktop" {
			return true
		}
	}
	return false
}

func codexExternalHolderPIDs(path string) (map[int]string, error) {
	holders := make(map[int]string)
	out, err := exec.Command("lsof", "-t", path).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return holders, nil
		}
		return nil, err
	}
	for _, field := range strings.Fields(string(out)) {
		pid, convErr := strconv.Atoi(field)
		if convErr != nil || pid <= 1 || pid == os.Getpid() {
			return nil, fmt.Errorf("invalid writer pid %q", field)
		}
		command, commandErr := exec.Command("ps", "-p", field, "-o", "command=").Output()
		if commandErr != nil {
			return nil, fmt.Errorf("classify writer pid %d: %w", pid, commandErr)
		}
		commandText := strings.TrimSpace(string(command))
		if codexWriterProcessCommandOwner(commandText, cfg.CodexSock) == "desktop" {
			holders[pid] = commandText
		}
	}
	return holders, nil
}
