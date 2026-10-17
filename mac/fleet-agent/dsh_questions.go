package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Wire shapes from DSH Desktop's dsh-user-questions/typert.host.js and
// dsh-api-gateway/client.js. Waterfall cancellation only carries eventId.
type dshQuestion struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Detail   string `json:"detail,omitempty"`
	Header   string `json:"header,omitempty"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description,omitempty"`
	} `json:"options,omitempty"`
	MultiSelect bool `json:"multiSelect,omitempty"`
}

type dshInteraction struct {
	SessionID string
	Kind      string
	Payload   json.RawMessage
	Questions []dshQuestion
	CallID    string // continued timed question: answer through userQuestions/answer
	Submitted bool
}

func (r dshInteraction) event(assistant, id string) ChatEvent {
	method := "item/commandExecution/requestApproval"
	body := map[string]any{"requestId": id, "raw": r.Payload, "allowedDecisions": []string{"allow_once", "decline"}}
	if r.Kind == "user-questions/request" {
		method = "item/tool/requestUserInput"
		questions := make([]map[string]any, 0, len(r.Questions))
		for _, q := range r.Questions {
			data, _ := json.Marshal(q)
			var question map[string]any
			_ = json.Unmarshal(data, &question)
			question["isOther"] = len(q.Options) > 0
			question["dshQuestion"] = true
			questions = append(questions, question)
		}
		body["questions"] = questions
	}
	var raw map[string]any
	_ = json.Unmarshal(r.Payload, &raw)
	if message, ok := raw["message"].(string); ok {
		body["message"] = message
	}
	body["requestMethod"] = method
	return newChatEvent("interaction_request", assistant, r.SessionID, "", id, body)
}

func (b *dshChatBackend) rememberInteraction(kind, id, sessionID string, payload json.RawMessage) {
	if id == "" || sessionID == "" {
		return
	}
	r := dshInteraction{SessionID: sessionID, Kind: kind, Payload: payload}
	if kind == "user-questions/request" {
		var request struct {
			Questions []dshQuestion `json:"questions"`
		}
		if json.Unmarshal(payload, &request) != nil || len(request.Questions) == 0 {
			return
		}
		r.Questions = request.Questions
	}
	b.eventMu.Lock()
	b.interactions[id] = r
	b.eventMu.Unlock()
	b.publish(sessionID, r.event(b.assistant(), id))
}

func (b *dshChatBackend) resolveInteraction(id string, response any) {
	b.eventMu.Lock()
	r, ok := b.interactions[id]
	delete(b.interactions, id)
	b.eventMu.Unlock()
	if ok {
		b.publish(r.SessionID, newChatEvent("interaction_resolved", b.assistant(), r.SessionID, "", id, map[string]any{"requestId": id, "response": response}))
	}
}

func (b *dshChatBackend) handleWaterfall(raw json.RawMessage) {
	var frame struct {
		Type    string          `json:"type"`
		Event   string          `json:"event"`
		EventID string          `json:"eventId"`
		AgentID string          `json:"agentId"`
		Request json.RawMessage `json:"request"`
		Payload json.RawMessage `json:"payload"` // older harness versions
	}
	if json.Unmarshal(raw, &frame) != nil {
		return
	}
	if frame.Type == "cancel" {
		b.resolveInteraction(frame.EventID, map[string]any{"decision": "cancelled"})
		return
	}
	kind, payload, sessionID := frame.Type, frame.Payload, frameSessionID(frame.Payload)
	if frame.Type == "waterfall" {
		kind, payload, sessionID = frame.Event, frame.Request, frame.AgentID
	}
	if kind == "approval/request" || kind == "user-questions/request" {
		b.rememberInteraction(kind, frame.EventID, sessionID, payload)
	}
}

func (b *dshChatBackend) pendingInteractions(sessionID string) []ChatEvent {
	b.eventMu.Lock()
	defer b.eventMu.Unlock()
	out := []ChatEvent{}
	for id, r := range b.interactions {
		if r.SessionID == sessionID && !r.Submitted {
			out = append(out, r.event(b.assistant(), id))
		}
	}
	return out
}

func (b *dshChatBackend) sessionHasQuestions(sessionID string) bool {
	b.eventMu.Lock()
	defer b.eventMu.Unlock()
	if b.questionWaiting[sessionID] {
		return true
	}
	for _, r := range b.interactions {
		if r.SessionID == sessionID && !r.Submitted {
			return true
		}
	}
	return false
}

