package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fleet-enroll/multiuser"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMessageAPIExplicitPathsIsolateOwners(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ENROLL_ACCESS_KEY_FILE", filepath.Join(dir, "global-key.json"))
	t.Setenv("ENROLL_MESSAGE_JOBS_FILE", filepath.Join(dir, "global-jobs.json"))
	first, err := newMessageAPIAt(filepath.Join(dir, "users", "1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := newMessageAPIAt(filepath.Join(dir, "users", "2"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := newAccessKey("first owner")
	if err != nil {
		t.Fatal(err)
	}
	first.keys = append(first.keys, key)
	first.jobs["private"] = &messageJob{ID: "private", Message: "owner one"}
	if err := first.saveKeysLocked(); err != nil {
		t.Fatal(err)
	}
	if err := first.saveJobsLocked(); err != nil {
		t.Fatal(err)
	}
	if err := second.load(); err != nil {
		t.Fatal(err)
	}
	if len(second.keys) != 0 || len(second.jobs) != 0 {
		t.Fatal("second owner loaded first owner's store")
	}
	reloaded, err := newMessageAPIAt(filepath.Join(dir, "users", "1"))
	if err != nil || len(reloaded.keys) != 1 || reloaded.jobs["private"].Message != "owner one" {
		t.Fatalf("private store did not survive restart: %v", err)
	}
	for _, path := range []string{first.keyFile, first.jobsFile} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("store permissions for %s: %v, %v", path, info, err)
		}
	}
	if _, err := os.Stat(os.Getenv("ENROLL_ACCESS_KEY_FILE")); !os.IsNotExist(err) {
		t.Fatal("explicit constructor touched global store")
	}
}

type applicationTestScope struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type applicationTestRegistry struct {
	mu      sync.Mutex
	users   []multiuser.User
	devices []multiuser.Device
	scopes  map[string]applicationTestScope
}

func newApplicationTestRegistry() *applicationTestRegistry {
	return &applicationTestRegistry{
		users: []multiuser.User{{ID: 1, Status: "active"}, {ID: 2, Status: "active"}},
		devices: []multiuser.Device{
			{ID: "m3", Index: 3, UserID: 1, NodeID: "node-3", IP: "127.0.0.1", Name: "First Mac", Status: "active"},
			{ID: "m9", Index: 9, UserID: 2, NodeID: "node-9", IP: "127.0.0.2", Name: "Other Mac", Status: "active"},
		},
		scopes: map[string]applicationTestScope{},
	}
}

func (registry *applicationTestRegistry) Users() ([]multiuser.User, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return append([]multiuser.User(nil), registry.users...), nil
}

func (registry *applicationTestRegistry) Devices(owner int64) ([]multiuser.Device, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	devices := []multiuser.Device{}
	for _, device := range registry.devices {
		if device.UserID == owner {
			devices = append(devices, device)
		}
	}
	return devices, nil
}

func (registry *applicationTestRegistry) AuthorizedDevice(owner int64, id string) (multiuser.Device, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	active := false
	for _, user := range registry.users {
		if user.ID == owner && user.Status == "active" {
			active = true
		}
	}
	for _, device := range registry.devices {
		if active && device.UserID == owner && device.ID == id && device.Status == "active" {
			return device, nil
		}
	}
	return multiuser.Device{}, errors.New("not authorized")
}

func (registry *applicationTestRegistry) ScopeContext(owner int64, id string) context.Context {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := strconv.FormatInt(owner, 10) + "/" + id
	if existing, ok := registry.scopes[key]; ok {
		return existing.ctx
	}
	ctx, cancel := context.WithCancel(context.Background())
	registry.scopes[key] = applicationTestScope{ctx: ctx, cancel: cancel}
	return ctx
}

func (registry *applicationTestRegistry) ProxyCredential(owner int64, id string) (string, error) {
	if _, err := registry.AuthorizedDevice(owner, id); err != nil {
		return "", err
	}
	return "test-secret-" + id, nil
}

