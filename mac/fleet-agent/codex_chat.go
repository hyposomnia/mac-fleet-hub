package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const chatHistoryPageSize = 40

// A migrated turn can contain an entire legacy transcript. Keep the fallback
// page to one full turn so old app-server versions do not multiply that cost.
const codexTurnsPageSize = 1
const codexTurnsCursorPrefix = "turn-items:"
const codexHistoryCacheLimit = 32
const codexConnectedSyncInterval = time.Second
const codexSyncSeenLimit = 256

type codexConnector func(context.Context) (codexRPCConn, func(), error)

type pendingCodexRequest struct {
	id        json.RawMessage
	method    string
	params    json.RawMessage
	sessionID string
}

type codexRolloutStamp struct {
	path    string
	size    int64
	modTime int64
}

type codexConnectedSync struct {
	cancel context.CancelFunc
}

type codexRolloutTaskState struct {
	path     string
	offset   int64
	turnID   string
	status   string
	terminal bool
}

type codexRolloutTurnMarker struct {
	AssistantText string
	Item          json.RawMessage
}

type codexRolloutToolsCache struct {
	path    string
	offset  int64
	markers map[string][]codexRolloutTurnMarker
}

type codexChatBackend struct {
	connect codexConnector
	restart func(error)

	connectMu     sync.Mutex
	restartOnce   sync.Once
	mu            sync.Mutex
	rpc           codexRPCConn
	cleanup       func()
	subs          map[string]map[chan ChatEvent]struct{}
	lastTurn      map[string]string
	approvalModes map[string]string
	pending       map[string]pendingCodexRequest
	resuming      map[string]bool
	backlog       map[string][]ChatEvent
	buffered      map[string][]ChatEvent
	freshThreads  map[string]bool
	syncers       map[string]*codexConnectedSync
	syncSeen      map[string]map[string][32]byte
	syncSeenOrder map[string][]string
	syncInterval  time.Duration
	rolloutStamp  func(string) (codexRolloutStamp, bool)
	// -1 means thread/items/list is unsupported, 0 unknown, 1 supported.
	itemsListSupport int
	historyPages     map[string]codexTurnsPage
	historyPageOrder []string
	historyUsage     map[string]codexTurnUsageCache
	rolloutToolsMu   sync.Mutex
	rolloutTools     map[string]*codexRolloutToolsCache
}

func newCodexChatBackend(connect codexConnector) *codexChatBackend {
	return &codexChatBackend{
		connect:       connect,
		restart:       scheduleFleetAgentRestart,
		subs:          map[string]map[chan ChatEvent]struct{}{},
		lastTurn:      map[string]string{},
		approvalModes: map[string]string{},
		pending:       map[string]pendingCodexRequest{},
		resuming:      map[string]bool{},
		backlog:       map[string][]ChatEvent{},
		buffered:      map[string][]ChatEvent{},
		freshThreads:  map[string]bool{},
		syncers:       map[string]*codexConnectedSync{},
		syncSeen:      map[string]map[string][32]byte{},
		syncSeenOrder: map[string][]string{},
		syncInterval:  codexConnectedSyncInterval,
		rolloutStamp:  codexSessionRolloutStamp,
		historyPages:  map[string]codexTurnsPage{},
		historyUsage:  map[string]codexTurnUsageCache{},
		rolloutTools:  map[string]*codexRolloutToolsCache{},
	}
}

func newAgentChatBackend() chatBackend {
	return newCodexChatBackend(connectCodexAppServerStdio)
}

func (b *codexChatBackend) ensure(ctx context.Context) (codexRPCConn, error) {
	b.mu.Lock()
	if b.rpc != nil {
		rpc := b.rpc
		b.mu.Unlock()
		return rpc, nil
	}
	b.mu.Unlock()

	b.connectMu.Lock()
	defer b.connectMu.Unlock()
	b.mu.Lock()
	if b.rpc != nil {
		rpc := b.rpc
		b.mu.Unlock()
		return rpc, nil
	}
	b.mu.Unlock()

	inner, cleanup, err := b.connect(ctx)
	if err != nil {
		if ctx.Err() == nil {
			wrapped := fmt.Errorf("Codex app-server initialization failed: %w", err)
			b.scheduleSelfRestart(wrapped)
			return nil, fmt.Errorf("%w: %v", errAgentRestarting, wrapped)
		}
		return nil, errAppServerUnavailable
	}
	rpc := codexRPCConn(&recoveringCodexRPC{backend: b, inner: inner})

	b.mu.Lock()
	b.rpc = rpc
	b.cleanup = cleanup
	b.mu.Unlock()
	go b.dispatch(rpc, rpc.notifications())
	return rpc, nil
}

func (b *codexChatBackend) resetRPC() {
	b.mu.Lock()
	cleanup := b.cleanup
	b.rpc = nil
	b.cleanup = nil
	b.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}

func codexThreadNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "thread not found")
}

type codexHistoryItemEntry struct {
	TurnID string          `json:"turnId"`
	Item   json.RawMessage `json:"item"`
}

type codexItemsPage struct {
	Data       []codexHistoryItemEntry `json:"data"`
	NextCursor *string                 `json:"nextCursor"`
}

type codexHistoryTurn struct {
	ID    string            `json:"id"`
	Items []json.RawMessage `json:"items"`
}

type codexTurnsPage struct {
	Data       []codexHistoryTurn `json:"data"`
	NextCursor *string            `json:"nextCursor"`
}

type codexTurnsCursor struct {
	Page   string `json:"page,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

type codexResumeWire struct {
	Thread struct {
		ID     string          `json:"id"`
		Status json.RawMessage `json:"status"`
		Turns  []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turns"`
	} `json:"thread"`
	Model           string          `json:"model"`
	ReasoningEffort string          `json:"reasoningEffort"`
	ServiceTier     string          `json:"serviceTier"`
	ApprovalPolicy  json.RawMessage `json:"approvalPolicy"`
	SandboxPolicy   json.RawMessage `json:"sandboxPolicy"`
	Sandbox         json.RawMessage `json:"sandbox"`
}

func codexThreadStatus(raw json.RawMessage) string {
	var status struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &status) == nil && status.Type != "" {
		return status.Type
	}
	var value string
	if json.Unmarshal(raw, &value) == nil && value != "" {
		return value
	}
	return "idle"
}

func codexStatusIsRunning(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "running", "inprogress", "in_progress", "started":
		return true
	default:
		return false
	}
}

func codexThreadStartParams(cwd, mode string) map[string]interface{} {
	params := map[string]interface{}{"cwd": cwd}
	switch mode {
	case "auto":
		params["approvalPolicy"] = "never"
		params["sandbox"] = "workspace-write"
	case "bypass":
		params["approvalPolicy"] = "never"
		params["sandbox"] = "danger-full-access"
	}
	return params
}

