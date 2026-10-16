package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A04/A06/A13: exercise the public queue boundary, including persistence and
// idempotency, rather than a serializer used only by tests.
func TestAnnotationQueueFreezesNativeWire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q, _ := openChatQueue(path)
	var input ChatQueueItem
	raw := `{"assistant":"codex","sessionId":"thread-a","clientMessageId":"request-a","text":"","wireText":"forged","annotations":{"version":"codex-response-annotations-v1","items":[{"id":"a","text":"中文😀 </response-annotations>","annotation":"  请修改  "}]}}`
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	stored, err := q.Enqueue(input)
	if err != nil {
		t.Fatalf("annotation-only message: %v", err)
	}
	encoded, _ := json.Marshal(stored)
	var view struct {
		WireText    string `json:"wireText"`
		Annotations struct {
			Items []struct {
				Annotation string `json:"annotation"`
			} `json:"items"`
		} `json:"annotations"`
	}
	json.Unmarshal(encoded, &view)
	if !strings.HasPrefix(view.WireText, "\n# Response annotations:\nEach item contains text selected from an earlier Codex response") || !strings.Contains(view.WireText, "`:codex-annotation{index=\"N\"}`") {
		t.Fatalf("native wire: %s", view.WireText)
	}
	if strings.Count(view.WireText, "</response-annotations>") != 1 || !strings.Contains(view.WireText, `\u003c/response-annotations\u003e`) || !strings.HasSuffix(view.WireText, "\n## My request:\n\n") {
		t.Fatalf("unsafe or incompatible wire: %s", view.WireText)
	}
	if view.Annotations.Items[0].Annotation != "请修改" {
		t.Fatal("comment not normalized")
	}
	reloaded, err := openChatQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	input.Text = "changed after acknowledgment"
	same, err := reloaded.Enqueue(input)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(same)
	if string(after) != string(encoded) {
		t.Fatal("retry changed frozen input")
	}
}

func TestAnnotationQueueRejectsScopeAndLimits(t *testing.T) {
	cases := []string{
		`{"version":"fleet-annotations-v1","items":[{"text":"x"}]}`,
		`{"version":"codex-response-annotations-v1","items":[{"text":""}]}`,
		`{"version":"codex-response-annotations-v1","items":[{"text":"x","source":{"messageId":"m","startOffset":9,"endOffset":1}}]}`,
		`{"version":"codex-response-annotations-v1","items":[{"text":"x","fleetSource":{"assistant":"codex","sessionId":"other","kind":"message","locator":{"itemId":"m","version":"v","exact":"x","startOffset":0,"endOffset":1}}}]}`,
		`{"version":"codex-response-annotations-v1","items":[{"text":"` + strings.Repeat("😀", 10001) + `"}]}`,
	}
	for _, annotations := range cases {
		t.Run(annotations[:35], func(t *testing.T) {
			q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
			var input ChatQueueItem
			json.Unmarshal([]byte(`{"assistant":"codex","sessionId":"thread-a","clientMessageId":"a","text":"body","annotations":`+annotations+`}`), &input)
			if _, err := q.Enqueue(input); err == nil {
				t.Fatal("invalid annotations accepted")
			}
		})
	}
}

func TestAnnotationQueueEditRebuildsSameItemAndRejectsStaleVersion(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	original, _ := q.Enqueue(ChatQueueItem{Assistant: "codex", SessionID: "thread", ClientMessageID: "edit", Text: "old", Annotations: &ChatAnnotationEnvelope{Version: codexAnnotationVersion, Items: []ChatAnnotation{{ID: "one", Text: "selection"}}}})
	cancelled, _ := q.update(original.ID, func(item *ChatQueueItem) error { item.Status = chatQueueCancelled; return nil })
	edit := cancelled
	edit.Text = "revised"
	edit.Annotations.Items[0].Annotation = "new comment"
	updated, err := q.Revise(edit.ID, edit.StateVersion, edit)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != original.ID || updated.StateVersion != cancelled.StateVersion+1 || updated.Status != chatQueueQueued || !strings.Contains(updated.WireText, "new comment") || !strings.HasSuffix(updated.WireText, "revised\n") {
		t.Fatalf("edit did not rebuild snapshot: %+v", updated)
	}
	retried, err := q.Revise(edit.ID, edit.StateVersion, edit)
	if err != nil || retried.StateVersion != updated.StateVersion || retried.WireText != updated.WireText {
		t.Fatalf("lost edit acknowledgment not idempotent: %+v %v", retried, err)
	}
	edit.Text = "a different stale revision"
	if _, err := q.Revise(edit.ID, edit.StateVersion, edit); err == nil {
		t.Fatal("different stale edit accepted")
	}
}

