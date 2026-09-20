package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func testGatewayEvent(typ, turnID, itemID string, data interface{}) gatewayChatEvent {
	raw, _ := json.Marshal(data)
	return gatewayChatEvent{Type: typ, TurnID: turnID, ItemID: itemID, Data: raw}
}

func TestMessageWorkerCreatesSessionAndCapturesFinalReply(t *testing.T) {
	messageID := "msg_test"
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/start", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"sessionId": "thread-1"})
	})
	mux.HandleFunc("/api/chat/queue", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"id": "queue-1", "clientMessageId": messageID, "status": "queued", "stateVersion": 1})
	})
	mux.HandleFunc("/api/chat/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []gatewayChatEvent{
			testGatewayEvent("user_done", "", "u1", map[string]string{"clientId": messageID}),
			testGatewayEvent("turn_started", "turn-1", "", nil),
			testGatewayEvent("assistant_done", "turn-1", "a1", map[string]string{"text": "最终回复"}),
			testGatewayEvent("turn_done", "turn-1", "", map[string]string{"status": "completed"}),
		} {
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	host, portText, _ := net.SplitHostPort(parsed.Host)
	port, _ := strconv.Atoi(portText)
	dir := t.TempDir()
	api := &messageAPI{
		jobsFile: filepath.Join(dir, "jobs.json"), jobs: map[string]*messageJob{},
		agentPort: port, client: server.Client(), wake: make(chan struct{}, 1),
		activeExec: map[string]bool{messageID: true}, activeCallback: map[string]bool{}, maxConcurrent: 1,
	}
	api.jobs[messageID] = &messageJob{
		ID: messageID, Status: messageQueued, DeviceID: "m1", DeviceName: "Mac mini M4", DeviceIP: host,
		AIClient: "codex", Assistant: "codex", ProjectName: "demo", ProjectPath: "/Users/a/demo",
		Message: "hello", CreatedAt: time.Now().UTC(),
	}
	api.executeJob(messageID)
	job := api.jobs[messageID]
	if job.Status != messageCompleted || job.SessionID != "thread-1" || job.TurnID != "turn-1" || job.AIMessage != "最终回复" {
		t.Fatalf("unexpected completed job: %+v", job)
	}
}

func TestMessageEventTrackerCorrelatesDeepSeekTurnAndFinalText(t *testing.T) {
	tracker := newMessageEventTracker("msg-1", "")
	events := []gatewayChatEvent{
		testGatewayEvent("user_done", "", "user-1", map[string]string{"clientId": "msg-1", "text": "hello"}),
		testGatewayEvent("turn_started", "turn-7", "", map[string]string{"turnId": "turn-7"}),
		testGatewayEvent("assistant_done", "turn-7", "answer-1", map[string]string{"text": "第一段"}),
		testGatewayEvent("assistant_done", "turn-7", "answer-2", map[string]string{"text": "第二段"}),
		testGatewayEvent("turn_done", "turn-7", "", map[string]string{"status": "completed"}),
	}
	for _, event := range events {
		tracker.process(event)
	}
	if tracker.targetTurn != "turn-7" || !tracker.started || !tracker.done {
		t.Fatalf("unexpected tracker: %+v", tracker)
	}
	if got := tracker.finalText(); got != "第一段\n\n第二段" {
		t.Fatalf("finalText=%q", got)
	}
}

func TestMessageEventTrackerHistoryRecoveryFindsNearestTurn(t *testing.T) {
	tracker := newMessageEventTracker("msg-2", "")
	tracker.processHistory([]gatewayChatEvent{
		testGatewayEvent("turn_started", "old-turn", "", nil),
		testGatewayEvent("turn_done", "old-turn", "", map[string]string{"status": "completed"}),
		testGatewayEvent("user_done", "", "user-2", map[string]string{"clientId": "msg-2"}),
		testGatewayEvent("turn_started", "new-turn", "", nil),
		testGatewayEvent("assistant_done", "new-turn", "answer", map[string]string{"text": "done"}),
		testGatewayEvent("turn_done", "new-turn", "", map[string]string{"status": "completed"}),
	})
	if tracker.targetTurn != "new-turn" || !tracker.done || tracker.finalText() != "done" {
		t.Fatalf("history recovery failed: %+v text=%q", tracker, tracker.finalText())
	}
}