func (b *codexChatBackend) Start(ctx context.Context, assistant, cwd, mode string) (ChatStartResult, error) {
	if assistant != "codex" {
		return ChatStartResult{}, errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return ChatStartResult{}, err
	}
	raw, err := rpc.call(ctx, "thread/start", codexThreadStartParams(cwd, mode))
	if err != nil {
		return ChatStartResult{}, err
	}
	var res struct {
		Thread struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionId"`
		} `json:"thread"`
		Cwd             string          `json:"cwd"`
		Model           string          `json:"model"`
		ReasoningEffort string          `json:"reasoningEffort"`
		ServiceTier     string          `json:"serviceTier"`
		ApprovalPolicy  json.RawMessage `json:"approvalPolicy"`
		Sandbox         json.RawMessage `json:"sandbox"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return ChatStartResult{}, fmt.Errorf("decode Codex thread start: %w", err)
	}
	sessionID := res.Thread.ID
	if sessionID == "" {
		sessionID = res.Thread.SessionID
	}
	if sessionID == "" {
		return ChatStartResult{}, errors.New("Codex thread/start missing thread id")
	}
	if res.Cwd == "" {
		res.Cwd = cwd
	}
	approvalMode := codexApprovalMode(res.ApprovalPolicy, res.Sandbox)
	b.mu.Lock()
	b.freshThreads[sessionID] = true
	b.approvalModes[sessionID] = approvalMode
	b.mu.Unlock()
	return ChatStartResult{
		SessionID: sessionID, Cwd: res.Cwd,
		Model: res.Model, Effort: res.ReasoningEffort, ServiceTier: res.ServiceTier,
		ApprovalMode: approvalMode,
		Models:       b.modelOptions(ctx, rpc),
	}, nil
}

func (b *codexChatBackend) resumeThread(ctx context.Context, rpc codexRPCConn, sessionID string) (codexResumeWire, error) {
	params := map[string]interface{}{"threadId": sessionID}
	raw, err := rpc.call(ctx, "thread/resume", params)
	if err != nil {
		return codexResumeWire{}, err
	}
	var res codexResumeWire
	_ = json.Unmarshal(raw, &res)
	return res, nil
}

func (b *codexChatBackend) Resume(ctx context.Context, assistant, sessionID, mode string) (ChatResumeResult, error) {
	if assistant != "codex" {
		return ChatResumeResult{}, errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return ChatResumeResult{}, err
	}
	b.beginResume(sessionID)
	resumeFinished := false
	defer func() {
		if !resumeFinished {
			b.finishResume(sessionID)
		}
	}()
	// Match the Desktop lifecycle: read persisted metadata without loading the
	// transcript, then resume and reconcile paginated history with notifications
	// that arrived during hydration.
	_, _ = rpc.call(ctx, "thread/read", map[string]interface{}{
		"threadId": sessionID, "includeTurns": false,
	})
	res, err := b.resumeThread(ctx, rpc, sessionID)
	if err != nil {
		return ChatResumeResult{}, err
	}
	threadID := res.Thread.ID
	if threadID == "" {
		threadID = sessionID
	}
	b.clearHistoryPages(sessionID)
	history, err := b.listHistory(ctx, rpc, sessionID, "")
	if err != nil {
		return ChatResumeResult{}, err
	}
	b.rememberSyncEvents(sessionID, history.Events)
	status := codexThreadStatus(res.Thread.Status)
	activeTurnID := ""
	latestTurns := res.Thread.Turns
	if codexStatusIsRunning(status) && len(latestTurns) == 0 {
		latestTurns = b.latestTurns(ctx, rpc, sessionID)
	}
	b.mu.Lock()
	if codexStatusIsRunning(status) {
		if len(latestTurns) > 0 {
			latest := latestTurns[0]
			if latest.Status == "" || codexStatusIsRunning(latest.Status) {
				activeTurnID = latest.ID
			}
		}
		if activeTurnID != "" {
			b.lastTurn[sessionID] = activeTurnID
		}
	} else {
		delete(b.lastTurn, sessionID)
	}
	b.mu.Unlock()
	b.finishResume(sessionID)
	resumeFinished = true
	sandboxPolicy := res.SandboxPolicy
	if len(sandboxPolicy) == 0 || string(sandboxPolicy) == "null" {
		sandboxPolicy = res.Sandbox
	}
	approvalMode := codexApprovalMode(res.ApprovalPolicy, sandboxPolicy)
	if rolloutMode := codexApprovalModeFromRollout(jsonlPathFor("codex", sessionID)); rolloutMode != "" {
		approvalMode = rolloutMode
	}
	b.applyApprovalMode(rpc, sessionID, approvalMode)
	return ChatResumeResult{
		SessionID: sessionID, ThreadID: threadID, Status: status, ActiveTurnID: activeTurnID,
		History: history, Model: res.Model, Effort: res.ReasoningEffort, ServiceTier: res.ServiceTier,
		ApprovalMode: approvalMode,
		Models:       b.modelOptions(ctx, rpc),
	}, nil
}

func (b *codexChatBackend) latestTurns(ctx context.Context, rpc codexRPCConn, sessionID string) []struct {
	ID     string `json:"id"`
	Status string `json:"status"`
} {
	raw, err := rpc.call(ctx, "thread/turns/list", map[string]interface{}{
		"threadId": sessionID, "limit": 1, "sortDirection": "desc", "itemsView": "notLoaded",
	})
	if err != nil {
		return nil
	}
	var page struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &page) != nil {
		return nil
	}
	return page.Data
}

func (b *codexChatBackend) beginResume(sessionID string) {
	b.mu.Lock()
	b.resuming[sessionID] = true
	delete(b.buffered, sessionID)
	b.mu.Unlock()
}

func (b *codexChatBackend) finishResume(sessionID string) {
	b.mu.Lock()
	events := b.buffered[sessionID]
	delete(b.buffered, sessionID)
	delete(b.resuming, sessionID)
	if len(events) > 0 {
		b.backlog[sessionID] = append(b.backlog[sessionID], events...)
	}
	b.mu.Unlock()
	b.rememberSyncEvents(sessionID, events)
}

func (b *codexChatBackend) History(ctx context.Context, assistant, sessionID, cursor string) (ChatHistoryPage, error) {
	if assistant != "codex" {
		return ChatHistoryPage{}, errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return ChatHistoryPage{}, err
	}
	page, err := b.listHistory(ctx, rpc, sessionID, cursor)
	if err != nil && codexThreadNotFound(err) {
		if _, resumeErr := b.resumeThread(ctx, rpc, sessionID); resumeErr != nil {
			return ChatHistoryPage{}, resumeErr
		}
		page, err = b.listHistory(ctx, rpc, sessionID, cursor)
	}
	if err != nil {
		return ChatHistoryPage{}, err
	}
	return page, nil
}

func (b *codexChatBackend) listHistory(ctx context.Context, rpc codexRPCConn, sessionID, cursor string) (ChatHistoryPage, error) {
	if strings.HasPrefix(cursor, codexTurnsCursorPrefix) {
		return b.listHistoryByTurns(ctx, rpc, sessionID, cursor)
	}
	b.mu.Lock()
	itemsListSupport := b.itemsListSupport
	b.mu.Unlock()
	if itemsListSupport < 0 {
		return b.listHistoryByTurns(ctx, rpc, sessionID, cursor)
	}
	params := map[string]interface{}{
		"threadId": sessionID, "limit": chatHistoryPageSize, "sortDirection": "desc",
	}
	if cursor != "" {
		params["cursor"] = cursor
	}
	raw, err := rpc.call(ctx, "thread/items/list", params)
	if err != nil {
		if codexItemsListUnsupported(err) && cursor == "" {
			b.mu.Lock()
			b.itemsListSupport = -1
			b.mu.Unlock()
			return b.listHistoryByTurns(ctx, rpc, sessionID, "")
		}
		return ChatHistoryPage{}, err
	}
	b.mu.Lock()
	b.itemsListSupport = 1
	b.mu.Unlock()
	var page codexItemsPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return ChatHistoryPage{}, fmt.Errorf("decode Codex history: %w", err)
	}
	return projectCodexHistoryPage(sessionID, page, b.rolloutUsage(sessionID)), nil
}

func codexItemsListUnsupported(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "thread/items/list") && strings.Contains(msg, "not supported")
}

func (b *codexChatBackend) listHistoryByTurns(ctx context.Context, rpc codexRPCConn, sessionID, cursor string) (ChatHistoryPage, error) {
	state, err := decodeCodexTurnsCursor(cursor)
	if err != nil {
		return ChatHistoryPage{}, err
	}
	events := make([]ChatEvent, 0, chatHistoryPageSize)
	seenPages := map[string]bool{}
	usageByTurn := b.rolloutUsage(sessionID)
	for len(events) < chatHistoryPageSize {
		if seenPages[state.Page] {
			return ChatHistoryPage{}, fmt.Errorf("Codex history cursor loop")
		}
		seenPages[state.Page] = true
		page, err := b.fetchTurnsPage(ctx, rpc, sessionID, state.Page)
		if err != nil {
			return ChatHistoryPage{}, err
		}
		page = b.enrichCodexTurnsPage(sessionID, page)
		entries := descendingCodexTurnItems(page)
		if state.Offset < 0 || state.Offset > len(entries) {
			return ChatHistoryPage{}, fmt.Errorf("invalid Codex history cursor offset")
		}
		for state.Offset < len(entries) && len(events) < chatHistoryPageSize {
			entry := entries[state.Offset]
			state.Offset++
			if ev, ok := projectCodexHistoryItemWithUsage(sessionID, entry.TurnID, entry.Item, usageByTurn[entry.TurnID]); ok {
				events = append(events, ev)
			}
		}
		if state.Offset < len(entries) {
			break
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			state = codexTurnsCursor{}
			break
		}
		state = codexTurnsCursor{Page: *page.NextCursor}
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	next := ""
	if state.Page != "" || state.Offset != 0 {
		next, err = encodeCodexTurnsCursor(state)
		if err != nil {
			return ChatHistoryPage{}, err
		}
	}
	return ChatHistoryPage{Events: events, NextCursor: next}, nil
}

func (b *codexChatBackend) fetchTurnsPage(ctx context.Context, rpc codexRPCConn, sessionID, cursor string) (codexTurnsPage, error) {
	cacheKey := sessionID + "\x00" + cursor
	b.mu.Lock()
	if page, ok := b.historyPages[cacheKey]; ok {
		b.mu.Unlock()
		return page, nil
	}
	b.mu.Unlock()
	params := map[string]interface{}{
		"threadId": sessionID, "limit": codexTurnsPageSize,
		"sortDirection": "desc", "itemsView": "full",
	}
	if cursor != "" {
		params["cursor"] = cursor
	}
	raw, err := rpc.call(ctx, "thread/turns/list", params)
	if err != nil {
		return codexTurnsPage{}, err
	}
	var page codexTurnsPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return codexTurnsPage{}, fmt.Errorf("decode Codex turn history: %w", err)
	}
	b.mu.Lock()
	if _, exists := b.historyPages[cacheKey]; !exists {
		if len(b.historyPageOrder) >= codexHistoryCacheLimit {
			delete(b.historyPages, b.historyPageOrder[0])
			b.historyPageOrder = b.historyPageOrder[1:]
		}
		b.historyPages[cacheKey] = page
		b.historyPageOrder = append(b.historyPageOrder, cacheKey)
	}
	b.mu.Unlock()
	return page, nil
}

func (b *codexChatBackend) clearHistoryPages(sessionID string) {
	prefix := sessionID + "\x00"
	b.mu.Lock()
	kept := b.historyPageOrder[:0]
	for _, key := range b.historyPageOrder {
		if strings.HasPrefix(key, prefix) {
			delete(b.historyPages, key)
			continue
		}
		kept = append(kept, key)
	}
	b.historyPageOrder = kept
	b.mu.Unlock()
}

func codexSessionRolloutStamp(sessionID string) (codexRolloutStamp, bool) {
	path := jsonlPathFor("codex", sessionID)
	if path == "" {
		return codexRolloutStamp{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return codexRolloutStamp{}, false
	}
	return codexRolloutStamp{path: path, size: info.Size(), modTime: info.ModTime().UnixNano()}, true
}

func updateCodexRolloutTaskState(stamp codexRolloutStamp, state *codexRolloutTaskState) error {
	if state.path != stamp.path || stamp.size < state.offset {
		*state = codexRolloutTaskState{path: stamp.path}
	}
	f, err := os.Open(stamp.path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(state.offset, io.SeekStart); err != nil {
		return err
	}

	baseOffset := state.offset
	decoder := json.NewDecoder(f)
	for {
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				Type   string `json:"type"`
				TurnID string `json:"turn_id"`
				Reason string `json:"reason"`
			} `json:"payload"`
		}
		err := decoder.Decode(&entry)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &typeErr) {
				state.offset = baseOffset + decoder.InputOffset()
				continue
			}
			return fmt.Errorf("decode Codex rollout lifecycle: %w", err)
		}
		state.offset = baseOffset + decoder.InputOffset()
		if entry.Type != "event_msg" || entry.Payload.TurnID == "" {
			continue
		}
		switch entry.Payload.Type {
		case "task_started":
			state.turnID = entry.Payload.TurnID
			state.status = "inProgress"
			state.terminal = false
		case "task_complete":
			if entry.Payload.TurnID == state.turnID {
				state.status = "completed"
				state.terminal = true
			}
		case "turn_aborted":
			if entry.Payload.TurnID == state.turnID {
				state.status = entry.Payload.Reason
				if state.status == "" {
					state.status = "interrupted"
				}
				state.terminal = true
			}
		}
	}
}

func chatEventSyncKey(event ChatEvent) string {
	return event.Type + "\x00" + event.TurnID + "\x00" + event.ItemID
}

func chatEventFingerprint(event ChatEvent) [32]byte {
	data := event.Data
	if event.Type == "assistant_done" {
		var payload map[string]json.RawMessage
		if json.Unmarshal(data, &payload) == nil {
			delete(payload, "usage")
			if normalized, err := json.Marshal(payload); err == nil {
				data = normalized
			}
		}
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(event.Type))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(event.Assistant))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(event.SessionID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(event.TurnID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(event.ItemID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(data)
	var fingerprint [32]byte
	copy(fingerprint[:], hash.Sum(nil))
	return fingerprint
}

func (b *codexChatBackend) rememberSyncEvents(sessionID string, events []ChatEvent) {
	if len(events) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.syncSeen[sessionID] == nil {
		b.syncSeen[sessionID] = map[string][32]byte{}
	}
	for _, event := range events {
		key := chatEventSyncKey(event)
		if _, exists := b.syncSeen[sessionID][key]; !exists {
			b.syncSeenOrder[sessionID] = append(b.syncSeenOrder[sessionID], key)
		}
		b.syncSeen[sessionID][key] = chatEventFingerprint(event)
	}
	for len(b.syncSeenOrder[sessionID]) > codexSyncSeenLimit {
		oldest := b.syncSeenOrder[sessionID][0]
		b.syncSeenOrder[sessionID] = b.syncSeenOrder[sessionID][1:]
		delete(b.syncSeen[sessionID], oldest)
	}
}

func (b *codexChatBackend) publishSyncEvent(event ChatEvent) {
	key := chatEventSyncKey(event)
	fingerprint := chatEventFingerprint(event)
	b.mu.Lock()
	if b.syncSeen[event.SessionID] == nil {
		b.syncSeen[event.SessionID] = map[string][32]byte{}
	}
	if b.syncSeen[event.SessionID][key] == fingerprint {
		b.mu.Unlock()
		return
	}
	if _, exists := b.syncSeen[event.SessionID][key]; !exists {
		b.syncSeenOrder[event.SessionID] = append(b.syncSeenOrder[event.SessionID], key)
	}
	b.syncSeen[event.SessionID][key] = fingerprint
	for len(b.syncSeenOrder[event.SessionID]) > codexSyncSeenLimit {
		oldest := b.syncSeenOrder[event.SessionID][0]
		b.syncSeenOrder[event.SessionID] = b.syncSeenOrder[event.SessionID][1:]
		delete(b.syncSeen[event.SessionID], oldest)
	}
	b.mu.Unlock()
	b.publish(event)
}

func (b *codexChatBackend) reconcileConnectedSession(ctx context.Context, rpc codexRPCConn, sessionID string, state codexRolloutTaskState) (bool, error) {
	if state.turnID != "" && !state.terminal {
		b.mu.Lock()
		previous := b.lastTurn[sessionID]
		b.lastTurn[sessionID] = state.turnID
		b.mu.Unlock()
		if previous != state.turnID {
			b.publish(newChatEvent("turn_started", "codex", sessionID, state.turnID, "", map[string]interface{}{
				"turn": map[string]string{"id": state.turnID, "status": state.status},
			}))
		}
	}

	b.clearHistoryPages(sessionID)
	history, historyErr := b.listHistory(ctx, rpc, sessionID, "")
	if historyErr != nil {
		return false, historyErr
	}
	for _, event := range history.Events {
		b.publishSyncEvent(event)
	}
	if state.turnID == "" || !state.terminal {
		return false, nil
	}
	b.mu.Lock()
	wasActive := b.lastTurn[sessionID] == state.turnID
	if wasActive {
		delete(b.lastTurn, sessionID)
	}
	b.mu.Unlock()
	if wasActive {
		b.publish(newChatEvent("turn_done", "codex", sessionID, state.turnID, "", map[string]interface{}{
			"turn": map[string]string{"id": state.turnID, "status": state.status},
		}))
	}
	return true, nil
}

func (b *codexChatBackend) runConnectedSync(ctx context.Context, sessionID string, syncer *codexConnectedSync) {
	defer func() {
		b.mu.Lock()
		if b.syncers[sessionID] == syncer {
			delete(b.syncers, sessionID)
		}
		b.mu.Unlock()
	}()
	ticker := time.NewTicker(b.syncInterval)
	defer ticker.Stop()
	var last codexRolloutStamp
	var taskState codexRolloutTaskState
	haveLast := false
	for {
		stamp, ok := b.rolloutStamp(sessionID)
		if ok && (!haveLast || stamp != last) {
			stateErr := updateCodexRolloutTaskState(stamp, &taskState)
			if stateErr == nil {
				rpc, err := b.ensure(ctx)
				if err == nil {
					completed, reconcileErr := b.reconcileConnectedSession(ctx, rpc, sessionID, taskState)
					if reconcileErr == nil {
						last = stamp
						haveLast = true
						if completed {
							return
						}
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type codexTokenUsage struct {
	InputTokens       int64
	OutputTokens      int64
	CachedInputTokens int64
}

func (u codexTokenUsage) empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.CachedInputTokens == 0
}

type codexTurnUsageCache struct {
	path  string
	mtime int64
	usage map[string]codexTokenUsage
}

func (b *codexChatBackend) rolloutUsage(sessionID string) map[string]codexTokenUsage {
	path := jsonlPathFor("codex", sessionID)
	if path == "" {
		return nil
	}
	var mtime int64
	if st, err := os.Stat(path); err == nil {
		mtime = st.ModTime().UnixMilli()
	}
	b.mu.Lock()
	if cached, ok := b.historyUsage[sessionID]; ok && cached.path == path && cached.mtime == mtime {
		b.mu.Unlock()
		return cached.usage
	}
	b.mu.Unlock()

	usage := codexTurnUsageFromRollout(path)
	b.mu.Lock()
	b.historyUsage[sessionID] = codexTurnUsageCache{path: path, mtime: mtime, usage: usage}
	b.mu.Unlock()
	return usage
}

func codexTurnUsageFromRollout(path string) map[string]codexTokenUsage {
	out := map[string]codexTokenUsage{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()

	currentTurnID := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var row struct {
			Type    string `json:"type"`
			Payload struct {
				Type        string `json:"type"`
				TurnID      string `json:"turn_id"`
				TurnIDCamel string `json:"turnId"`
				Internal    struct {
					TurnID      string `json:"turn_id"`
					TurnIDCamel string `json:"turnId"`
				} `json:"internal_chat_message_metadata_passthrough"`
				Info struct {
					LastTokenUsage struct {
						InputTokens            int64 `json:"input_tokens"`
						InputTokensCamel       int64 `json:"inputTokens"`
						OutputTokens           int64 `json:"output_tokens"`
						OutputTokensCamel      int64 `json:"outputTokens"`
						CachedInputTokens      int64 `json:"cached_input_tokens"`
						CachedInputTokensCamel int64 `json:"cachedInputTokens"`
					} `json:"last_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		if turnID := firstNonEmpty(row.Payload.TurnID, row.Payload.TurnIDCamel, row.Payload.Internal.TurnID, row.Payload.Internal.TurnIDCamel); turnID != "" {
			currentTurnID = turnID
		}
		if row.Type != "event_msg" || row.Payload.Type != "token_count" || currentTurnID == "" {
			continue
		}
		last := row.Payload.Info.LastTokenUsage
		usage := codexTokenUsage{
			InputTokens:       firstPositiveInt64(last.InputTokens, last.InputTokensCamel),
			OutputTokens:      firstPositiveInt64(last.OutputTokens, last.OutputTokensCamel),
			CachedInputTokens: firstPositiveInt64(last.CachedInputTokens, last.CachedInputTokensCamel),
		}
		if !usage.empty() {
			out[currentTurnID] = usage
		}
	}
	return out
}