func (registry *applicationTestRegistry) revoke(owner int64, id string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if id == "" {
		for index := range registry.users {
			if registry.users[index].ID == owner {
				registry.users[index].Status = "disabled"
			}
		}
	} else {
		for index := range registry.devices {
			if registry.devices[index].ID == id {
				registry.devices[index].Status = "revoked"
			}
		}
	}
	prefix := strconv.FormatInt(owner, 10) + "/"
	for key, scope := range registry.scopes {
		if key == prefix+id || (id == "" && strings.HasPrefix(key, prefix)) {
			scope.cancel()
			delete(registry.scopes, key)
		}
	}
}

func applicationTestRequest(handler http.Handler, method, path, bearer, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func applicationTestKey(t *testing.T, handler http.Handler) string {
	t.Helper()
	response := applicationTestRequest(handler, "POST", "/api/settings/access-key/rotate", "", "")
	var result struct {
		Key string `json:"key"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Key == "" {
		t.Fatalf("rotate: %d %s", response.Code, response.Body.String())
	}
	return result.Key
}

func TestUserApplicationsIsolateBearerKeysAndRecords(t *testing.T) {
	registry := newApplicationTestRegistry()
	apps := newUserApplications(t.TempDir())
	apps.server = registry
	t.Cleanup(apps.Close)
	first := apps.Handler(registry.users[0])
	second := apps.Handler(registry.users[1])
	firstKey := applicationTestKey(t, first)
	secondKey := applicationTestKey(t, second)
	api, err := apps.application(1)
	if err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	api.jobs["private"] = &messageJob{ID: "private", Status: messageCompleted, Message: "owner one only", AIMessage: "owner one only", DeviceID: "m3", DeviceNodeID: "node-3", DeviceIP: "127.0.0.1", AccessKeyID: api.keys[0].ID, CompletedAt: time.Now()}
	api.mu.Unlock()
	response := applicationTestRequest(apps, "GET", "/api/v1/messages/private", firstKey, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "owner one only") {
		t.Fatalf("own job: %d %s", response.Code, response.Body.String())
	}
	response = applicationTestRequest(apps, "GET", "/api/v1/messages/private", secondKey, "")
	if response.Code != 404 || strings.Contains(response.Body.String(), "owner one only") {
		t.Fatalf("cross-user job: %d %s", response.Code, response.Body.String())
	}
	response = applicationTestRequest(second, "GET", "/api/automation/message-records", "", "")
	if response.Code != 200 || strings.Contains(response.Body.String(), "owner one only") {
		t.Fatalf("cross-user records: %d %s", response.Code, response.Body.String())
	}
	response = applicationTestRequest(second, "POST", "/api/automation/access-keys", "", `{"name":"cross binding","binding":{"device_id":"m3"}}`)
	if response.Code != 400 {
		t.Fatalf("cross-owner binding accepted: %d %s", response.Code, response.Body.String())
	}
	registry.revoke(1, "")
	response = applicationTestRequest(apps, "GET", "/api/v1/messages/private", firstKey, "")
	if response.Code != 401 {
		t.Fatalf("disabled owner's bearer accepted: %d", response.Code)
	}
	response = applicationTestRequest(first, "GET", "/api/automation/access-keys", "", "")
	if response.Code != 401 {
		t.Fatalf("cached handler accepted disabled owner: %d", response.Code)
	}
}

func TestUserApplicationsResolveGlobalDeviceIDsDynamically(t *testing.T) {
	registry := newApplicationTestRegistry()
	apps := newUserApplications(t.TempDir())
	apps.server = registry
	t.Cleanup(apps.Close)
	api, err := apps.application(1)
	if err != nil {
		t.Fatal(err)
	}
	device, problem := api.resolveDevice("m3")
	if problem != nil || device.ID != "m3" {
		t.Fatalf("global ID lost: %#v %v", device, problem)
	}
	for _, input := range []string{"m1", "m9", "Other Mac"} {
		if _, problem := api.resolveDevice(input); problem == nil {
			t.Fatalf("resolved unowned or renumbered %q", input)
		}
	}
	registry.mu.Lock()
	registry.devices[0].Name = "Renamed Mac"
	registry.devices = append(registry.devices, multiuser.Device{ID: "m12", Index: 12, UserID: 1, NodeID: "node-12", IP: "127.0.0.3", Name: "New Mac", Status: "active"})
	registry.mu.Unlock()
	device, problem = api.resolveDevice("renamed mac")
	if problem != nil || device.ID != "m3" {
		t.Fatalf("stale name: %#v %v", device, problem)
	}
	device, problem = api.resolveDevice("m12")
	if problem != nil || device.ID != "m12" {
		t.Fatalf("new device absent: %#v %v", device, problem)
	}
}

func TestUserApplicationsGuardEveryAgentRequest(t *testing.T) {
	for _, change := range []string{"revoke", "disable", "owner", "node", "ip", "missing-node"} {
		t.Run(change, func(t *testing.T) {
			registry := newApplicationTestRegistry()
			var calls atomic.Int64
			agent := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("X-Fleet-Device-ID") != "m3" || request.Header.Get("X-Fleet-Device-Token") != "test-secret-m3" {
					t.Error("automation missing device authorization")
				}
				calls.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				io.WriteString(writer, `{}`)
			}))
			defer agent.Close()
			apps := newUserApplications(t.TempDir())
			apps.server = registry
			t.Cleanup(apps.Close)
			api, err := apps.application(1)
			if err != nil {
				t.Fatal(err)
			}
			_, port, _ := net.SplitHostPort(strings.TrimPrefix(agent.URL, "http://"))
			api.agentPort, _ = strconv.Atoi(port)
			job := &messageJob{ID: "job", DeviceID: "m3", DeviceNodeID: "node-3", DeviceIP: "127.0.0.1", SessionID: "thread", Status: messageQueued, CreatedAt: time.Now()}
			if err := api.jobJSON(context.Background(), job, "GET", "chat/history", nil, nil); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("initial authorized request missing")
			}
			if change == "revoke" {
				registry.revoke(1, "m3")
			} else if change == "disable" {
				registry.revoke(1, "")
			} else {
				registry.mu.Lock()
				switch change {
				case "owner":
					registry.devices[0].UserID = 2
				case "node":
					registry.devices[0].NodeID = "replacement-node"
				case "ip":
					registry.devices[0].IP = "127.0.0.2"
				case "missing-node":
					job.DeviceNodeID = ""
				}
				registry.mu.Unlock()
			}
			if err := api.jobJSON(context.Background(), job, "POST", "chat/queue", nil, nil); err == nil {
				t.Fatal("unauthorized delivery accepted")
			}
			if _, err := api.pollAgentQueue(context.Background(), job, "queue"); err == nil {
				t.Fatal("unauthorized polling accepted")
			}
			if _, err := api.fetchHistory(context.Background(), job); err == nil {
				t.Fatal("unauthorized history accepted")
			}
			for range api.streamEvents(context.Background(), job) {
			}
			api.mu.Lock()
			api.maxConcurrent = 0
			api.jobs[job.ID] = job
			api.mu.Unlock()
			api.executeJob(job.ID)
			if calls.Load() != 1 {
				t.Fatalf("request reached stale device after %s: %d calls", change, calls.Load())
			}
		})
	}
}

func TestUserApplicationsRevokeCancelsActiveEvents(t *testing.T) {
	registry := newApplicationTestRegistry()
	started := make(chan struct{})
	stopped := make(chan struct{})
	agent := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(200)
		writer.(http.Flusher).Flush()
		close(started)
		<-request.Context().Done()
		close(stopped)
	}))
	defer agent.Close()
	apps := newUserApplications(t.TempDir())
	apps.server = registry
	t.Cleanup(apps.Close)
	api, err := apps.application(1)
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(agent.URL, "http://"))
	api.agentPort, _ = strconv.Atoi(port)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events := api.streamEvents(ctx, &messageJob{DeviceID: "m3", DeviceNodeID: "node-3", DeviceIP: "127.0.0.1"})
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("stream never started")
	}
	registry.revoke(1, "m3")
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("revoke did not cancel active HTTP")
	}
	select {
	case _, open := <-events:
		if open {
			t.Fatal("events stream still open")
		}
	case <-ctx.Done():
		t.Fatal("events goroutine did not exit")
	}
}

func TestUserApplicationsStartResumesAllOwnerStores(t *testing.T) {
	registry := newApplicationTestRegistry()
	dir := t.TempDir()
	for _, user := range registry.users {
		api, err := newMessageAPIAt(filepath.Join(dir, "users", strconv.FormatInt(user.ID, 10)))
		if err != nil {
			t.Fatal(err)
		}
		api.jobs["pending"] = &messageJob{ID: "pending", Status: messageQueued, DeviceID: "revoked", DeviceIP: "127.0.0.1", CreatedAt: time.Now()}
		if err := api.saveJobsLocked(); err != nil {
			t.Fatal(err)
		}
	}
	apps := newUserApplications(dir)
	apps.server = registry
	t.Cleanup(apps.Close)
	if err := apps.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, user := range registry.users {
		api, err := apps.application(user.ID)
		if err != nil {
			t.Fatal(err)
		}
		for {
			api.mu.Lock()
			status := api.jobs["pending"].Status
			api.mu.Unlock()
			if status == messageFailed {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("owner %d worker did not resume at startup", user.ID)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestUserApplicationsCallbackScopeCancelsOnDisable(t *testing.T) {
	registry := newApplicationTestRegistry()
	apps := newUserApplications(t.TempDir())
	apps.server = registry
	t.Cleanup(apps.Close)
	api, err := apps.application(1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := api.scopeCallback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	registry.revoke(1, "")
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("disabled owner did not cancel callback")
	}
	if _, cancel, err := api.scopeCallback(context.Background()); err == nil {
		cancel()
		t.Fatal("disabled owner can retry callback")
	}
	api.mu.Lock()
	api.jobs["callback"] = &messageJob{ID: "callback", Status: messageCompleted, CompletedAt: time.Now(), CallbackURL: "https://127.0.0.1/blocked", Callback: callbackState{Status: "delivered"}}
	api.mu.Unlock()
	api.deliverCallback("callback")
	api.mu.Lock()
	defer api.mu.Unlock()
	if !strings.Contains(api.jobs["callback"].Callback.LastError, "用户已禁用") {
		t.Fatalf("callback bypassed owner guard: %#v", api.jobs["callback"].Callback)
	}
}

func TestUserApplicationsHistorySurvivesDeviceChanges(t *testing.T) {
	for _, change := range []string{"revoke", "owner", "node", "ip", "missing-node", "removed"} {
		t.Run(change, func(t *testing.T) {
			registry := newApplicationTestRegistry()
			apps := newUserApplications(t.TempDir())
			apps.server = registry
			t.Cleanup(apps.Close)
			handler := apps.Handler(registry.users[0])
			key := applicationTestKey(t, handler)
			otherHandler := apps.Handler(registry.users[1])
			otherKey := applicationTestKey(t, otherHandler)
			api, err := apps.application(1)
			if err != nil {
				t.Fatal(err)
			}
			body := `{"device":"m3","ai_client":"codex","project":"demo","message":"hello"}`
			var parsed submitMessageRequest
			if err := json.Unmarshal([]byte(body), &parsed); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(parsed)
			api.mu.Lock()
			api.jobs["private"] = &messageJob{ID: "private", Status: messageCompleted, AIMessage: "revoked content", DeviceID: "m3", DeviceNodeID: "node-3", DeviceIP: "127.0.0.1", AccessKeyID: api.keys[0].ID, IdempotencyKey: "retry", RequestHash: hashString(string(raw)), CompletedAt: time.Now()}
			api.mu.Unlock()
			if change == "revoke" {
				registry.revoke(1, "m3")
			} else {
				registry.mu.Lock()
				switch change {
				case "owner":
					registry.devices[0].UserID = 2
				case "node":
					registry.devices[0].NodeID = "replacement-node"
				case "ip":
					registry.devices[0].IP = "127.0.0.3"
				case "missing-node":
					registry.devices[0].NodeID = ""
				case "removed":
					registry.devices = registry.devices[1:]
				}
				registry.mu.Unlock()
			}
			response := applicationTestRequest(apps, "GET", "/api/v1/messages/private", key, "")
			if response.Code != 200 || !strings.Contains(response.Body.String(), "revoked content") {
				t.Fatalf("owned public history: %d %s", response.Code, response.Body.String())
			}
			response = applicationTestRequest(handler, "GET", "/api/message-records", "", "")
			if response.Code != 200 || !strings.Contains(response.Body.String(), "revoked content") {
				t.Fatalf("owned private history: %d %s", response.Code, response.Body.String())
			}
			response = applicationTestRequest(apps, "GET", "/api/v1/messages/private", otherKey, "")
			if response.Code != 404 || strings.Contains(response.Body.String(), "revoked content") {
				t.Fatalf("cross-owner public history: %d %s", response.Code, response.Body.String())
			}
			response = applicationTestRequest(otherHandler, "GET", "/api/message-records", "", "")
			if response.Code != 200 || strings.Contains(response.Body.String(), "revoked content") {
				t.Fatalf("cross-owner private history: %d %s", response.Code, response.Body.String())
			}
			request := httptest.NewRequest("POST", "/api/v1/messages", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+key)
			request.Header.Set("Idempotency-Key", "retry")
			response = httptest.NewRecorder()
			apps.ServeHTTP(response, request)
			var result struct {
				ID string `json:"message_id"`
			}
			if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.ID != "private" {
				t.Fatalf("owned idempotent retry: %d %s", response.Code, response.Body.String())
			}
			api.mu.Lock()
			count := len(api.jobs)
			status := api.jobs["private"].Status
			api.mu.Unlock()
			if count != 1 || status != messageCompleted {
				t.Fatal("idempotent retry queued another device operation")
			}
			if change == "revoke" || change == "owner" || change == "missing-node" || change == "removed" {
				response = applicationTestRequest(apps, "POST", "/api/v1/messages", key, body)
				if response.Code != 404 {
					t.Fatalf("new operation on unavailable device: %d %s", response.Code, response.Body.String())
				}
			}
			registry.revoke(1, "")
			response = applicationTestRequest(apps, "GET", "/api/v1/messages/private", key, "")
			if response.Code != 401 {
				t.Fatalf("disabled owner history: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestUserApplicationsSubmitUsesScopedAgentAndPersistsNode(t *testing.T) {
	registry := newApplicationTestRegistry()
	agent := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		io.WriteString(writer, `{"sessions":[{"sessionId":"thread","cwd":"/work/demo","title":"Test"}]}`)
	}))
	defer agent.Close()
	apps := newUserApplications(t.TempDir())
	apps.server = registry
	t.Cleanup(apps.Close)
	key := applicationTestKey(t, apps.Handler(registry.users[0]))
	api, err := apps.application(1)
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(agent.URL, "http://"))
	api.agentPort, _ = strconv.Atoi(port)
	api.mu.Lock()
	api.maxConcurrent = 0
	api.mu.Unlock()
	response := applicationTestRequest(apps, "POST", "/api/v1/messages", key, `{"device":"m3","ai_client":"codex","project":"demo","message":"hello"}`)
	if response.Code != 202 {
		t.Fatalf("submit: %d %s", response.Code, response.Body.String())
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	for _, job := range api.jobs {
		if job.DeviceID != "m3" || job.DeviceNodeID != "node-3" {
			t.Fatalf("target identity not persisted: %#v", job)
		}
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("message_id")) {
		t.Fatal("missing message ID")
	}
}

func TestUserApplicationsRejectUnscopedRequestsAndAgentRedirects(t *testing.T) {
	registry := newApplicationTestRegistry()
	var redirected atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		redirected.Add(1)
		io.WriteString(writer, `{}`)
	}))
	defer other.Close()
	agent := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, other.URL+"/api/chat/history", http.StatusTemporaryRedirect)
	}))
	defer agent.Close()
	apps := newUserApplications(t.TempDir())
	apps.server = registry
	t.Cleanup(apps.Close)
	api, err := apps.application(1)
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(agent.URL, "http://"))
	api.agentPort, _ = strconv.Atoi(port)
	if err := api.agentJSON(context.Background(), "127.0.0.1", "GET", "chat/history", nil, nil); err == nil {
		t.Fatal("agent accepted request without an owner/device scope")
	}
	job := &messageJob{DeviceID: "m3", DeviceNodeID: "node-3", DeviceIP: "127.0.0.1"}
	if err := api.jobJSON(context.Background(), job, "GET", "chat/history", nil, nil); err == nil {
		t.Fatal("agent redirect was treated as a successful response")
	}
	if redirected.Load() != 0 {
		t.Fatal("agent redirect reached a different device")
	}
}

func TestUserApplicationsCloseCancelsActiveJSON(t *testing.T) {
	registry := newApplicationTestRegistry()
	started := make(chan struct{})
	agent := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer agent.Close()
	apps := newUserApplications(t.TempDir())
	apps.server = registry
	t.Cleanup(apps.Close)
	api, err := apps.application(1)
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(agent.URL, "http://"))
	api.agentPort, _ = strconv.Atoi(port)
	finished := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		finished <- api.jobJSON(ctx, &messageJob{DeviceID: "m3", DeviceNodeID: "node-3", DeviceIP: "127.0.0.1"}, "GET", "chat/history", nil, nil)
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("request never started")
	}
	apps.Close()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled request succeeded")
		}
	case <-ctx.Done():
		t.Fatal("Close did not cancel active HTTP")
	}
}

func TestUserApplicationsStartupDeliversPersistedJobsWithoutBrowser(t *testing.T) {
	registry := newApplicationTestRegistry()
	registry.devices[1].IP = "127.0.0.1"
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/start", func(writer http.ResponseWriter, request *http.Request) {
		var input struct {
			Cwd string `json:"cwd"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		writeJSON(writer, 200, map[string]string{"sessionId": filepath.Base(input.Cwd)})
	})
	mux.HandleFunc("/api/chat/queue", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, 200, agentQueueItem{ID: "queue", Status: "queued"})
	})
	mux.HandleFunc("/api/chat/events", func(writer http.ResponseWriter, request *http.Request) {
		messageID := "pending-" + request.URL.Query().Get("sessionId")
		writer.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []gatewayChatEvent{
			testGatewayEvent("user_done", "turn", "", map[string]string{"clientId": messageID}),
			testGatewayEvent("turn_started", "turn", "", nil),
			testGatewayEvent("assistant_done", "turn", "reply", map[string]string{"text": messageID}),
			testGatewayEvent("turn_done", "turn", "", map[string]string{"status": "completed"}),
		} {
			raw, _ := json.Marshal(event)
			io.WriteString(writer, "data: "+string(raw)+"\n\n")
		}
	})
	agent := httptest.NewServer(mux)
	defer agent.Close()
	dir := t.TempDir()
	for index, user := range registry.users {
		api, err := newMessageAPIAt(filepath.Join(dir, "users", strconv.FormatInt(user.ID, 10)))
		if err != nil {
			t.Fatal(err)
		}
		device := registry.devices[index]
		messageID := "pending-" + strconv.FormatInt(user.ID, 10)
		status := messageQueued
		if index == 1 {
			status = messageRunning
		}
		api.jobs[messageID] = &messageJob{ID: messageID, DeviceID: device.ID, DeviceNodeID: device.NodeID, DeviceIP: device.IP, Status: status, ProjectPath: "/work/" + strconv.FormatInt(user.ID, 10), CreatedAt: time.Now()}
		if err := api.saveJobsLocked(); err != nil {
			t.Fatal(err)
		}
	}
	apps := newUserApplications(dir)
	apps.server = registry
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(agent.URL, "http://"))
	apps.agentPort, _ = strconv.Atoi(port)
	t.Cleanup(apps.Close)
	if err := apps.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, user := range registry.users {
		api, err := apps.application(user.ID)
		if err != nil {
			t.Fatal(err)
		}
		messageID := "pending-" + strconv.FormatInt(user.ID, 10)
		for {
			api.mu.Lock()
			job := cloneMessageJob(api.jobs[messageID])
			api.mu.Unlock()
			if job.Status == messageCompleted && job.AIMessage == messageID {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("owner %d job did not resume: %#v", user.ID, job)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

type applicationIdleTransport struct{ closed bool }

func (transport *applicationIdleTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unused")
}

func (transport *applicationIdleTransport) CloseIdleConnections() { transport.closed = true }

func TestUserApplicationsTransportClosesIdleConnections(t *testing.T) {
	base := &applicationIdleTransport{}
	client := &http.Client{Transport: &applicationTransport{base: base}}
	client.CloseIdleConnections()
	if !base.closed {
		t.Fatal("scoped transport retained idle agent connections")
	}
}

func TestUserApplicationsLegacyMigrationPinsOnlyVerifiedIdentity(t *testing.T) {
	registry := newApplicationTestRegistry()
	registry.devices[0].IP = "100.64.0.3"
	apps := newUserApplications(t.TempDir())
	apps.server = registry
	t.Cleanup(apps.Close)
	raw := []byte(`{"version":1,"jobs":[{"message_id":"pending","status":"queued","device_id":"m3","device_ip":"100.64.0.3"}]}`)
	job := &messageJob{ID: "pending", Status: messageQueued, DeviceID: "m3", DeviceIP: "100.64.0.3"}
	if _, cancel, err := apps.jobContext(context.Background(), 1, job); err == nil {
		cancel()
		t.Fatal("runtime inferred a node identity from the stored IP")
	}
	verified := append([]multiuser.Device(nil), registry.devices[:1]...)
	migrated, err := pinLegacyMessageJobs(raw, 1, verified)
	if err != nil {
		t.Fatal(err)
	}
	var disk messageStoreDisk
	if err := json.Unmarshal(migrated, &disk); err != nil {
		t.Fatal(err)
	}
	job = disk.Jobs[0]
	if job.DeviceID != "m3" || job.DeviceIP != "100.64.0.3" || job.DeviceNodeID != "node-3" {
		t.Fatalf("migration changed the target rather than pinning its identity: %#v", job)
	}
	ctx, cancel, err := apps.jobContext(context.Background(), 1, job)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	registry.revoke(1, "m3")
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("migrated job ignored revoke")
	}
	if _, cancel, err := apps.jobContext(context.Background(), 1, job); err == nil {
		cancel()
		t.Fatal("migration bypassed current device authorization")
	}
}

func TestUserApplicationsLegacyMigrationRejectsUntrustedMapping(t *testing.T) {
	for _, change := range []string{"owner", "revoked", "missing-node", "wrong-index", "duplicate-node", "duplicate-ip"} {
		t.Run(change, func(t *testing.T) {
			device := multiuser.Device{ID: "m3", Index: 3, UserID: 1, NodeID: "node-3", IP: "100.64.0.3", Status: "active"}
			devices := []multiuser.Device{device}
			switch change {
			case "owner":
				devices[0].UserID = 2
			case "revoked":
				devices[0].Status = "revoked"
			case "missing-node":
				devices[0].NodeID = ""
			case "wrong-index":
				devices[0].Index = 1
			case "duplicate-node":
				device.ID, device.Index, device.IP = "m9", 9, "100.64.0.9"
				devices = append(devices, device)
			case "duplicate-ip":
				device.ID, device.Index, device.NodeID = "m9", 9, "node-9"
				devices = append(devices, device)
			}
			raw := []byte(`{"version":1,"jobs":[{"message_id":"pending","status":"queued","device_id":"m3","device_ip":"100.64.0.3"}]}`)
			if _, err := pinLegacyMessageJobs(raw, 1, devices); err == nil {
				t.Fatalf("accepted %s mapping", change)
			}
		})
	}
}
