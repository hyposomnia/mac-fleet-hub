package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyNameAliasResolvesSessionAndHidesTarget(t *testing.T) {
	dir := t.TempDir()
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sessions" || r.URL.Query().Get("assistant") != "codex" {
			t.Errorf("unexpected agent request: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"sessions": []targetSession{{SessionID: "thread-1", Cwd: "/Users/a/private-project", Title: "Private session"}}})
	}))
	defer agent.Close()
	api := &messageAPI{keyFile: filepath.Join(dir, "keys.json"), jobsFile: filepath.Join(dir, "jobs.json"), macIPs: []string{"127.0.0.1"}, agentPort: agent.Listener.Addr().(*net.TCPAddr).Port, client: agent.Client(), jobs: map[string]*messageJob{}, wake: make(chan struct{}, 1)}
	api.resolveTarget = api.resolveMessageTarget
	create := httptest.NewRecorder()
	api.handleAccessKeys(create, httptest.NewRequest(http.MethodPost, "/automation/access-keys", strings.NewReader(`{"name":"每日同步","binding":{"device_id":"m1","ai_client":"codex","project_path":"/Users/a/private-project","session_id":"thread-1"}}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("key create: %d %s", create.Code, create.Body.String())
	}
	var key accessKeyState
	if err := json.Unmarshal(create.Body.Bytes(), &key); err != nil {
		t.Fatal(err)
	}
	submit := func(payload, idem string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+key.Secret)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idem)
		rr := httptest.NewRecorder()
		api.handleMessages(rr, req)
		return rr
	}
	for _, payload := range []string{
		`{"alias":"每日同步","device":"","message":"hello"}`,
		`{"Alias":"每日同步","Session":null,"message":"hello"}`,
	} {
		rr := submit(payload, "")
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "invalid_target") {
			t.Fatalf("mixed target %s: %d %s", payload, rr.Code, rr.Body.String())
		}
	}
	if rr := submit(`{"alias":"别的密钥","message":"hello"}`, ""); rr.Code != http.StatusNotFound {
		t.Fatalf("wrong key name: %d %s", rr.Code, rr.Body.String())
	}
	first := submit(`{"alias":"每日同步","message":"hello"}`, "request-1")
	replay := submit(`{"alias":"每日同步","message":"hello"}`, "request-1")
	if first.Code != http.StatusAccepted || replay.Code != http.StatusAccepted || first.Body.String() != replay.Body.String() {
		t.Fatalf("submit/replay: %d %s / %d %s", first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}
	var result struct {
		MessageID string `json:"message_id"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &result)
	job := api.jobs[result.MessageID]
	if job == nil || job.TargetAlias != "每日同步" || job.SessionID != "thread-1" {
		t.Fatalf("job target: %#v", job)
	}
	read := httptest.NewRequest(http.MethodGet, "/v1/messages/"+result.MessageID, nil)
	read.Header.Set("Authorization", "Bearer "+key.Secret)
	rr := httptest.NewRecorder()
	api.handleMessages(rr, read)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"alias":"每日同步"`) {
		t.Fatalf("public result: %d %s", rr.Code, rr.Body.String())
	}
	for _, private := range []string{"device", "ai_client", "project", "session_id", "private-project", "thread-1"} {
		if bytes.Contains(rr.Body.Bytes(), []byte(private)) {
			t.Errorf("public result leaked %s: %s", private, rr.Body.String())
		}
	}
	failed := cloneMessageJob(job)
	failed.Status = messageFailed
	failed.Error = &messageError{Code: "execution_failed", Message: "/Users/a/private-project on m1", Retryable: true}
	publicFailed, _ := json.Marshal(publicMessage(failed, false))
	if bytes.Contains(publicFailed, []byte("private-project")) {
		t.Fatalf("failure/callback leaked target: %s", publicFailed)
	}
	if admin := publicMessage(job, true); admin["project"] != "/Users/a/private-project" {
		t.Fatalf("admin record lost target: %#v", admin)
	}
	// Renaming the key changes the alias immediately, including on retries.
	change := httptest.NewRecorder()
	api.handleAccessKeys(change, httptest.NewRequest(http.MethodPatch, "/automation/access-keys/"+key.ID, strings.NewReader(`{"name":"新名称"}`)))
	if change.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", change.Code, change.Body.String())
	}
	if rr := submit(`{"alias":"每日同步","message":"hello"}`, "request-1"); rr.Code != http.StatusNotFound {
		t.Fatalf("old alias after rename: %d %s", rr.Code, rr.Body.String())
	}
	if rr := submit(`{"alias":"新名称","message":"hello"}`, "request-2"); rr.Code != http.StatusAccepted {
		t.Fatalf("new alias: %d %s", rr.Code, rr.Body.String())
	}
}

func TestKeyNameAliasRequiresCompleteSessionBinding(t *testing.T) {
	dir := t.TempDir()
	key, _ := newAccessKey("Sync")
	api := &messageAPI{keyFile: filepath.Join(dir, "keys.json"), jobsFile: filepath.Join(dir, "jobs.json"), keys: []accessKeyState{key}, macIPs: []string{"127.0.0.1"}, jobs: map[string]*messageJob{}}
	api.resolveTarget = func(context.Context, submitMessageRequest) (resolvedTarget, *apiProblem) {
		t.Fatal("incomplete binding should not resolve")
		return resolvedTarget{}, nil
	}
	submit := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"alias":"Sync","message":"hello"}`))
		req.Header.Set("Authorization", "Bearer "+key.Secret)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		api.handleMessages(rr, req)
		return rr
	}
	for _, binding := range []*accessKeyBinding{nil, {DeviceID: "m1"}, {DeviceID: "m1", AIClient: "codex"}, {DeviceID: "m1", AIClient: "codex", ProjectPath: "/private"}} {
		api.keys[0].Binding = binding
		rr := submit()
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "alias_requires_bound_session") {
			t.Fatalf("binding=%#v: %d %s", binding, rr.Code, rr.Body.String())
		}
	}
}