func (b *codexChatBackend) enrichCodexTurnsPage(sessionID string, page codexTurnsPage) codexTurnsPage {
	markersByTurn := b.codexRolloutToolMarkers(sessionID, page)
	if len(markersByTurn) == 0 {
		return page
	}
	out := page
	out.Data = append([]codexHistoryTurn(nil), page.Data...)
	for i, turn := range out.Data {
		markers := markersByTurn[turn.ID]
		if len(markers) == 0 {
			continue
		}
		out.Data[i].Items = mergeCodexRolloutTools(turn.Items, markers)
	}
	return out
}

func (b *codexChatBackend) codexRolloutToolMarkers(sessionID string, page codexTurnsPage) map[string][]codexRolloutTurnMarker {
	path := jsonlPathFor("codex", sessionID)
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}

	b.rolloutToolsMu.Lock()
	defer b.rolloutToolsMu.Unlock()
	cache := b.rolloutTools[sessionID]
	if cache == nil || cache.path != path || info.Size() < cache.offset {
		cache = &codexRolloutToolsCache{path: path, markers: map[string][]codexRolloutTurnMarker{}}
		b.rolloutTools[sessionID] = cache
	}
	if info.Size() > cache.offset {
		b.extendCodexRolloutToolCache(cache)
	}

	out := map[string][]codexRolloutTurnMarker{}
	for _, turn := range page.Data {
		if markers := cache.markers[turn.ID]; len(markers) > 0 {
			out[turn.ID] = append([]codexRolloutTurnMarker(nil), markers...)
		}
	}
	return out
}

func (b *codexChatBackend) extendCodexRolloutToolCache(cache *codexRolloutToolsCache) {
	f, err := os.Open(cache.path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(cache.offset, io.SeekStart); err != nil {
		return
	}

	reader := bufio.NewReaderSize(f, 64*1024)
	offset := cache.offset
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			complete := line[len(line)-1] == '\n' || json.Valid(line)
			if complete {
				offset += int64(len(line))
				cache.offset = offset
				if bytes.Contains(line, []byte(`"type":"custom_tool_call"`)) ||
					bytes.Contains(line, []byte(`"role":"assistant"`)) {
					codexAppendRolloutToolMarkers(cache.markers, line)
				}
			}
		}
		if readErr != nil {
			return
		}
	}
}

