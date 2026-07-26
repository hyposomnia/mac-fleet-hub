package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ChatEvent is the stable dashboard-facing projection for self-rendered agent
// chat. It intentionally hides Codex app-server / future Claude-specific wire
// shapes from the browser.
type ChatEvent struct {
	Type      string          `json:"type"`
	Assistant string          `json:"assistant"`
	SessionID string          `json:"sessionId"`
	TurnID    string          `json:"turnId,omitempty"`
	ItemID    string          `json:"itemId,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func newChatEvent(typ, assistant, sessionID, turnID, itemID string, data interface{}) ChatEvent {
	var raw json.RawMessage
	if data != nil {
		if b, err := json.Marshal(data); err == nil {
			raw = b
		}
	}
	return ChatEvent{
		Type:      typ,
		Assistant: assistant,
		SessionID: sessionID,
		TurnID:    turnID,
		ItemID:    itemID,
		Data:      raw,
	}
}

func mapCodexServerRequest(n rpcNotification) ChatEvent {
	p := codexEventEnvelope(n.Params)
	data := p.asMap()
	data["requestId"], _ = json.Marshal(rpcIDKey(n.ID))
	data["requestMethod"], _ = json.Marshal(n.Method)
	return newChatEvent("interaction_request", "codex", p.ThreadID, p.TurnID, p.ItemID, data)
}

func mapCodexNotification(n rpcNotification) []ChatEvent {
	switch n.Method {
	case "thread/status/changed":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("thread_status", "codex", p.ThreadID, "", "", p.asMap())}
	case "thread/tokenUsage/updated":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("turn_usage", "codex", p.ThreadID, p.TurnID, "", p.asMap())}
	case "turn/started":
		var p struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(n.Params, &p)
		var data map[string]interface{}
		_ = json.Unmarshal(n.Params, &data)
		return []ChatEvent{newChatEvent("turn_started", "codex", p.ThreadID, p.Turn.ID, "", data)}
	case "item/agentMessage/delta":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("assistant_delta", "codex", p.ThreadID, p.TurnID, p.ItemID, p.asMap())}
	case "item/reasoning/summaryTextDelta":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("reasoning_delta", "codex", p.ThreadID, p.TurnID, p.ItemID, p.asMap())}
	case "item/plan/delta":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("plan_delta", "codex", p.ThreadID, p.TurnID, p.ItemID, p.asMap())}
	case "turn/plan/updated":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("todo_update", "codex", p.ThreadID, p.TurnID, "todo-"+p.TurnID, p.asMap())}
	case "item/started":
		threadID, turnID, item := codexNotificationItem(n.Params)
		if ev, ok := projectCodexSemanticItem(threadID, turnID, item, "inProgress"); ok {
			return []ChatEvent{ev}
		}
		if ev, ok := projectCodexToolItem(threadID, turnID, item, "inProgress"); ok {
			return []ChatEvent{ev}
		}
		return nil
	case "item/completed":
		p := codexCompletedEnvelope(n.Params)
		switch p.ItemType {
		case "agentMessage":
			return []ChatEvent{newChatEvent("assistant_done", "codex", p.ThreadID, p.TurnID, p.ItemID, p.asMap())}
		default:
			_, _, item := codexNotificationItem(n.Params)
			if ev, ok := projectCodexSemanticItem(p.ThreadID, p.TurnID, item, "completed"); ok {
				return []ChatEvent{ev}
			}
			if ev, ok := projectCodexToolItem(p.ThreadID, p.TurnID, item, "completed"); ok {
				return []ChatEvent{ev}
			}
			return nil
		}
	case "item/commandExecution/outputDelta", "item/commandExec/outputDelta":
		p := codexEventEnvelope(n.Params)
		data := p.asMap()
		data["kind"] = json.RawMessage(`"commandExecution"`)
		return []ChatEvent{newChatEvent("tool_delta", "codex", p.ThreadID, p.TurnID, p.ItemID, data)}
	case "item/mcpToolCall/progress":
		p := codexEventEnvelope(n.Params)
		data := p.asMap()
		data["kind"] = json.RawMessage(`"mcpToolCall"`)
		return []ChatEvent{newChatEvent("tool_delta", "codex", p.ThreadID, p.TurnID, p.ItemID, data)}
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval":
		return []ChatEvent{mapCodexServerRequest(n)}
	case "item/tool/requestUserInput", "tool/requestUserInput", "mcpServer/elicitation/request":
		return []ChatEvent{mapCodexServerRequest(n)}
	case "item/fileChange/patchUpdated", "turn/diff/updated":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("diff_update", "codex", p.ThreadID, p.TurnID, p.ItemID, p.asMap())}
	case "item/autoApprovalReview/started":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("review_status", "codex", p.ThreadID, p.TurnID, p.ItemID, map[string]interface{}{
			"status": "inProgress", "review": p.asMap(),
		})}
	case "item/autoApprovalReview/completed":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("review_status", "codex", p.ThreadID, p.TurnID, p.ItemID, map[string]interface{}{
			"status": "completed", "review": p.asMap(),
		})}
	case "turn/completed":
		var p struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(n.Params, &p)
		var data map[string]json.RawMessage
		_ = json.Unmarshal(n.Params, &data)
		events := make([]ChatEvent, 0, 2)
		if strings.EqualFold(p.Turn.Status, "failed") && p.Turn.Error != nil && p.Turn.Error.Message != "" {
			errorData := make(map[string]json.RawMessage, len(data)+1)
			for key, value := range data {
				errorData[key] = value
			}
			message, _ := json.Marshal(p.Turn.Error.Message)
			errorData["message"] = message
			events = append(events, newChatEvent("error", "codex", p.ThreadID, p.Turn.ID, "", errorData))
		}
		return append(events, newChatEvent("turn_done", "codex", p.ThreadID, p.Turn.ID, "", data))
	case "error":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("error", "codex", p.ThreadID, p.TurnID, p.ItemID, p.asMap())}
	default:
		return nil
	}
}

type codexEventMeta struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	raw      json.RawMessage
}

func codexEventEnvelope(raw json.RawMessage) codexEventMeta {
	var p codexEventMeta
	_ = json.Unmarshal(raw, &p)
	p.raw = append(json.RawMessage(nil), raw...)
	return p
}

func (p codexEventMeta) asMap() map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	if len(p.raw) == 0 {
		return m
	}
	_ = json.Unmarshal(p.raw, &m)
	return m
}

type codexCompletedMeta struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string
	ItemType string
	raw      json.RawMessage
}

func codexCompletedEnvelope(raw json.RawMessage) codexCompletedMeta {
	var p struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Item     struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"item"`
	}
	_ = json.Unmarshal(raw, &p)
	return codexCompletedMeta{
		ThreadID: p.ThreadID,
		TurnID:   p.TurnID,
		ItemID:   p.Item.ID,
		ItemType: p.Item.Type,
		raw:      append(json.RawMessage(nil), raw...),
	}
}

