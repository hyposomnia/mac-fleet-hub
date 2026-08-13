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
	chatQueueSteering             = "steering"
	chatQueueWaitingWriter        = "waiting_writer"
	chatQueueWriterConfirmation   = "writer_confirmation_required"
	chatQueueWaitingTurn          = "waiting_turn"
	chatQueueWaitingAccess        = "waiting_access"
	chatQueueTakeoverCheck        = "takeover_check"
	chatQueueTakeoverConfirmation = "takeover_confirmation_required"
	chatQueueTakingOver           = "taking_over"
	chatQueueSending              = "sending"
	chatQueueRecovering           = "recovering"
	chatQueueUncertain            = "uncertain"
	chatQueueSent                 = "sent"
	chatQueueFailed               = "failed"
	chatQueueCancelled            = "cancelled"

	chatDeliveryAuto  = "auto"
	chatDeliveryNext  = "next"
	chatDeliverySteer = "steer"
	chatDeliveryStart = "start"

	chatAccessReadWrite = "read_write"
	chatAccessReadOnly  = "read_only"
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
	DeliveryMode    string               `json:"deliveryMode"`
	Delivery        string               `json:"delivery,omitempty"`
	WriterOwner     string               `json:"writerOwner,omitempty"`
	Status          string               `json:"status"`
	StateVersion    int64                `json:"stateVersion"`
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

type ChatSessionState struct {
	AccessMode string `json:"accessMode"`
	UpdatedAt  int64  `json:"updatedAt"`
}

type chatQueueDisk struct {
	Version  int                         `json:"version"`
	Sessions map[string]ChatSessionState `json:"sessions,omitempty"`
	Items    []ChatQueueItem             `json:"items"`
}

type chatQueue struct {
	mu         sync.Mutex
	opMu       sync.Mutex
	path       string
	items      map[string]ChatQueueItem
	sessions   map[string]ChatSessionState
	sessionOps map[string]*sync.Mutex
}

type chatQueueSender interface {
	Deliver(ChatQueueItem) (chatQueueDeliveryResult, error)
	Recover(ChatQueueItem) (chatQueueDeliveryResult, bool, error)
}

type chatQueueDeliveryResult struct {
	TurnID   string
	Delivery string
}

type chatQueueWorker struct {
	queue  *chatQueue
	sender chatQueueSender
	wake   chan struct{}
}

type backendChatQueueSender struct{ backend chatBackend }

