package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	messageQueued       = "queued"
	messageRunning      = "running"
	messageFailed       = "failed"
	messageCompleted    = "completed"
	defaultAccessKeyRPM = 10
	maxAccessKeyRPM     = 10000
)

type accessKeyState struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	RPM        int               `json:"rpm"`
	Secret     string            `json:"key,omitempty"`
	Hash       string            `json:"hash"`
	Prefix     string            `json:"prefix"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at,omitempty"`
	LastUsedAt time.Time         `json:"last_used_at,omitempty"`
	Binding    *accessKeyBinding `json:"binding,omitempty"`
}

// A binding is a hierarchy of canonical target identifiers. Empty trailing fields
// mean the key may address any target beneath the last specified level.
type accessKeyBinding struct {
	DeviceID    string `json:"device_id"`
	AIClient    string `json:"ai_client,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
}

func (b *accessKeyBinding) allows(target resolvedTarget) bool {
	return b == nil || (b.DeviceID == target.DeviceID &&
		(b.AIClient == "" || b.AIClient == target.AIClient) &&
		(b.ProjectPath == "" || b.ProjectPath == target.ProjectPath) &&
		(b.SessionID == "" || b.SessionID == target.SessionID))
}

func (b *accessKeyBinding) allowsJob(job *messageJob) bool {
	return b.allows(resolvedTarget{DeviceID: job.DeviceID, AIClient: job.AIClient,
		ProjectPath: job.ProjectPath, SessionID: job.SessionID})
}

type accessKeyStoreDisk struct {
	Version int              `json:"version"`
	Keys    []accessKeyState `json:"keys"`
}

type messageError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type callbackState struct {
	Status        string    `json:"status,omitempty"`
	Attempts      int       `json:"attempts,omitempty"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	NextAttemptAt time.Time `json:"next_attempt_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

type messageJob struct {
	ID                 string        `json:"message_id"`
	Status             string        `json:"status"`
	DeviceID           string        `json:"device_id"`
	DeviceName         string        `json:"device_name"`
	DeviceIP           string        `json:"device_ip"`
	AIClient           string        `json:"ai_client"`
	Assistant          string        `json:"assistant"`
	ProjectName        string        `json:"project_name"`
	ProjectPath        string        `json:"project_path"`
	SessionInput       string        `json:"session_input,omitempty"`
	SessionID          string        `json:"session_id,omitempty"`
	SessionName        string        `json:"session_name,omitempty"`
	Message            string        `json:"message"`
	AIMessage          string        `json:"ai_message,omitempty"`
	CallbackURL        string        `json:"callback_url,omitempty"`
	CallbackSigningKey string        `json:"callback_signing_key,omitempty"`
	Callback           callbackState `json:"callback,omitempty"`
	Error              *messageError `json:"error,omitempty"`
	IdempotencyKey     string        `json:"idempotency_key,omitempty"`
	RequestHash        string        `json:"request_hash,omitempty"`
	TargetAlias        string        `json:"target_alias,omitempty"`
	AccessKeyID        string        `json:"access_key_id,omitempty"`
	AccessKeyName      string        `json:"access_key_name,omitempty"`
	AgentQueueID       string        `json:"agent_queue_id,omitempty"`
	TurnID             string        `json:"turn_id,omitempty"`
	CreatedAt          time.Time     `json:"created_at"`
	StartedAt          time.Time     `json:"started_at,omitempty"`
	CompletedAt        time.Time     `json:"completed_at,omitempty"`
	FailedAt           time.Time     `json:"failed_at,omitempty"`
}

type messageStoreDisk struct {
	Version int           `json:"version"`
	Jobs    []*messageJob `json:"jobs"`
}

type submitMessageRequest struct {
	Alias       string  `json:"alias,omitempty"`
	Device      string  `json:"device"`
	AIClient    string  `json:"ai_client"`
	Project     string  `json:"project"`
	Session     *string `json:"session,omitempty"`
	Message     string  `json:"message"`
	CallbackURL *string `json:"callback_url,omitempty"`
}

type targetSession struct {
	SessionID   string `json:"sessionId"`
	Cwd         string `json:"cwd"`
	Title       string `json:"title"`
	ProjectName string `json:"projectName"`
	ProjectCwd  string `json:"projectCwd"`
}

type resolvedTarget struct {
	DeviceID    string
	DeviceName  string
	DeviceIP    string
	AIClient    string
	Assistant   string
	ProjectName string
	ProjectPath string
	SessionID   string
	SessionName string
}

type apiProblem struct {
	Status  int
	Code    string
	Message string
	Details interface{}
}

func (p *apiProblem) Error() string { return p.Code + ": " + p.Message }

type messageAPI struct {
	mu             sync.Mutex
	keys           []accessKeyState
	rateLimits     map[string][]time.Time
	jobs           map[string]*messageJob
	keyFile        string
	jobsFile       string
	macIPs         []string
	agentPort      int
	client         *http.Client
	wake           chan struct{}
	activeExec     map[string]bool
	activeCallback map[string]bool
	maxConcurrent  int
	resolveTarget  func(context.Context, submitMessageRequest) (resolvedTarget, *apiProblem)
}

func newMessageAPIFromEnv() (*messageAPI, error) {
	macIPs := strings.Fields(strings.ReplaceAll(envOr("ENROLL_MAC_IPS", ""), ",", " "))
	api := &messageAPI{
		keyFile:        envOr("ENROLL_ACCESS_KEY_FILE", "/var/lib/fleet-enroll/access-key.json"),
		jobsFile:       envOr("ENROLL_MESSAGE_JOBS_FILE", "/var/lib/fleet-enroll/message-jobs.json"),
		macIPs:         macIPs,
		agentPort:      envInt("ENROLL_AGENT_PORT", 7682),
		client:         &http.Client{Timeout: 20 * time.Second},
		wake:           make(chan struct{}, 1),
		jobs:           map[string]*messageJob{},
		rateLimits:     map[string][]time.Time{},
		activeExec:     map[string]bool{},
		activeCallback: map[string]bool{},
		maxConcurrent:  envInt("ENROLL_MESSAGE_CONCURRENCY", 4),
	}
	if api.maxConcurrent < 1 {
		api.maxConcurrent = 1
	}
	api.resolveTarget = api.resolveMessageTarget
	if err := api.load(); err != nil {
		return nil, err
	}
	return api, nil
}

func (a *messageAPI) load() error {
	if raw, err := os.ReadFile(a.keyFile); err == nil {
		var disk accessKeyStoreDisk
		if err := json.Unmarshal(raw, &disk); err != nil {
			return fmt.Errorf("读取 access key: %w", err)
		}
		if disk.Keys != nil {
			a.keys = disk.Keys
		} else {
			// v1 只保存一个不可逆哈希。迁移后继续接受旧密钥，但无法展示原文。
			var legacy struct {
				Hash       string    `json:"hash"`
				Prefix     string    `json:"prefix"`
				CreatedAt  time.Time `json:"created_at"`
				LastUsedAt time.Time `json:"last_used_at"`
			}
			if err := json.Unmarshal(raw, &legacy); err != nil {
				return fmt.Errorf("读取旧版 access key: %w", err)
			}
			if legacy.Hash != "" {
				idSuffix := legacy.Hash
				if len(idSuffix) > 16 {
					idSuffix = idSuffix[:16]
				}
				a.keys = []accessKeyState{{
					ID: "key_legacy_" + idSuffix, Name: "默认密钥", Hash: legacy.Hash,
					Prefix: legacy.Prefix, CreatedAt: legacy.CreatedAt, LastUsedAt: legacy.LastUsedAt,
				}}
			}
			if err := a.saveKeysLocked(); err != nil {
				return fmt.Errorf("迁移 access key: %w", err)
			}
		}
		migrateRPM := false
		for i := range a.keys {
			if a.keys[i].RPM <= 0 {
				a.keys[i].RPM = defaultAccessKeyRPM
				migrateRPM = true
			}
			if a.keys[i].Name == "" {
				a.keys[i].Name = fmt.Sprintf("访问密钥 %d", i+1)
			}
			if a.keys[i].Hash == "" && a.keys[i].Secret != "" {
				a.keys[i].Hash = hashString(a.keys[i].Secret)
			}
		}
		// Persist defaults for existing keys and remove the former standalone alias store.
		var oldFields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &oldFields)
		_, hadAliases := oldFields["aliases"]
		if migrateRPM || hadAliases {
			if err := a.saveKeysLocked(); err != nil {
				return fmt.Errorf("迁移访问密钥配置: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if raw, err := os.ReadFile(a.jobsFile); err == nil {
		var disk messageStoreDisk
		if err := json.Unmarshal(raw, &disk); err != nil {
			return fmt.Errorf("读取 message jobs: %w", err)
		}
		for _, job := range disk.Jobs {
			if job == nil || job.ID == "" {
				continue
			}
			if job.Status == messageRunning {
				job.Status = messageQueued
				job.StartedAt = time.Time{}
			}
			a.jobs[job.ID] = job
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func writePrivateJSON(path string, value interface{}) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".fleet-state-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (a *messageAPI) saveKeysLocked() error {
	keys := append([]accessKeyState(nil), a.keys...)
	if keys == nil {
		keys = []accessKeyState{}
	}
	return writePrivateJSON(a.keyFile, accessKeyStoreDisk{Version: 3, Keys: keys})
}

func (a *messageAPI) saveJobsLocked() error {
	jobs := make([]*messageJob, 0, len(a.jobs))
	for _, job := range a.jobs {
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.Before(jobs[j].CreatedAt) })
	return writePrivateJSON(a.jobsFile, messageStoreDisk{Version: 1, Jobs: jobs})
}

func randomToken(prefix string, size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func accessKeyResponse(key accessKeyState) map[string]interface{} {
	return map[string]interface{}{
		"id": key.ID, "name": key.Name, "rpm": accessKeyRPM(key.RPM), "key": key.Secret, "recoverable": key.Secret != "",
		"prefix": key.Prefix, "created_at": omitZeroTime(key.CreatedAt),
		"updated_at": omitZeroTime(key.UpdatedAt), "last_used_at": omitZeroTime(key.LastUsedAt),
		"binding": key.Binding,
	}
}

func accessKeyRPM(value int) int {
	if value <= 0 {
		return defaultAccessKeyRPM
	}
	return value
}

func validateAccessKeyName(value string) (string, *apiProblem) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 80 {
		return "", &apiProblem{Status: 400, Code: "invalid_request", Message: "密钥名称不能为空且最多 80 个字符"}
	}
	return value, nil
}

func newAccessKey(name string) (accessKeyState, error) {
	secret, err := randomToken("mfh_live_", 32)
	if err != nil {
		return accessKeyState{}, err
	}
	id, err := randomToken("key_", 12)
	if err != nil {
		return accessKeyState{}, err
	}
	prefix := secret
	if len(prefix) > 17 {
		prefix = prefix[:17]
	}
	now := time.Now().UTC()
	return accessKeyState{
		ID: id, Name: name, RPM: defaultAccessKeyRPM, Secret: secret, Hash: hashString(secret), Prefix: prefix,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func decodeAccessKeyEdit(w http.ResponseWriter, r *http.Request, fallback string, fallbackRPM int) (string, int, *accessKeyBinding, bool, *apiProblem) {
	var body struct {
		Name    string          `json:"name"`
		RPM     *int            `json:"rpm"`
		Binding json.RawMessage `json:"binding"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		return "", 0, nil, false, &apiProblem{Status: 400, Code: "invalid_request", Message: "请求 JSON 格式或字段不正确"}
	}
	if err := dec.Decode(new(interface{})); !errors.Is(err, io.EOF) {
		return "", 0, nil, false, &apiProblem{Status: 400, Code: "invalid_request", Message: "请求只能包含一个 JSON 对象"}
	}
	if strings.TrimSpace(body.Name) == "" {
		body.Name = fallback
	}
	name, problem := validateAccessKeyName(body.Name)
	if problem != nil {
		return "", 0, nil, false, problem
	}
	rpm := accessKeyRPM(fallbackRPM)
	if body.RPM != nil {
		rpm = *body.RPM
		if rpm < 1 || rpm > maxAccessKeyRPM {
			return "", 0, nil, false, &apiProblem{Status: 400, Code: "invalid_rpm", Message: "RPM 须在 1 到 10000 之间"}
		}
	}
	if len(body.Binding) == 0 || string(body.Binding) == "null" {
		return name, rpm, nil, len(body.Binding) > 0, nil
	}
	var binding accessKeyBinding
	bindDec := json.NewDecoder(bytes.NewReader(body.Binding))
	bindDec.DisallowUnknownFields()
	if err := bindDec.Decode(&binding); err != nil || bindDec.Decode(new(interface{})) != io.EOF {
		return "", 0, nil, false, &apiProblem{Status: 400, Code: "invalid_binding", Message: "密钥绑定字段不正确"}
	}
	return name, rpm, &binding, true, nil
}

