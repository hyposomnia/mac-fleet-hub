package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type recordsTestPage struct {
	Messages   []map[string]interface{} `json:"messages"`
	NextCursor string                   `json:"next_cursor"`
	HasMore    bool                     `json:"has_more"`
	Fields     []struct {
		Name string `json:"name"`
	} `json:"filter_fields"`
}

func recordsTestRequest(a *messageAPI, query url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRecorder()
	a.handleMessageRecords(r, httptest.NewRequest(http.MethodGet, "/automation/message-records?"+query.Encode(), nil))
	return r
}

func recordsTestRead(t *testing.T, a *messageAPI, query url.Values) recordsTestPage {
	t.Helper()
	r := recordsTestRequest(a, query)
	if r.Code != 200 {
		t.Fatalf("status %d: %s", r.Code, r.Body.String())
	}
	var p recordsTestPage
	if err := json.Unmarshal(r.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMessageRecordsCursorAndSummary(t *testing.T) {
	base := time.Date(2026, 8, 4, 9, 0, 0, 100, time.UTC)
	a := &messageAPI{jobs: map[string]*messageJob{}}
	for _, id := range []string{"msg_a", "msg_b", "msg_c", "msg_d", "msg_e"} {
		a.jobs[id] = &messageJob{ID: id, CreatedAt: base, Status: messageCompleted, Message: strings.Repeat("正文", 10000), AIMessage: strings.Repeat("回复", 10000), CallbackSigningKey: "secret-signature", RequestHash: "private-hash"}
	}
	q := url.Values{"limit": {"2"}, "view": {"summary"}}
	first := recordsTestRead(t, a, q)
	if len(first.Messages) != 2 || first.Messages[0]["message_id"] != "msg_e" || first.Messages[1]["message_id"] != "msg_d" || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page: count=%d cursor=%q more=%v", len(first.Messages), first.NextCursor, first.HasMore)
	}
	for _, m := range first.Messages {
		if _, ok := m["message"]; ok {
			t.Fatal("summary contains full message")
		}
		if _, ok := m["ai_message"]; ok {
			t.Fatal("summary contains full AI reply")
		}
	}
	if r := recordsTestRequest(a, q); r.Body.Len() > 4000 {
		t.Fatalf("unbounded summary: %d bytes", r.Body.Len())
	}
	a.jobs["new"] = &messageJob{ID: "new", CreatedAt: base.Add(time.Second)}
	q.Set("cursor", first.NextCursor)
	second := recordsTestRead(t, a, q)
	if len(second.Messages) != 2 || second.Messages[0]["message_id"] != "msg_c" || second.Messages[1]["message_id"] != "msg_b" || !second.HasMore {
		t.Fatalf("second page: %#v", second)
	}
	q.Set("cursor", second.NextCursor)
	last := recordsTestRead(t, a, q)
	if len(last.Messages) != 1 || last.Messages[0]["message_id"] != "msg_a" || last.HasMore || last.NextCursor != "" {
		t.Fatalf("last page: %#v", last)
	}
	r := recordsTestRequest(a, url.Values{"detail": {"msg_a"}})
	if r.Code != 200 || !strings.Contains(r.Body.String(), strings.Repeat("回复", 10000)) || strings.Contains(r.Body.String(), "secret-signature") || strings.Contains(r.Body.String(), "private-hash") {
		t.Fatalf("detail status=%d bytes=%d", r.Code, r.Body.Len())
	}
	if r := recordsTestRequest(a, url.Values{"detail": {"missing"}}); r.Code != 404 {
		t.Fatalf("missing detail: %d", r.Code)
	}
}

func TestMessageRecordsAllStructuredFilters(t *testing.T) {
	x := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	target := &messageJob{ID: "match", Status: messageFailed, DeviceID: "m7", DeviceName: "Studio", AIClient: "deepseek", ProjectPath: "/work/demo", ProjectName: "Demo", SessionID: "thread-7", SessionName: "Review", TargetAlias: "Deploy", AccessKeyID: "key-7", AccessKeyName: "Webhook", TurnID: "turn-7", AgentQueueID: "queue-7", IdempotencyKey: "request-7", CreatedAt: x, StartedAt: x, FailedAt: x, CompletedAt: x, CallbackURL: "https://example.test/hook", Callback: callbackState{Status: "delivery_failed", Attempts: 3, LastAttemptAt: x, NextAttemptAt: x, LastError: "network trouble"}, Error: &messageError{Code: "device_unavailable", Message: "offline", Retryable: true}, Message: "Needle user text", AIMessage: "answer"}
	other := &messageJob{ID: "other", Status: messageCompleted, DeviceID: "m8", DeviceName: "Laptop", AIClient: "codex", ProjectPath: "/work/else", ProjectName: "Else", SessionID: "thread-8", SessionName: "Build", TargetAlias: "Build", AccessKeyID: "key-8", AccessKeyName: "Scheduler", TurnID: "turn-8", AgentQueueID: "queue-8", IdempotencyKey: "request-8", CreatedAt: x.Add(time.Hour), StartedAt: x.Add(time.Hour), FailedAt: x.Add(time.Hour), CompletedAt: x.Add(time.Hour), CallbackURL: "https://else.test/hook", Callback: callbackState{Status: "delivered", Attempts: 1, LastAttemptAt: x.Add(time.Hour), NextAttemptAt: x.Add(time.Hour), LastError: "else"}, Error: &messageError{Code: "timeout", Message: "timed out", Retryable: false}, Message: "else", AIMessage: "haystack"}
	a := &messageAPI{jobs: map[string]*messageJob{"match": target, "other": other}}
	tests := map[string]string{"message_id": "match", "status": "failed", "device_id": "m7", "device_name": "Studio", "ai_client": "deepseek", "project": "/work/demo", "project_name": "Demo", "session_id": "thread-7", "session_name": "Review", "alias": "Deploy", "access_key_id": "key-7", "access_key_name": "Webhook", "turn_id": "turn-7", "agent_queue_id": "queue-7", "idempotency_key": "request-7", "created_at": x.Format(time.RFC3339Nano), "started_at": x.Format(time.RFC3339Nano), "completed_at": x.Format(time.RFC3339Nano), "failed_at": x.Format(time.RFC3339Nano), "callback_url": "https://example.test/hook", "callback_status": "delivery_failed", "callback_attempts": "3", "callback_last_attempt_at": x.Format(time.RFC3339Nano), "callback_next_attempt_at": x.Format(time.RFC3339Nano), "callback_last_error": "network trouble", "error_code": "device_unavailable", "error_message": "offline", "error_retryable": "true"}
	meta := recordsTestRead(t, a, url.Values{"include_filters": {"1"}})
	if len(meta.Fields) != len(tests) {
		t.Fatalf("filter schema count=%d want %d", len(meta.Fields), len(tests))
	}
	for field, value := range tests {
		t.Run(field, func(t *testing.T) {
			filters, _ := json.Marshal([]map[string]string{{"field": field, "op": "eq", "value": value}})
			p := recordsTestRead(t, a, url.Values{"filters": {string(filters)}})
			if len(p.Messages) != 1 || p.Messages[0]["message_id"] != "match" {
				t.Fatalf("filter %s: %#v", field, p.Messages)
			}
		})
	}
	combined := `[{"field":"created_at","op":"lte","value":"2026-07-09T12:00:00Z"},{"field":"callback_attempts","op":"gte","value":"3"},{"field":"project_name","op":"contains","value":"dem"}]`
	p := recordsTestRead(t, a, url.Values{"filters": {combined}, "q": {"needle"}, "limit": {"1"}})
	if len(p.Messages) != 1 || p.Messages[0]["message_id"] != "match" || p.HasMore {
		t.Fatalf("combined filtering: %#v", p)
	}
	p = recordsTestRead(t, a, url.Values{"q": {"haystack"}})
	if len(p.Messages) != 1 || p.Messages[0]["message_id"] != "other" {
		t.Fatalf("AI text search: %#v", p)
	}
	a.jobs["empty"] = &messageJob{ID: "empty", CreatedAt: x}
	p = recordsTestRead(t, a, url.Values{"filters": {`[{"field":"error_retryable","op":"empty"}]`}})
	if len(p.Messages) != 1 || p.Messages[0]["message_id"] != "empty" {
		t.Fatalf("missing != false: %#v", p)
	}
}

func TestMessageRecordsRejectInvalidQueries(t *testing.T) {
	a := &messageAPI{jobs: map[string]*messageJob{"a": {ID: "a"}, "b": {ID: "b"}}}
	for _, q := range []url.Values{
		{"limit": {"0"}}, {"limit": {"101"}}, {"limit": {"foo"}}, {"cursor": {"garbage"}},
		{"filters": {"not-json"}}, {"filters": {`[{"field":"device_ip","op":"eq","value":"private"}]`}},
		{"filters": {`[{"field":"status","op":"gte","value":"failed"}]`}},
		{"filters": {`[{"field":"created_at","op":"gte","value":"bad date"}]`}},
		{"filters": {`[{"field":"callback_attempts","op":"eq","value":"NaN"}]`}},
		{"filters": {`[{"field":"error_retryable","op":"eq","value":"maybe"}]`}},
		{"filters": {`[{"field":"status","op":"eq","value":"bogus"}]`}},
	} {
		if r := recordsTestRequest(a, q); r.Code != 400 {
			t.Errorf("query %v returned %d", q, r.Code)
		}
	}
	p := recordsTestRead(t, a, url.Values{"limit": {"1"}})
	if r := recordsTestRequest(a, url.Values{"cursor": {p.NextCursor}, "q": {"different"}}); r.Code != 400 {
		t.Fatalf("cursor accepted changed filters: %d", r.Code)
	}
}

func TestMessageRecordsDefaultSizeAndMetadata(t *testing.T) {
	a := &messageAPI{jobs: map[string]*messageJob{}, keys: []accessKeyState{{ID: "active", Name: "Current name", Secret: "never-return-key"}}}
	base := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("message-%03d", i)
		a.jobs[id] = &messageJob{ID: id, CreatedAt: base.Add(time.Duration(i) * time.Second), AccessKeyID: "active", AccessKeyName: "Old name", Status: messageQueued}
	}
	a.jobs["legacy"] = &messageJob{ID: "legacy", CreatedAt: base.Add(-time.Second)}
	a.jobs["deleted"] = &messageJob{ID: "deleted", AccessKeyID: "deleted-key", AccessKeyName: "Deleted name", CreatedAt: base.Add(-time.Hour)}
	r := recordsTestRequest(a, url.Values{"view": {"summary"}, "include_filters": {"1"}})
	var first recordsTestPage
	if err := json.Unmarshal(r.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Messages) != 25 || first.Messages[0]["message_id"] != "message-059" || !first.HasMore {
		t.Fatalf("default page: count=%d more=%v", len(first.Messages), first.HasMore)
	}
	if r.Header().Get("Cache-Control") != "no-store" || strings.Contains(r.Body.String(), "never-return-key") || !strings.Contains(r.Body.String(), "Deleted name") || !strings.Contains(r.Body.String(), "Current name") {
		t.Fatal("bad metadata or caching")
	}
	legacy := recordsTestRead(t, a, url.Values{"access_key_id": {"__legacy__"}})
	if len(legacy.Messages) != 1 || legacy.Messages[0]["message_id"] != "legacy" {
		t.Fatal("legacy filter failed")
	}
}