func codexNotificationItem(raw json.RawMessage) (string, string, json.RawMessage) {
	var p struct {
		ThreadID string          `json:"threadId"`
		TurnID   string          `json:"turnId"`
		Item     json.RawMessage `json:"item"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.ThreadID, p.TurnID, p.Item
}

func (p codexCompletedMeta) asMap() map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	if len(p.raw) == 0 {
		return m
	}
	_ = json.Unmarshal(p.raw, &m)
	return m
}

var errAppServerUnavailable = errors.New("appserver_unavailable")
var errAppServerTimeout = errors.New("appserver_timeout")
var errAppServerRecovered = errors.New("appserver_recovered")
var errAgentRestarting = errors.New("agent_restarting")
var errUnsupportedChatAssistant = errors.New("unsupported_assistant")
var errNoActiveChatTurn = errors.New("no_active_turn")
var errChatRequestNotFound = errors.New("chat_request_not_found")
var errInvalidChatSkill = errors.New("invalid_chat_skill")

type ChatStartResult struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
}

type ChatResumeResult struct {
	SessionID    string            `json:"sessionId"`
	ThreadID     string            `json:"threadId"`
	Status       string            `json:"status"`
	ActiveTurnID string            `json:"activeTurnId,omitempty"`
	History      ChatHistoryPage   `json:"history"`
	Model        string            `json:"model,omitempty"`
	Effort       string            `json:"effort,omitempty"`
	ServiceTier  string            `json:"serviceTier,omitempty"`
	ApprovalMode string            `json:"approvalMode,omitempty"`
	Models       []ChatModelOption `json:"models,omitempty"`
}

type ChatHistoryPage struct {
	Events     []ChatEvent `json:"events"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

type ChatModelOption struct {
	Value              string                      `json:"value"`
	DisplayName        string                      `json:"displayName"`
	Description        string                      `json:"description,omitempty"`
	DefaultEffort      string                      `json:"defaultEffort,omitempty"`
	SupportedEfforts   []ChatReasoningEffortOption `json:"supportedEfforts,omitempty"`
	DefaultServiceTier string                      `json:"defaultServiceTier,omitempty"`
	ServiceTiers       []ChatServiceTierOption     `json:"serviceTiers,omitempty"`
	IsDefault          bool                        `json:"isDefault,omitempty"`
}

type ChatReasoningEffortOption struct {
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

type ChatServiceTierOption struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ChatTurnOptions struct {
	Model               string  `json:"model,omitempty"`
	Effort              string  `json:"effort,omitempty"`
	ServiceTier         *string `json:"serviceTier,omitempty"`
	ApprovalMode        string  `json:"approvalMode,omitempty"`
	ClientUserMessageID string  `json:"-"`
}

type ChatAttachment struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	MIME string `json:"mime,omitempty"`
	Size int64  `json:"size,omitempty"`
	URL  string `json:"url,omitempty"`
	Path string `json:"-"`
}

type ChatSkill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"-"`
	Scope       string `json:"scope,omitempty"`
}

func chatSkillID(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return hex.EncodeToString(sum[:16])
}

type ChatInputResult struct {
	TurnID string `json:"turnId,omitempty"`
}

type chatBackend interface {
	Start(ctx context.Context, assistant, cwd, mode string) (ChatStartResult, error)
	Resume(ctx context.Context, assistant, sessionID, mode string) (ChatResumeResult, error)
	History(ctx context.Context, assistant, sessionID, cursor string) (ChatHistoryPage, error)
	Skills(ctx context.Context, assistant, cwd string) ([]ChatSkill, error)
	Input(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment, skills []ChatSkill, opts ChatTurnOptions) (ChatInputResult, error)
	Steer(ctx context.Context, assistant, sessionID, clientMessageID, text string, images []ChatAttachment, skills []ChatSkill) (ChatInputResult, error)
	Events(ctx context.Context, assistant, sessionID string) (<-chan ChatEvent, error)
	Respond(ctx context.Context, assistant, sessionID, requestID string, response json.RawMessage) error
	Interrupt(ctx context.Context, assistant, sessionID string) error
	Settings(ctx context.Context, assistant, sessionID, approvalMode string) error
}

var agentChatBackend chatBackend = unavailableChatBackend{}

type unavailableChatBackend struct{}

func (unavailableChatBackend) Start(context.Context, string, string, string) (ChatStartResult, error) {
	return ChatStartResult{}, errAppServerUnavailable
}
func (unavailableChatBackend) Resume(context.Context, string, string, string) (ChatResumeResult, error) {
	return ChatResumeResult{}, errAppServerUnavailable
}
func (unavailableChatBackend) History(context.Context, string, string, string) (ChatHistoryPage, error) {
	return ChatHistoryPage{}, errAppServerUnavailable
}
func (unavailableChatBackend) Skills(context.Context, string, string) ([]ChatSkill, error) {
	return nil, errAppServerUnavailable
}
func (unavailableChatBackend) Input(context.Context, string, string, string, []ChatAttachment, []ChatSkill, ChatTurnOptions) (ChatInputResult, error) {
	return ChatInputResult{}, errAppServerUnavailable
}
func (unavailableChatBackend) Steer(context.Context, string, string, string, string, []ChatAttachment, []ChatSkill) (ChatInputResult, error) {
	return ChatInputResult{}, errAppServerUnavailable
}
func (unavailableChatBackend) Events(context.Context, string, string) (<-chan ChatEvent, error) {
	return nil, errAppServerUnavailable
}
func (unavailableChatBackend) Respond(context.Context, string, string, string, json.RawMessage) error {
	return errAppServerUnavailable
}
func (unavailableChatBackend) Interrupt(context.Context, string, string) error {
	return errAppServerUnavailable
}
func (unavailableChatBackend) Settings(context.Context, string, string, string) error {
	return errAppServerUnavailable
}

func writeChatErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errUnsupportedChatAssistant) {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	if errors.Is(err, errAppServerUnavailable) {
		writeErr(w, http.StatusServiceUnavailable, "appserver_unavailable", "Codex app-server 不可用，可用终端打开。")
		return
	}
	if errors.Is(err, errAppServerTimeout) {
		writeErr(w, http.StatusGatewayTimeout, "appserver_timeout", "Codex app-server 响应超时，连接已重置，请刷新重试。")
		return
	}
	if errors.Is(err, errAppServerRecovered) {
		writeErr(w, http.StatusServiceUnavailable, "appserver_recovered", "Codex 连接异常但已恢复。请确认刚才的操作结果后重试。")
		return
	}
	if errors.Is(err, errAgentRestarting) {
		writeErr(w, http.StatusServiceUnavailable, "agent_restarting", "Codex 连接恢复失败，fleet-agent 正在自动重启，请稍后重试。")
		return
	}
	if errors.Is(err, errNoActiveChatTurn) {
		writeErr(w, http.StatusConflict, "no_active_turn", "当前没有可引导的运行中任务。")
		return
	}
	if errors.Is(err, errChatRequestNotFound) {
		writeErr(w, http.StatusConflict, "chat_request_not_found", "这个交互请求已经失效或已被处理。")
		return
	}
	writeErr(w, http.StatusInternalServerError, "chat_failed", err.Error())
}

func handleChatStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Assistant string `json:"assistant"`
		Cwd       string `json:"cwd"`
		Mode      string `json:"mode"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || req.Cwd == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	assistant := normAssistant(req.Assistant)
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	res, err := agentChatBackend.Start(r.Context(), assistant, req.Cwd, normMode(req.Mode, false))
	if err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, res)
}

func handleChatResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Assistant string `json:"assistant"`
		SessionID string `json:"sessionId"`
		Mode      string `json:"mode"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || req.SessionID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	assistant := normAssistant(req.Assistant)
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	res, err := agentChatBackend.Resume(r.Context(), assistant, req.SessionID, normMode(req.Mode, false))
	if err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, res)
}

func handleChatSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Assistant    string `json:"assistant"`
		SessionID    string `json:"sessionId"`
		ApprovalMode string `json:"approvalMode"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || req.SessionID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	assistant := normAssistant(req.Assistant)
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	opts, err := normalizeChatTurnOptions(ChatTurnOptions{ApprovalMode: req.ApprovalMode})
	if err != nil || opts.ApprovalMode == "" {
		writeErr(w, http.StatusBadRequest, "bad_chat_options", "无效的审批模式")
		return
	}
	if err := agentChatBackend.Settings(r.Context(), assistant, req.SessionID, opts.ApprovalMode); err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"approvalMode": opts.ApprovalMode})
}

func handleChatInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Assistant    string  `json:"assistant"`
		SessionID    string  `json:"sessionId"`
		Text         string  `json:"text"`
		Model        string  `json:"model"`
		Effort       string  `json:"effort"`
		ServiceTier  *string `json:"serviceTier"`
		ApprovalMode string  `json:"approvalMode"`
		ClientID     string  `json:"clientMessageId"`
		Cwd          string  `json:"cwd"`
		Skills       []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"skills"`
		Images []struct {
			ID string `json:"id"`
		} `json:"images"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || req.SessionID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" && len(req.Images) == 0 && len(req.Skills) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sendText := req.Text
	if text == "" {
		sendText = ""
	}
	assistant := normAssistant(req.Assistant)
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	images := make([]ChatAttachment, 0, len(req.Images))
	for _, img := range req.Images {
		att, err := resolveChatUpload(req.SessionID, img.ID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_attachment", err.Error())
			return
		}
		images = append(images, att)
	}
	skills, err := resolveRequestedChatSkills(r.Context(), assistant, req.Cwd, req.Skills)
	if err != nil {
		if errors.Is(err, errInvalidChatSkill) {
			writeErr(w, http.StatusBadRequest, "bad_skill", err.Error())
		} else {
			writeChatErr(w, err)
		}
		return
	}
	opts, err := normalizeChatTurnOptions(ChatTurnOptions{
		Model: req.Model, Effort: req.Effort, ServiceTier: req.ServiceTier, ApprovalMode: req.ApprovalMode,
		ClientUserMessageID: req.ClientID,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_chat_options", err.Error())
		return
	}
	res, err := agentChatBackend.Input(r.Context(), assistant, req.SessionID, sendText, images, skills, opts)
	if err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, res)
}

