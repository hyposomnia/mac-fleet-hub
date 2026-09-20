package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestResolveProjectAndSessionCaseInsensitive(t *testing.T) {
	sessions := []targetSession{
		{SessionID: "THREAD-A", Title: "修复登录测试", Cwd: "/Users/alice/Work/Example"},
		{SessionID: "thread-b", Title: "架构分析", Cwd: "/Users/alice/Work/Example"},
	}
	name, path, problem := resolveProject("eXaMpLe", sessions)
	if problem != nil || name != "Example" || path != "/Users/alice/Work/Example" {
		t.Fatalf("resolveProject = name=%q path=%q problem=%v", name, path, problem)
	}
	id, title, problem := resolveSession("thread-a", path, sessions)
	if problem != nil || id != "THREAD-A" || title != "修复登录测试" {
		t.Fatalf("resolveSession(id) = id=%q title=%q problem=%v", id, title, problem)
	}
	id, title, problem = resolveSession("架构分析", stringsToDifferentCase(path), sessions)
	if problem != nil || id != "thread-b" || title != "架构分析" {
		t.Fatalf("resolveSession(name) = id=%q title=%q problem=%v", id, title, problem)
	}
	_, _, problem = resolveSession("THREAD-A", "/Users/alice/Other", sessions)
	if problem == nil || problem.Code != "session_project_mismatch" {
		t.Fatalf("want session_project_mismatch, got %#v", problem)
	}
}

func stringsToDifferentCase(value string) string {
	b := []byte(value)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		} else if c >= 'a' && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}

func TestResolveAmbiguousProjectAndSessionReturnsCandidates(t *testing.T) {
	sessions := []targetSession{
		{SessionID: "s1", Title: "同名会话", Cwd: "/Users/a/one/demo", ProjectName: "Demo"},
		{SessionID: "s2", Title: "同名会话", Cwd: "/Users/a/two/demo", ProjectName: "demo"},
		{SessionID: "s3", Title: "同名会话", Cwd: "/Users/a/one/demo", ProjectName: "Demo"},
	}
	_, _, problem := resolveProject("DEMO", sessions)
	if problem == nil || problem.Code != "ambiguous_project" {
		t.Fatalf("want ambiguous_project, got %#v", problem)
	}
	_, _, problem = resolveSession("同名会话", "/users/A/ONE/DEMO", sessions)
	if problem == nil || problem.Code != "ambiguous_session" {
		t.Fatalf("want ambiguous_session, got %#v", problem)
	}
}

func TestAccessKeyAndMessageIdempotency(t *testing.T) {
	dir := t.TempDir()
	api := &messageAPI{
		keyFile:        filepath.Join(dir, "key.json"),
		jobsFile:       filepath.Join(dir, "jobs.json"),
		jobs:           map[string]*messageJob{},
		wake:           make(chan struct{}, 1),
		activeExec:     map[string]bool{},
		activeCallback: map[string]bool{},
		maxConcurrent:  1,
	}
	api.resolveTarget = func(context.Context, submitMessageRequest) (resolvedTarget, *apiProblem) {
		return resolvedTarget{
			DeviceID: "m1", DeviceName: "Mac mini M4", DeviceIP: "100.64.0.2",
			AIClient: "codex", Assistant: "codex", ProjectName: "demo", ProjectPath: "/Users/a/demo",
		}, nil
	}
	rotateReq := httptest.NewRequest(http.MethodPost, "/settings/access-key/rotate", nil)
	rotateRR := httptest.NewRecorder()
	api.handleAccessKey(rotateRR, rotateReq)
	if rotateRR.Code != http.StatusOK {
		t.Fatalf("rotate status=%d body=%s", rotateRR.Code, rotateRR.Body.String())
	}
	var rotated struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(rotateRR.Body.Bytes(), &rotated) != nil || rotated.Key == "" {
		t.Fatalf("bad rotate body: %s", rotateRR.Body.String())
	}
	body := []byte(`{"device":"mac MINI m4","ai_client":"codex","project":"DEMO","message":"hello"}`)
	submit := func(payload []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+rotated.Key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "request-1")
		rr := httptest.NewRecorder()
		api.handleMessages(rr, req)
		return rr
	}
	first := submit(body)
	second := submit(body)
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("first=%d %s second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	var firstResult, secondResult map[string]string
	_ = json.Unmarshal(first.Body.Bytes(), &firstResult)
	_ = json.Unmarshal(second.Body.Bytes(), &secondResult)
	if firstResult["message_id"] == "" || firstResult["message_id"] != secondResult["message_id"] {
		t.Fatalf("idempotency mismatch: %#v %#v", firstResult, secondResult)
	}
	conflict := submit([]byte(`{"device":"Mac mini M4","ai_client":"codex","project":"demo","message":"different"}`))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestMessageAPIRejectsMissingKeyAndPrivateCallback(t *testing.T) {
	dir := t.TempDir()
	api := &messageAPI{
		keyFile: filepath.Join(dir, "key.json"), jobsFile: filepath.Join(dir, "jobs.json"),
		jobs: map[string]*messageJob{}, wake: make(chan struct{}, 1),
		activeExec: map[string]bool{}, activeCallback: map[string]bool{}, maxConcurrent: 1,
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{}`)))
	rr := httptest.NewRecorder()
	api.handleMessages(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing key status=%d", rr.Code)
	}
	if problem := validatePublicCallbackURL(context.Background(), "https://127.0.0.1/hook"); problem == nil || problem.Code != "invalid_callback_url" {
		t.Fatalf("private callback should fail, got %#v", problem)
	}
}
