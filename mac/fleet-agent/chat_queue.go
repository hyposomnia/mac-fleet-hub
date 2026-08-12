package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	chatQueueQueued               = "queued"
	chatQueueWaitingWriter        = "waiting_writer"
	chatQueueTakeoverCheck        = "takeover_check"
	chatQueueTakeoverConfirmation = "takeover_confirmation_required"
	chatQueueTakingOver           = "taking_over"
	chatQueueSending              = "sending"
	chatQueueSent                 = "sent"
	chatQueueFailed               = "failed"
	chatQueueCancelled            = "cancelled"
)

var errChatQueueStateConflict = errors.New("queue_state_conflict")

type ChatTakeoverImpact struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title,omitempty"`
	TurnID    string `json:"turnId,omitempty"`
	Active    bool   `json:"active"`
}

type ChatQueueItem struct {
	ID              string               `json:"id"`
	ClientMessageID string               `json:"clientMessageId"`
	Assistant       string               `json:"assistant"`
	SessionID       string               `json:"sessionId"`
	Cwd             string               `json:"cwd,omitempty"`
	Text            string               `json:"text,omitempty"`
	DisplayText     string               `json:"displayText,omitempty"`
	Images          []ChatAttachment     `json:"images,omitempty"`
	Skills          []ChatSkill          `json:"skills,omitempty"`
	Options         ChatTurnOptions      `json:"options,omitempty"`
	Status          string               `json:"status"`
	Decision        string               `json:"decision,omitempty"`
	AuditVersion    string               `json:"auditVersion,omitempty"`
	Affected        []ChatTakeoverImpact `json:"affected,omitempty"`
	TurnID          string               `json:"turnId,omitempty"`
	Error           string               `json:"error,omitempty"`
	CreatedAt       int64                `json:"createdAt"`
	UpdatedAt       int64                `json:"updatedAt"`
	AttemptedAt     int64                `json:"attemptedAt,omitempty"`
	SentAt          int64                `json:"sentAt,omitempty"`
}

type chatQueueDisk struct {
	Version int             `json:"version"`
	Items   []ChatQueueItem `json:"items"`
}

type chatQueue struct {
	mu    sync.Mutex
	path  string
	items map[string]ChatQueueItem
}

type chatQueueSender interface {
	Send(ChatQueueItem) (string, error)
}

type chatQueueWorker struct {
	queue  *chatQueue
	sender chatQueueSender
	wake   chan struct{}
}

type backendChatQueueSender struct{ backend chatBackend }

func (s backendChatQueueSender) Send(item ChatQueueItem) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	images := make([]ChatAttachment, 0, len(item.Images))
	for _, stored := range item.Images {
		attachment, err := resolveChatUpload(item.SessionID, stored.ID)
		if err != nil {
			return "", err
		}
		images = append(images, attachment)
	}
	skills := make([]ChatSkill, 0, len(item.Skills))
	requested := make([]struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}, 0, len(item.Skills))
	for _, skill := range item.Skills {
		requested = append(requested, struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}{ID: skill.ID, Name: skill.Name})
	}
	resolved, err := resolveRequestedChatSkills(ctx, item.Assistant, item.Cwd, requested)
	if err != nil {
		return "", err
	}
	skills = append(skills, resolved...)
	options := item.Options
	options.ClientUserMessageID = item.ClientMessageID
	result, err := s.backend.Input(ctx, item.Assistant, item.SessionID, item.Text, images, skills, options)
	return result.TurnID, err
}

var agentChatQueue = &chatQueue{items: map[string]ChatQueueItem{}}
var agentChatQueueWorker *chatQueueWorker
var agentChatTakeover *chatTakeoverService

func newChatQueueWorker(queue *chatQueue, sender chatQueueSender) *chatQueueWorker {
	return &chatQueueWorker{queue: queue, sender: sender, wake: make(chan struct{}, 1)}
}

func (w *chatQueueWorker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *chatQueueWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
			w.processOne()
		case <-ticker.C:
			w.processOne()
		}
	}
}

func (w *chatQueueWorker) processOne() {
	items := w.queue.List("", "")
	for _, item := range items {
		if item.Status != chatQueueQueued && item.Status != chatQueueWaitingWriter {
			continue
		}
		_, _ = w.queue.update(item.ID, func(current *ChatQueueItem) error {
			current.Status, current.AttemptedAt, current.Error = chatQueueSending, time.Now().UnixMilli(), ""
			return nil
		})
		turnID, err := w.sender.Send(item)
		if errors.Is(err, errExternalChatTurn) || errors.Is(err, errThreadReadOnly) {
			_, _ = w.queue.update(item.ID, func(current *ChatQueueItem) error {
				current.Status, current.Error = chatQueueWaitingWriter, ""
				return nil
			})
			return
		}
		if err != nil {
			_, _ = w.queue.update(item.ID, func(current *ChatQueueItem) error {
				current.Status, current.Error = chatQueueFailed, err.Error()
				return nil
			})
			return
		}
		_, _ = w.queue.update(item.ID, func(current *ChatQueueItem) error {
			current.Status, current.TurnID, current.SentAt = chatQueueSent, turnID, time.Now().UnixMilli()
			return nil
		})
		return
	}
}