func codexAppendRolloutToolMarkers(markers map[string][]codexRolloutTurnMarker, line []byte) {
	var row struct {
		Type    string `json:"type"`
		Payload struct {
			Type     string `json:"type"`
			ID       string `json:"id"`
			Role     string `json:"role"`
			Name     string `json:"name"`
			Input    string `json:"input"`
			Status   string `json:"status"`
			Internal struct {
				TurnID      string `json:"turn_id"`
				TurnIDCamel string `json:"turnId"`
			} `json:"internal_chat_message_metadata_passthrough"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &row) != nil || row.Type != "response_item" {
		return
	}
	turnID := firstNonEmpty(row.Payload.Internal.TurnID, row.Payload.Internal.TurnIDCamel)
	if turnID == "" {
		return
	}
	switch row.Payload.Type {
	case "message":
		if row.Payload.Role != "assistant" {
			return
		}
		var text strings.Builder
		for _, part := range row.Payload.Content {
			if part.Type == "output_text" || part.Type == "text" {
				text.WriteString(part.Text)
			}
		}
		if text.Len() > 0 {
			markers[turnID] = append(markers[turnID], codexRolloutTurnMarker{AssistantText: text.String()})
		}
	case "custom_tool_call":
		if row.Payload.Name != "exec" {
			return
		}
		for _, item := range codexCodeModeToolItems(row.Payload.ID, row.Payload.Input, row.Payload.Status) {
			markers[turnID] = append(markers[turnID], codexRolloutTurnMarker{Item: item})
		}
	}
}

type codexNestedToolCall struct {
	name string
	args string
}

func codexCodeModeToolItems(callID, input, status string) []json.RawMessage {
	if strings.TrimSpace(status) == "" {
		status = "completed"
	}
	calls := codexNestedToolCalls(input)
	items := make([]json.RawMessage, 0, len(calls))
	for index, call := range calls {
		id := fmt.Sprintf("%s:%s:%d", callID, call.name, index+1)
		var item interface{}
		switch call.name {
		case "exec_command":
			command, _ := codexJSStringField(call.args, "cmd")
			cwd, _ := codexJSStringField(call.args, "workdir")
			item = map[string]interface{}{
				"id": id, "type": "commandExecution", "command": command, "cwd": cwd, "status": status,
			}
		case "view_image":
			path, _ := codexJSStringField(call.args, "path")
			item = map[string]interface{}{"id": id, "type": "imageView", "path": path, "status": status}
		case "image_gen__imagegen":
			prompt, _ := codexJSStringField(call.args, "prompt")
			item = map[string]interface{}{
				"id": id, "type": "imageGeneration", "revisedPrompt": prompt, "status": status,
			}
		case "apply_patch", "wait", "write_stdin":
			continue
		default:
			item = map[string]interface{}{
				"id": id, "type": "dynamicToolCall", "namespace": "code_mode", "tool": call.name,
				"status": status, "success": status != "failed",
			}
		}
		raw, err := json.Marshal(item)
		if err == nil {
			items = append(items, raw)
		}
	}
	return items
}

func codexNestedToolCalls(input string) []codexNestedToolCall {
	calls := []codexNestedToolCall{}
	for i := 0; i < len(input); {
		if next, ok := codexSkipJSQuotedOrComment(input, i); ok {
			i = next
			continue
		}
		if !strings.HasPrefix(input[i:], "tools.") || (i > 0 && codexJSIdentifierByte(input[i-1])) {
			i++
			continue
		}
		nameStart := i + len("tools.")
		nameEnd := nameStart
		for nameEnd < len(input) && codexJSIdentifierByte(input[nameEnd]) {
			nameEnd++
		}
		if nameEnd == nameStart {
			i++
			continue
		}
		open := nameEnd
		for open < len(input) && (input[open] == ' ' || input[open] == '\t' || input[open] == '\r' || input[open] == '\n') {
			open++
		}
		if open >= len(input) || input[open] != '(' {
			i = nameEnd
			continue
		}
		close := codexJSCallEnd(input, open)
		if close < 0 {
			break
		}
		calls = append(calls, codexNestedToolCall{name: input[nameStart:nameEnd], args: input[open+1 : close]})
		i = close + 1
	}
	return calls
}

func codexJSIdentifierByte(ch byte) bool {
	return ch == '_' || ch == '$' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}

func codexSkipJSQuotedOrComment(input string, start int) (int, bool) {
	if start >= len(input) {
		return start, false
	}
	if input[start] == '"' || input[start] == '\'' || input[start] == '`' {
		end := codexJSQuotedEnd(input, start)
		if end < 0 {
			return len(input), true
		}
		return end + 1, true
	}
	if input[start] != '/' || start+1 >= len(input) {
		return start, false
	}
	switch input[start+1] {
	case '/':
		if end := strings.IndexByte(input[start+2:], '\n'); end >= 0 {
			return start + 2 + end + 1, true
		}
		return len(input), true
	case '*':
		if end := strings.Index(input[start+2:], "*/"); end >= 0 {
			return start + 2 + end + 2, true
		}
		return len(input), true
	default:
		return start, false
	}
}

func codexJSCallEnd(input string, open int) int {
	depth := 1
	for i := open + 1; i < len(input); {
		if next, ok := codexSkipJSQuotedOrComment(input, i); ok {
			i = next
			continue
		}
		switch input[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

func codexJSQuotedEnd(input string, start int) int {
	quote := input[start]
	for i := start + 1; i < len(input); i++ {
		if input[i] == '\\' {
			i++
			continue
		}
		if input[i] == quote {
			return i
		}
	}
	return -1
}

func codexJSStringField(input, field string) (string, bool) {
	depth := 0
	for i := 0; i < len(input); {
		if next, ok := codexSkipJSQuotedOrComment(input, i); ok {
			i = next
			continue
		}
		switch input[i] {
		case '{':
			depth++
			i++
			continue
		case '}':
			depth--
			i++
			continue
		}
		if depth != 1 || !codexJSIdentifierByte(input[i]) {
			i++
			continue
		}
		start := i
		for i < len(input) && codexJSIdentifierByte(input[i]) {
			i++
		}
		if input[start:i] != field {
			continue
		}
		for i < len(input) && (input[i] == ' ' || input[i] == '\t' || input[i] == '\r' || input[i] == '\n') {
			i++
		}
		if i >= len(input) || input[i] != ':' {
			continue
		}
		i++
		for i < len(input) && (input[i] == ' ' || input[i] == '\t' || input[i] == '\r' || input[i] == '\n') {
			i++
		}
		if i >= len(input) || input[i] != '"' && input[i] != '\'' && input[i] != '`' {
			continue
		}
		end := codexJSQuotedEnd(input, i)
		if end < 0 {
			return "", false
		}
		return codexDecodeJSString(input[i : end+1])
	}
	return "", false
}

func codexDecodeJSString(literal string) (string, bool) {
	if len(literal) < 2 {
		return "", false
	}
	if literal[0] == '"' {
		var value string
		if json.Unmarshal([]byte(literal), &value) == nil {
			return value, true
		}
		return "", false
	}
	body := literal[1 : len(literal)-1]
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			out.WriteByte(body[i])
			continue
		}
		i++
		switch body[i] {
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case 'x':
			if i+2 < len(body) {
				if value, err := strconv.ParseUint(body[i+1:i+3], 16, 8); err == nil {
					out.WriteByte(byte(value))
					i += 2
					continue
				}
			}
			out.WriteByte('x')
		default:
			out.WriteByte(body[i])
		}
	}
	return out.String(), true
}

func mergeCodexRolloutTools(items []json.RawMessage, markers []codexRolloutTurnMarker) []json.RawMessage {
	assistantTexts := make([]string, 0)
	existing := map[string]int{}
	for _, raw := range items {
		var item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &item) == nil {
			if item.Type == "agentMessage" {
				assistantTexts = append(assistantTexts, item.Text)
			}
			if key := codexToolItemKey(raw); key != "" {
				existing[key]++
			}
		}
	}
	slots := make([][]json.RawMessage, len(assistantTexts)+1)
	anchor := 0
	for _, marker := range markers {
		if marker.AssistantText != "" {
			if anchor < len(assistantTexts) && strings.TrimSpace(marker.AssistantText) == strings.TrimSpace(assistantTexts[anchor]) {
				anchor++
			}
			continue
		}
		if len(marker.Item) == 0 {
			continue
		}
		if key := codexToolItemKey(marker.Item); key != "" && existing[key] > 0 {
			existing[key]--
			continue
		}
		slots[anchor] = append(slots[anchor], marker.Item)
	}

	toolCount := 0
	for _, slot := range slots {
		toolCount += len(slot)
	}
	if toolCount == 0 {
		return items
	}
	out := make([]json.RawMessage, 0, len(items)+toolCount)
	assistantIndex := 0
	for _, raw := range items {
		var item struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &item)
		if item.Type == "agentMessage" && assistantIndex == 0 {
			out = append(out, slots[0]...)
		}
		out = append(out, raw)
		if item.Type == "agentMessage" {
			assistantIndex++
			if assistantIndex < len(slots) {
				out = append(out, slots[assistantIndex]...)
			}
		}
	}
	if assistantIndex == 0 {
		out = append(out, slots[0]...)
	}
	return out
}

