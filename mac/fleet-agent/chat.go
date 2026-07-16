package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

func mapCodexNotification(n rpcNotification) []ChatEvent {
	switch n.Method {
	case "thread/status/changed":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("thread_status", "codex", p.ThreadID, "", "", p.asMap())}
	case "item/agentMessage/delta":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("assistant_delta", "codex", p.ThreadID, p.TurnID, p.ItemID, p.asMap())}
	case "item/completed":
		p := codexCompletedEnvelope(n.Params)
		switch p.ItemType {
		case "agentMessage":
			return []ChatEvent{newChatEvent("assistant_done", "codex", p.ThreadID, p.TurnID, p.ItemID, p.asMap())}
		default:
			return nil
		}
	case "item/commandExecution/outputDelta", "item/commandExec/outputDelta":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("tool_delta", "codex", p.ThreadID, p.TurnID, p.ItemID, p.asMap())}
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval":
		p := codexEventEnvelope(n.Params)
		data := p.asMap()
		if len(n.ID) > 0 {
			data["requestId"] = append(json.RawMessage(nil), n.ID...)
		}
		return []ChatEvent{newChatEvent("approval_request", "codex", p.ThreadID, p.TurnID, p.ItemID, data)}
	case "item/fileChange/patchUpdated":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("diff_update", "codex", p.ThreadID, p.TurnID, p.ItemID, p.asMap())}
	case "turn/completed":
		p := codexEventEnvelope(n.Params)
		return []ChatEvent{newChatEvent("turn_done", "codex", p.ThreadID, p.TurnID, p.ItemID, p.asMap())}
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

func (p codexCompletedMeta) asMap() map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	if len(p.raw) == 0 {
		return m
	}
	_ = json.Unmarshal(p.raw, &m)
	return m
}

var errAppServerUnavailable = errors.New("appserver_unavailable")
var errUnsupportedChatAssistant = errors.New("unsupported_assistant")

type ChatResumeResult struct {
	SessionID string `json:"sessionId"`
	ThreadID  string `json:"threadId"`
	Status    string `json:"status"`
}

type ChatInputResult struct {
	TurnID string `json:"turnId,omitempty"`
}

type chatBackend interface {
	Resume(ctx context.Context, assistant, sessionID, mode string) (ChatResumeResult, error)
	Input(ctx context.Context, assistant, sessionID, text string) (ChatInputResult, error)
	Events(ctx context.Context, assistant, sessionID string) (<-chan ChatEvent, error)
	Approve(ctx context.Context, assistant, sessionID, requestID, decision string) error
	Interrupt(ctx context.Context, assistant, sessionID string) error
}

var agentChatBackend chatBackend = unavailableChatBackend{}

type unavailableChatBackend struct{}

func (unavailableChatBackend) Resume(context.Context, string, string, string) (ChatResumeResult, error) {
	return ChatResumeResult{}, errAppServerUnavailable
}
func (unavailableChatBackend) Input(context.Context, string, string, string) (ChatInputResult, error) {
	return ChatInputResult{}, errAppServerUnavailable
}
func (unavailableChatBackend) Events(context.Context, string, string) (<-chan ChatEvent, error) {
	return nil, errAppServerUnavailable
}
func (unavailableChatBackend) Approve(context.Context, string, string, string, string) error {
	return errAppServerUnavailable
}
func (unavailableChatBackend) Interrupt(context.Context, string, string) error {
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
	writeErr(w, http.StatusInternalServerError, "chat_failed", err.Error())
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

func handleChatInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Assistant string `json:"assistant"`
		SessionID string `json:"sessionId"`
		Text      string `json:"text"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || req.SessionID == "" || req.Text == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	assistant := normAssistant(req.Assistant)
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	res, err := agentChatBackend.Input(r.Context(), assistant, req.SessionID, req.Text)
	if err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, res)
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
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || req.SessionID == "" || req.RequestID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	assistant := normAssistant(req.Assistant)
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "自绘界面暂只支持 Codex。")
		return
	}
	if err := agentChatBackend.Approve(r.Context(), assistant, req.SessionID, req.RequestID, req.Decision); err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