func (s backendChatQueueSender) resolve(item ChatQueueItem) ([]ChatAttachment, []ChatSkill, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	images := make([]ChatAttachment, 0, len(item.Images))
	for _, stored := range item.Images {
		attachment, err := resolveChatUpload(item.SessionID, stored.ID)
		if err != nil {
			return nil, nil, err
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
		return nil, nil, err
	}
	skills = append(skills, resolved...)
	return images, skills, nil
}

func (s backendChatQueueSender) Deliver(item ChatQueueItem) (chatQueueDeliveryResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	images, skills, err := s.resolve(item)
	if err != nil {
		return chatQueueDeliveryResult{}, err
	}
	if item.DeliveryMode == chatDeliveryAuto {
		result, steerErr := s.backend.Steer(ctx, item.Assistant, item.SessionID, item.ClientMessageID, item.Text, images, skills)
		if steerErr == nil {
			return chatQueueDeliveryResult{TurnID: result.TurnID, Delivery: chatDeliverySteer}, nil
		}
		if !errors.Is(steerErr, errNoActiveChatTurn) {
			return chatQueueDeliveryResult{}, steerErr
		}
	}
	options := item.Options
	options.ClientUserMessageID = item.ClientMessageID
	options.ForceTakeover = item.Decision == "force" || item.Decision == "confirm-force"
	result, err := s.backend.Input(ctx, item.Assistant, item.SessionID, item.Text, images, skills, options)
	return chatQueueDeliveryResult{TurnID: result.TurnID, Delivery: chatDeliveryStart}, err
}

func (s backendChatQueueSender) Recover(item ChatQueueItem) (chatQueueDeliveryResult, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cursor := ""
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		page, err := s.backend.History(ctx, item.Assistant, item.SessionID, cursor)
		if err != nil {
			return chatQueueDeliveryResult{}, false, err
		}
		for _, event := range page.Events {
			if event.Type != "user_done" {
				continue
			}
			var data struct {
				ClientID string `json:"clientId"`
			}
			_ = json.Unmarshal(event.Data, &data)
			if data.ClientID == item.ClientMessageID {
				delivery := item.Delivery
				if delivery == "" {
					delivery = chatDeliveryStart
				}
				return chatQueueDeliveryResult{TurnID: event.TurnID, Delivery: delivery}, true, nil
			}
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}
	return chatQueueDeliveryResult{}, false, nil
}

var agentChatQueue = &chatQueue{items: map[string]ChatQueueItem{}, sessions: map[string]ChatSessionState{}}
var agentChatQueueWorker *chatQueueWorker
var agentChatTakeover *chatTakeoverService

var chatControlSnapshotClock = struct {
	sync.Mutex
	epoch   string
	version uint64
}{epoch: newChatControlEpoch()}

func newChatControlEpoch() string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func nextChatControlSnapshotVersion() (string, uint64) {
	chatControlSnapshotClock.Lock()
	defer chatControlSnapshotClock.Unlock()
	chatControlSnapshotClock.version++
	return chatControlSnapshotClock.epoch, chatControlSnapshotClock.version
}

func buildChatSessionControlSnapshot(ctx context.Context, assistant, sessionID string) (ChatSessionControlSnapshot, error) {
	assistant = normAssistant(assistant)
	sessionID = strings.TrimSpace(sessionID)
	if assistant != "codex" || sessionID == "" {
		return ChatSessionControlSnapshot{}, errors.New("invalid chat control identity")
	}
	operation := agentChatQueue.sessionOperation(assistant, sessionID)
	operation.Lock()
	defer operation.Unlock()
	return buildChatSessionControlSnapshotLocked(ctx, assistant, sessionID)
}

// buildChatSessionControlSnapshotLocked requires the session operation lock.
// Access transitions use it so their persisted state, app-server side effects,
// and returned browser projection are one indivisible operation.
func buildChatSessionControlSnapshotLocked(ctx context.Context, assistant, sessionID string) (ChatSessionControlSnapshot, error) {
	runtimeState, err := agentChatBackend.Control(ctx, assistant, sessionID)
	if err != nil {
		return ChatSessionControlSnapshot{}, err
	}
	phase := "unknown"
	if runtimeState.Status == "running" || codexStatusIsRunning(runtimeState.Status) {
		phase = "running"
	} else if runtimeState.Status == "idle" {
		phase = "idle"
	}
	accessMode := agentChatQueue.AccessMode(assistant, sessionID)
	items := agentChatQueue.ListVisible(assistant, sessionID)
	epoch, version := nextChatControlSnapshotVersion()
	return ChatSessionControlSnapshot{
		ServerEpoch: epoch, SnapshotVersion: version,
		AccessMode:  accessMode,
		WriterOwner: runtimeState.WriterOwner, TurnPhase: phase,
		ActiveTurnID: runtimeState.ActiveTurnID, TurnOwner: runtimeState.TurnOwner,
		Items: items,
	}, nil
}

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
	item, ok, err := w.queue.ClaimNext()
	if err != nil || !ok {
		return
	}
	operation := w.queue.sessionOperation(item.Assistant, item.SessionID)
	operation.Lock()
	defer operation.Unlock()
	current, exists := w.queue.get(item.ID)
	if !exists || current.Status != item.Status {
		return
	}
	if current.Status != chatQueueRecovering && w.queue.AccessMode(current.Assistant, current.SessionID) == chatAccessReadOnly {
		_, _ = w.queue.updateExpected(current.ID, []string{current.Status}, func(blocked *ChatQueueItem) error {
			blocked.Status, blocked.Error = chatQueueWaitingAccess, ""
			return nil
		})
		return
	}
	item = current
	if item.Status == chatQueueRecovering {
		result, delivered, recoverErr := w.sender.Recover(item)
		_, _ = w.queue.updateExpected(item.ID, []string{chatQueueRecovering}, func(current *ChatQueueItem) error {
			if recoverErr != nil {
				current.Status = chatQueueUncertain
				current.Error = "无法核对上次投递结果：" + recoverErr.Error()
			} else if delivered {
				current.Status, current.TurnID, current.Delivery, current.SentAt = chatQueueSent, result.TurnID, result.Delivery, time.Now().UnixMilli()
			} else {
				current.Status = chatQueueUncertain
				current.Error = "上次投递结果未知；请先核对会话历史，再选择重试或取消。"
			}
			return nil
		})
		return
	}
	result, deliverErr := w.sender.Deliver(item)
	claimedStatus := item.Status
	if errors.Is(deliverErr, errFleetChatTurnRunning) {
		_, _ = w.queue.updateExpected(item.ID, []string{claimedStatus}, func(current *ChatQueueItem) error {
			current.Status, current.WriterOwner, current.Error = chatQueueWaitingTurn, "fleet", ""
			return nil
		})
		return
	}
	if (errors.Is(deliverErr, errExternalChatTurn) || errors.Is(deliverErr, errThreadReadOnly)) && item.Decision != "force" && item.Decision != "confirm-force" {
		_, _ = w.queue.updateExpected(item.ID, []string{claimedStatus}, func(current *ChatQueueItem) error {
			if current.Decision == "wait" {
				current.Status = chatQueueWaitingWriter
			} else {
				current.Status = chatQueueWriterConfirmation
			}
			current.WriterOwner, current.Error = "desktop", ""
			return nil
		})
		return
	}
	if deliverErr != nil {
		_, _ = w.queue.updateExpected(item.ID, []string{claimedStatus}, func(current *ChatQueueItem) error {
			current.Status, current.Error = chatQueueFailed, deliverErr.Error()
			if current.Decision == "force" || current.Decision == "confirm-force" {
				current.Error = "强制接管后仍无法取得该会话控制权，请重新尝试。"
			}
			return nil
		})
		return
	}
	_, _ = w.queue.updateExpected(item.ID, []string{claimedStatus}, func(current *ChatQueueItem) error {
		current.Status, current.TurnID, current.Delivery, current.SentAt = chatQueueSent, result.TurnID, result.Delivery, time.Now().UnixMilli()
		return nil
	})
}

