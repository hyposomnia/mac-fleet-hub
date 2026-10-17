package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const dshQuestionRequestFixture = `{"questions":[{"id":"choice","question":"Which?","options":[{"label":"A"},{"label":"B"}]},{"id":"multi","question":"Choose several","multiSelect":true,"options":[{"label":"X"},{"label":"Y"}]},{"id":"text","question":"Details?"}]}`
const dshQuestionResponseFixture = `{"answers":{"text":{"answers":["自由文本"]},"multi":{"answers":["Y","X"]},"choice":{"answers":["B"]}}}`

func TestDSHQuestionsTranslateDescriptorOrderedAnswers(t *testing.T) {
	var request struct {
		Questions []dshQuestion `json:"questions"`
	}
	_ = json.Unmarshal([]byte(dshQuestionRequestFixture), &request)
	got, err := dshQuestionAnswers(request.Questions, json.RawMessage(dshQuestionResponseFixture))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	want := `[{"id":"choice","selected":["B"]},{"id":"multi","selected":["Y","X"]},{"custom":"自由文本","id":"text","selected":[]}]`
	if string(raw) != want {
		t.Fatalf("answers=%s", raw)
	}
	for _, response := range []string{
		`{}`, `{"answers":{"choice":{"answers":["B"]}}}`,
		strings.Replace(dshQuestionResponseFixture, `["B"]`, `["A","B"]`, 1),
		strings.Replace(dshQuestionResponseFixture, `["B"]`, `[" "]`, 1),
		strings.Replace(dshQuestionResponseFixture, `"text":`, `"unknown":`, 1),
	} {
		if _, err := dshQuestionAnswers(request.Questions, json.RawMessage(response)); err == nil {
			t.Fatalf("accepted invalid response %s", response)
		}
	}
}

func TestDSHQuestionsWaterfallAndPayloadFreeCancellation(t *testing.T) {
	b := newDSHChatBackend(dshHarnessSpec{})
	frame := `{"type":"waterfall","event":"user-questions/request","eventId":"event-q","agentId":"session-1","request":` + dshQuestionRequestFixture + `}`
	b.handleWaterfall(json.RawMessage(frame))
	pending := b.pendingInteractions("session-1")
	if len(pending) != 1 || pending[0].SessionID != "session-1" {
		t.Fatalf("pending=%+v", pending)
	}
	var body struct {
		Questions []map[string]any `json:"questions"`
	}
	_ = json.Unmarshal(pending[0].Data, &body)
	if body.Questions[0]["isOther"] != true || body.Questions[1]["multiSelect"] != true {
		t.Fatalf("questions=%+v", body.Questions)
	}
	if len(b.pendingInteractions("session-2")) != 0 {
		t.Fatal("question leaked to another session")
	}
	b.handleWaterfall(json.RawMessage(`{"type":"cancel","eventId":"event-q"}`))
	if b.sessionHasQuestions("session-1") {
		t.Fatal("cancel did not clear waiting")
	}
	backlog := b.backlog["session-1"]
	if backlog[len(backlog)-1].Type != "interaction_resolved" {
		t.Fatalf("backlog=%+v", backlog)
	}
}

func TestDSHQuestionsRespondPostsNativeArray(t *testing.T) {
	host := newRecordingHost(t, nil)
	host.host.streamReply = func(endpoint, id string, send func(map[string]any)) {
		if endpoint == "$events" {
			send(map[string]any{"type": "item", "streamId": id, "value": map[string]any{"type": "ready", "clientId": "client-q"}})
		}
	}
	b := newTestDSHBackend(t, host.host)
	b.rememberInteraction("user-questions/request", "event-q", "session-1", json.RawMessage(dshQuestionRequestFixture))
	if err := b.Respond(context.Background(), "dsh", "session-other", "event-q", json.RawMessage(dshQuestionResponseFixture)); err != errChatRequestNotFound {
		t.Fatalf("cross-session err=%v", err)
	}
	if err := b.Respond(context.Background(), "dsh", "session-1", "event-q", json.RawMessage(dshQuestionResponseFixture)); err != nil {
		t.Fatal(err)
	}
	args := host.calls["$events/result"]
	raw, _ := json.Marshal(args)
	if args["clientId"] != "client-q" || !strings.Contains(string(raw), `"answers":[{"id":"choice","selected":["B"]}`) {
		t.Fatalf("native outcome=%s", raw)
	}
	if len(b.pendingInteractions("session-1")) != 0 {
		t.Fatal("answered question remains pending")
	}
}

func TestDSHQuestionsContinuedRestoreAndAnswer(t *testing.T) {
	host := newRecordingHost(t, nil)
	host.host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
		host.calls[endpoint] = args
		if endpoint == "userQuestions/answer" {
			return true, nil
		}
		return nil, nil
	}
	b := newTestDSHBackend(t, host.host)
	listing := `{"items":[{"sessionId":"session-1","projections":{"values":{"userQuestions":{"active":[{"callId":"call-1","state":"continued","questions":[{"id":"q","question":"Details?"}]}],"settled":[]}}}}]}`
	b.syncQuestionListing(json.RawMessage(listing))
	if !b.sessionHasQuestions("session-1") || len(b.pendingInteractions("session-1")) != 1 {
		t.Fatal("continued question was not restored")
	}
	if err := b.Respond(context.Background(), "dsh", "session-1", "dsh-question/call-1", json.RawMessage(`{"answers":{"q":{"answers":["late answer"]}}}`)); err != nil {
		t.Fatal(err)
	}
	args := host.calls["userQuestions/answer"]
	if args["agentId"] != "session-1" || args["callId"] != "call-1" {
		t.Fatalf("args=%+v", args)
	}
	b.syncQuestionListing(json.RawMessage(listing))
	if b.sessionHasQuestions("session-1") {
		t.Fatal("refresh reopened a submitted reply")
	}
	if err := b.Respond(context.Background(), "dsh", "session-1", "dsh-question/call-1", json.RawMessage(`{"answers":{"q":{"answers":["again"]}}}`)); err != errChatRequestNotFound {
		t.Fatalf("duplicate reply err=%v", err)
	}
	b.syncQuestionListing(json.RawMessage(`{"items":[{"sessionId":"session-1","projections":{"values":{"userQuestions":{"active":[],"settled":[]}}}}]}`))
	if len(b.interactions) != 0 {
		t.Fatal("settled question retained")
	}
}

func TestDSHQuestionsOpenProjectionMarksUnopenedSessionWaiting(t *testing.T) {
	b := newDSHChatBackend(dshHarnessSpec{})
	b.syncQuestionListing(json.RawMessage(`{"items":[{"sessionId":"session-1","projections":{"values":{"userQuestions":{"active":[{"callId":"open-call","state":"open","questions":[{"id":"q","question":"Continue?"}]}]}}}}]}`))
	if !b.sessionHasQuestions("session-1") || b.sessionHasQuestions("session-2") {
		t.Fatal("open question waiting identity incorrect")
	}
}
