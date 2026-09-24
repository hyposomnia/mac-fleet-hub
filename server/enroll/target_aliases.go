package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
)

// A target alias always identifies one existing session. Key bindings remain
// independent and are checked against the resolved target for every request.
type targetAlias struct {
	Alias     string           `json:"alias"`
	Target    accessKeyBinding `json:"target"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

func validTargetAlias(name string) bool {
	runes := []rune(name)
	if len(runes) == 0 || len(runes) > 64 || !unicode.IsLetter(runes[0]) && !unicode.IsDigit(runes[0]) {
		return false
	}
	for _, r := range runes[1:] {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func hasField(fields map[string]json.RawMessage, target string) bool {
	for name := range fields {
		if strings.EqualFold(name, target) {
			return true
		}
	}
	return false
}

func hasExplicitTarget(fields map[string]json.RawMessage) bool {
	for _, name := range []string{"device", "ai_client", "project", "session"} {
		if hasField(fields, name) {
			return true
		}
	}
	return false
}

func (a *messageAPI) targetAliasLocked(name string) (targetAlias, bool) {
	for _, item := range a.aliases {
		if strings.EqualFold(item.Alias, name) {
			return item, true
		}
	}
	return targetAlias{}, false
}

func (a *messageAPI) aliasMatchesJobLocked(name string, job *messageJob) bool {
	if name == "" {
		return job.TargetAlias == ""
	}
	alias, ok := a.targetAliasLocked(name)
	return ok && alias.Alias == job.TargetAlias && alias.Target.allowsJob(job) &&
		alias.Target.DeviceID == job.DeviceID && alias.Target.AIClient == job.AIClient &&
		alias.Target.ProjectPath == job.ProjectPath && alias.Target.SessionID == job.SessionID
}

func decodeTargetAlias(w http.ResponseWriter, r *http.Request) (string, *accessKeyBinding, *apiProblem) {
	var body struct {
		Alias  string           `json:"alias"`
		Target accessKeyBinding `json:"target"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return "", nil, &apiProblem{Status: 400, Code: "invalid_target_alias", Message: "别名请求 JSON 格式或字段不正确"}
	}
	if err := dec.Decode(new(interface{})); !errors.Is(err, io.EOF) {
		return "", nil, &apiProblem{Status: 400, Code: "invalid_target_alias", Message: "请求只能包含一个 JSON 对象"}
	}
	body.Alias = strings.TrimSpace(body.Alias)
	if !validTargetAlias(body.Alias) {
		return "", nil, &apiProblem{Status: 400, Code: "invalid_target_alias", Message: "别名须以文字或数字开头，只能包含文字、数字、点、横线和下划线，最多 64 字"}
	}
	if body.Target.AIClient == "" || body.Target.ProjectPath == "" || body.Target.SessionID == "" {
		return "", nil, &apiProblem{Status: 400, Code: "invalid_target_alias", Message: "别名须指定完整的设备、客户端、项目路径和已有会话 ID"}
	}
	return body.Alias, &body.Target, nil
}

func (a *messageAPI) handleTargetAliases(w http.ResponseWriter, r *http.Request) {
	const base = "/automation/access-keys/aliases"
	if r.URL.Path == base {
		switch r.Method {
		case http.MethodGet:
			a.mu.Lock()
			aliases := append([]targetAlias{}, a.aliases...)
			a.mu.Unlock()
			sort.Slice(aliases, func(i, j int) bool { return aliases[i].Alias < aliases[j].Alias })
			writeJSON(w, http.StatusOK, map[string]interface{}{"aliases": aliases})
		case http.MethodPost:
			a.saveTargetAlias(w, r, "")
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	name := strings.TrimPrefix(r.URL.Path, base+"/")
	if !validTargetAlias(name) {
		writeAPIProblem(w, &apiProblem{Status: 404, Code: "target_alias_not_found", Message: "别名不存在"})
		return
	}
	switch r.Method {
	case http.MethodPatch:
		a.saveTargetAlias(w, r, name)
	case http.MethodDelete:
		a.mu.Lock()
		previous := append([]targetAlias(nil), a.aliases...)
		index := -1
		for i := range a.aliases {
			if strings.EqualFold(a.aliases[i].Alias, name) {
				index = i
				break
			}
		}
		if index < 0 {
			a.mu.Unlock()
			writeAPIProblem(w, &apiProblem{Status: 404, Code: "target_alias_not_found", Message: "别名不存在"})
			return
		}
		a.aliases = append(a.aliases[:index], a.aliases[index+1:]...)
		err := a.saveKeysLocked()
		if err != nil {
			a.aliases = previous
		}
		a.mu.Unlock()
		if err != nil {
			writeAPIProblem(w, &apiProblem{Status: 500, Code: "alias_save_failed", Message: "删除别名失败"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *messageAPI) saveTargetAlias(w http.ResponseWriter, r *http.Request, oldName string) {
	name, target, problem := decodeTargetAlias(w, r)
	if problem != nil {
		writeAPIProblem(w, problem)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	validated, problem := a.validateBinding(ctx, target)
	cancel()
	if problem != nil {
		writeAPIProblem(w, problem)
		return
	}
	a.mu.Lock()
	previous := append([]targetAlias(nil), a.aliases...)
	index := -1
	for i := range a.aliases {
		if strings.EqualFold(a.aliases[i].Alias, oldName) {
			index = i
		}
		if strings.EqualFold(a.aliases[i].Alias, name) && (oldName == "" || !strings.EqualFold(a.aliases[i].Alias, oldName)) {
			a.mu.Unlock()
			writeAPIProblem(w, &apiProblem{Status: 409, Code: "target_alias_exists", Message: "别名已存在"})
			return
		}
	}
	if oldName != "" && index < 0 {
		a.mu.Unlock()
		writeAPIProblem(w, &apiProblem{Status: 404, Code: "target_alias_not_found", Message: "别名不存在"})
		return
	}
	now := time.Now().UTC()
	item := targetAlias{Alias: name, Target: *validated, CreatedAt: now, UpdatedAt: now}
	if index >= 0 {
		item.CreatedAt = a.aliases[index].CreatedAt
		a.aliases[index] = item
	} else {
		a.aliases = append(a.aliases, item)
	}
	err := a.saveKeysLocked()
	if err != nil {
		a.aliases = previous
	}
	a.mu.Unlock()
	if err != nil {
		writeAPIProblem(w, &apiProblem{Status: 500, Code: "alias_save_failed", Message: "保存别名失败"})
		return
	}
	status := http.StatusOK
	if oldName == "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, item)
}