func codexToolItemKey(raw json.RawMessage) string {
	var item struct {
		Type      string `json:"type"`
		Command   string `json:"command"`
		Cwd       string `json:"cwd"`
		Path      string `json:"path"`
		Namespace string `json:"namespace"`
		Tool      string `json:"tool"`
	}
	if json.Unmarshal(raw, &item) != nil {
		return ""
	}
	switch item.Type {
	case "commandExecution":
		return item.Type + "\x00" + item.Command + "\x00" + item.Cwd
	case "imageView":
		return item.Type + "\x00" + item.Path
	case "dynamicToolCall":
		return item.Type + "\x00" + item.Namespace + "\x00" + item.Tool
	default:
		return ""
	}
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func descendingCodexTurnItems(page codexTurnsPage) []codexHistoryItemEntry {
	entries := make([]codexHistoryItemEntry, 0)
	for _, turn := range page.Data {
		for i := len(turn.Items) - 1; i >= 0; i-- {
			entries = append(entries, codexHistoryItemEntry{TurnID: turn.ID, Item: turn.Items[i]})
		}
	}
	return entries
}

func encodeCodexTurnsCursor(cursor codexTurnsCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return codexTurnsCursorPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeCodexTurnsCursor(cursor string) (codexTurnsCursor, error) {
	if cursor == "" {
		return codexTurnsCursor{}, nil
	}
	if !strings.HasPrefix(cursor, codexTurnsCursorPrefix) {
		return codexTurnsCursor{Page: cursor}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, codexTurnsCursorPrefix))
	if err != nil {
		return codexTurnsCursor{}, fmt.Errorf("decode Codex history cursor: %w", err)
	}
	var state codexTurnsCursor
	if err := json.Unmarshal(raw, &state); err != nil {
		return codexTurnsCursor{}, fmt.Errorf("decode Codex history cursor: %w", err)
	}
	return state, nil
}

func codexUserInput(text string, images []ChatAttachment, skills []ChatSkill) []map[string]string {
	input := make([]map[string]string, 0, 1+len(images)+len(skills))
	for _, skill := range skills {
		if skill.Name != "" && skill.Path != "" {
			input = append(input, map[string]string{"type": "skill", "name": skill.Name, "path": skill.Path})
		}
	}
	if text != "" {
		input = append(input, map[string]string{"type": "text", "text": text})
	}
	for _, img := range images {
		if img.Path != "" {
			input = append(input, map[string]string{"type": "localImage", "path": img.Path})
		}
	}
	return input
}

func (b *codexChatBackend) Skills(ctx context.Context, assistant, cwd string) ([]ChatSkill, error) {
	if assistant != "codex" {
		return nil, errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return nil, err
	}
	params := map[string]interface{}{}
	if cwd != "" {
		params["cwds"] = []string{cwd}
	}
	raw, err := rpc.call(ctx, "skills/list", params)
	if err != nil {
		return nil, err
	}
	var res struct {
		Data []struct {
			Cwd    string `json:"cwd"`
			Skills []struct {
				Name                   string `json:"name"`
				Description            string `json:"description"`
				ShortDescription       string `json:"shortDescription"`
				LegacyShortDescription string `json:"short_description"`
				Path                   string `json:"path"`
				Scope                  string `json:"scope"`
				Enabled                bool   `json:"enabled"`
				Interface              struct {
					ShortDescription string `json:"shortDescription"`
				} `json:"interface"`
			} `json:"skills"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("decode skills/list response: %w", err)
	}
	var skills []ChatSkill
	seenPaths := map[string]bool{}
	for _, entry := range res.Data {
		if cwd != "" && entry.Cwd != "" && filepath.Clean(entry.Cwd) != filepath.Clean(cwd) {
			continue
		}
		for _, skill := range entry.Skills {
			if skill.Enabled && skill.Name != "" && skill.Path != "" && !seenPaths[skill.Path] {
				description := strings.TrimSpace(skill.ShortDescription)
				if description == "" {
					description = strings.TrimSpace(skill.LegacyShortDescription)
				}
				if description == "" {
					description = strings.TrimSpace(skill.Interface.ShortDescription)
				}
				if description == "" {
					description = skill.Description
				}
				skills = append(skills, ChatSkill{
					ID: chatSkillID(skill.Path), Name: skill.Name, Description: description,
					Path: skill.Path, Scope: skill.Scope,
				})
				seenPaths[skill.Path] = true
			}
		}
	}
	sort.SliceStable(skills, func(i, j int) bool {
		return strings.ToLower(skills[i].Name) < strings.ToLower(skills[j].Name)
	})
	return skills, nil
}

func (b *codexChatBackend) Input(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment, skills []ChatSkill, opts ChatTurnOptions) (ChatInputResult, error) {
	if assistant != "codex" {
		return ChatInputResult{}, errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return ChatInputResult{}, err
	}
	b.mu.Lock()
	fresh := b.freshThreads[sessionID]
	b.mu.Unlock()
	if !fresh {
		_, err = b.resumeThread(ctx, rpc, sessionID)
	}
	if err != nil {
		if !codexThreadNotFound(err) {
			return ChatInputResult{}, err
		}
		b.resetRPC()
		rpc, err = b.ensure(ctx)
		if err != nil {
			return ChatInputResult{}, err
		}
		if _, err := b.resumeThread(ctx, rpc, sessionID); err != nil {
			return ChatInputResult{}, err
		}
	}
	input := codexUserInput(text, images, skills)
	params := codexTurnStartParams(sessionID, input, opts)
	if opts.ApprovalMode != "" {
		b.mu.Lock()
		b.approvalModes[sessionID] = opts.ApprovalMode
		b.mu.Unlock()
	}
	raw, err := rpc.call(ctx, "turn/start", params)
	if err != nil {
		if !codexThreadNotFound(err) {
			return ChatInputResult{}, err
		}
		b.resetRPC()
		rpc, err = b.ensure(ctx)
		if err != nil {
			return ChatInputResult{}, err
		}
		if _, err := b.resumeThread(ctx, rpc, sessionID); err != nil {
			return ChatInputResult{}, err
		}
		raw, err = rpc.call(ctx, "turn/start", params)
		if err != nil {
			return ChatInputResult{}, err
		}
	}
	var res struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return ChatInputResult{}, fmt.Errorf("decode turn/start response: %w", err)
	}
	if res.Turn.ID == "" {
		return ChatInputResult{}, fmt.Errorf("turn/start response missing turn id")
	}
	b.mu.Lock()
	delete(b.freshThreads, sessionID)
	b.lastTurn[sessionID] = res.Turn.ID
	b.mu.Unlock()
	return ChatInputResult{TurnID: res.Turn.ID}, nil
}

func (b *codexChatBackend) Steer(ctx context.Context, assistant, sessionID, clientMessageID, text string, images []ChatAttachment, skills []ChatSkill) (ChatInputResult, error) {
	if assistant != "codex" {
		return ChatInputResult{}, errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return ChatInputResult{}, err
	}
	b.mu.Lock()
	turnID := b.lastTurn[sessionID]
	b.mu.Unlock()
	if turnID == "" {
		return ChatInputResult{}, errNoActiveChatTurn
	}
	params := map[string]interface{}{
		"threadId": sessionID, "expectedTurnId": turnID, "input": codexUserInput(text, images, skills),
	}
	if clientMessageID != "" {
		params["clientUserMessageId"] = clientMessageID
	}
	raw, err := rpc.call(ctx, "turn/steer", params)
	if err != nil {
		if actualTurnID := codexActualTurnID(err); actualTurnID != "" && actualTurnID != turnID {
			params["expectedTurnId"] = actualTurnID
			b.mu.Lock()
			b.lastTurn[sessionID] = actualTurnID
			b.mu.Unlock()
			raw, err = rpc.call(ctx, "turn/steer", params)
		}
		if err != nil {
			return ChatInputResult{}, err
		}
	}
	var res struct {
		TurnID string `json:"turnId"`
	}
	_ = json.Unmarshal(raw, &res)
	if res.TurnID == "" {
		res.TurnID = turnID
	}
	return ChatInputResult{TurnID: res.TurnID}, nil
}

var codexExpectedTurnPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)expected active turn id [^[:alnum:]-]*([[:alnum:]-]+)[^[:alnum:]-]+but found [^[:alnum:]-]*([[:alnum:]-]+)`),
	regexp.MustCompile(`(?i)ExpectedTurnMismatch[^}]*actual:\s*"([^"]+)"`),
}

func codexActualTurnID(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for index, pattern := range codexExpectedTurnPatterns {
		match := pattern.FindStringSubmatch(message)
		if len(match) < 2 {
			continue
		}
		if index == 0 && len(match) >= 3 {
			return match[2]
		}
		return match[1]
	}
	return ""
}

func codexTurnStartParams(sessionID string, input []map[string]string, opts ChatTurnOptions) map[string]interface{} {
	params := map[string]interface{}{"threadId": sessionID, "input": input}
	if opts.ClientUserMessageID != "" {
		params["clientUserMessageId"] = opts.ClientUserMessageID
	}
	if opts.Model != "" {
		params["model"] = opts.Model
	}
	if opts.Effort != "" {
		params["effort"] = opts.Effort
	}
	if opts.ServiceTier != nil {
		if *opts.ServiceTier == "" {
			params["serviceTier"] = nil
		} else {
			params["serviceTier"] = *opts.ServiceTier
		}
	}
	for key, value := range codexApprovalSettingsParams(opts.ApprovalMode) {
		params[key] = value
	}
	return params
}

func codexApprovalSettingsParams(approvalMode string) map[string]interface{} {
	switch approvalMode {
	case "untrusted", "on-request":
		return map[string]interface{}{
			"approvalPolicy": approvalMode,
			"sandboxPolicy":  map[string]interface{}{"type": "workspaceWrite"},
		}
	case "full-access":
		return map[string]interface{}{
			"approvalPolicy": "never",
			"sandboxPolicy":  map[string]interface{}{"type": "dangerFullAccess"},
		}
	default:
		return nil
	}
}

func (b *codexChatBackend) Settings(ctx context.Context, assistant, sessionID, approvalMode string) error {
	if assistant != "codex" {
		return errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return err
	}
	params := map[string]interface{}{"threadId": sessionID}
	for key, value := range codexApprovalSettingsParams(approvalMode) {
		params[key] = value
	}
	if _, err = rpc.call(ctx, "thread/settings/update", params); err != nil {
		return err
	}
	b.applyApprovalMode(rpc, sessionID, approvalMode)
	return nil
}

func codexAutomaticApprovalResponse(method string, paramsRaw json.RawMessage) (interface{}, bool) {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		return map[string]interface{}{"decision": "accept"}, true
	case "item/permissions/requestApproval":
		var params struct {
			Permissions map[string]interface{} `json:"permissions"`
		}
		if json.Unmarshal(paramsRaw, &params) != nil || params.Permissions == nil {
			return nil, false
		}
		return map[string]interface{}{"permissions": params.Permissions, "scope": "session"}, true
	default:
		return nil, false
	}
}

func (b *codexChatBackend) applyApprovalMode(rpc codexRPCConn, sessionID, approvalMode string) {
	b.mu.Lock()
	b.approvalModes[sessionID] = approvalMode
	if approvalMode != "full-access" {
		b.mu.Unlock()
		return
	}
	pending := make(map[string]pendingCodexRequest)
	for key, request := range b.pending {
		if request.sessionID != sessionID {
			continue
		}
		if _, ok := codexAutomaticApprovalResponse(request.method, request.params); !ok {
			continue
		}
		pending[key] = request
		delete(b.pending, key)
	}
	b.mu.Unlock()

	for key, request := range pending {
		result, _ := codexAutomaticApprovalResponse(request.method, request.params)
		if err := rpc.respond(request.id, result); err != nil {
			b.mu.Lock()
			b.pending[key] = request
			b.mu.Unlock()
			continue
		}
		b.publish(newChatEvent("interaction_resolved", "codex", sessionID, "", "", map[string]interface{}{
			"requestId": key, "requestMethod": request.method, "response": result,
		}))
	}
}

func (b *codexChatBackend) autoApprove(rpc codexRPCConn, n rpcNotification) bool {
	sessionID := codexRequestSessionID(n.Params)
	b.mu.Lock()
	fullAccess := b.approvalModes[sessionID] == "full-access"
	b.mu.Unlock()
	if !fullAccess {
		return false
	}
	result, ok := codexAutomaticApprovalResponse(n.Method, n.Params)
	if !ok {
		return false
	}
	return rpc.respond(n.ID, result) == nil
}

func codexApprovalMode(policyRaw, sandboxRaw json.RawMessage) string {
	var policy string
	_ = json.Unmarshal(policyRaw, &policy)
	if mode := codexApprovalModeFromSettings(policy, sandboxRaw, "", ""); mode != "" {
		return mode
	}
	return "on-request"
}

type codexRolloutApprovalSettings struct {
	ApprovalPolicy string          `json:"approval_policy"`
	SandboxPolicy  json.RawMessage `json:"sandbox_policy"`
	Permission     struct {
		Type string `json:"type"`
	} `json:"permission_profile"`
	ActivePermission struct {
		ID string `json:"id"`
	} `json:"active_permission_profile"`
}

func codexApprovalModeFromSettings(policy string, sandboxRaw json.RawMessage, permissionType, activePermissionID string) string {
	var sandbox struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(sandboxRaw, &sandbox)
	policy = strings.ToLower(strings.TrimSpace(policy))
	permissionType = strings.ToLower(strings.TrimSpace(permissionType))
	normalizedSandbox := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(sandbox.Type))
	normalizedActive := strings.NewReplacer("-", "", "_", "", ":", "").Replace(strings.ToLower(activePermissionID))
	if normalizedSandbox == "dangerfullaccess" || normalizedActive == "dangerfullaccess" ||
		(policy == "never" && permissionType == "disabled") {
		return "full-access"
	}
	switch policy {
	case "untrusted", "on-request":
		return policy
	case "never":
		return "on-request"
	default:
		return ""
	}
}

