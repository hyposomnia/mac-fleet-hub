package main

import (
	"container/heap"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultRecordPageSize = 25
const maxRecordPageSize = 100

type messageRecordField struct {
	Name      string   `json:"name"`
	Label     string   `json:"label"`
	Type      string   `json:"type"`
	Options   []string `json:"options,omitempty"`
	Operators []string `json:"operators"`
	read      func(*messageJob) string
}

func recordTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func callbackRecordValue(j *messageJob, value string) string {
	if j.CallbackURL == "" {
		return ""
	}
	return value
}

var messageRecordFields = []messageRecordField{
	{Name: "message_id", Label: "消息 ID", Type: "string", read: func(j *messageJob) string { return j.ID }},
	{Name: "status", Label: "消息状态", Type: "enum", Options: []string{messageQueued, messageRunning, messageCompleted, messageFailed}, read: func(j *messageJob) string { return j.Status }},
	{Name: "device_id", Label: "设备 ID", Type: "string", read: func(j *messageJob) string { return j.DeviceID }},
	{Name: "device_name", Label: "设备名称", Type: "string", read: func(j *messageJob) string { return j.DeviceName }},
	{Name: "ai_client", Label: "AI 客户端", Type: "enum", Options: []string{"codex", "deepseek"}, read: func(j *messageJob) string { return j.AIClient }},
	{Name: "project", Label: "项目路径", Type: "string", read: func(j *messageJob) string { return j.ProjectPath }},
	{Name: "project_name", Label: "项目名称", Type: "string", read: func(j *messageJob) string { return j.ProjectName }},
	{Name: "session_id", Label: "会话 ID", Type: "string", read: func(j *messageJob) string { return j.SessionID }},
	{Name: "session_name", Label: "会话名称", Type: "string", read: func(j *messageJob) string { return j.SessionName }},
	{Name: "alias", Label: "调用别名", Type: "string", read: func(j *messageJob) string { return j.TargetAlias }},
	{Name: "access_key_id", Label: "访问密钥 ID", Type: "string", read: func(j *messageJob) string { return j.AccessKeyID }},
	{Name: "access_key_name", Label: "访问密钥名称", Type: "string", read: func(j *messageJob) string { return j.AccessKeyName }},
	{Name: "turn_id", Label: "Turn ID", Type: "string", read: func(j *messageJob) string { return j.TurnID }},
	{Name: "agent_queue_id", Label: "投递队列 ID", Type: "string", read: func(j *messageJob) string { return j.AgentQueueID }},
	{Name: "idempotency_key", Label: "幂等键", Type: "string", read: func(j *messageJob) string { return j.IdempotencyKey }},
	{Name: "created_at", Label: "创建时间", Type: "time", read: func(j *messageJob) string { return recordTime(j.CreatedAt) }},
	{Name: "started_at", Label: "开始时间", Type: "time", read: func(j *messageJob) string { return recordTime(j.StartedAt) }},
	{Name: "completed_at", Label: "完成时间", Type: "time", read: func(j *messageJob) string { return recordTime(j.CompletedAt) }},
	{Name: "failed_at", Label: "失败时间", Type: "time", read: func(j *messageJob) string { return recordTime(j.FailedAt) }},
	{Name: "callback_url", Label: "回调地址", Type: "string", read: func(j *messageJob) string { return j.CallbackURL }},
	{Name: "callback_status", Label: "回调状态", Type: "enum", Options: []string{"pending", "retrying", "delivered", "delivery_failed"}, read: func(j *messageJob) string { return callbackRecordValue(j, j.Callback.Status) }},
	{Name: "callback_attempts", Label: "回调尝试次数", Type: "number", read: func(j *messageJob) string { return callbackRecordValue(j, strconv.Itoa(j.Callback.Attempts)) }},
	{Name: "callback_last_attempt_at", Label: "回调最近尝试时间", Type: "time", read: func(j *messageJob) string { return callbackRecordValue(j, recordTime(j.Callback.LastAttemptAt)) }},
	{Name: "callback_next_attempt_at", Label: "回调下次尝试时间", Type: "time", read: func(j *messageJob) string { return callbackRecordValue(j, recordTime(j.Callback.NextAttemptAt)) }},
	{Name: "callback_last_error", Label: "回调最近错误", Type: "string", read: func(j *messageJob) string { return callbackRecordValue(j, j.Callback.LastError) }},
	{Name: "error_code", Label: "错误码", Type: "string", read: func(j *messageJob) string {
		if j.Error == nil {
			return ""
		}
		return j.Error.Code
	}},
	{Name: "error_message", Label: "错误说明", Type: "string", read: func(j *messageJob) string {
		if j.Error == nil {
			return ""
		}
		return j.Error.Message
	}},
	{Name: "error_retryable", Label: "错误可重试", Type: "boolean", Options: []string{"true", "false"}, read: func(j *messageJob) string {
		if j.Error == nil {
			return ""
		}
		return strconv.FormatBool(j.Error.Retryable)
	}},
}

func recordFieldOperators(kind string) []string {
	ops := []string{"eq"}
	if kind == "string" {
		ops = append(ops, "contains")
	}
	if kind == "time" || kind == "number" {
		ops = append(ops, "gte", "lte")
	}
	return append(ops, "empty", "not_empty")
}

type messageRecordFilter struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

func compileRecordFilter(f messageRecordFilter) (func(*messageJob) bool, error) {
	var field *messageRecordField
	for i := range messageRecordFields {
		if messageRecordFields[i].Name == f.Field {
			field = &messageRecordFields[i]
			break
		}
	}
	if field == nil {
		return nil, fmt.Errorf("未知筛选字段：%s", f.Field)
	}
	allowed := false
	for _, op := range recordFieldOperators(field.Type) {
		if f.Op == op {
			allowed = true
		}
	}
	if !allowed || len(f.Value) > 2048 {
		return nil, fmt.Errorf("%s 的操作或值无效", field.Label)
	}
	if f.Op == "empty" || f.Op == "not_empty" {
		return func(j *messageJob) bool { return (field.read(j) == "") == (f.Op == "empty") }, nil
	}
	switch field.Type {
	case "time":
		want, err := time.Parse(time.RFC3339Nano, f.Value)
		if err != nil {
			return nil, fmt.Errorf("%s 须为 RFC3339 时间", field.Label)
		}
		return func(j *messageJob) bool {
			got, err := time.Parse(time.RFC3339Nano, field.read(j))
			if err != nil {
				return false
			}
			return (f.Op == "eq" && got.Equal(want)) || (f.Op == "gte" && !got.Before(want)) || (f.Op == "lte" && !got.After(want))
		}, nil
	case "number":
		want, err := strconv.Atoi(f.Value)
		if err != nil || want < 0 {
			return nil, fmt.Errorf("%s 须为非负整数", field.Label)
		}
		return func(j *messageJob) bool {
			got, err := strconv.Atoi(field.read(j))
			if err != nil {
				return false
			}
			return (f.Op == "eq" && got == want) || (f.Op == "gte" && got >= want) || (f.Op == "lte" && got <= want)
		}, nil
	case "boolean":
		if f.Value != "true" && f.Value != "false" {
			return nil, fmt.Errorf("%s 须为 true 或 false", field.Label)
		}
	case "enum":
		valid := false
		for _, value := range field.Options {
			if value == f.Value {
				valid = true
				break
			}
		}
		if !valid {
			return nil, fmt.Errorf("%s 的选项无效", field.Label)
		}
	}
	return func(j *messageJob) bool {
		got := field.read(j)
		if f.Op == "contains" {
			return strings.Contains(strings.ToLower(got), strings.ToLower(f.Value))
		}
		return got == f.Value
	}, nil
}

type messageRecordCursor struct {
	CreatedAt  time.Time `json:"created_at"`
	ID         string    `json:"id"`
	FilterHash string    `json:"filter_hash"`
}

type messageRecordQuery struct {
	limit       int
	filters     []func(*messageJob) bool
	text        string
	fingerprint string
	cursor      *messageRecordCursor
}

func parseMessageRecordQuery(q url.Values) (messageRecordQuery, error) {
	query := messageRecordQuery{limit: defaultRecordPageSize, text: strings.TrimSpace(q.Get("q"))}
	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxRecordPageSize {
			return query, fmt.Errorf("每页条数须为 1–%d", maxRecordPageSize)
		}
		query.limit = limit
	}
	if len(query.text) > 1024 {
		return query, fmt.Errorf("搜索关键词过长")
	}
	var filters []messageRecordFilter
	if raw := q.Get("filters"); raw != "" {
		if len(raw) > 16384 {
			return query, fmt.Errorf("筛选条件过长")
		}
		if err := json.Unmarshal([]byte(raw), &filters); err != nil || len(filters) > 40 {
			return query, fmt.Errorf("筛选条件格式无效（最多 40 条）")
		}
	}
	if id := strings.TrimSpace(q.Get("access_key_id")); id != "" {
		f := messageRecordFilter{Field: "access_key_id", Op: "eq", Value: id}
		if id == "__legacy__" {
			f.Op = "empty"
			f.Value = ""
		}
		filters = append(filters, f)
	}
	for _, f := range filters {
		match, err := compileRecordFilter(f)
		if err != nil {
			return query, err
		}
		query.filters = append(query.filters, match)
	}
	raw, _ := json.Marshal(struct {
		Filters []messageRecordFilter
		Text    string
	}{filters, query.text})
	query.fingerprint = hashString(string(raw))
	if raw := q.Get("cursor"); raw != "" {
		if len(raw) > 2048 {
			return query, fmt.Errorf("分页游标无效")
		}
		data, err := base64.RawURLEncoding.DecodeString(raw)
		var cursor messageRecordCursor
		if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.ID == "" || cursor.FilterHash != query.fingerprint {
			return query, fmt.Errorf("分页游标无效或筛选条件已变更")
		}
		query.cursor = &cursor
	}
	return query, nil
}

