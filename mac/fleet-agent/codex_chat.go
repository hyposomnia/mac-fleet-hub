package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

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
}

func newCodexChatBackend(connect codexConnector) *codexChatBackend {
	return &codexChatBackend{
		connect:  connect,
		subs:     map[string]map[chan ChatEvent]struct{}{},
		lastTurn: map[string]string{},
		pending:  map[string]pendingApproval{},
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

func (b *codexChatBackend) Resume(ctx context.Context, assistant, sessionID, mode string) (ChatResumeResult, error) {
	if assistant != "codex" {
		return ChatResumeResult{}, errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return ChatResumeResult{}, err
	}
	raw, err := rpc.call(ctx, "thread/resume", map[string]interface{}{"threadId": sessionID})
	if err != nil {
		return ChatResumeResult{}, err
	}
	var res struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	_ = json.Unmarshal(raw, &res)
	threadID := res.Thread.ID
	if threadID == "" {
		threadID = sessionID
	}
	return ChatResumeResult{SessionID: sessionID, ThreadID: threadID, Status: "connected"}, nil
}

func (b *codexChatBackend) Input(ctx context.Context, assistant, sessionID, text string) (ChatInputResult, error) {
	if assistant != "codex" {
		return ChatInputResult{}, errUnsupportedChatAssistant
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return ChatInputResult{}, err
	}
	raw, err := rpc.call(ctx, "turn/start", map[string]interface{}{
		"threadId": sessionID,
		"input": []map[string]string{
			{"type": "text", "text": text},
		},
	})
	if err != nil {
		return ChatInputResult{}, err
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
