package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

const chatHistoryPageSize = 40
const codexTurnsPageSize = 12
const codexTurnsCursorPrefix = "turn-items:"
const codexHistoryCacheLimit = 32

type codexConnector func(context.Context) (codexRPCConn, func(), error)

type pendingApproval struct {
	id     json.RawMessage
	method string
}

type codexChatBackend struct {
	connect codexConnector

	mu       sync.Mutex
	rpc      codexRPCConn
	cleanup  func()
	subs     map[string]map[chan ChatEvent]struct{}
	lastTurn map[string]string
	pending  map[string]pendingApproval
	// -1 means thread/items/list is unsupported, 0 unknown, 1 supported.
	itemsListSupport int
	historyPages     map[string]codexTurnsPage
	historyPageOrder []string
}

func newCodexChatBackend(connect codexConnector) *codexChatBackend {
	return &codexChatBackend{
		connect:      connect,
		subs:         map[string]map[chan ChatEvent]struct{}{},
		lastTurn:     map[string]string{},
		pending:      map[string]pendingApproval{},
		historyPages: map[string]codexTurnsPage{},
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

	rpc, cleanup, err := b.connect(ctx)
	if err != nil {
		return nil, errAppServerUnavailable
	}

	b.mu.Lock()
	if b.rpc == nil {
		b.rpc = rpc
		b.cleanup = cleanup
		go b.dispatch(rpc.notifications())
	} else {
		if cleanup != nil {
			cleanup()
		}
	}
	rpc = b.rpc
	b.mu.Unlock()
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
	} `json:"thread"`
	InitialTurnsPage struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	} `json:"initialTurnsPage"`
	Model           string          `json:"model"`
	ReasoningEffort string          `json:"reasoningEffort"`
	ServiceTier     string          `json:"serviceTier"`
	ApprovalPolicy  json.RawMessage `json:"approvalPolicy"`
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