func codexApprovalModeFromRollout(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	mode := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var row struct {
			Type    string `json:"type"`
			Payload struct {
				codexRolloutApprovalSettings
				Type           string                       `json:"type"`
				ThreadSettings codexRolloutApprovalSettings `json:"thread_settings"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		var settings codexRolloutApprovalSettings
		switch {
		case row.Type == "turn_context":
			settings = row.Payload.codexRolloutApprovalSettings
		case row.Type == "event_msg" && row.Payload.Type == "thread_settings_applied":
			settings = row.Payload.ThreadSettings
		default:
			continue
		}
		if current := codexApprovalModeFromSettings(
			settings.ApprovalPolicy,
			settings.SandboxPolicy,
			settings.Permission.Type,
			settings.ActivePermission.ID,
		); current != "" {
			mode = current
		}
	}
	return mode
}

func (b *codexChatBackend) modelOptions(ctx context.Context, rpc codexRPCConn) []ChatModelOption {
	raw, err := rpc.call(ctx, "model/list", map[string]interface{}{"limit": 100, "includeHidden": false})
	if err != nil {
		return nil
	}
	var res struct {
		Data []struct {
			ID                        string `json:"id"`
			Model                     string `json:"model"`
			DisplayName               string `json:"displayName"`
			Description               string `json:"description"`
			DefaultReasoningEffort    string `json:"defaultReasoningEffort"`
			DefaultServiceTier        string `json:"defaultServiceTier"`
			SupportedReasoningEfforts []struct {
				ReasoningEffort string `json:"reasoningEffort"`
				Description     string `json:"description"`
			} `json:"supportedReasoningEfforts"`
			ServiceTiers []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"serviceTiers"`
			Hidden    bool `json:"hidden"`
			IsDefault bool `json:"isDefault"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return nil
	}
	out := make([]ChatModelOption, 0, len(res.Data))
	for _, m := range res.Data {
		value := m.Model
		if value == "" {
			value = m.ID
		}
		if value == "" || m.Hidden {
			continue
		}
		name := m.DisplayName
		if name == "" {
			name = value
		}
		efforts := make([]ChatReasoningEffortOption, 0, len(m.SupportedReasoningEfforts))
		for _, effort := range m.SupportedReasoningEfforts {
			if effort.ReasoningEffort == "" {
				continue
			}
			efforts = append(efforts, ChatReasoningEffortOption{
				Value: effort.ReasoningEffort, Description: effort.Description,
			})
		}
		tiers := make([]ChatServiceTierOption, 0, len(m.ServiceTiers))
		for _, tier := range m.ServiceTiers {
			if tier.ID == "" {
				continue
			}
			name := tier.Name
			if name == "" {
				name = tier.ID
			}
			tiers = append(tiers, ChatServiceTierOption{
				Value: tier.ID, Name: name, Description: tier.Description,
			})
		}
		out = append(out, ChatModelOption{
			Value: value, DisplayName: name, Description: m.Description,
			DefaultEffort: m.DefaultReasoningEffort, SupportedEfforts: efforts,
			DefaultServiceTier: m.DefaultServiceTier, ServiceTiers: tiers, IsDefault: m.IsDefault,
		})
	}
	return out
}

func projectCodexHistoryPage(sessionID string, page codexItemsPage, usageByTurn map[string]codexTokenUsage) ChatHistoryPage {
	events := make([]ChatEvent, 0, len(page.Data))
	for i := len(page.Data) - 1; i >= 0; i-- {
		entry := page.Data[i]
		if ev, ok := projectCodexHistoryItemWithUsage(sessionID, entry.TurnID, entry.Item, usageByTurn[entry.TurnID]); ok {
			events = append(events, ev)
		}
	}
	next := ""
	if page.NextCursor != nil {
		next = *page.NextCursor
	}
	return ChatHistoryPage{Events: events, NextCursor: next}
}

func projectCodexHistoryItem(sessionID, turnID string, raw json.RawMessage) (ChatEvent, bool) {
	return projectCodexHistoryItemWithUsage(sessionID, turnID, raw, codexTokenUsage{})
}

func projectCodexHistoryItemWithUsage(sessionID, turnID string, raw json.RawMessage, usage codexTokenUsage) (ChatEvent, bool) {
	var base struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &base) != nil || base.ID == "" {
		return ChatEvent{}, false
	}
	switch base.Type {
	case "userMessage":
		var item struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				URL  string `json:"url"`
				Path string `json:"path"`
				Name string `json:"name"`
			} `json:"content"`
		}
		_ = json.Unmarshal(raw, &item)
		texts := []string{}
		images := []map[string]string{}
		for _, part := range item.Content {
			switch part.Type {
			case "text":
				if strings.TrimSpace(part.Text) != "" {
					texts = append(texts, part.Text)
				}
			case "image":
				img := map[string]string{"name": "历史图片"}
				if len(part.URL) <= 1<<20 {
					img["url"] = part.URL
				}
				images = append(images, img)
			case "localImage":
				images = append(images, map[string]string{"name": filepath.Base(part.Path), "path": part.Path})
			case "skill":
				texts = append(texts, "$"+part.Name)
			case "mention":
				texts = append(texts, "@"+part.Name)
			}
		}
		text := visibleCodexHistoryUserText(strings.Join(texts, "\n"))
		if text == "" && len(images) == 0 {
			return ChatEvent{}, false
		}
		data := map[string]interface{}{}
		_ = json.Unmarshal(raw, &data)
		data["text"] = text
		data["images"] = images
		return newChatEvent("user_done", "codex", sessionID, turnID, base.ID, data), true
	case "agentMessage":
		var item struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &item)
		if strings.TrimSpace(item.Text) == "" {
			return ChatEvent{}, false
		}
		data := map[string]interface{}{}
		_ = json.Unmarshal(raw, &data)
		data["text"] = item.Text
		mergeCodexHistoryUsage(data, usage)
		return newChatEvent("assistant_done", "codex", sessionID, turnID, base.ID, data), true
	default:
		if ev, ok := projectCodexSemanticItem(sessionID, turnID, raw, "completed"); ok {
			return ev, true
		}
		return projectCodexToolItem(sessionID, turnID, raw, "completed")
	}
}

func projectCodexSemanticItem(sessionID, turnID string, raw json.RawMessage, lifecycleStatus string) (ChatEvent, bool) {
	var item struct {
		ID         string   `json:"id"`
		Type       string   `json:"type"`
		Text       string   `json:"text"`
		Summary    []string `json:"summary"`
		Review     string   `json:"review"`
		DurationMS *int64   `json:"durationMs"`
		ClientID   *string  `json:"clientId"`
	}
	if json.Unmarshal(raw, &item) != nil || item.ID == "" {
		return ChatEvent{}, false
	}
	data := map[string]interface{}{"status": lifecycleStatus}
	switch item.Type {
	case "userMessage":
		return projectCodexHistoryItem(sessionID, turnID, raw)
	case "reasoning":
		data["summary"] = strings.Join(item.Summary, "\n\n")
		if item.DurationMS != nil {
			data["durationMs"] = *item.DurationMS
		}
		return newChatEvent("reasoning_update", "codex", sessionID, turnID, item.ID, data), true
	case "plan":
		data["text"] = item.Text
		return newChatEvent("plan_update", "codex", sessionID, turnID, item.ID, data), true
	case "contextCompaction":
		return newChatEvent("context_compaction", "codex", sessionID, turnID, item.ID, data), true
	case "enteredReviewMode":
		data["active"] = true
		data["review"] = item.Review
		return newChatEvent("review_status", "codex", sessionID, turnID, item.ID, data), true
	case "exitedReviewMode":
		data["active"] = false
		data["review"] = item.Review
		return newChatEvent("review_status", "codex", sessionID, turnID, item.ID, data), true
	default:
		return ChatEvent{}, false
	}
}

func mergeCodexHistoryUsage(data map[string]interface{}, usage codexTokenUsage) {
	if usage.empty() {
		return
	}
	usageMap, _ := data["usage"].(map[string]interface{})
	if usageMap == nil {
		usageMap = map[string]interface{}{}
	}
	if usage.InputTokens > 0 && !hasAnyUsageKey(usageMap, "inputTokens", "input_tokens", "promptTokens", "prompt_tokens") {
		usageMap["inputTokens"] = usage.InputTokens
	}
	if usage.OutputTokens > 0 && !hasAnyUsageKey(usageMap, "outputTokens", "output_tokens", "completionTokens", "completion_tokens") {
		usageMap["outputTokens"] = usage.OutputTokens
	}
	if usage.CachedInputTokens > 0 && !hasAnyUsageKey(usageMap, "cachedInputTokens", "cached_input_tokens", "inputCachedTokens", "input_cached_tokens") {
		usageMap["cachedInputTokens"] = usage.CachedInputTokens
	}
	if len(usageMap) > 0 {
		data["usage"] = usageMap
	}
}

func hasAnyUsageKey(m map[string]interface{}, keys ...string) bool {
	for _, key := range keys {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}

func projectCodexToolItem(sessionID, turnID string, raw json.RawMessage, lifecycleStatus string) (ChatEvent, bool) {
	var base struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &base) != nil || base.ID == "" {
		return ChatEvent{}, false
	}
	if base.Type == "fileChange" {
		var item map[string]interface{}
		_ = json.Unmarshal(raw, &item)
		if status, ok := item["status"].(string); !ok || strings.TrimSpace(status) == "" {
			item["status"] = lifecycleStatus
		}
		return newChatEvent("diff_update", "codex", sessionID, turnID, base.ID, item), true
	}

	data := map[string]interface{}{"kind": base.Type, "status": lifecycleStatus}
	switch base.Type {
	case "commandExecution":
		var item struct {
			Command          string                   `json:"command"`
			CommandActions   []map[string]interface{} `json:"commandActions"`
			Cwd              string                   `json:"cwd"`
			AggregatedOutput string                   `json:"aggregatedOutput"`
			Status           string                   `json:"status"`
			ExitCode         *int                     `json:"exitCode"`
			DurationMS       *int64                   `json:"durationMs"`
		}
		_ = json.Unmarshal(raw, &item)
		data["title"] = "运行命令"
		data["summary"] = item.Command
		data["commandActions"] = item.CommandActions
		data["meta"] = item.Cwd
		data["output"] = capChatHistoryText(item.AggregatedOutput, 32<<10)
		if item.Status != "" {
			data["status"] = item.Status
		}
		if item.ExitCode != nil {
			data["exitCode"] = *item.ExitCode
		}
		if item.DurationMS != nil {
			data["durationMs"] = *item.DurationMS
		}
	case "mcpToolCall":
		var item struct {
			Server    string          `json:"server"`
			Tool      string          `json:"tool"`
			Status    string          `json:"status"`
			Arguments json.RawMessage `json:"arguments"`
			Result    json.RawMessage `json:"result"`
			Error     *struct {
				Message string `json:"message"`
			} `json:"error"`
			DurationMS *int64 `json:"durationMs"`
			AppContext *struct {
				AppName    string `json:"appName"`
				ActionName string `json:"actionName"`
			} `json:"appContext"`
		}
		_ = json.Unmarshal(raw, &item)
		title, summary := codexToolName(item.AppContext, item.Server, item.Tool, item.Arguments)
		data["title"] = title
		data["summary"] = summary
		data["detail"] = compactChatJSON(item.Arguments, 12<<10)
		data["output"] = compactMCPResult(item.Result, 16<<10)
		if item.Error != nil {
			data["output"] = item.Error.Message
			data["status"] = "failed"
		} else if item.Status != "" {
			data["status"] = item.Status
		}
		if item.DurationMS != nil {
			data["durationMs"] = *item.DurationMS
		}
	case "dynamicToolCall":
		var item struct {
			Namespace    string          `json:"namespace"`
			Tool         string          `json:"tool"`
			Arguments    json.RawMessage `json:"arguments"`
			Status       string          `json:"status"`
			ContentItems json.RawMessage `json:"contentItems"`
			Success      *bool           `json:"success"`
			DurationMS   *int64          `json:"durationMs"`
		}
		_ = json.Unmarshal(raw, &item)
		data["title"] = "调用工具"
		data["summary"] = strings.Trim(strings.Join([]string{item.Namespace, item.Tool}, " · "), " ·")
		data["detail"] = compactChatJSON(item.Arguments, 12<<10)
		data["output"] = compactDynamicToolResult(item.ContentItems, 16<<10)
		if item.Status != "" {
			data["status"] = item.Status
		}
		if item.Success != nil && !*item.Success {
			data["status"] = "failed"
		}
		if item.DurationMS != nil {
			data["durationMs"] = *item.DurationMS
		}
	case "webSearch":
		var item struct {
			Query  string          `json:"query"`
			Action json.RawMessage `json:"action"`
		}
		_ = json.Unmarshal(raw, &item)
		data["title"] = "网页搜索"
		data["summary"] = item.Query
		data["detail"] = compactChatJSON(item.Action, 8<<10)
	case "imageView":
		var item struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(raw, &item)
		data["title"] = "查看图片"
		data["summary"] = item.Path
		data["mediaPath"] = item.Path
	case "imageGeneration":
		var item struct {
			Status        string `json:"status"`
			RevisedPrompt string `json:"revisedPrompt"`
			SavedPath     string `json:"savedPath"`
		}
		_ = json.Unmarshal(raw, &item)
		data["title"] = "生成图片"
		data["summary"] = firstNonEmpty(item.SavedPath, item.RevisedPrompt)
		data["mediaPath"] = item.SavedPath
		if item.Status != "" {
			data["status"] = item.Status
		}
	case "sleep":
		var item struct {
			DurationMS int64 `json:"durationMs"`
		}
		_ = json.Unmarshal(raw, &item)
		data["title"] = "等待"
		data["summary"] = formatToolDuration(item.DurationMS)
		data["durationMs"] = item.DurationMS
	case "collabAgentToolCall":
		var item struct {
			Tool              string                 `json:"tool"`
			Status            string                 `json:"status"`
			ReceiverThreadIDs []string               `json:"receiverThreadIds"`
			Prompt            string                 `json:"prompt"`
			AgentsStates      map[string]interface{} `json:"agentsStates"`
		}
		_ = json.Unmarshal(raw, &item)
		data["title"] = collabToolTitle(item.Tool)
		data["summary"] = strings.Join(item.ReceiverThreadIDs, ", ")
		data["detail"] = capChatHistoryText(item.Prompt, 12<<10)
		data["output"] = compactChatValue(item.AgentsStates, 12<<10)
		if item.Status != "" {
			data["status"] = item.Status
		}
	case "subAgentActivity":
		var item struct {
			Kind      string `json:"kind"`
			AgentPath string `json:"agentPath"`
		}
		_ = json.Unmarshal(raw, &item)
		data["title"] = "子任务活动"
		data["summary"] = item.AgentPath
		data["detail"] = item.Kind
	default:
		return ChatEvent{}, false
	}
	return newChatEvent("tool_update", "codex", sessionID, turnID, base.ID, data), true
}

func codexToolName(appContext *struct {
	AppName    string `json:"appName"`
	ActionName string `json:"actionName"`
}, server, tool string, arguments json.RawMessage) (string, string) {
	summary := strings.Trim(strings.Join([]string{server, tool}, " · "), " ·")
	if appContext != nil {
		name := strings.Trim(strings.Join([]string{appContext.AppName, appContext.ActionName}, " · "), " ·")
		if name != "" {
			return name, summary
		}
	}
	if title, detail, ok := codexNodeReplToolName(server, tool, arguments); ok {
		return title, detail
	}
	name := summary
	if name != "" {
		return name, summary
	}
	return "MCP 工具", summary
}

func codexNodeReplToolName(server, tool string, arguments json.RawMessage) (string, string, bool) {
	if !strings.EqualFold(strings.TrimSpace(server), "node_repl") || !strings.EqualFold(strings.TrimSpace(tool), "js") {
		return "", "", false
	}
	var args struct {
		Title string `json:"title"`
		Code  string `json:"code"`
	}
	_ = json.Unmarshal(arguments, &args)
	detail := strings.TrimSpace(args.Title)
	probe := strings.ToLower(args.Title + "\n" + args.Code)
	switch {
	case strings.Contains(probe, "browser-client") || strings.Contains(probe, "agent.browsers") ||
		strings.Contains(probe, "globalthis.browser") || strings.Contains(probe, "globalthis.chrome") ||
		strings.Contains(probe, "playwright"):
		return "调用内部浏览器", detail, true
	case strings.Contains(probe, "computer-use") || strings.Contains(probe, "globalthis.sky") || strings.Contains(probe, "sky."):
		return "操作本机界面", detail, true
	default:
		return "运行 JavaScript", detail, true
	}
}

func compactChatJSON(raw json.RawMessage, max int) string {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" || text == "{}" || text == "[]" {
		return ""
	}
	var value interface{}
	if json.Unmarshal(raw, &value) == nil {
		return compactChatValue(value, max)
	}
	return capChatHistoryText(text, max)
}

func compactChatValue(value interface{}, max int) string {
	if value == nil {
		return ""
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return capChatHistoryText(string(b), max)
}

func compactMCPResult(raw json.RawMessage, max int) string {
	var result struct {
		Content           []json.RawMessage `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return compactChatJSON(raw, max)
	}
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		var item struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
			URI      string `json:"uri"`
			Resource *struct {
				Text string `json:"text"`
				URI  string `json:"uri"`
			} `json:"resource"`
		}
		if json.Unmarshal(content, &item) != nil {
			continue
		}
		switch item.Type {
		case "text":
			if strings.TrimSpace(item.Text) != "" {
				parts = append(parts, item.Text)
			}
		case "image", "audio":
			label := "媒体"
			if item.Type == "image" {
				label = "图片"
			}
			if item.MimeType != "" {
				label += " " + item.MimeType
			}
			parts = append(parts, "["+label+"]")
		case "resource", "resource_link":
			if item.Resource != nil && strings.TrimSpace(item.Resource.Text) != "" {
				parts = append(parts, item.Resource.Text)
			} else {
				uri := item.URI
				if uri == "" && item.Resource != nil {
					uri = item.Resource.URI
				}
				parts = append(parts, firstNonEmpty(uri, "[资源]"))
			}
		}
	}
	if len(parts) > 0 {
		return capChatHistoryText(strings.Join(parts, "\n"), max)
	}
	return compactChatJSON(result.StructuredContent, max)
}