func TestSameKeyNameAliasUsesAuthenticatedKeyScope(t *testing.T) {
	dir := t.TempDir()
	first, _ := newAccessKey("Sync")
	second, _ := newAccessKey("Sync")
	first.Binding = &accessKeyBinding{DeviceID: "m1", AIClient: "codex", ProjectPath: "/private/one", SessionID: "thread-1"}
	second.Binding = &accessKeyBinding{DeviceID: "m2", AIClient: "codex", ProjectPath: "/private/two", SessionID: "thread-2"}
	api := &messageAPI{keyFile: filepath.Join(dir, "keys.json"), jobsFile: filepath.Join(dir, "jobs.json"), keys: []accessKeyState{first, second}, macIPs: []string{"127.0.0.1", "127.0.0.2"}, jobs: map[string]*messageJob{}, wake: make(chan struct{}, 1)}
	api.resolveTarget = func(_ context.Context, req submitMessageRequest) (resolvedTarget, *apiProblem) {
		return resolvedTarget{DeviceID: req.Device, AIClient: req.AIClient, ProjectPath: req.Project, SessionID: *req.Session}, nil
	}
	for _, tc := range []struct {
		key  accessKeyState
		want string
	}{{first, "thread-1"}, {second, "thread-2"}} {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"alias":"Sync","message":"hello"}`))
		req.Header.Set("Authorization", "Bearer "+tc.key.Secret)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		api.handleMessages(rr, req)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("submit: %d %s", rr.Code, rr.Body.String())
		}
		var result struct {
			MessageID string `json:"message_id"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &result)
		if api.jobs[result.MessageID].SessionID != tc.want {
			t.Fatalf("wrong session: %#v", api.jobs[result.MessageID])
		}
	}
}

func TestKeyAliasRenameDuringResolutionRejectsInFlightRequest(t *testing.T) {
	dir := t.TempDir()
	key, _ := newAccessKey("Sync")
	key.Binding = &accessKeyBinding{DeviceID: "m1", AIClient: "codex", ProjectPath: "/private", SessionID: "thread-1"}
	api := &messageAPI{keyFile: filepath.Join(dir, "keys.json"), jobsFile: filepath.Join(dir, "jobs.json"), keys: []accessKeyState{key}, macIPs: []string{"127.0.0.1"}, jobs: map[string]*messageJob{}, wake: make(chan struct{}, 1)}
	api.resolveTarget = func(_ context.Context, req submitMessageRequest) (resolvedTarget, *apiProblem) {
		api.mu.Lock()
		api.keys[0].Name = "Renamed"
		api.mu.Unlock()
		return resolvedTarget{DeviceID: req.Device, AIClient: req.AIClient, ProjectPath: req.Project, SessionID: *req.Session}, nil
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"alias":"Sync","message":"hello"}`))
	req.Header.Set("Authorization", "Bearer "+key.Secret)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	api.handleMessages(rr, req)
	if rr.Code != http.StatusForbidden || len(api.jobs) != 0 {
		t.Fatalf("renamed key accepted old alias: %d %s", rr.Code, rr.Body.String())
	}
	admin := httptest.NewRecorder()
	api.handleAccessKeys(admin, httptest.NewRequest(http.MethodGet, "/automation/access-keys/aliases", nil))
	if admin.Code != http.StatusMethodNotAllowed {
		t.Fatalf("separate alias endpoint still active: %d %s", admin.Code, admin.Body.String())
	}
}
