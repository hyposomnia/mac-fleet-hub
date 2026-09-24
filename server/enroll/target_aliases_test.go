package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTargetAliasesResolveWithinKeyScopeAndHideTarget(t *testing.T) {
	dir := t.TempDir()
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sessions" || r.URL.Query().Get("assistant") != "codex" {
			t.Errorf("unexpected agent request: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"sessions": []targetSession{{SessionID: "thread-1", Cwd: "/Users/a/secret-project", Title: "Private session"}}})
	}))
	defer agent.Close()
	api := &messageAPI{keyFile: filepath.Join(dir, "keys.json"), jobsFile: filepath.Join(dir, "jobs.json"), macIPs: []string{"127.0.0.1"}, agentPort: agent.Listener.Addr().(*net.TCPAddr).Port, client: agent.Client(), jobs: map[string]*messageJob{}, wake: make(chan struct{}, 1)}
	api.resolveTarget = api.resolveMessageTarget
	callAdmin := func(method, path, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		api.handleAccessKeys(rr, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rr
	}
	target := `{"alias":"private-work","target":{"device_id":"m1","ai_client":"codex","project_path":"/Users/a/secret-project","session_id":"thread-1"}}`
	for _, invalid := range []string{
		`{"alias":"partial","target":{"device_id":"m1","ai_client":"codex","project_path":"/Users/a/secret-project"}}`,
		`{"alias":"partial","target":{"device_id":"m1","ai_client":"codex","session_id":"thread-1"}}`,
		`{"alias":"missing","target":{"device_id":"m1","ai_client":"codex","project_path":"/Users/a/secret-project","session_id":"missing"}}`,
		`{"alias":"bad name","target":{"device_id":"m1","ai_client":"codex","project_path":"/Users/a/secret-project","session_id":"thread-1"}}`,
		`{"alias":"private-work","target":{"device_id":"m1","ai_client":"codex","project_path":"/Users/a/secret-project","session_id":"thread-1","unknown":true}}`,
	} {
		rr := callAdmin(http.MethodPost, "/automation/access-keys/aliases", invalid)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid alias %s: %d %s", invalid, rr.Code, rr.Body.String())
		}
	}
	created := callAdmin(http.MethodPost, "/automation/access-keys/aliases", target)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	if rr := callAdmin(http.MethodPost, "/automation/access-keys/aliases", strings.Replace(target, "private-work", "PRIVATE-WORK", 1)); rr.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d %s", rr.Code, rr.Body.String())
	}
	loaded := &messageAPI{keyFile: api.keyFile, jobsFile: api.jobsFile, jobs: map[string]*messageJob{}}
	if err := loaded.load(); err != nil || len(loaded.aliases) != 1 || loaded.aliases[0].Target.SessionID != "thread-1" {
		t.Fatalf("alias persistence: %v %#v", err, loaded.aliases)
	}
	key, _ := newAccessKey("test")
	key.Binding = &accessKeyBinding{DeviceID: "m1", AIClient: "codex", ProjectPath: "/Users/a/secret-project", SessionID: "thread-1"}
	api.keys = []accessKeyState{key}
	if err := api.saveKeysLocked(); err != nil {
		t.Fatal(err)
	}
	submit := func(body, idem string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key.Secret)
		req.Header.Set("Idempotency-Key", idem)
		rr := httptest.NewRecorder()
		api.handleMessages(rr, req)
		return rr
	}
	for _, body := range []string{
		`{"alias":"private-work","device":"","message":"hello"}`,
		`{"alias":"private-work","session":null,"message":"hello"}`,
		`{"Alias":"private-work","Device":"m1","message":"hello"}`,
	} {
		rr := submit(body, "")
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "invalid_target") {
			t.Fatalf("mixed target %s: %d %s", body, rr.Code, rr.Body.String())
		}
	}
	first := submit(`{"alias":"PRIVATE-WORK","message":"hello"}`, "request-1")
	replay := submit(`{"alias":"private-work","message":"hello"}`, "request-1")
	if first.Code != http.StatusAccepted || replay.Code != http.StatusAccepted || first.Body.String() != replay.Body.String() {
		t.Fatalf("alias submit/replay: %d %s / %d %s", first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}
	var result struct {
		MessageID string `json:"message_id"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &result)
	job := api.jobs[result.MessageID]
	if job == nil || job.TargetAlias != "private-work" || job.SessionID != "thread-1" {
		t.Fatalf("resolved job: %#v", job)
	}
	get := httptest.NewRequest(http.MethodGet, "/v1/messages/"+result.MessageID, nil)
	get.Header.Set("Authorization", "Bearer "+key.Secret)
	rr := httptest.NewRecorder()
	api.handleMessages(rr, get)
	for _, private := range []string{"device", "ai_client", "project", "session_id", "secret-project", "thread-1", "Private session"} {
		if bytes.Contains(rr.Body.Bytes(), []byte(private)) {
			t.Errorf("public result exposes %q: %s", private, rr.Body.String())
		}
	}
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"alias":"private-work"`) {
		t.Fatalf("public result: %d %s", rr.Code, rr.Body.String())
	}
	failed := cloneMessageJob(job)
	failed.Status = messageFailed
	failed.Error = &messageError{Code: "internal_error", Message: "failed /Users/a/secret-project on m1", Retryable: true}
	publicFailure, _ := json.Marshal(publicMessage(failed, false))
	if bytes.Contains(publicFailure, []byte("secret-project")) || bytes.Contains(publicFailure, []byte("m1")) {
		t.Fatalf("public failure exposes target: %s", publicFailure)
	}
	if admin := publicMessage(job, true); admin["project"] != "/Users/a/secret-project" {
		t.Fatalf("admin loses target: %#v", admin)
	}
	callback, _ := json.Marshal(publicMessage(job, false))
	if bytes.Contains(callback, []byte("secret-project")) {
		t.Fatalf("callback exposes target: %s", callback)
	}
	// An alias may change, but an idempotent replay must never silently point at a different target.
	patched := callAdmin(http.MethodPatch, "/automation/access-keys/aliases/private-work", strings.Replace(target, "private-work", "new-name", 1))
	if patched.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", patched.Code, patched.Body.String())
	}
	if rr := submit(`{"alias":"private-work","message":"hello"}`, "request-1"); rr.Code != http.StatusNotFound {
		t.Fatalf("old alias: %d %s", rr.Code, rr.Body.String())
	}
	if rr := callAdmin(http.MethodDelete, "/automation/access-keys/aliases/new-name", ""); rr.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
}