func (q *chatQueue) get(id string) (ChatQueueItem, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[id]
	return item, ok
}

func chatSessionStateKey(assistant, sessionID string) string {
	return normAssistant(assistant) + "\x00" + strings.TrimSpace(sessionID)
}

func (q *chatQueue) sessionOperation(assistant, sessionID string) *sync.Mutex {
	key := chatSessionStateKey(assistant, sessionID)
	q.opMu.Lock()
	defer q.opMu.Unlock()
	if q.sessionOps == nil {
		q.sessionOps = map[string]*sync.Mutex{}
	}
	operation := q.sessionOps[key]
	if operation == nil {
		operation = &sync.Mutex{}
		q.sessionOps[key] = operation
	}
	return operation
}

func normalizeChatAccessMode(mode string) string {
	if mode == chatAccessReadOnly {
		return chatAccessReadOnly
	}
	return chatAccessReadWrite
}

func (q *chatQueue) AccessMode(assistant, sessionID string) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return normalizeChatAccessMode(q.sessions[chatSessionStateKey(assistant, sessionID)].AccessMode)
}

func (q *chatQueue) SetAccessMode(assistant, sessionID, mode string) (ChatSessionState, error) {
	assistant = normAssistant(assistant)
	sessionID = strings.TrimSpace(sessionID)
	mode = normalizeChatAccessMode(mode)
	if assistant != "codex" || sessionID == "" {
		return ChatSessionState{}, errors.New("invalid session access state")
	}
	operation := q.sessionOperation(assistant, sessionID)
	operation.Lock()
	defer operation.Unlock()
	return q.setAccessModeLocked(assistant, sessionID, mode)
}

// setAccessModeLocked requires the per-session operation lock so callers can
// serialize persistence with backend release and the response snapshot.
func (q *chatQueue) setAccessModeLocked(assistant, sessionID, mode string) (ChatSessionState, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	key := chatSessionStateKey(assistant, sessionID)
	previous, existed := q.sessions[key]
	state := ChatSessionState{AccessMode: mode, UpdatedAt: time.Now().UnixMilli()}
	q.sessions[key] = state
	changedItems := map[string]ChatQueueItem{}
	if mode == chatAccessReadWrite {
		for id, item := range q.items {
			if item.Assistant == assistant && item.SessionID == sessionID && item.Status == chatQueueWaitingAccess {
				changedItems[id] = item
				item.Status, item.Error = chatQueueQueued, ""
				item.StateVersion++
				item.UpdatedAt = state.UpdatedAt
				q.items[id] = item
			}
		}
	} else {
		for id, item := range q.items {
			if item.Assistant != assistant || item.SessionID != sessionID {
				continue
			}
			switch item.Status {
			case chatQueueQueued, chatQueueWaitingWriter, chatQueueWriterConfirmation, chatQueueWaitingTurn:
				changedItems[id] = item
				item.Status, item.Error = chatQueueWaitingAccess, ""
				item.StateVersion++
				item.UpdatedAt = state.UpdatedAt
				q.items[id] = item
			}
		}
	}
	if err := q.saveLocked(); err != nil {
		if existed {
			q.sessions[key] = previous
		} else {
			delete(q.sessions, key)
		}
		for id, item := range changedItems {
			q.items[id] = item
		}
		return ChatSessionState{}, err
	}
	return state, nil
}

