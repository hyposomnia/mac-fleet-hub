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
	"testing"
	"time"
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

func TestAccessKeyCRUDAndMessageRecordFilter(t *testing.T) {
	dir := t.TempDir()
	api := &messageAPI{
		keyFile: filepath.Join(dir, "keys.json"), jobsFile: filepath.Join(dir, "jobs.json"),
		keys: []accessKeyState{}, jobs: map[string]*messageJob{}, wake: make(chan struct{}, 1),
		activeExec: map[string]bool{}, activeCallback: map[string]bool{}, maxConcurrent: 1,
	}
	create := func(name string) accessKeyState {
		req := httptest.NewRequest(http.MethodPost, "/automation/access-keys", bytes.NewReader([]byte(`{"name":"`+name+`"}`)))
		rr := httptest.NewRecorder()
		api.handleAccessKeys(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %q status=%d body=%s", name, rr.Code, rr.Body.String())
		}
		var key accessKeyState
		if err := json.Unmarshal(rr.Body.Bytes(), &key); err != nil || key.ID == "" || key.Secret == "" {
			t.Fatalf("bad create response: %v %s", err, rr.Body.String())
		}
		return key
	}
	first := create("CI")
	second := create("Webhook")

	listReq := httptest.NewRequest(http.MethodGet, "/automation/access-keys", nil)
	listRR := httptest.NewRecorder()
	api.handleAccessKeys(listRR, listReq)
	if listRR.Code != http.StatusOK || !bytes.Contains(listRR.Body.Bytes(), []byte(first.Secret)) || !bytes.Contains(listRR.Body.Bytes(), []byte(second.Secret)) {
		t.Fatalf("list must return both repeat-viewable secrets: %d %s", listRR.Code, listRR.Body.String())
	}

	renameReq := httptest.NewRequest(http.MethodPatch, "/automation/access-keys/"+first.ID, bytes.NewReader([]byte(`{"name":"CI 生产"}`)))
	renameRR := httptest.NewRecorder()
	api.handleAccessKeys(renameRR, renameReq)
	if renameRR.Code != http.StatusOK || !bytes.Contains(renameRR.Body.Bytes(), []byte("CI 生产")) {
		t.Fatalf("rename status=%d body=%s", renameRR.Code, renameRR.Body.String())
	}

	now := time.Now().UTC()
	api.jobs["msg_first"] = &messageJob{ID: "msg_first", Status: messageQueued, AccessKeyID: first.ID, AccessKeyName: "CI 生产", Message: "one", CreatedAt: now}
	api.jobs["msg_second"] = &messageJob{ID: "msg_second", Status: messageQueued, AccessKeyID: second.ID, AccessKeyName: "Webhook", Message: "two", CreatedAt: now.Add(time.Second)}
	filterReq := httptest.NewRequest(http.MethodGet, "/automation/message-records?access_key_id="+first.ID, nil)
	filterRR := httptest.NewRecorder()
	api.handleMessageRecords(filterRR, filterReq)
	if filterRR.Code != http.StatusOK || !bytes.Contains(filterRR.Body.Bytes(), []byte("msg_first")) || bytes.Contains(filterRR.Body.Bytes(), []byte("msg_second")) {
		t.Fatalf("filtered records status=%d body=%s", filterRR.Code, filterRR.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/automation/access-keys/"+second.ID, nil)
	deleteRR := httptest.NewRecorder()
	api.handleAccessKeys(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleteRR.Code, deleteRR.Body.String())
	}
	authReq := httptest.NewRequest(http.MethodGet, "/v1/messages/unknown", nil)
	authReq.Header.Set("Authorization", "Bearer "+second.Secret)
	if _, problem := api.authenticate(authReq); problem == nil || problem.Code != "invalid_access_key" {
		t.Fatalf("deleted key still authenticates: %#v", problem)
	}
}

func TestLegacyAccessKeyMigratesWithoutRevocation(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key.json")
	secret := "mfh_live_legacy-secret"
	legacy := map[string]interface{}{
		"version": 1, "hash": hashString(secret), "prefix": "mfh_live_legacy", "created_at": time.Now().UTC(),
	}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(keyFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	api := &messageAPI{keyFile: keyFile, jobsFile: filepath.Join(dir, "jobs.json"), jobs: map[string]*messageJob{}}
	if err := api.load(); err != nil {
		t.Fatal(err)
	}
	if len(api.keys) != 1 || api.keys[0].ID == "" || api.keys[0].Secret != "" || api.keys[0].Name != "默认密钥" {
		t.Fatalf("unexpected migrated key: %#v", api.keys)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/messages/unknown", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	if _, problem := api.authenticate(req); problem != nil {
		t.Fatalf("legacy key revoked during migration: %v", problem)
	}
	migrated, err := os.ReadFile(keyFile)
	if err != nil || !bytes.Contains(migrated, []byte(`"keys"`)) {
		t.Fatalf("legacy store not migrated: %v %s", err, migrated)
	}
}

func TestAccessKeyBindingCreateEditPersistAndValidation(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sessions" || r.URL.Query().Get("assistant") != "codex" {
			t.Errorf("unexpected sessions request: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"sessions": []targetSession{{SessionID: "thread-1", Cwd: "/Users/a/demo", Title: "Demo"}}})
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	api := &messageAPI{keyFile: filepath.Join(dir, "keys.json"), jobsFile: filepath.Join(dir, "jobs.json"),
		macIPs: []string{"127.0.0.1"}, agentPort: port, client: server.Client(), jobs: map[string]*messageJob{}}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		api.handleAccessKeys(rr, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		return rr
	}
	for _, body := range []string{
		`{"name":"CI","binding":{"device_id":"m1","project_path":"/Users/a/demo"}}`,
		`{"name":"CI","binding":{"device_id":"m1","ai_client":"other"}}`,
		`{"name":"CI","binding":{"device_id":"m1","ai_client":"codex","project_path":"demo"}}`,
		`{"name":"CI","binding":{"device_id":"m2"}}`,
		`{"name":"CI","binding":{"device_id":"m1","unknown":"x"}}`,
		`{"name":"CI","binding":{"device_id":"m1","ai_client":"codex","project_path":"/Users/a/demo","session_id":"wrong"}}`,
	} {
		rr := call(http.MethodPost, "/automation/access-keys", body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid binding %s: %d %s", body, rr.Code, rr.Body.String())
		}
	}
	created := call(http.MethodPost, "/automation/access-keys", `{"name":"CI","binding":{"device_id":"m1","ai_client":"codex","project_path":"/Users/a/demo","session_id":"thread-1"}}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var key accessKeyState
	if err := json.Unmarshal(created.Body.Bytes(), &key); err != nil || key.Binding == nil || key.Binding.SessionID != "thread-1" {
		t.Fatalf("create binding: %v %s", err, created.Body.String())
	}
	loaded := &messageAPI{keyFile: api.keyFile, jobsFile: api.jobsFile, jobs: map[string]*messageJob{}}
	if err := loaded.load(); err != nil || len(loaded.keys) != 1 || loaded.keys[0].Binding.SessionID != "thread-1" {
		t.Fatalf("binding persistence: %v %#v", err, loaded.keys)
	}
	api.resolveTarget = api.resolveMessageTarget
	api.wake = make(chan struct{}, 1)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"device":"m1","ai_client":"codex","project":"DEMO","session":"demo","message":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key.Secret)
	accepted := httptest.NewRecorder()
	api.handleMessages(accepted, request)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("canonical target names should match binding: %d %s", accepted.Code, accepted.Body.String())
	}
	// Changing only the authorization scope must not depend on the job store.
	jobsFile := api.jobsFile
	api.jobsFile = dir // A directory cannot be replaced by the job-store file.
	patched := call(http.MethodPatch, "/automation/access-keys/"+key.ID, `{"binding":{"device_id":"m1"}}`)
	api.jobsFile = jobsFile
	if patched.Code != http.StatusOK || api.keys[0].Name != "CI" || api.keys[0].Binding.AIClient != "" {
		t.Fatalf("patch should preserve name and narrow hierarchy: %d %s", patched.Code, patched.Body.String())
	}
	stored := &messageAPI{keyFile: api.keyFile, jobsFile: api.jobsFile, jobs: map[string]*messageJob{}}
	if err := stored.load(); err != nil || stored.keys[0].Binding.AIClient != "" {
		t.Fatalf("scope edit must persist independently of job store: %v %#v", err, stored.keys)
	}
	cleared := call(http.MethodPatch, "/automation/access-keys/"+key.ID, `{"binding":null}`)
	if cleared.Code != http.StatusOK || api.keys[0].Binding != nil {
		t.Fatalf("clear binding: %d %s", cleared.Code, cleared.Body.String())
	}
}

func TestBoundKeyCannotSubmitOrReadOutsideScope(t *testing.T) {
	dir := t.TempDir()
	first, _ := newAccessKey("bound")
	first.Binding = &accessKeyBinding{DeviceID: "m1", AIClient: "codex", ProjectPath: "/Users/a/demo", SessionID: "thread-1"}
	second, _ := newAccessKey("other")
	api := &messageAPI{keyFile: filepath.Join(dir, "keys.json"), jobsFile: filepath.Join(dir, "jobs.json"),
		macIPs: []string{"100.64.0.2", "100.64.0.3"}, keys: []accessKeyState{first, second}, jobs: map[string]*messageJob{}, wake: make(chan struct{}, 1)}
	api.resolveTarget = func(_ context.Context, req submitMessageRequest) (resolvedTarget, *apiProblem) {
		return resolvedTarget{DeviceID: req.Device, AIClient: req.AIClient, ProjectPath: req.Project, SessionID: *req.Session}, nil
	}
	submit := func(secret, device, client, project, session, idem string) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(submitMessageRequest{Device: device, AIClient: client, Project: project, Session: &session, Message: "hello"})
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idem)
		rr := httptest.NewRecorder()
		api.handleMessages(rr, req)
		return rr
	}
	for _, tc := range []struct{ device, client, project, session string }{
		{"m2", "codex", "/Users/a/demo", "thread-1"},
		{"m1", "deepseek", "/Users/a/demo", "thread-1"},
		{"m1", "codex", "/Users/a/other", "thread-1"},
		{"m1", "codex", "/Users/a/demo", "thread-2"},
		{"m1", "codex", "/Users/a/demo", ""},
	} {
		rr := submit(first.Secret, tc.device, tc.client, tc.project, tc.session, "")
		if rr.Code != http.StatusForbidden || !bytes.Contains(rr.Body.Bytes(), []byte("access_key_scope_mismatch")) {
			t.Fatalf("out-of-scope target %+v: %d %s", tc, rr.Code, rr.Body.String())
		}
	}
	accepted := submit(first.Secret, "m1", "codex", "/Users/a/demo", "thread-1", "retry-1")
	if accepted.Code != http.StatusAccepted || len(api.jobs) != 1 {
		t.Fatalf("valid target: %d %s", accepted.Code, accepted.Body.String())
	}
	var result map[string]string
	_ = json.Unmarshal(accepted.Body.Bytes(), &result)
	read := func(secret string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/messages/"+result["message_id"], nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		rr := httptest.NewRecorder()
		api.handleMessages(rr, req)
		return rr
	}
	if rr := read(second.Secret); rr.Code != http.StatusNotFound {
		t.Fatalf("other key read: %d %s", rr.Code, rr.Body.String())
	}
	if rr := read(first.Secret); rr.Code != http.StatusOK {
		t.Fatalf("own read: %d %s", rr.Code, rr.Body.String())
	}
	api.mu.Lock()
	api.keys[0].Binding = &accessKeyBinding{DeviceID: "m2"}
	api.mu.Unlock()
	if rr := read(first.Secret); rr.Code != http.StatusNotFound {
		t.Fatalf("read after narrowing: %d %s", rr.Code, rr.Body.String())
	}
	if rr := submit(first.Secret, "m1", "codex", "/Users/a/demo", "thread-1", "retry-1"); rr.Code != http.StatusForbidden {
		t.Fatalf("idempotent replay after narrowing: %d %s", rr.Code, rr.Body.String())
	}
}

func TestAccessKeyRPMCreateEditAndLegacyDefault(t *testing.T) {
	dir := t.TempDir()
	api := &messageAPI{keyFile: filepath.Join(dir, "keys.json"), jobsFile: filepath.Join(dir, "jobs.json"), jobs: map[string]*messageJob{}}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		api.handleAccessKeys(rr, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		return rr
	}
	for _, rpm := range []string{"0", "-1", "10001", "1.5", `"ten"`} {
		rr := call(http.MethodPost, "/automation/access-keys", `{"name":"invalid","rpm":`+rpm+`}`)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("rpm %s accepted: %d %s", rpm, rr.Code, rr.Body.String())
		}
	}
	created := call(http.MethodPost, "/automation/access-keys", `{"name":"CI"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var key accessKeyState
	if err := json.Unmarshal(created.Body.Bytes(), &key); err != nil || key.RPM != 10 {
		t.Fatalf("default rpm: %v %+v", err, key)
	}
	path := "/automation/access-keys/" + key.ID
	if rr := call(http.MethodPatch, path, `{"rpm":2}`); rr.Code != http.StatusOK || api.keys[0].RPM != 2 {
		t.Fatalf("edit rpm: %d %s", rr.Code, rr.Body.String())
	}
	if rr := call(http.MethodPatch, path, `{"name":"renamed"}`); rr.Code != http.StatusOK || api.keys[0].RPM != 2 {
		t.Fatalf("name-only edit must retain rpm: %d %s", rr.Code, rr.Body.String())
	}
	loaded := &messageAPI{keyFile: api.keyFile, jobsFile: api.jobsFile, jobs: map[string]*messageJob{}}
	if err := loaded.load(); err != nil || len(loaded.keys) != 1 || loaded.keys[0].RPM != 2 {
		t.Fatalf("persisted rpm: %v %+v", err, loaded.keys)
	}
	// A key written before this field existed must remain usable and inherit 10 RPM.
	loaded.keys[0].RPM = 0
	if err := loaded.saveKeysLocked(); err != nil {
		t.Fatal(err)
	}
	migrated := &messageAPI{keyFile: api.keyFile, jobsFile: api.jobsFile, jobs: map[string]*messageJob{}}
	if err := migrated.load(); err != nil || migrated.keys[0].RPM != 10 {
		t.Fatalf("legacy rpm migration: %v %+v", err, migrated.keys)
	}
	data, err := os.ReadFile(api.keyFile)
	if err != nil || !bytes.Contains(data, []byte(`"rpm": 10`)) {
		t.Fatalf("legacy rpm not persisted: %v", err)
	}
}

func TestAccessKeyRPMRollingWindowAndIndependentKeys(t *testing.T) {
	first, _ := newAccessKey("first")
	first.RPM = 2
	second, _ := newAccessKey("second")
	second.RPM = 1
	api := &messageAPI{keys: []accessKeyState{first, second}}
	start := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	allow := func(key accessKeyState, at time.Time) (*apiProblem, string) {
		rr := httptest.NewRecorder()
		problem := api.checkRateLimit(rr, key, at)
		return problem, rr.Header().Get("Retry-After")
	}
	for _, at := range []time.Time{start, start.Add(10 * time.Second)} {
		if problem, _ := allow(first, at); problem != nil {
			t.Fatalf("first key rejected early: %v", problem)
		}
	}
	if problem, wait := allow(first, start.Add(10*time.Second)); problem == nil || problem.Status != http.StatusTooManyRequests || problem.Code != "rate_limit_exceeded" || wait != "50" {
		t.Fatalf("first key limit: %v Retry-After=%q", problem, wait)
	}
	if problem, _ := allow(second, start.Add(10*time.Second)); problem != nil {
		t.Fatalf("second key shared first key's limit: %v", problem)
	}
	if problem, _ := allow(first, start.Add(time.Minute)); problem != nil {
		t.Fatalf("rolling window did not expire: %v", problem)
	}
	api.mu.Lock()
	api.keys[0].RPM = 1
	api.mu.Unlock()
	if problem, _ := allow(first, start.Add(time.Minute+time.Second)); problem == nil || problem.Status != http.StatusTooManyRequests {
		t.Fatalf("rpm edit not applied immediately: %v", problem)
	}
	api.mu.Lock()
	api.keys[0].Hash = "rotated"
	api.mu.Unlock()
	if problem, _ := allow(first, start.Add(2*time.Minute)); problem == nil || problem.Status != http.StatusUnauthorized {
		t.Fatalf("rotated key still usable: %v", problem)
	}
}

func TestAccessKeyRPMCountsSubmissionsAndResultReads(t *testing.T) {
	dir := t.TempDir()
	key, _ := newAccessKey("CI")
	key.RPM = 2
	api := &messageAPI{keyFile: filepath.Join(dir, "keys.json"), jobsFile: filepath.Join(dir, "jobs.json"), keys: []accessKeyState{key}, jobs: map[string]*messageJob{}, wake: make(chan struct{}, 1)}
	api.resolveTarget = func(context.Context, submitMessageRequest) (resolvedTarget, *apiProblem) {
		return resolvedTarget{DeviceID: "m1", AIClient: "codex", Assistant: "codex", ProjectPath: "/repo"}, nil
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+key.Secret)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		api.handleMessages(rr, req)
		return rr
	}
	body := `{"device":"m1","ai_client":"codex","project":"/repo","message":"hello"}`
	first := call(http.MethodPost, "/v1/messages", body)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first submit: %d %s", first.Code, first.Body.String())
	}
	var response struct {
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil || response.MessageID == "" {
		t.Fatalf("first message id: %v %s", err, first.Body.String())
	}
	read := call(http.MethodGet, "/v1/messages/"+response.MessageID, "")
	if read.Code != http.StatusOK {
		t.Fatalf("read: %d %s", read.Code, read.Body.String())
	}
	limited := call(http.MethodPost, "/v1/messages", body)
	if limited.Code != http.StatusTooManyRequests || !bytes.Contains(limited.Body.Bytes(), []byte("rate_limit_exceeded")) || limited.Header().Get("Retry-After") == "" || len(api.jobs) != 1 {
		t.Fatalf("third request: %d %s retry=%q jobs=%d", limited.Code, limited.Body.String(), limited.Header().Get("Retry-After"), len(api.jobs))
	}
}