func TestAliasMustStayInsideCurrentKeyBinding(t *testing.T) {
	dir := t.TempDir()
	key, _ := newAccessKey("restricted")
	key.Binding = &accessKeyBinding{DeviceID: "m2"}
	api := &messageAPI{keyFile: filepath.Join(dir, "keys.json"), jobsFile: filepath.Join(dir, "jobs.json"), keys: []accessKeyState{key}, aliases: []targetAlias{{Alias: "secret", Target: accessKeyBinding{DeviceID: "m1", AIClient: "codex", ProjectPath: "/private", SessionID: "session"}}}, jobs: map[string]*messageJob{}, macIPs: []string{"127.0.0.1", "127.0.0.2"}}
	api.resolveTarget = func(context.Context, submitMessageRequest) (resolvedTarget, *apiProblem) {
		t.Fatal("out-of-scope alias must not resolve")
		return resolvedTarget{}, nil
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"alias":"secret","message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key.Secret)
	rr := httptest.NewRecorder()
	api.handleMessages(rr, req)
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "access_key_scope_mismatch") {
		t.Fatalf("scope: %d %s", rr.Code, rr.Body.String())
	}
}

func TestLegacyKeysSurviveAliasPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	old := accessKeyStoreDisk{Version: 2, Keys: []accessKeyState{{ID: "key_old", Hash: hashString("old")}}}
	raw, _ := json.Marshal(old)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	api := &messageAPI{keyFile: path, jobsFile: filepath.Join(dir, "jobs.json"), jobs: map[string]*messageJob{}}
	if err := api.load(); err != nil {
		t.Fatal(err)
	}
	api.aliases = []targetAlias{{Alias: "demo", CreatedAt: time.Now().UTC()}}
	if err := api.saveKeysLocked(); err != nil {
		t.Fatal(err)
	}
	reloaded := &messageAPI{keyFile: path, jobsFile: api.jobsFile, jobs: map[string]*messageJob{}}
	if err := reloaded.load(); err != nil || len(reloaded.keys) != 1 || len(reloaded.aliases) != 1 {
		t.Fatalf("reload: %v keys=%d aliases=%d", err, len(reloaded.keys), len(reloaded.aliases))
	}
}