func (b *codexChatBackend) resumeThread(ctx context.Context, rpc codexRPCConn, sessionID string) (codexResumeWire, error) {
	params := map[string]interface{}{
		"threadId":     sessionID,
		"excludeTurns": true,
		"initialTurnsPage": map[string]interface{}{
			"limit": 1, "sortDirection": "desc", "itemsView": "summary",
		},
	}
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
	status := codexThreadStatus(res.Thread.Status)
	activeTurnID := ""
	b.mu.Lock()
	if status == "active" {
		if len(res.InitialTurnsPage.Data) > 0 {
			activeTurnID = res.InitialTurnsPage.Data[0].ID
		}
		if activeTurnID != "" {
			b.lastTurn[sessionID] = activeTurnID
		}
	} else {
		delete(b.lastTurn, sessionID)
	}
	b.mu.Unlock()
	return ChatResumeResult{
		SessionID: sessionID, ThreadID: threadID, Status: status, ActiveTurnID: activeTurnID,
		History: history, Model: res.Model, Effort: res.ReasoningEffort, ServiceTier: res.ServiceTier,
		ApprovalMode: codexApprovalMode(res.ApprovalPolicy, res.Sandbox),
		Models:       b.modelOptions(ctx, rpc),
	}, nil
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
	return projectCodexHistoryPage(sessionID, page), nil
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
	for len(events) < chatHistoryPageSize {
		if seenPages[state.Page] {
			return ChatHistoryPage{}, fmt.Errorf("Codex history cursor loop")
		}
		seenPages[state.Page] = true
		page, err := b.fetchTurnsPage(ctx, rpc, sessionID, state.Page)
		if err != nil {
			return ChatHistoryPage{}, err
		}
		entries := descendingCodexTurnItems(page)
		if state.Offset < 0 || state.Offset > len(entries) {
			return ChatHistoryPage{}, fmt.Errorf("invalid Codex history cursor offset")
		}
		for state.Offset < len(entries) && len(events) < chatHistoryPageSize {
			entry := entries[state.Offset]
			state.Offset++
			if ev, ok := projectCodexHistoryItem(sessionID, entry.TurnID, entry.Item); ok {
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

func codexUserInput(text string, images []ChatAttachment) []map[string]string {
	input := make([]map[string]string, 0, 1+len(images))
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

func (b *codexChatBackend) Input(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment, opts ChatTurnOptions) (ChatInputResult, error) {
	if assistant != "codex" {
		return ChatInputResult{}, errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return ChatInputResult{}, err
	}
	if _, err := b.resumeThread(ctx, rpc, sessionID); err != nil {
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
	input := codexUserInput(text, images)
	params := codexTurnStartParams(sessionID, input, opts)
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
	_ = json.Unmarshal(raw, &res)
	if res.Turn.ID != "" {
		b.mu.Lock()
		b.lastTurn[sessionID] = res.Turn.ID
		b.mu.Unlock()
	}
	return ChatInputResult{TurnID: res.Turn.ID}, nil
}

func (b *codexChatBackend) Steer(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment) (ChatInputResult, error) {
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
	raw, err := rpc.call(ctx, "turn/steer", map[string]interface{}{
		"threadId": sessionID, "expectedTurnId": turnID, "input": codexUserInput(text, images),
	})
	if err != nil {
		return ChatInputResult{}, err
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

func codexTurnStartParams(sessionID string, input []map[string]string, opts ChatTurnOptions) map[string]interface{} {
	params := map[string]interface{}{"threadId": sessionID, "input": input}
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
	switch opts.ApprovalMode {
	case "untrusted", "on-request", "never":
		params["approvalPolicy"] = opts.ApprovalMode
		params["sandboxPolicy"] = map[string]interface{}{"type": "workspaceWrite"}
	case "full-access":
		params["approvalPolicy"] = "never"
		params["sandboxPolicy"] = map[string]interface{}{"type": "dangerFullAccess"}
	}
	return params
}

func codexApprovalMode(policyRaw, sandboxRaw json.RawMessage) string {
	var sandbox struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(sandboxRaw, &sandbox)
	if sandbox.Type == "dangerFullAccess" {
		return "full-access"
	}
	var policy string
	if json.Unmarshal(policyRaw, &policy) == nil {
		switch policy {
		case "untrusted", "on-request", "never":
			return policy
		}
	}
	return "on-request"
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

func projectCodexHistoryPage(sessionID string, page codexItemsPage) ChatHistoryPage {
	events := make([]ChatEvent, 0, len(page.Data))
	for i := len(page.Data) - 1; i >= 0; i-- {
		entry := page.Data[i]
		if ev, ok := projectCodexHistoryItem(sessionID, entry.TurnID, entry.Item); ok {
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
				images = append(images, map[string]string{"name": filepath.Base(part.Path)})
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
		return newChatEvent("assistant_done", "codex", sessionID, turnID, base.ID, data), true
	default:
		return projectCodexToolItem(sessionID, turnID, raw, "completed")
	}
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
		return newChatEvent("diff_update", "codex", sessionID, turnID, base.ID, item), true
	}

	data := map[string]interface{}{"kind": base.Type, "status": lifecycleStatus}
	switch base.Type {
	case "commandExecution":
		var item struct {
			Command          string `json:"command"`
			Cwd              string `json:"cwd"`
			AggregatedOutput string `json:"aggregatedOutput"`
			Status           string `json:"status"`
			ExitCode         *int   `json:"exitCode"`
			DurationMS       *int64 `json:"durationMs"`
		}
		_ = json.Unmarshal(raw, &item)
		data["title"] = "运行命令"
		data["summary"] = item.Command
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
		data["title"] = codexToolName(item.AppContext, item.Server, item.Tool)
		data["summary"] = strings.Trim(strings.Join([]string{item.Server, item.Tool}, " · "), " ·")
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
	case "imageGeneration":
		var item struct {
			Status        string `json:"status"`
			RevisedPrompt string `json:"revisedPrompt"`
			SavedPath     string `json:"savedPath"`
		}
		_ = json.Unmarshal(raw, &item)
		data["title"] = "生成图片"
		data["summary"] = firstNonEmpty(item.SavedPath, item.RevisedPrompt)
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
}, server, tool string) string {
	if appContext != nil {
		name := strings.Trim(strings.Join([]string{appContext.AppName, appContext.ActionName}, " · "), " ·")
		if name != "" {
			return name
		}
	}
	name := strings.Trim(strings.Join([]string{server, tool}, " · "), " ·")
	if name != "" {
		return name
	}
	return "MCP 工具"
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
	ch := make(chan ChatEvent, 64)
	b.mu.Lock()
	if b.subs[sessionID] == nil {
		b.subs[sessionID] = map[chan ChatEvent]struct{}{}
	}
	b.subs[sessionID][ch] = struct{}{}
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		delete(b.subs[sessionID], ch)
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
		return nil
	}
	_, err = rpc.call(ctx, "turn/interrupt", map[string]string{"threadId": sessionID, "turnId": turnID})
	return err
}

func (b *codexChatBackend) Approve(ctx context.Context, assistant, sessionID, requestID, decision string) error {
	if assistant != "codex" {
		return errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return err
	}
	b.mu.Lock()
	p, ok := b.pending[requestID]
	if ok {
		delete(b.pending, requestID)
	}
	b.mu.Unlock()
	if !ok {
		p = pendingApproval{id: json.RawMessage(requestID), method: "item/commandExecution/requestApproval"}
	}
	if p.method != "item/commandExecution/requestApproval" {
		return fmt.Errorf("approval kind not supported yet: %s", p.method)
	}
	result := "decline"
	switch decision {
	case "approved", "approve", "accept":
		result = "accept"
	case "abort", "cancel":
		result = "cancel"
	case "denied", "deny", "decline", "":
		result = "decline"
	}
	if err := rpc.respond(p.id, result); err != nil {
		return err
	}
	b.publish(newChatEvent("approval_resolved", "codex", sessionID, "", "", map[string]string{"requestId": requestID, "decision": result}))
	return nil
}

func (b *codexChatBackend) dispatch(notes <-chan rpcNotification) {
	for n := range notes {
		if len(n.ID) > 0 {
			if key := rpcIDKey(n.ID); key != "" {
				b.mu.Lock()
				b.pending[key] = pendingApproval{id: append(json.RawMessage(nil), n.ID...), method: n.Method}
				b.mu.Unlock()
			}
		}
		for _, ev := range mapCodexNotification(n) {
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
			b.publish(ev)
		}
	}
}

func (b *codexChatBackend) publish(ev ChatEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[ev.SessionID] {
		select {
		case ch <- ev:
		default:
		}
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