func recordNewer(a, b *messageJob) bool {
	if a.CreatedAt.Equal(b.CreatedAt) {
		return a.ID > b.ID
	}
	return a.CreatedAt.After(b.CreatedAt)
}

// The oldest candidate is at the root, bounding page selection to limit + 1.
type messageRecordHeap []*messageJob

func (h messageRecordHeap) Len() int            { return len(h) }
func (h messageRecordHeap) Less(i, j int) bool  { return recordNewer(h[j], h[i]) }
func (h messageRecordHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *messageRecordHeap) Push(v interface{}) { *h = append(*h, v.(*messageJob)) }
func (h *messageRecordHeap) Pop() interface{} {
	old := *h
	v := old[len(old)-1]
	*h = old[:len(old)-1]
	return v
}

func recordPreview(s string) string {
	count := 0
	for i := range s {
		if count == 180 {
			return s[:i] + "…"
		}
		count++
	}
	return s
}

func (a *messageAPI) handleMessageRecords(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	q := r.URL.Query()
	if id := q.Get("detail"); id != "" {
		a.mu.Lock()
		job := cloneMessageJob(a.jobs[id])
		a.mu.Unlock()
		if job == nil {
			writeAPIProblem(w, &apiProblem{Status: 404, Code: "message_not_found", Message: "记录不存在或已过保留期"})
			return
		}
		writeJSON(w, 200, map[string]interface{}{"message": publicMessage(job, true)})
		return
	}
	query, err := parseMessageRecordQuery(q)
	if err != nil {
		writeAPIProblem(w, &apiProblem{Status: 400, Code: "invalid_record_query", Message: err.Error()})
		return
	}
	summary := q.Get("view") == "summary"
	metadata := q.Get("include_filters") == "1" || !summary
	candidates := &messageRecordHeap{}
	keyJobs := map[string]*messageJob{}
	deviceJobs := map[string]*messageJob{}
	devices := map[string]string{}
	keyNames := map[string]string{}
	a.mu.Lock()
	for _, job := range a.jobs {
		if metadata {
			if job.DeviceID != "" {
				if previous := deviceJobs[job.DeviceID]; previous == nil || recordNewer(job, previous) {
					deviceJobs[job.DeviceID] = job
				}
			}
			if previous := keyJobs[job.AccessKeyID]; previous == nil || recordNewer(job, previous) {
				keyJobs[job.AccessKeyID] = job
			}
		}
		if query.cursor != nil && !recordNewer(&messageJob{ID: query.cursor.ID, CreatedAt: query.cursor.CreatedAt}, job) {
			continue
		}
		matches := true
		for _, match := range query.filters {
			if !match(job) {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		if query.text != "" {
			needle := strings.ToLower(query.text)
			if !strings.Contains(strings.ToLower(job.Message), needle) && !strings.Contains(strings.ToLower(job.AIMessage), needle) && !(job.Error != nil && strings.Contains(strings.ToLower(job.Error.Message), needle)) {
				continue
			}
		}
		if candidates.Len() < query.limit+1 {
			heap.Push(candidates, job)
		} else if recordNewer(job, (*candidates)[0]) {
			(*candidates)[0] = job
			heap.Fix(candidates, 0)
		}
	}
	jobs := make([]*messageJob, 0, candidates.Len())
	for _, job := range *candidates {
		jobs = append(jobs, cloneMessageJob(job))
	}
	for id, job := range keyJobs {
		keyNames[id] = job.AccessKeyName
	}
	for id, job := range deviceJobs {
		devices[id] = job.DeviceName
	}
	if metadata {
		for _, key := range a.keys {
			keyNames[key.ID] = key.Name
		}
	}
	a.mu.Unlock()
	sort.Slice(jobs, func(i, j int) bool { return recordNewer(jobs[i], jobs[j]) })
	hasMore := len(jobs) > query.limit
	if hasMore {
		jobs = jobs[:query.limit]
	}
	items := make([]map[string]interface{}, 0, len(jobs))
	for _, job := range jobs {
		item := publicMessage(job, true)
		if summary {
			delete(item, "message")
			delete(item, "ai_message")
			item["message_preview"] = recordPreview(job.Message)
			if job.Error != nil {
				item["error"] = map[string]interface{}{"code": job.Error.Code, "retryable": job.Error.Retryable}
			}
			if callback, ok := item["callback"].(map[string]interface{}); ok {
				delete(callback, "last_error")
			}
		}
		items = append(items, item)
	}
	response := map[string]interface{}{"messages": items, "has_more": hasMore, "next_cursor": ""}
	if hasMore {
		last := jobs[len(jobs)-1]
		raw, _ := json.Marshal(messageRecordCursor{CreatedAt: last.CreatedAt, ID: last.ID, FilterHash: query.fingerprint})
		response["next_cursor"] = base64.RawURLEncoding.EncodeToString(raw)
	}
	if metadata {
		keys := make([]map[string]string, 0, len(keyNames))
		for id, name := range keyNames {
			if id == "" {
				id = "__legacy__"
				name = "旧记录（未标记密钥）"
			}
			keys = append(keys, map[string]string{"id": id, "name": name})
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i]["name"] == keys[j]["name"] {
				return keys[i]["id"] < keys[j]["id"]
			}
			return keys[i]["name"] < keys[j]["name"]
		})
		fields := append([]messageRecordField(nil), messageRecordFields...)
		for i := range fields {
			fields[i].Operators = recordFieldOperators(fields[i].Type)
		}
		response["access_keys"] = keys
		response["devices"] = devices
		response["filter_fields"] = fields
	}
	writeJSON(w, http.StatusOK, response)
}