func compactDynamicToolResult(raw json.RawMessage, max int) string {
	var items []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"imageUrl"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return compactChatJSON(raw, max)
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "inputText":
			if strings.TrimSpace(item.Text) != "" {
				parts = append(parts, item.Text)
			}
		case "inputImage":
			parts = append(parts, "[图片]")
		}
	}
	return capChatHistoryText(strings.Join(parts, "\n"), max)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func formatToolDuration(ms int64) string {
	if ms <= 0 {
		return ""
	}
	if ms < 1000 {
		return fmt.Sprintf("%d ms", ms)
	}
	return fmt.Sprintf("%.1f 秒", float64(ms)/1000)
}

func collabToolTitle(tool string) string {
	switch tool {
	case "spawnAgent":
		return "启动子任务"
	case "sendInput":
		return "发送子任务消息"
	case "resumeAgent":
		return "恢复子任务"
	case "wait":
		return "等待子任务"
	case "closeAgent":
		return "关闭子任务"
	default:
		return "多代理调用"
	}
}

func visibleCodexHistoryUserText(s string) string {
	text := strings.TrimSpace(s)
	if strings.HasPrefix(text, "# Files mentioned by the user") {
		for _, marker := range []string{"## My request for Codex:", "## My request:"} {
			if i := strings.Index(text, marker); i >= 0 {
				return strings.TrimSpace(text[i+len(marker):])
			}
		}
		return ""
	}
	if codexInjected(text) {
		return ""
	}
	return text
}

func capChatHistoryText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…（历史输出已截断）"
}

func (b *codexChatBackend) Events(ctx context.Context, assistant, sessionID string) (<-chan ChatEvent, error) {
	if assistant != "codex" {
		return nil, errUnsupportedChatAssistant
	}
	if _, err := b.ensure(ctx); err != nil {
		return nil, err
	}
	b.mu.Lock()
	backlog := append([]ChatEvent(nil), b.backlog[sessionID]...)
	for _, request := range b.pending {
		if request.sessionID != sessionID {
			continue
		}
		backlog = append(backlog, mapCodexServerRequest(rpcNotification{
			ID: request.id, Method: request.method, Params: request.params,
		}))
	}
	bufferSize := 64
	if required := len(backlog) + 32; required > bufferSize {
		bufferSize = required
	}
	ch := make(chan ChatEvent, bufferSize)
	for _, event := range backlog {
		ch <- event
	}
	if b.subs[sessionID] == nil {
		b.subs[sessionID] = map[chan ChatEvent]struct{}{}
	}
	b.subs[sessionID][ch] = struct{}{}
	var syncer *codexConnectedSync
	if b.syncers[sessionID] == nil {
		syncCtx, cancel := context.WithCancel(context.Background())
		syncer = &codexConnectedSync{cancel: cancel}
		b.syncers[sessionID] = syncer
		go b.runConnectedSync(syncCtx, sessionID, syncer)
	}
	delete(b.backlog, sessionID)
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		delete(b.subs[sessionID], ch)
		if len(b.subs[sessionID]) == 0 {
			delete(b.subs, sessionID)
			if current := b.syncers[sessionID]; current != nil {
				current.cancel()
			}
		}
		b.mu.Unlock()
		close(ch)
	}()
	return ch, nil
}