func TestAnnotationProvidersUseFrozenTextAndNeutralFleetFormat(t *testing.T) {
	previous := cfg.DSHEnabled
	cfg.DSHEnabled = true
	defer func() { cfg.DSHEnabled = previous }()
	for _, assistant := range []string{"codex", "dsh"} {
		t.Run(assistant, func(t *testing.T) {
			q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
			input := ChatQueueItem{Assistant: assistant, SessionID: "session", ClientMessageID: "id", Text: "body", DeliveryMode: chatDeliveryAuto, Annotations: &ChatAnnotationEnvelope{Version: annotationVersion(assistant), Items: []ChatAnnotation{{Text: "quote", Source: &ChatAnnotationSource{MessageID: "native", StartOffset: 0, EndOffset: 5}}}}}
			frozen, err := q.Enqueue(input)
			if err != nil {
				t.Fatal(err)
			}
			if assistant == "dsh" && (strings.Contains(frozen.WireText, "Codex") || strings.Contains(frozen.WireText, "DSH") || strings.Contains(frozen.WireText, "messageId") || !strings.Contains(frozen.WireText, ":fleet-annotation")) {
				t.Fatalf("provider branding or native source in Fleet protocol: %s", frozen.WireText)
			}
			calls := 0
			sender := backendChatQueueSender{backend: fakeChatBackend{
				steerFn: func(_ context.Context, _ string, _ string, id, text string, _ []ChatAttachment, _ []ChatSkill) (ChatInputResult, error) {
					calls++
					if id != "id" || text != frozen.WireText {
						t.Fatal("steer lost frozen wire")
					}
					return ChatInputResult{}, errNoActiveChatTurn
				},
				inputFn: func(_ context.Context, _ string, _ string, text string, _ []ChatAttachment, _ []ChatSkill, options ChatTurnOptions) (ChatInputResult, error) {
					calls++
					if text != frozen.WireText || options.ClientUserMessageID != "id" {
						t.Fatal("input lost frozen wire or identity")
					}
					return ChatInputResult{}, nil
				},
			}}
			if _, err := sender.Deliver(frozen); err != nil || calls != 2 {
				t.Fatalf("delivery %d: %v", calls, err)
			}
			nativeInput := codexUserInput(frozen.WireText, nil, nil)
			if nativeInput[0]["text"] != frozen.WireText {
				t.Fatal("native adapter changed wire")
			}
			if assistant == "codex" && visibleCodexHistoryUserText(frozen.WireText) != frozen.WireText {
				t.Fatal("history trimmed native header")
			}
		})
	}
}