func (a *messageAPI) validateBinding(ctx context.Context, binding *accessKeyBinding) (*accessKeyBinding, *apiProblem) {
	if binding == nil {
		return nil, nil
	}
	if binding.DeviceID == "" || len(binding.DeviceID) > 128 ||
		(binding.AIClient == "" && (binding.ProjectPath != "" || binding.SessionID != "")) ||
		(binding.ProjectPath == "" && binding.SessionID != "") ||
		(binding.AIClient != "" && binding.AIClient != "codex" && binding.AIClient != "deepseek") ||
		(binding.ProjectPath != "" && (!filepath.IsAbs(binding.ProjectPath) || filepath.Clean(binding.ProjectPath) != binding.ProjectPath || len(binding.ProjectPath) > 4096)) ||
		len(binding.SessionID) > 256 {
		return nil, &apiProblem{Status: 400, Code: "invalid_binding", Message: "绑定须按设备、AI 客户端、项目绝对路径、会话 ID 逐级指定"}
	}
	device, problem := a.resolveDevice(binding.DeviceID)
	if problem != nil || device.ID != binding.DeviceID {
		return nil, &apiProblem{Status: 400, Code: "invalid_binding", Message: "绑定设备 ID 不存在"}
	}
	if binding.SessionID != "" {
		assistant := binding.AIClient
		if assistant == "deepseek" {
			assistant = "dsh"
		}
		sessions, problem := a.fetchSessions(ctx, device, assistant)
		if problem != nil {
			return nil, problem
		}
		found := false
		for _, session := range sessions {
			if session.SessionID == binding.SessionID && sessionProjectPath(session) == binding.ProjectPath {
				found = true
				break
			}
		}
		if !found {
			return nil, &apiProblem{Status: 400, Code: "invalid_binding", Message: "绑定会话 ID 不属于指定设备、客户端及项目"}
		}
	}
	copy := *binding
	return &copy, nil
}