func (b *codexChatBackend) Interrupt(ctx context.Context, assistant, sessionID string) error {
	if assistant != "codex" {
		return errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return err
	}
	b.mu.Lock()
	turnID := b.lastTurn[sessionID]
	b.mu.Unlock()
	if turnID == "" {
		return errNoActiveChatTurn
	}
	_, err = rpc.call(ctx, "turn/interrupt", map[string]string{"threadId": sessionID, "turnId": turnID})
	if err == nil {
		if cleaner, ok := rpc.(interface{ terminateCommandDescendants() }); ok {
			cleaner.terminateCommandDescendants()
		}
	}
	return err
}

func (b *codexChatBackend) Respond(ctx context.Context, assistant, sessionID, requestID string, response json.RawMessage) error {
	if assistant != "codex" {
		return errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return err
	}
	b.mu.Lock()
	p, ok := b.pending[requestID]
	b.mu.Unlock()
	if !ok {
		return errChatRequestNotFound
	}
	if p.sessionID != sessionID {
		return errChatRequestNotFound
	}
	result, err := normalizeCodexServerResponse(p, response)
	if err != nil {
		return err
	}
	if err := rpc.respond(p.id, result); err != nil {
		return err
	}
	b.mu.Lock()
	if current, exists := b.pending[requestID]; exists && string(current.id) == string(p.id) {
		delete(b.pending, requestID)
	}
	b.mu.Unlock()
	b.publish(newChatEvent("interaction_resolved", "codex", sessionID, "", "", map[string]interface{}{
		"requestId": requestID, "requestMethod": p.method, "response": result,
	}))
	return nil
}

func (b *codexChatBackend) dispatch(rpc codexRPCConn, notes <-chan rpcNotification) {
	for n := range notes {
		if len(n.ID) > 0 && b.autoApprove(rpc, n) {
			continue
		}
		if len(n.ID) > 0 && codexServerRequestSupported(n.Method) {
			if key := rpcIDKey(n.ID); key != "" {
				sessionID := codexRequestSessionID(n.Params)
				b.mu.Lock()
				b.pending[key] = pendingCodexRequest{
					id: append(json.RawMessage(nil), n.ID...), method: n.Method,
					params: append(json.RawMessage(nil), n.Params...), sessionID: sessionID,
				}
				b.mu.Unlock()
			}
		}
		for _, ev := range mapCodexNotification(n) {
			b.rememberSyncEvents(ev.SessionID, []ChatEvent{ev})
			if ev.Type == "turn_started" && ev.SessionID != "" && ev.TurnID != "" {
				b.mu.Lock()
				b.lastTurn[ev.SessionID] = ev.TurnID
				b.mu.Unlock()
			}
			if ev.Type == "turn_done" && ev.SessionID != "" {
				b.mu.Lock()
				if ev.TurnID == "" || b.lastTurn[ev.SessionID] == ev.TurnID {
					delete(b.lastTurn, ev.SessionID)
				}
				b.mu.Unlock()
			}
			b.mu.Lock()
			if b.resuming[ev.SessionID] {
				b.buffered[ev.SessionID] = append(b.buffered[ev.SessionID], ev)
				b.mu.Unlock()
				continue
			}
			b.mu.Unlock()
			b.publish(ev)
		}
	}
}

func (b *codexChatBackend) publish(ev ChatEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[ev.SessionID] {
		ch <- ev
	}
}

func rpcIDKey(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return fmt.Sprintf("%d", n)
	}
	return string(raw)
}

func codexServerRequestSupported(method string) bool {
	switch method {
	case "item/commandExecution/requestApproval",
		"item/fileChange/requestApproval",
		"item/permissions/requestApproval",
		"item/tool/requestUserInput",
		"tool/requestUserInput",
		"mcpServer/elicitation/request":
		return true
	default:
		return false
	}
}

func codexRequestSessionID(raw json.RawMessage) string {
	var params struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(raw, &params)
	return params.ThreadID
}

func normalizeCodexServerResponse(request pendingCodexRequest, raw json.RawMessage) (interface{}, error) {
	var response map[string]interface{}
	if err := json.Unmarshal(raw, &response); err != nil || response == nil {
		return nil, fmt.Errorf("invalid response for %s", request.method)
	}
	switch request.method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		return normalizeCodexApprovalResponse(request.method, response)
	case "item/permissions/requestApproval":
		return normalizeCodexPermissionsResponse(request.params, response)
	case "item/tool/requestUserInput", "tool/requestUserInput":
		return normalizeCodexUserInputResponse(request.params, response)
	case "mcpServer/elicitation/request":
		return normalizeCodexElicitationResponse(request.params, response)
	default:
		return nil, fmt.Errorf("unsupported Codex server request: %s", request.method)
	}
}

func normalizeCodexApprovalResponse(method string, response map[string]interface{}) (interface{}, error) {
	if len(response) != 1 {
		return nil, fmt.Errorf("invalid approval response")
	}
	decision, ok := response["decision"].(string)
	if !ok {
		return nil, fmt.Errorf("approval response is missing decision")
	}
	allowed := map[string]bool{"accept": true, "acceptForSession": true, "decline": true, "cancel": true}
	if !allowed[decision] {
		return nil, fmt.Errorf("invalid approval decision")
	}
	if method == "item/fileChange/requestApproval" && decision == "acceptForSession" {
		return map[string]interface{}{"decision": decision}, nil
	}
	return map[string]interface{}{"decision": decision}, nil
}

func normalizeCodexPermissionsResponse(paramsRaw json.RawMessage, response map[string]interface{}) (interface{}, error) {
	permissions, ok := response["permissions"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("permissions response is missing permissions")
	}
	scope := "turn"
	if value, exists := response["scope"]; exists {
		var valid bool
		scope, valid = value.(string)
		if !valid || (scope != "turn" && scope != "session") {
			return nil, fmt.Errorf("invalid permission scope")
		}
	}
	result := map[string]interface{}{"permissions": permissions, "scope": scope}
	if value, exists := response["strictAutoReview"]; exists {
		strict, valid := value.(bool)
		if !valid {
			return nil, fmt.Errorf("invalid strictAutoReview value")
		}
		result["strictAutoReview"] = strict
	}
	if len(response) > len(result) {
		return nil, fmt.Errorf("invalid permissions response fields")
	}
	if len(permissions) == 0 {
		return result, nil
	}
	var params struct {
		Permissions map[string]interface{} `json:"permissions"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.Permissions == nil {
		return nil, fmt.Errorf("permission request is malformed")
	}
	if !jsonValuesEqual(params.Permissions, permissions) {
		return nil, fmt.Errorf("granted permissions must match the requested permissions")
	}
	return result, nil
}

func normalizeCodexUserInputResponse(paramsRaw json.RawMessage, response map[string]interface{}) (interface{}, error) {
	if len(response) != 1 {
		return nil, fmt.Errorf("invalid user input response")
	}
	answers, ok := response["answers"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("user input response is missing answers")
	}
	var params struct {
		Questions []struct {
			ID string `json:"id"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		return nil, fmt.Errorf("user input request is malformed")
	}
	questionIDs := make(map[string]bool, len(params.Questions))
	for _, question := range params.Questions {
		questionIDs[question.ID] = true
	}
	normalized := make(map[string]interface{}, len(answers))
	for id, answerRaw := range answers {
		if !questionIDs[id] {
			return nil, fmt.Errorf("unknown user input question: %s", id)
		}
		answer, ok := answerRaw.(map[string]interface{})
		if !ok || len(answer) != 1 {
			return nil, fmt.Errorf("invalid answer for question: %s", id)
		}
		values, ok := answer["answers"].([]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid answer for question: %s", id)
		}
		stringsOut := make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if !ok || len(text) > 32*1024 {
				return nil, fmt.Errorf("invalid answer for question: %s", id)
			}
			stringsOut = append(stringsOut, text)
		}
		normalized[id] = map[string]interface{}{"answers": stringsOut}
	}
	return map[string]interface{}{"answers": normalized}, nil
}

func normalizeCodexElicitationResponse(paramsRaw json.RawMessage, response map[string]interface{}) (interface{}, error) {
	action, ok := response["action"].(string)
	if !ok || (action != "accept" && action != "decline" && action != "cancel") {
		return nil, fmt.Errorf("invalid elicitation action")
	}
	if action != "accept" {
		if len(response) != 1 {
			return nil, fmt.Errorf("declined elicitation cannot include content")
		}
		return map[string]interface{}{"action": action}, nil
	}
	var params struct {
		Mode            string                 `json:"mode"`
		RequestedSchema map[string]interface{} `json:"requestedSchema"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		return nil, fmt.Errorf("elicitation request is malformed")
	}
	result := map[string]interface{}{"action": action}
	if params.Mode == "url" {
		if len(response) != 1 {
			return nil, fmt.Errorf("URL elicitation response cannot include content")
		}
		return result, nil
	}
	content, ok := response["content"].(map[string]interface{})
	if !ok || len(response) != 2 {
		return nil, fmt.Errorf("accepted elicitation is missing content")
	}
	if err := validateCodexElicitationContent(params.RequestedSchema, content); err != nil {
		return nil, err
	}
	result["content"] = content
	return result, nil
}

func validateCodexElicitationContent(schema, content map[string]interface{}) error {
	properties, _ := schema["properties"].(map[string]interface{})
	required := map[string]bool{}
	if values, ok := schema["required"].([]interface{}); ok {
		for _, value := range values {
			if key, ok := value.(string); ok {
				required[key] = true
			}
		}
	}
	for key := range required {
		if _, exists := content[key]; !exists {
			return fmt.Errorf("elicitation field is required: %s", key)
		}
	}
	for key, value := range content {
		field, ok := properties[key].(map[string]interface{})
		if !ok {
			return fmt.Errorf("unknown elicitation field: %s", key)
		}
		fieldType, _ := field["type"].(string)
		switch fieldType {
		case "string":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("elicitation field %s must be text", key)
			}
		case "number", "integer":
			number, ok := value.(float64)
			if !ok || (fieldType == "integer" && number != float64(int64(number))) {
				return fmt.Errorf("elicitation field %s must be numeric", key)
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("elicitation field %s must be boolean", key)
			}
		case "array":
			if _, ok := value.([]interface{}); !ok {
				return fmt.Errorf("elicitation field %s must be a list", key)
			}
		default:
			return fmt.Errorf("unsupported elicitation field type: %s", fieldType)
		}
	}
	return nil
}

func jsonValuesEqual(left, right interface{}) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}