func chatQueueActionable(status string) bool {
	switch status {
	case chatQueueQueued, chatQueueWaitingWriter, chatQueueWaitingTurn, chatQueueWaitingAccess, chatQueueRecovering:
		return true
	default:
		return false
	}
}

func (q *chatQueue) ClaimNext() (ChatQueueItem, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := make([]ChatQueueItem, 0, len(q.items))
	for _, item := range q.items {
		if item.Status != chatQueueSent && item.Status != chatQueueCancelled {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt < items[j].CreatedAt })
	seenSession := map[string]bool{}
	dirty := false
	for _, snapshot := range items {
		key := chatSessionStateKey(snapshot.Assistant, snapshot.SessionID)
		if seenSession[key] {
			continue
		}
		seenSession[key] = true
		if !chatQueueActionable(snapshot.Status) {
			continue
		}
		current := q.items[snapshot.ID]
		if normalizeChatAccessMode(q.sessions[key].AccessMode) == chatAccessReadOnly && current.Status != chatQueueRecovering {
			if current.Status != chatQueueWaitingAccess {
				current.Status, current.Error = chatQueueWaitingAccess, ""
				current.StateVersion++
				current.UpdatedAt = time.Now().UnixMilli()
				q.items[current.ID] = current
				dirty = true
			}
			continue
		}
		if current.Status == chatQueueWaitingAccess {
			current.Status = chatQueueQueued
		}
		if current.Status != chatQueueRecovering {
			if current.DeliveryMode == chatDeliveryAuto {
				current.Status = chatQueueSteering
			} else {
				current.Status = chatQueueSending
			}
		}
		current.AttemptedAt, current.Error, current.UpdatedAt = time.Now().UnixMilli(), "", time.Now().UnixMilli()
		current.StateVersion++
		q.items[current.ID] = current
		if err := q.saveLocked(); err != nil {
			q.items[current.ID] = snapshot
			return ChatQueueItem{}, false, err
		}
		return current, true, nil
	}
	if dirty {
		if err := q.saveLocked(); err != nil {
			return ChatQueueItem{}, false, err
		}
	}
	return ChatQueueItem{}, false, nil
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
		snapshot, err := buildChatSessionControlSnapshot(r.Context(), assistant, sessionID)
		if err != nil {
			writeChatErr(w, err)
			return
		}
		writeJSON(w, snapshot)
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
		StateVersion int64  `json:"stateVersion"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || req.ID == "" || req.StateVersion <= 0 {
		http.Error(w, "bad request", 400)
		return
	}
	item, err := agentChatTakeover.Decide(req.ID, req.Action, req.AuditVersion, req.StateVersion)
	if errors.Is(err, errFleetChatReadOnly) {
		writeChatErr(w, err)
		return
	}
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
	q := &chatQueue{path: path, items: map[string]ChatQueueItem{}, sessions: map[string]ChatSessionState{}, sessionOps: map[string]*sync.Mutex{}}
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
	for key, state := range disk.Sessions {
		state.AccessMode = normalizeChatAccessMode(state.AccessMode)
		q.sessions[key] = state
	}
	dirty := false
	for _, item := range disk.Items {
		if item.ID != "" {
			if item.DeliveryMode == "" {
				item.DeliveryMode = chatDeliveryNext
			}
			if item.StateVersion == 0 {
				item.StateVersion = 1
			}
			if item.Status == chatQueueSending || item.Status == chatQueueSteering || item.Status == chatQueueTakingOver {
				item.Status = chatQueueRecovering
				item.StateVersion++
				dirty = true
			} else if item.Status == chatQueueTakeoverCheck {
				item.Status = chatQueueFailed
				item.Error = "服务重启中断了接管检查，请重新尝试。"
				item.StateVersion++
				dirty = true
			}
			q.items[item.ID] = item
		}
	}
	if dirty {
		if err := q.saveLocked(); err != nil {
			return nil, err
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
	input.WriterOwner = ""
	input.ID = ""
	input.Status = ""
	input.Delivery = ""
	input.StateVersion = 0
	input.Decision = ""
	input.AuditVersion = ""
	input.Affected = nil
	input.TurnID = ""
	input.Error = ""
	input.CreatedAt = 0
	input.UpdatedAt = 0
	input.AttemptedAt = 0
	input.SentAt = 0
	if input.DeliveryMode == "" {
		input.DeliveryMode = chatDeliveryAuto
	}
	if input.ClientMessageID == "" || input.Assistant != "codex" || input.SessionID == "" {
		return ChatQueueItem{}, errors.New("invalid queue item")
	}
	if len(input.ClientMessageID) > 200 || strings.ContainsAny(input.ClientMessageID, "\r\n\x00") ||
		len(input.SessionID) > 200 || strings.ContainsAny(input.SessionID, "\r\n\x00") {
		return ChatQueueItem{}, errors.New("invalid queue identity")
	}
	normalizedOptions, err := normalizeChatTurnOptions(input.Options)
	if err != nil {
		return ChatQueueItem{}, err
	}
	input.Options = normalizedOptions
	if input.DeliveryMode != chatDeliveryAuto && input.DeliveryMode != chatDeliveryNext {
		return ChatQueueItem{}, errors.New("invalid delivery mode")
	}
	if strings.TrimSpace(input.Text) == "" && len(input.Images) == 0 && len(input.Skills) == 0 {
		return ChatQueueItem{}, errors.New("empty queue item")
	}
	if len(input.Images) > 20 || len(input.Skills) > 100 {
		return ChatQueueItem{}, errors.New("queue item exceeds attachment limits")
	}
	for _, image := range input.Images {
		if strings.TrimSpace(image.ID) == "" || len(image.ID) > 200 || strings.ContainsAny(image.ID, "\r\n\x00") {
			return ChatQueueItem{}, errors.New("invalid queue attachment")
		}
	}
	for _, item := range q.items {
		if item.ClientMessageID == input.ClientMessageID && item.Assistant == input.Assistant && item.SessionID == input.SessionID {
			return item, nil
		}
	}
	now := time.Now().UnixMilli()
	status := chatQueueQueued
	if normalizeChatAccessMode(q.sessions[chatSessionStateKey(input.Assistant, input.SessionID)].AccessMode) == chatAccessReadOnly {
		status = chatQueueWaitingAccess
	}
	input.ID, input.Status, input.CreatedAt, input.UpdatedAt, input.StateVersion = newChatQueueID(), status, now, now, 1
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

func (q *chatQueue) ListVisible(assistant, sessionID string) []ChatQueueItem {
	items := q.List(assistant, sessionID)
	visible := items[:0]
	for _, item := range items {
		if item.Status != chatQueueSent && item.Status != chatQueueCancelled {
			visible = append(visible, item)
		}
	}
	return visible
}

func (q *chatQueue) RequireTakeoverConfirmation(id, audit string, affected []ChatTakeoverImpact) (ChatQueueItem, error) {
	return q.updateExpected(id, []string{chatQueueTakeoverCheck}, func(item *ChatQueueItem) error {
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
	return q.updateExpected(id, nil, mutate)
}

func (q *chatQueue) updateExpected(id string, expected []string, mutate func(*ChatQueueItem) error) (ChatQueueItem, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[id]
	if !ok {
		return ChatQueueItem{}, os.ErrNotExist
	}
	if len(expected) > 0 {
		allowed := false
		for _, status := range expected {
			allowed = allowed || item.Status == status
		}
		if !allowed {
			return ChatQueueItem{}, errChatQueueStateConflict
		}
	}
	previous := item
	if err := mutate(&item); err != nil {
		return ChatQueueItem{}, err
	}
	item.UpdatedAt = time.Now().UnixMilli()
	item.StateVersion++
	q.items[id] = item
	if err := q.saveLocked(); err != nil {
		q.items[id] = previous
		return ChatQueueItem{}, err
	}
	return item, nil
}

func (q *chatQueue) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(q.path), 0o700); err != nil {
		return err
	}
	disk := chatQueueDisk{Version: 2, Sessions: make(map[string]ChatSessionState, len(q.sessions)), Items: make([]ChatQueueItem, 0, len(q.items))}
	for key, state := range q.sessions {
		disk.Sessions[key] = state
	}
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