func (q *chatQueue) get(id string) (ChatQueueItem, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[id]
	return item, ok
}

func handleChatQueue(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		assistant := normAssistant(r.URL.Query().Get("assistant"))
		sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
		if assistant != "codex" || sessionID == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]interface{}{"items": agentChatQueue.List(assistant, sessionID)})
	case http.MethodPost:
		var item ChatQueueItem
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&item) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		stored, err := agentChatQueue.Enqueue(item)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_queue_item", err.Error())
			return
		}
		if agentChatQueueWorker != nil {
			agentChatQueueWorker.Wake()
		}
		writeJSON(w, stored)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleChatQueueDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		ID           string `json:"id"`
		Action       string `json:"action"`
		AuditVersion string `json:"auditVersion"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || req.ID == "" {
		http.Error(w, "bad request", 400)
		return
	}
	item, err := agentChatTakeover.Decide(req.ID, req.Action, req.AuditVersion)
	if errors.Is(err, errChatQueueStateConflict) {
		writeErr(w, 409, "queue_state_conflict", "队列状态或接管审计已变化，请重新确认。")
		return
	}
	if err != nil {
		writeErr(w, 500, "takeover_failed", err.Error())
		return
	}
	writeJSON(w, item)
}

func openChatQueue(path string) (*chatQueue, error) {
	q := &chatQueue{path: path, items: map[string]ChatQueueItem{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return q, nil
	}
	if err != nil {
		return nil, err
	}
	var disk chatQueueDisk
	if err := json.Unmarshal(raw, &disk); err != nil {
		return nil, fmt.Errorf("decode chat queue: %w", err)
	}
	for _, item := range disk.Items {
		if item.ID != "" {
			q.items[item.ID] = item
		}
	}
	return q, nil
}

func newChatQueueID() string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return "queue-" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("queue-%d", time.Now().UnixNano())
}

func (q *chatQueue) Enqueue(input ChatQueueItem) (ChatQueueItem, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	input.ClientMessageID = strings.TrimSpace(input.ClientMessageID)
	input.Assistant = normAssistant(input.Assistant)
	input.SessionID = strings.TrimSpace(input.SessionID)
	if input.ClientMessageID == "" || input.Assistant != "codex" || input.SessionID == "" {
		return ChatQueueItem{}, errors.New("invalid queue item")
	}
	for _, item := range q.items {
		if item.ClientMessageID == input.ClientMessageID && item.Assistant == input.Assistant && item.SessionID == input.SessionID {
			return item, nil
		}
	}
	now := time.Now().UnixMilli()
	input.ID, input.Status, input.CreatedAt, input.UpdatedAt = newChatQueueID(), chatQueueQueued, now, now
	q.items[input.ID] = input
	if err := q.saveLocked(); err != nil {
		delete(q.items, input.ID)
		return ChatQueueItem{}, err
	}
	return input, nil
}

func (q *chatQueue) List(assistant, sessionID string) []ChatQueueItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := make([]ChatQueueItem, 0)
	for _, item := range q.items {
		if (assistant == "" || item.Assistant == assistant) && (sessionID == "" || item.SessionID == sessionID) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt < items[j].CreatedAt })
	return items
}

func (q *chatQueue) RequireTakeoverConfirmation(id, audit string, affected []ChatTakeoverImpact) (ChatQueueItem, error) {
	return q.update(id, func(item *ChatQueueItem) error {
		if item.Status == chatQueueSent || item.Status == chatQueueCancelled {
			return errChatQueueStateConflict
		}
		item.Status, item.Decision, item.AuditVersion = chatQueueTakeoverConfirmation, "force", audit
		item.Affected = append([]ChatTakeoverImpact(nil), affected...)
		return nil
	})
}

func (q *chatQueue) ConfirmTakeover(id, audit string) (ChatQueueItem, error) {
	return q.update(id, func(item *ChatQueueItem) error {
		if item.Status != chatQueueTakeoverConfirmation || audit == "" || audit != item.AuditVersion {
			return errChatQueueStateConflict
		}
		item.Status, item.Decision = chatQueueTakingOver, "confirm-force"
		return nil
	})
}

func (q *chatQueue) update(id string, mutate func(*ChatQueueItem) error) (ChatQueueItem, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[id]
	if !ok {
		return ChatQueueItem{}, os.ErrNotExist
	}
	if err := mutate(&item); err != nil {
		return ChatQueueItem{}, err
	}
	item.UpdatedAt = time.Now().UnixMilli()
	q.items[id] = item
	if err := q.saveLocked(); err != nil {
		return ChatQueueItem{}, err
	}
	return item, nil
}

func (q *chatQueue) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(q.path), 0o700); err != nil {
		return err
	}
	disk := chatQueueDisk{Version: 1, Items: make([]ChatQueueItem, 0, len(q.items))}
	for _, item := range q.items {
		disk.Items = append(disk.Items, item)
	}
	sort.Slice(disk.Items, func(i, j int) bool { return disk.Items[i].CreatedAt < disk.Items[j].CreatedAt })
	raw, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(q.path), ".chat-queue-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(append(raw, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, q.path)
}