func (a *messageAPI) handleAccessKeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	const base = "/automation/access-keys"
	if r.URL.Path == base {
		switch r.Method {
		case http.MethodGet:
			a.mu.Lock()
			keys := append([]accessKeyState(nil), a.keys...)
			a.mu.Unlock()
			sort.Slice(keys, func(i, j int) bool { return keys[i].CreatedAt.Before(keys[j].CreatedAt) })
			items := make([]map[string]interface{}, 0, len(keys))
			for _, key := range keys {
				items = append(items, accessKeyResponse(key))
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"keys": items})
		case http.MethodPost:
			a.mu.Lock()
			fallback := fmt.Sprintf("访问密钥 %d", len(a.keys)+1)
			a.mu.Unlock()
			name, rpm, requestedBinding, _, problem := decodeAccessKeyEdit(w, r, fallback, defaultAccessKeyRPM)
			if problem != nil {
				writeAPIProblem(w, problem)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			binding, problem := a.validateBinding(ctx, requestedBinding)
			cancel()
			if problem != nil {
				writeAPIProblem(w, problem)
				return
			}
			key, err := newAccessKey(name)
			if err != nil {
				writeAPIProblem(w, &apiProblem{Status: 500, Code: "key_generation_failed", Message: "生成访问密钥失败"})
				return
			}
			key.Binding = binding
			key.RPM = rpm
			a.mu.Lock()
			a.keys = append(a.keys, key)
			err = a.saveKeysLocked()
			if err != nil {
				a.keys = a.keys[:len(a.keys)-1]
			}
			a.mu.Unlock()
			if err != nil {
				writeAPIProblem(w, &apiProblem{Status: 500, Code: "key_save_failed", Message: "保存访问密钥失败"})
				return
			}
			writeJSON(w, http.StatusCreated, accessKeyResponse(key))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	id := strings.TrimPrefix(r.URL.Path, base+"/")
	if id == "" || strings.Contains(id, "/") {
		writeAPIProblem(w, &apiProblem{Status: 404, Code: "access_key_not_found", Message: "访问密钥不存在"})
		return
	}
	switch r.Method {
	case http.MethodPatch:
		a.mu.Lock()
		fallback := ""
		fallbackRPM := defaultAccessKeyRPM
		for _, key := range a.keys {
			if key.ID == id {
				fallback = key.Name
				fallbackRPM = accessKeyRPM(key.RPM)
				break
			}
		}
		a.mu.Unlock()
		if fallback == "" {
			writeAPIProblem(w, &apiProblem{Status: 404, Code: "access_key_not_found", Message: "访问密钥不存在"})
			return
		}
		name, rpm, requestedBinding, hasBinding, problem := decodeAccessKeyEdit(w, r, fallback, fallbackRPM)
		if problem != nil {
			writeAPIProblem(w, problem)
			return
		}
		var binding *accessKeyBinding
		if hasBinding {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			binding, problem = a.validateBinding(ctx, requestedBinding)
			cancel()
			if problem != nil {
				writeAPIProblem(w, problem)
				return
			}
		}
		a.mu.Lock()
		index := -1
		for i := range a.keys {
			if a.keys[i].ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			a.mu.Unlock()
			writeAPIProblem(w, &apiProblem{Status: 404, Code: "access_key_not_found", Message: "访问密钥不存在"})
			return
		}
		previous := a.keys[index]
		a.keys[index].Name = name
		a.keys[index].RPM = rpm
		if hasBinding {
			a.keys[index].Binding = binding
		}
		a.keys[index].UpdatedAt = time.Now().UTC()
		err := a.saveKeysLocked()
		if err != nil {
			a.keys[index] = previous
		} else if previous.Name != name {
			for _, job := range a.jobs {
				if job.AccessKeyID == id {
					job.AccessKeyName = name
				}
			}
			if jobsErr := a.saveJobsLocked(); jobsErr != nil {
				log.Printf("更新访问密钥的历史消息名称失败: %v", jobsErr)
			}
		}
		key := a.keys[index]
		a.mu.Unlock()
		if err != nil {
			writeAPIProblem(w, &apiProblem{Status: 500, Code: "key_save_failed", Message: "保存访问密钥失败"})
			return
		}
		writeJSON(w, http.StatusOK, accessKeyResponse(key))
	case http.MethodDelete:
		a.mu.Lock()
		index := -1
		for i := range a.keys {
			if a.keys[i].ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			a.mu.Unlock()
			writeAPIProblem(w, &apiProblem{Status: 404, Code: "access_key_not_found", Message: "访问密钥不存在"})
			return
		}
		previous := append([]accessKeyState(nil), a.keys...)
		a.keys = append(a.keys[:index], a.keys[index+1:]...)
		err := a.saveKeysLocked()
		if err != nil {
			a.keys = previous
		} else {
			delete(a.rateLimits, id)
		}
		a.mu.Unlock()
		if err != nil {
			writeAPIProblem(w, &apiProblem{Status: 500, Code: "key_revoke_failed", Message: "删除访问密钥失败"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// 兼容上一版管理页面；新页面使用 /automation/access-keys CRUD。
func (a *messageAPI) handleAccessKey(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/settings/access-key" && r.Method == http.MethodGet:
		a.mu.Lock()
		defer a.mu.Unlock()
		var key accessKeyState
		if len(a.keys) > 0 {
			key = a.keys[0]
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"enabled":      key.Hash != "",
			"prefix":       key.Prefix,
			"created_at":   omitZeroTime(key.CreatedAt),
			"last_used_at": omitZeroTime(key.LastUsedAt),
		})
	case r.URL.Path == "/settings/access-key/rotate" && r.Method == http.MethodPost:
		key, err := newAccessKey("默认密钥")
		if err != nil {
			writeAPIProblem(w, &apiProblem{Status: 500, Code: "key_generation_failed", Message: "生成访问密钥失败"})
			return
		}
		a.mu.Lock()
		previous := append([]accessKeyState(nil), a.keys...)
		if len(a.keys) == 0 {
			a.keys = append(a.keys, key)
		} else {
			key.ID, key.Name = a.keys[0].ID, a.keys[0].Name
			key.Binding = a.keys[0].Binding
			key.RPM = accessKeyRPM(a.keys[0].RPM)
			a.keys[0] = key
		}
		err = a.saveKeysLocked()
		if err != nil {
			a.keys = previous
		} else {
			delete(a.rateLimits, key.ID)
		}
		a.mu.Unlock()
		if err != nil {
			writeAPIProblem(w, &apiProblem{Status: 500, Code: "key_save_failed", Message: "保存访问密钥失败"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"key": key.Secret, "prefix": key.Prefix, "created_at": key.CreatedAt})
	case r.URL.Path == "/settings/access-key" && r.Method == http.MethodDelete:
		a.mu.Lock()
		previous := append([]accessKeyState(nil), a.keys...)
		if len(a.keys) > 0 {
			a.keys = a.keys[1:]
		}
		err := a.saveKeysLocked()
		if err != nil {
			a.keys = previous
		} else if len(previous) > 0 {
			delete(a.rateLimits, previous[0].ID)
		}
		a.mu.Unlock()
		if err != nil {
			writeAPIProblem(w, &apiProblem{Status: 500, Code: "key_revoke_failed", Message: "撤销访问密钥失败"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func omitZeroTime(value time.Time) interface{} {
	if value.IsZero() {
		return nil
	}
	return value
}

func (a *messageAPI) authenticate(r *http.Request) (accessKeyState, *apiProblem) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) < 8 || !strings.EqualFold(header[:7], "Bearer ") {
		return accessKeyState{}, &apiProblem{Status: 401, Code: "invalid_access_key", Message: "访问密钥无效或已撤销"}
	}
	provided := hashString(strings.TrimSpace(header[7:]))
	a.mu.Lock()
	defer a.mu.Unlock()
	index := -1
	for i := range a.keys {
		if a.keys[i].Hash != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(a.keys[i].Hash)) == 1 {
			index = i
		}
	}
	if index < 0 {
		return accessKeyState{}, &apiProblem{Status: 401, Code: "invalid_access_key", Message: "访问密钥无效或已撤销"}
	}
	now := time.Now().UTC()
	if a.keys[index].LastUsedAt.IsZero() || now.Sub(a.keys[index].LastUsedAt) >= time.Minute {
		a.keys[index].LastUsedAt = now
		if err := a.saveKeysLocked(); err != nil {
			log.Printf("更新 access key 最近使用时间失败: %v", err)
		}
	}
	return a.keys[index], nil
}

// Every authenticated public API request consumes one slot in its key's rolling minute.
// Check the current key again so edits and revocations take effect immediately.
func (a *messageAPI) checkRateLimit(w http.ResponseWriter, key accessKeyState, now time.Time) *apiProblem {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, active := a.currentKeyLocked(key)
	if !active {
		return &apiProblem{Status: http.StatusUnauthorized, Code: "invalid_access_key", Message: "访问密钥无效或已撤销"}
	}
	if a.rateLimits == nil {
		a.rateLimits = map[string][]time.Time{}
	}
	cutoff := now.Add(-time.Minute)
	requests := a.rateLimits[key.ID]
	first := 0
	for first < len(requests) && !requests[first].After(cutoff) {
		first++
	}
	requests = requests[first:]
	if len(requests) >= accessKeyRPM(current.RPM) {
		a.rateLimits[key.ID] = requests
		wait := requests[0].Add(time.Minute).Sub(now)
		seconds := int((wait + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		return &apiProblem{Status: http.StatusTooManyRequests, Code: "rate_limit_exceeded", Message: "访问密钥每分钟请求次数已达上限"}
	}
	a.rateLimits[key.ID] = append(requests, now)
	return nil
}

func scopeMismatch() *apiProblem {
	return &apiProblem{Status: 403, Code: "access_key_scope_mismatch", Message: "访问密钥未获授权访问该目标"}
}

func (a *messageAPI) preflightBinding(binding *accessKeyBinding, req submitMessageRequest) bool {
	if binding == nil {
		return true
	}
	device, problem := a.resolveDevice(req.Device)
	if problem != nil || device.ID != binding.DeviceID ||
		(binding.AIClient != "" && req.AIClient != binding.AIClient) ||
		(binding.SessionID != "" && (req.Session == nil || *req.Session == "")) {
		return false
	}
	return binding.ProjectPath == "" || !filepath.IsAbs(req.Project) ||
		strings.EqualFold(filepath.Clean(req.Project), binding.ProjectPath)
}

// Re-check the current key under the same lock used to persist or return a job.
// A revoked, rotated, or narrowed key must not retain its old permissions in flight.
func (a *messageAPI) currentKeyLocked(key accessKeyState) (accessKeyState, bool) {
	for _, current := range a.keys {
		if current.ID == key.ID && current.Hash == key.Hash {
			return current, true
		}
	}
	return accessKeyState{}, false
}

func writeAPIProblem(w http.ResponseWriter, problem *apiProblem) {
	requestID, _ := randomToken("req_", 9)
	errorBody := map[string]interface{}{
		"code": problem.Code, "message": problem.Message, "request_id": requestID,
	}
	if problem.Details != nil {
		errorBody["details"] = problem.Details
	}
	writeJSON(w, problem.Status, map[string]interface{}{"error": errorBody})
}

func (a *messageAPI) handleMessages(w http.ResponseWriter, r *http.Request) {
	key, problem := a.authenticate(r)
	if problem != nil {
		writeAPIProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if problem := a.checkRateLimit(w, key, time.Now().UTC()); problem != nil {
		writeAPIProblem(w, problem)
		return
	}
	if r.URL.Path == "/v1/messages" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		a.submitMessage(w, r, key)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/messages/")
	if id == "" || strings.Contains(id, "/") {
		writeAPIProblem(w, &apiProblem{Status: 404, Code: "message_not_found", Message: "message_id 不存在"})
		return
	}
	a.mu.Lock()
	job := cloneMessageJob(a.jobs[id])
	current, active := a.currentKeyLocked(key)
	a.mu.Unlock()
	if !active || job == nil || job.AccessKeyID != key.ID || !current.Binding.allowsJob(job) {
		writeAPIProblem(w, &apiProblem{Status: 404, Code: "message_not_found", Message: "message_id 不存在或已过保留期"})
		return
	}
	writeJSON(w, http.StatusOK, publicMessage(job, false))
}

func (a *messageAPI) submitMessage(w http.ResponseWriter, r *http.Request, key accessKeyState) {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "application/json" {
		writeAPIProblem(w, &apiProblem{Status: 415, Code: "unsupported_media_type", Message: "Content-Type 必须是 application/json"})
		return
	}
	requestBody, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil {
		writeAPIProblem(w, &apiProblem{Status: 400, Code: "invalid_request", Message: "请求体过大或无法读取"})
		return
	}
	var req submitMessageRequest
	dec := json.NewDecoder(bytes.NewReader(requestBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeAPIProblem(w, &apiProblem{Status: 400, Code: "invalid_request", Message: "请求 JSON 格式或字段不正确"})
		return
	}
	if err := dec.Decode(new(interface{})); !errors.Is(err, io.EOF) {
		writeAPIProblem(w, &apiProblem{Status: 400, Code: "invalid_request", Message: "请求只能包含一个 JSON 对象"})
		return
	}
	// Presence matters: an alias and even an empty explicit target cannot be combined.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(requestBody, &fields); err != nil {
		writeAPIProblem(w, &apiProblem{Status: 400, Code: "invalid_request", Message: "请求 JSON 格式不正确"})
		return
	}
	req.Alias = strings.TrimSpace(req.Alias)
	if hasField(fields, "alias") {
		if req.Alias == "" || hasExplicitTarget(fields) {
			writeAPIProblem(w, &apiProblem{Status: 400, Code: "invalid_target", Message: "alias 与设备、客户端、项目、会话目标只能二选一"})
			return
		}
	}
	if req.Alias == "" && (req.Device == "" || req.Project == "") {
		writeAPIProblem(w, &apiProblem{Status: 400, Code: "invalid_request", Message: "device 和 project 为必填字段"})
		return
	}
	if req.Alias != "" {
		a.mu.Lock()
		binding, problem := a.keyAliasTargetLocked(key, req.Alias)
		a.mu.Unlock()
		if problem != nil {
			writeAPIProblem(w, problem)
			return
		}
		req.Device = binding.DeviceID
		req.AIClient = binding.AIClient
		req.Project = binding.ProjectPath
		req.Session = &binding.SessionID
	}
	req.Device = strings.TrimSpace(req.Device)
	req.AIClient = strings.ToLower(strings.TrimSpace(req.AIClient))
	req.Project = strings.TrimSpace(req.Project)
	req.Message = strings.TrimSpace(req.Message)
	if req.Session != nil {
		value := strings.TrimSpace(*req.Session)
		req.Session = &value
	}
	if req.CallbackURL != nil {
		value := strings.TrimSpace(*req.CallbackURL)
		req.CallbackURL = &value
	}
	if req.Device == "" || len(req.Device) > 128 || req.Project == "" || len(req.Project) > 4096 || req.Message == "" || len(req.Message) > 200<<10 {
		writeAPIProblem(w, &apiProblem{Status: 400, Code: "invalid_request", Message: "device、project 和非空 message 为必填字段"})
		return
	}
	if req.AIClient != "codex" && req.AIClient != "deepseek" {
		writeAPIProblem(w, &apiProblem{Status: 400, Code: "invalid_request", Message: "ai_client 只能是 codex 或 deepseek"})
		return
	}
	if req.CallbackURL != nil && *req.CallbackURL != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		problem := validatePublicCallbackURL(ctx, *req.CallbackURL)
		cancel()
		if problem != nil {
			writeAPIProblem(w, problem)
			return
		}
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !validIdempotencyKey(idempotencyKey) {
		writeAPIProblem(w, &apiProblem{Status: 400, Code: "invalid_request", Message: "Idempotency-Key 不合法"})
		return
	}
	requestJSON, _ := json.Marshal(req)
	requestHash := hashString(string(requestJSON))
	a.mu.Lock()
	current, active := a.currentKeyLocked(key)
	a.mu.Unlock()
	if !active || !a.preflightBinding(current.Binding, req) {
		writeAPIProblem(w, scopeMismatch())
		return
	}
	if idempotencyKey != "" {
		a.mu.Lock()
		for _, existing := range a.jobs {
			if existing.AccessKeyID != key.ID || existing.IdempotencyKey != idempotencyKey {
				continue
			}
			if existing.RequestHash != requestHash {
				a.mu.Unlock()
				writeAPIProblem(w, &apiProblem{Status: 409, Code: "idempotency_conflict", Message: "相同 Idempotency-Key 对应了不同请求"})
				return
			}
			current, active := a.currentKeyLocked(key)
			if !active || !a.preflightBinding(current.Binding, req) || !current.Binding.allowsJob(existing) || !a.aliasMatchesJobLocked(key, req.Alias, existing) {
				a.mu.Unlock()
				writeAPIProblem(w, scopeMismatch())
				return
			}
			id := existing.ID
			a.mu.Unlock()
			w.Header().Set("Location", "/api/v1/messages/"+id)
			writeJSON(w, http.StatusAccepted, map[string]string{"message_id": id})
			return
		}
		a.mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	target, problem := a.resolveTarget(ctx, req)
	cancel()
	if problem != nil {
		writeAPIProblem(w, problem)
		return
	}
	a.mu.Lock()
	current, active = a.currentKeyLocked(key)
	a.mu.Unlock()
	if !active || !a.preflightBinding(current.Binding, req) || !current.Binding.allows(target) {
		writeAPIProblem(w, scopeMismatch())
		return
	}
	id, err := randomToken("msg_", 16)
	if err != nil {
		writeAPIProblem(w, &apiProblem{Status: 500, Code: "internal_error", Message: "生成 message_id 失败"})
		return
	}
	now := time.Now().UTC()
	job := &messageJob{
		ID: id, Status: messageQueued,
		DeviceID: target.DeviceID, DeviceName: target.DeviceName, DeviceIP: target.DeviceIP,
		AIClient: target.AIClient, Assistant: target.Assistant,
		ProjectName: target.ProjectName, ProjectPath: target.ProjectPath,
		SessionID: target.SessionID, SessionName: target.SessionName,
		Message: req.Message, IdempotencyKey: idempotencyKey, RequestHash: requestHash, CreatedAt: now,
		AccessKeyID: key.ID, AccessKeyName: key.Name, CallbackSigningKey: key.Hash, TargetAlias: req.Alias,
	}
	if req.Session != nil {
		job.SessionInput = *req.Session
	}
	if req.CallbackURL != nil {
		job.CallbackURL = *req.CallbackURL
		if job.CallbackURL != "" {
			job.Callback.Status = "pending"
		}
	}
	a.mu.Lock()
	current, active = a.currentKeyLocked(key)
	if !active || !a.preflightBinding(current.Binding, req) || !current.Binding.allows(target) || !a.aliasMatchesJobLocked(key, req.Alias, job) {
		a.mu.Unlock()
		writeAPIProblem(w, scopeMismatch())
		return
	}
	a.jobs[id] = job
	err = a.saveJobsLocked()
	if err != nil {
		delete(a.jobs, id)
	}
	a.mu.Unlock()
	if err != nil {
		writeAPIProblem(w, &apiProblem{Status: 500, Code: "internal_error", Message: "持久化消息失败"})
		return
	}
	a.signal()
	w.Header().Set("Location", "/api/v1/messages/"+id)
	writeJSON(w, http.StatusAccepted, map[string]string{"message_id": id})
}

func validIdempotencyKey(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func (a *messageAPI) handleMessageRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 100
	if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed > 0 && parsed <= 500 {
		limit = parsed
	}
	accessKeyID := strings.TrimSpace(r.URL.Query().Get("access_key_id"))
	a.mu.Lock()
	jobs := make([]*messageJob, 0, len(a.jobs))
	recordKeyNames := map[string]string{}
	hasLegacy := false
	for _, job := range a.jobs {
		if job.AccessKeyID == "" {
			hasLegacy = true
		} else {
			recordKeyNames[job.AccessKeyID] = job.AccessKeyName
		}
		if accessKeyID == "__legacy__" && job.AccessKeyID != "" {
			continue
		}
		if accessKeyID != "" && accessKeyID != "__legacy__" && job.AccessKeyID != accessKeyID {
			continue
		}
		jobs = append(jobs, cloneMessageJob(job))
	}
	a.mu.Unlock()
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	items := make([]map[string]interface{}, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, publicMessage(job, true))
	}
	recordKeys := make([]map[string]string, 0, len(recordKeyNames)+1)
	for id, name := range recordKeyNames {
		recordKeys = append(recordKeys, map[string]string{"id": id, "name": name})
	}
	sort.Slice(recordKeys, func(i, j int) bool { return recordKeys[i]["name"] < recordKeys[j]["name"] })
	if hasLegacy {
		recordKeys = append(recordKeys, map[string]string{"id": "__legacy__", "name": "旧记录（未标记密钥）"})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"messages": items, "access_keys": recordKeys})
}

func publicMessage(job *messageJob, admin bool) map[string]interface{} {
	result := map[string]interface{}{
		"message_id": job.ID, "status": job.Status,
		"created_at": job.CreatedAt,
	}
	if job.TargetAlias != "" {
		result["alias"] = job.TargetAlias
	}
	if job.TargetAlias == "" || admin {
		result["device"] = map[string]string{"id": job.DeviceID, "name": job.DeviceName}
		result["ai_client"] = job.AIClient
		result["project"] = job.ProjectPath
		result["project_name"] = job.ProjectName
		if job.SessionID != "" {
			result["session_id"] = job.SessionID
		} else {
			result["session_id"] = nil
		}
		if job.SessionName != "" {
			result["session_name"] = job.SessionName
		}
	}
	if !job.StartedAt.IsZero() {
		result["started_at"] = job.StartedAt
	}
	if job.Status == messageCompleted {
		result["ai_message"] = map[string]string{"role": "assistant", "content": job.AIMessage, "format": "markdown"}
		result["completed_at"] = job.CompletedAt
	}
	if job.Status == messageFailed {
		if job.TargetAlias != "" && !admin && job.Error != nil {
			result["error"] = &messageError{Code: job.Error.Code, Message: "执行失败；请联系管理员查看详情", Retryable: job.Error.Retryable}
		} else {
			result["error"] = job.Error
		}
		result["failed_at"] = job.FailedAt
	}
	if job.CallbackURL != "" {
		result["callback"] = map[string]interface{}{
			"status": job.Callback.Status, "attempts": job.Callback.Attempts,
			"last_attempt_at": omitZeroTime(job.Callback.LastAttemptAt),
		}
	}
	if admin {
		result["message"] = job.Message
		result["callback_url"] = job.CallbackURL
		result["turn_id"] = job.TurnID
		result["access_key"] = map[string]string{"id": job.AccessKeyID, "name": job.AccessKeyName}
	}
	return result
}

func cloneMessageJob(job *messageJob) *messageJob {
	if job == nil {
		return nil
	}
	copy := *job
	if job.Error != nil {
		errCopy := *job.Error
		copy.Error = &errCopy
	}
	return &copy
}

func (a *messageAPI) resolveMessageTarget(ctx context.Context, req submitMessageRequest) (resolvedTarget, *apiProblem) {
	device, problem := a.resolveDevice(req.Device)
	if problem != nil {
		return resolvedTarget{}, problem
	}
	assistant := req.AIClient
	if assistant == "deepseek" {
		assistant = "dsh"
	}
	sessions, problem := a.fetchSessions(ctx, device, assistant)
	if problem != nil {
		return resolvedTarget{}, problem
	}
	projectName, projectPath, problem := resolveProject(req.Project, sessions)
	if problem != nil {
		return resolvedTarget{}, problem
	}
	sessionID, sessionName := "", ""
	if req.Session != nil && *req.Session != "" {
		sessionID, sessionName, problem = resolveSession(*req.Session, projectPath, sessions)
		if problem != nil {
			return resolvedTarget{}, problem
		}
	}
	return resolvedTarget{
		DeviceID: device.ID, DeviceName: device.Name, DeviceIP: device.IP,
		AIClient: req.AIClient, Assistant: assistant,
		ProjectName: projectName, ProjectPath: projectPath, SessionID: sessionID, SessionName: sessionName,
	}, nil
}

type resolvedDevice struct{ ID, Name, IP string }

func (a *messageAPI) resolveDevice(input string) (resolvedDevice, *apiProblem) {
	names := loadNames()
	devices := make([]resolvedDevice, 0, len(a.macIPs))
	for i, ip := range a.macIPs {
		id := fmt.Sprintf("m%d", i+1)
		name := strings.TrimSpace(names[id])
		if name == "" {
			name = fmt.Sprintf("Mac %d", i+1)
		}
		devices = append(devices, resolvedDevice{ID: id, Name: name, IP: ip})
	}
	var byName []resolvedDevice
	for _, device := range devices {
		if strings.EqualFold(device.Name, input) {
			byName = append(byName, device)
		}
	}
	if len(byName) == 1 {
		return byName[0], nil
	}
	if len(byName) > 1 {
		candidates := make([]map[string]string, 0, len(byName))
		for _, item := range byName {
			candidates = append(candidates, map[string]string{"id": item.ID, "name": item.Name})
		}
		return resolvedDevice{}, &apiProblem{Status: 409, Code: "ambiguous_device", Message: "设备显示名称不唯一，请改用设备 ID", Details: map[string]interface{}{"candidates": candidates}}
	}
	for _, device := range devices {
		if strings.EqualFold(device.ID, input) {
			return device, nil
		}
	}
	return resolvedDevice{}, &apiProblem{Status: 404, Code: "device_not_found", Message: "找不到指定设备"}
}

func sessionProjectPath(session targetSession) string {
	if strings.TrimSpace(session.ProjectCwd) != "" {
		return filepath.Clean(session.ProjectCwd)
	}
	return filepath.Clean(session.Cwd)
}

func sessionProjectName(session targetSession) string {
	if strings.TrimSpace(session.ProjectName) != "" {
		return strings.TrimSpace(session.ProjectName)
	}
	return filepath.Base(sessionProjectPath(session))
}

func resolveProject(input string, sessions []targetSession) (string, string, *apiProblem) {
	input = strings.TrimSpace(input)
	type project struct{ Name, Path string }
	projects := map[string]project{}
	for _, session := range sessions {
		path := sessionProjectPath(session)
		if path == "." || path == "" {
			continue
		}
		key := strings.ToLower(path)
		if _, ok := projects[key]; !ok {
			projects[key] = project{Name: sessionProjectName(session), Path: path}
		}
	}
	if filepath.IsAbs(input) {
		for _, item := range projects {
			if strings.EqualFold(item.Path, filepath.Clean(input)) {
				return item.Name, item.Path, nil
			}
		}
		return filepath.Base(filepath.Clean(input)), filepath.Clean(input), nil
	}
	var matches []project
	for _, item := range projects {
		if strings.EqualFold(item.Name, input) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return "", "", &apiProblem{Status: 404, Code: "project_not_found", Message: "找不到指定项目；可改用项目绝对路径"}
	}
	if len(matches) > 1 {
		sort.Slice(matches, func(i, j int) bool { return matches[i].Path < matches[j].Path })
		candidates := make([]map[string]string, 0, len(matches))
		for _, item := range matches {
			candidates = append(candidates, map[string]string{"name": item.Name, "path": item.Path})
		}
		return "", "", &apiProblem{Status: 409, Code: "ambiguous_project", Message: "项目名称不唯一，请改用项目路径", Details: map[string]interface{}{"candidates": candidates}}
	}
	return matches[0].Name, matches[0].Path, nil
}

func resolveSession(input, projectPath string, sessions []targetSession) (string, string, *apiProblem) {
	input = strings.TrimSpace(input)
	var inProject []targetSession
	for _, session := range sessions {
		if strings.EqualFold(session.SessionID, input) {
			if !strings.EqualFold(sessionProjectPath(session), projectPath) {
				return "", "", &apiProblem{Status: 409, Code: "session_project_mismatch", Message: "指定会话不属于该项目"}
			}
			return session.SessionID, session.Title, nil
		}
		if strings.EqualFold(sessionProjectPath(session), projectPath) {
			inProject = append(inProject, session)
		}
	}
	var matches []targetSession
	for _, session := range inProject {
		if strings.EqualFold(strings.TrimSpace(session.Title), input) {
			matches = append(matches, session)
		}
	}
	if len(matches) == 0 {
		return "", "", &apiProblem{Status: 404, Code: "session_not_found", Message: "在指定项目中找不到该会话名称或 ID"}
	}
	if len(matches) > 1 {
		candidates := make([]map[string]string, 0, len(matches))
		for _, item := range matches {
			candidates = append(candidates, map[string]string{"name": item.Title, "id": item.SessionID})
		}
		return "", "", &apiProblem{Status: 409, Code: "ambiguous_session", Message: "会话名称不唯一，请改用会话 ID", Details: map[string]interface{}{"candidates": candidates}}
	}
	return matches[0].SessionID, matches[0].Title, nil
}

func (a *messageAPI) fetchSessions(ctx context.Context, device resolvedDevice, assistant string) ([]targetSession, *apiProblem) {
	var all []targetSession
	cursor := ""
	for page := 0; page < 100; page++ {
		query := url.Values{"assistant": {assistant}, "limit": {"100"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var response struct {
			Sessions   []targetSession `json:"sessions"`
			NextCursor string          `json:"nextCursor"`
		}
		if err := a.agentJSON(ctx, device.IP, http.MethodGet, "sessions?"+query.Encode(), nil, &response); err != nil {
			return nil, &apiProblem{Status: 503, Code: "device_unavailable", Message: "目标设备不可达或 AI 客户端不可用"}
		}
		all = append(all, response.Sessions...)
		if response.NextCursor == "" || response.NextCursor == cursor || assistant != "codex" {
			break
		}
		cursor = response.NextCursor
	}
	return all, nil
}

func (a *messageAPI) agentJSON(ctx context.Context, ip, method, path string, input, output interface{}) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(encoded))
	}
	endpoint := fmt.Sprintf("http://%s:%d/api/%s", ip, a.agentPort, path)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &apiErr)
		message := apiErr.Message
		if message == "" {
			message = apiErr.Error
		}
		if message == "" {
			message = strings.TrimSpace(string(data))
		}
		return fmt.Errorf("agent HTTP %d: %s", resp.StatusCode, message)
	}
	if output != nil && len(data) > 0 {
		return json.Unmarshal(data, output)
	}
	return nil
}

func validatePublicCallbackURL(ctx context.Context, raw string) *apiProblem {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return &apiProblem{Status: 400, Code: "invalid_callback_url", Message: "callback_url 必须是无用户信息和片段的公网 HTTPS URL"}
	}
	port := parsed.Port()
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return &apiProblem{Status: 400, Code: "invalid_callback_url", Message: "callback_url 端口不合法"}
		}
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return &apiProblem{Status: 400, Code: "invalid_callback_url", Message: "callback_url 域名无法解析"}
	}
	for _, address := range addresses {
		if !publicCallbackIP(address.IP) {
			return &apiProblem{Status: 400, Code: "invalid_callback_url", Message: "callback_url 不得解析到内网、mesh、loopback 或保留地址"}
		}
	}
	return nil
}

func publicCallbackIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// CGNAT 100.64.0.0/10 (also Fleet mesh) and documentation/benchmark ranges.
		if v4[0] == 100 && v4[1]&0xc0 == 0x40 {
			return false
		}
		if v4[0] == 192 && v4[1] == 0 && v4[2] == 2 || v4[0] == 198 && v4[1] == 51 && v4[2] == 100 || v4[0] == 203 && v4[1] == 0 && v4[2] == 113 {
			return false
		}
	}
	return ip.IsGlobalUnicast()
}

func (a *messageAPI) signal() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}