func handleChatSteer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Assistant       string `json:"assistant"`
		SessionID       string `json:"sessionId"`
		ClientMessageID string `json:"clientMessageId"`
		Text            string `json:"text"`
		Cwd             string `json:"cwd"`
		Skills          []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"skills"`
		Images []struct {
			ID string `json:"id"`
		} `json:"images"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || req.SessionID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" && len(req.Images) == 0 && len(req.Skills) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req.ClientMessageID = strings.TrimSpace(req.ClientMessageID)
	if len(req.ClientMessageID) > 200 || strings.ContainsAny(req.ClientMessageID, "\r\n\x00") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	assistant := normAssistant(req.Assistant)
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	images := make([]ChatAttachment, 0, len(req.Images))
	for _, img := range req.Images {
		att, err := resolveChatUpload(req.SessionID, img.ID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_attachment", err.Error())
			return
		}
		images = append(images, att)
	}
	skills, err := resolveRequestedChatSkills(r.Context(), assistant, req.Cwd, req.Skills)
	if err != nil {
		if errors.Is(err, errInvalidChatSkill) {
			writeErr(w, http.StatusBadRequest, "bad_skill", err.Error())
		} else {
			writeChatErr(w, err)
		}
		return
	}
	sendText := req.Text
	if text == "" {
		sendText = ""
	}
	res, err := agentChatBackend.Steer(r.Context(), assistant, req.SessionID, req.ClientMessageID, sendText, images, skills)
	if err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, res)
}

func handleChatSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Assistant string `json:"assistant"`
		Cwd       string `json:"cwd"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	assistant := normAssistant(req.Assistant)
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	skills, err := agentChatBackend.Skills(r.Context(), assistant, strings.TrimSpace(req.Cwd))
	if err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, map[string]interface{}{"skills": skills})
}

func resolveRequestedChatSkills(ctx context.Context, assistant, cwd string, requested []struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}) ([]ChatSkill, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	available, err := agentChatBackend.Skills(ctx, assistant, strings.TrimSpace(cwd))
	if err != nil {
		return nil, err
	}
	byID := make(map[string]ChatSkill, len(available))
	firstByName := make(map[string]ChatSkill, len(available))
	for _, skill := range available {
		if skill.ID != "" {
			byID[skill.ID] = skill
		}
		if _, exists := firstByName[skill.Name]; !exists {
			firstByName[skill.Name] = skill
		}
	}
	resolved := make([]ChatSkill, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	for _, item := range requested {
		id := strings.TrimSpace(item.ID)
		name := strings.TrimSpace(item.Name)
		if len(id) > 128 || strings.ContainsAny(id, "\r\n\x00") ||
			name == "" || len(name) > 200 || strings.ContainsAny(name, "\r\n\x00") {
			return nil, fmt.Errorf("%w: 无效的 skill", errInvalidChatSkill)
		}
		var (
			skill ChatSkill
			ok    bool
		)
		if id != "" {
			skill, ok = byID[id]
			if ok && skill.Name != name {
				ok = false
			}
		} else {
			skill, ok = firstByName[name]
		}
		if !ok {
			return nil, fmt.Errorf("%w: skill %q 不存在或未启用", errInvalidChatSkill, name)
		}
		key := skill.ID
		if key == "" {
			key = skill.Path
		}
		if !seen[key] {
			resolved = append(resolved, skill)
			seen[key] = true
		}
	}
	return resolved, nil
}

func normalizeChatTurnOptions(opts ChatTurnOptions) (ChatTurnOptions, error) {
	opts.Model = strings.TrimSpace(opts.Model)
	opts.Effort = strings.TrimSpace(opts.Effort)
	opts.ApprovalMode = strings.TrimSpace(opts.ApprovalMode)
	opts.ClientUserMessageID = strings.TrimSpace(opts.ClientUserMessageID)
	if len(opts.Model) > 160 || strings.ContainsAny(opts.Model, "\r\n\x00") {
		return ChatTurnOptions{}, fmt.Errorf("无效的模型")
	}
	if len(opts.Effort) > 40 || strings.ContainsAny(opts.Effort, "\r\n\x00") {
		return ChatTurnOptions{}, fmt.Errorf("无效的推理强度")
	}
	if len(opts.ClientUserMessageID) > 200 || strings.ContainsAny(opts.ClientUserMessageID, "\r\n\x00") {
		return ChatTurnOptions{}, fmt.Errorf("无效的客户端消息 ID")
	}
	if opts.ServiceTier != nil {
		tier := strings.TrimSpace(*opts.ServiceTier)
		if len(tier) > 80 || strings.ContainsAny(tier, "\r\n\x00") {
			return ChatTurnOptions{}, fmt.Errorf("无效的速度选项")
		}
		opts.ServiceTier = &tier
	}
	switch opts.ApprovalMode {
	case "", "untrusted", "on-request", "full-access":
	case "never":
		opts.ApprovalMode = "on-request"
	default:
		return ChatTurnOptions{}, fmt.Errorf("无效的审批模式")
	}
	return opts, nil
}

func handleChatHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	assistant := normAssistant(r.URL.Query().Get("assistant"))
	sessionID := r.URL.Query().Get("sessionId")
	cursor := r.URL.Query().Get("cursor")
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	if sessionID == "" || len(cursor) > 4096 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	page, err := agentChatBackend.History(r.Context(), assistant, sessionID, cursor)
	if err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, page)
}

const maxChatUploadBytes int64 = 20 << 20

var chatUploadRoot = defaultChatUploadRoot

func defaultChatUploadRoot() string {
	if d, err := os.UserCacheDir(); err == nil && d != "" {
		return filepath.Join(d, "mac-fleet-hub", "chat-uploads")
	}
	return filepath.Join(os.TempDir(), "mac-fleet-hub", "chat-uploads")
}

func chatUploadSessionDir(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(chatUploadRoot(), hex.EncodeToString(sum[:]))
}

func chatAttachmentURL(sessionID, id string) string {
	return "/api/chat/attachment?sessionId=" + urlQueryEscape(sessionID) + "&id=" + urlQueryEscape(id)
}

func urlQueryEscape(s string) string {
	r := strings.NewReplacer("%", "%25", " ", "%20", "&", "%26", "?", "%3F", "=", "%3D", "#", "%23", "+", "%2B")
	return r.Replace(s)
}

func handleChatUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxChatUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(maxChatUploadBytes); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_upload", "图片过大或上传格式不正确。")
		return
	}
	assistant := normAssistant(r.FormValue("assistant"))
	sessionID := r.FormValue("sessionId")
	if assistant != "codex" || sessionID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_upload", "没有收到图片文件。")
		return
	}
	defer file.Close()

	sniff := make([]byte, 512)
	n, _ := file.Read(sniff)
	detected := http.DetectContentType(sniff[:n])
	declared, _, _ := mime.ParseMediaType(header.Header.Get("Content-Type"))
	mimeType := declared
	if !strings.HasPrefix(mimeType, "image/") && strings.HasPrefix(detected, "image/") {
		mimeType = detected
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedChatImage(mimeType, ext) {
		writeErr(w, http.StatusBadRequest, "bad_upload", "只支持上传图片。")
		return
	}
	if ext == "" {
		ext = extFromImageMIME(mimeType)
	}
	id, err := randomChatUploadID(ext)
	if err != nil {
		writeChatErr(w, err)
		return
	}
	dir := chatUploadSessionDir(sessionID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		writeChatErr(w, err)
		return
	}
	dst := filepath.Join(dir, id)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		writeChatErr(w, err)
		return
	}
	reader := io.MultiReader(bytes.NewReader(sniff[:n]), file)
	size, copyErr := io.Copy(out, io.LimitReader(reader, maxChatUploadBytes+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(dst)
		if copyErr != nil {
			writeChatErr(w, copyErr)
		} else {
			writeChatErr(w, closeErr)
		}
		return
	}
	if size > maxChatUploadBytes {
		_ = os.Remove(dst)
		writeErr(w, http.StatusRequestEntityTooLarge, "upload_too_large", "图片不能超过 20MB。")
		return
	}
	writeJSON(w, ChatAttachment{
		ID: id, Name: filepath.Base(header.Filename), MIME: mimeType, Size: size,
		URL: chatAttachmentURL(sessionID, id),
	})
}

func handleChatAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	att, err := resolveChatUpload(r.URL.Query().Get("sessionId"), r.URL.Query().Get("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, att.Path)
}

func handleChatMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" || len(path) > 16*1024 || !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxChatUploadBytes {
		http.NotFound(w, r)
		return
	}
	sniff := make([]byte, 512)
	n, _ := file.Read(sniff)
	contentType := http.DetectContentType(sniff[:n])
	if !strings.HasPrefix(contentType, "image/") {
		http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

func resolveChatUpload(sessionID, id string) (ChatAttachment, error) {
	if sessionID == "" || id == "" || filepath.Base(id) != id || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		return ChatAttachment{}, fmt.Errorf("无效的图片附件")
	}
	path := filepath.Join(chatUploadSessionDir(sessionID), id)
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return ChatAttachment{}, fmt.Errorf("图片附件不存在或已失效")
	}
	return ChatAttachment{ID: id, Path: path, Size: st.Size(), URL: chatAttachmentURL(sessionID, id)}, nil
}

func allowedChatImage(mimeType, ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	switch strings.ToLower(mimeType) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func extFromImageMIME(mimeType string) string {
	switch strings.ToLower(mimeType) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".img"
	}
}

func randomChatUploadID(ext string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]) + ext, nil
}

func handleChatEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	assistant := normAssistant(r.URL.Query().Get("assistant"))
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	events, err := agentChatBackend.Events(r.Context(), assistant, sessionID)
	if err != nil {
		writeChatErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(b)
			_, _ = w.Write([]byte("\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		case <-heartbeat.C:
			_, _ = w.Write([]byte(": ping\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

func handleChatInterrupt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Assistant string `json:"assistant"`
		SessionID string `json:"sessionId"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || req.SessionID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	assistant := normAssistant(req.Assistant)
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	if err := agentChatBackend.Interrupt(r.Context(), assistant, req.SessionID); err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func handleChatRespond(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Assistant string          `json:"assistant"`
		SessionID string          `json:"sessionId"`
		RequestID string          `json:"requestId"`
		Response  json.RawMessage `json:"response"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil ||
		req.SessionID == "" || req.RequestID == "" || len(req.Response) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	assistant := normAssistant(req.Assistant)
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	if err := agentChatBackend.Respond(r.Context(), assistant, req.SessionID, req.RequestID, req.Response); err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// Deprecated compatibility endpoint for dashboard builds older than /chat/respond.
func handleChatApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Assistant string `json:"assistant"`
		SessionID string `json:"sessionId"`
		RequestID string `json:"requestId"`
		Decision  string `json:"decision"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil ||
		req.SessionID == "" || req.RequestID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	decision := "decline"
	switch req.Decision {
	case "approved", "approve", "accept":
		decision = "accept"
	case "always", "acceptForSession":
		decision = "acceptForSession"
	case "abort", "cancel":
		decision = "cancel"
	}
	response, _ := json.Marshal(map[string]string{"decision": decision})
	if err := agentChatBackend.Respond(r.Context(), normAssistant(req.Assistant), req.SessionID, req.RequestID, response); err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