// The existing session/list refresh also exposes timed questions after their
// foreground wait ends. Restore them on reload, including never-opened chats.
func (b *dshChatBackend) syncQuestionListing(raw json.RawMessage) {
	var listing struct {
		Items []struct {
			SessionID   string `json:"sessionId"`
			Projections struct {
				Values struct {
					UserQuestions *struct {
						Active []struct {
							CallID    string        `json:"callId"`
							Questions []dshQuestion `json:"questions"`
							State     string        `json:"state"`
						} `json:"active"`
					} `json:"userQuestions"`
				} `json:"values"`
			} `json:"projections"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &listing) != nil {
		return
	}
	var events []ChatEvent
	b.eventMu.Lock()
	for _, item := range listing.Items {
		projection := item.Projections.Values.UserQuestions
		if projection == nil {
			continue
		}
		active := map[string]bool{}
		b.questionWaiting[item.SessionID] = false
		for _, q := range projection.Active {
			// Open requests are delivered through $events, whose eventId is required
			// by the reverse channel. Continued ones have a durable callId instead.
			if q.State == "open" {
				b.questionWaiting[item.SessionID] = true
			}
			if q.State != "continued" || q.CallID == "" || len(q.Questions) == 0 {
				continue
			}
			id := "dsh-question/" + q.CallID
			active[id] = true
			if previous, ok := b.interactions[id]; ok && previous.SessionID == item.SessionID {
				continue
			}
			r := dshInteraction{SessionID: item.SessionID, Kind: "user-questions/request", Questions: q.Questions, CallID: q.CallID}
			b.interactions[id] = r
			events = append(events, r.event(b.assistant(), id))
		}
		for id, r := range b.interactions {
			if r.SessionID == item.SessionID && r.CallID != "" && !active[id] {
				delete(b.interactions, id)
				events = append(events, newChatEvent("interaction_resolved", b.assistant(), item.SessionID, "", id, map[string]any{"requestId": id}))
			}
		}
	}
	b.eventMu.Unlock()
	for _, ev := range events {
		b.publish(ev.SessionID, ev)
	}
}

func dshQuestionAnswers(questions []dshQuestion, response json.RawMessage) ([]map[string]any, error) {
	var body struct {
		Answers map[string]struct {
			Answers []string `json:"answers"`
			Custom  *string  `json:"custom"`
		} `json:"answers"`
	}
	if json.Unmarshal(response, &body) != nil || len(body.Answers) != len(questions) {
		return nil, errors.New("请回答所有问题")
	}
	out := make([]map[string]any, 0, len(questions))
	seen := map[string]bool{}
	for _, q := range questions {
		answer, ok := body.Answers[q.ID]
		if !ok || seen[q.ID] || q.ID == "" || len(answer.Answers) == 0 {
			return nil, errors.New("请回答所有问题")
		}
		seen[q.ID] = true
		labels := map[string]bool{}
		for _, o := range q.Options {
			labels[o.Label] = true
		}
		selected := []string{}
		custom := []string{}
		explicitCustom := ""
		if answer.Custom != nil {
			explicitCustom = *answer.Custom
		}
		for _, value := range answer.Answers {
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("问题 %s 的答案为空", q.ID)
			}
			if answer.Custom != nil && value == explicitCustom {
				custom = append(custom, value)
				continue
			}
			if labels[value] {
				selected = append(selected, value)
			} else {
				custom = append(custom, value)
			}
		}
		if !q.MultiSelect && len(selected)+len(custom) > 1 {
			return nil, fmt.Errorf("问题 %s 只允许一个答案", q.ID)
		}
		if len(custom) > 1 {
			return nil, fmt.Errorf("问题 %s 只允许一项自定义回答", q.ID)
		}
		item := map[string]any{"id": q.ID, "selected": selected}
		if len(custom) > 0 {
			item["custom"] = custom[0]
		}
		out = append(out, item)
	}
	return out, nil
}

func (b *dshChatBackend) respondInteraction(ctx context.Context, client *dshClient, sessionID, id string, response json.RawMessage) error {
	b.eventMu.Lock()
	r, tracked := b.interactions[id]
	b.eventMu.Unlock()
	if tracked && (r.SessionID != sessionID || r.Submitted) {
		return errChatRequestNotFound
	}
	var outcome map[string]any
	var err error
	if tracked && r.Kind == "user-questions/request" {
		answers, e := dshQuestionAnswers(r.Questions, response)
		if e != nil {
			return e
		}
		if r.CallID != "" {
			raw, e := client.call(ctx, "userQuestions/answer", map[string]any{"agentId": sessionID, "callId": r.CallID, "answer": map[string]any{"answers": answers}})
			if e != nil {
				return b.translateCallError(e)
			}
			if string(raw) != "true" {
				return errChatRequestNotFound
			}
			b.eventMu.Lock()
			if current, ok := b.interactions[id]; ok {
				current.Submitted = true
				b.interactions[id] = current
			}
			b.eventMu.Unlock()
			b.publish(sessionID, newChatEvent("interaction_resolved", b.assistant(), sessionID, "", id, map[string]any{"requestId": id, "response": json.RawMessage(response)}))
			return nil
		}
		outcome = map[string]any{"kind": "result", "value": map[string]any{"answers": answers}}
	} else {
		outcome, err = dshApprovalOutcome(response)
		if err != nil {
			return err
		}
	}
	clientID, err := b.ensureWaterfall(ctx, client)
	if err != nil {
		return err
	}
	_, err = client.call(ctx, "$events/result", map[string]any{"clientId": clientID, "eventId": id, "outcome": outcome})
	if err != nil {
		return b.translateCallError(err)
	}
	b.resolveInteraction(id, json.RawMessage(response))
	return nil
}