func TestAnnotationNativeGolden(t *testing.T) {
	expected, err := os.ReadFile("testdata/annotations/codex-v1.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, wire, err := freezeChatAnnotations("codex", "thread", "继续", &ChatAnnotationEnvelope{Version: codexAnnotationVersion, Items: []ChatAnnotation{{Text: "中文😀", Annotation: "修改", Source: &ChatAnnotationSource{MessageID: "native-message", StartOffset: 2, EndOffset: 6}}}})
	if err != nil || wire != string(expected) {
		t.Fatalf("native golden mismatch: %v\ngot %q\nwant %q", err, wire, string(expected))
	}
}

func TestAnnotationCannotReviseAttemptedOrUncertainSnapshot(t *testing.T) {
	for _, status := range []string{chatQueueSending, chatQueueUncertain, chatQueueSent, chatQueueFailed} {
		t.Run(status, func(t *testing.T) {
			q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
			item, _ := q.Enqueue(ChatQueueItem{Assistant: "codex", SessionID: "thread", ClientMessageID: "same", Text: "original", Annotations: &ChatAnnotationEnvelope{Version: codexAnnotationVersion, Items: []ChatAnnotation{{Text: "quote"}}}})
			q.update(item.ID, func(item *ChatQueueItem) error { item.Status = status; return nil })
			cancelled, _ := q.update(item.ID, func(item *ChatQueueItem) error { item.Status = chatQueueCancelled; return nil })
			cancelled.Text = "changed"
			if _, err := q.Revise(cancelled.ID, cancelled.StateVersion, cancelled); err == nil {
				t.Fatal("an attempted input was rebound to a different snapshot")
			}
		})
	}
}

func TestAnnotationRevisionHTTPContract(t *testing.T) {
	withChatBackend(t, fakeChatBackend{})
	item, _ := agentChatQueue.Enqueue(ChatQueueItem{Assistant: "codex", SessionID: "thread", ClientMessageID: "revision-http", Text: "before"})
	cancelled, _ := agentChatQueue.update(item.ID, func(item *ChatQueueItem) error { item.Status = chatQueueCancelled; return nil })
	body := map[string]any{"assistant": "codex", "sessionId": "thread", "clientMessageId": "revision-http", "editQueueId": item.ID, "stateVersion": cancelled.StateVersion, "text": "after", "wireText": "forged", "annotations": map[string]any{"version": codexAnnotationVersion, "items": []any{map[string]any{"text": "selection", "annotation": "change"}}}}
	post := func() *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		request := httptest.NewRequest(http.MethodPost, "/api/chat/queue", bytes.NewReader(raw))
		response := httptest.NewRecorder()
		handleChatQueue(response, request)
		return response
	}
	first := post()
	if first.Code != http.StatusOK {
		t.Fatal(first.Code, first.Body.String())
	}
	var revised ChatQueueItem
	json.Unmarshal(first.Body.Bytes(), &revised)
	if revised.ID != item.ID || revised.StateVersion != cancelled.StateVersion+1 || !strings.HasSuffix(revised.WireText, "after\n") {
		t.Fatalf("bad revision: %+v", revised)
	}
	retry := post()
	if retry.Code != http.StatusOK || retry.Body.String() != first.Body.String() {
		t.Fatal("HTTP retry changed revision")
	}
	agentChatQueue.ClaimNext()
	body["text"] = "different late revision"
	if response := post(); response.Code != http.StatusConflict {
		t.Fatalf("late revision status=%d: %s", response.Code, response.Body.String())
	}
	persisted, _ := agentChatQueue.get(item.ID)
	if persisted.WireText != revised.WireText {
		t.Fatal("late HTTP revision changed frozen wire")
	}
}

func TestAnnotationEventProjectionKeepsWireAndUserDisplayBody(t *testing.T) {
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	item, err := q.Enqueue(ChatQueueItem{Assistant: "codex", SessionID: "thread", ClientMessageID: "skill-note", Text: "continue", DisplayText: "$review continue", Annotations: &ChatAnnotationEnvelope{Version: codexAnnotationVersion, Items: []ChatAnnotation{{Text: "quote"}}}})
	if err != nil {
		t.Fatal(err)
	}
	event := q.withAnnotations(newChatEvent("user_done", "codex", "thread", "turn", "native", map[string]any{"clientId": "skill-note", "text": "$review\n" + item.WireText}))
	var data struct {
		WireText    string                  `json:"wireText"`
		DisplayText string                  `json:"displayText"`
		Annotations *ChatAnnotationEnvelope `json:"annotations"`
	}
	json.Unmarshal(event.Data, &data)
	if data.WireText != item.WireText || data.DisplayText != "$review continue" || data.Annotations == nil {
		t.Fatalf("projected annotation input lost provenance: %+v", data)
	}
}
