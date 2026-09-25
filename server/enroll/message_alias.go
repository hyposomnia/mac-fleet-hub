package main

import (
	"encoding/json"
	"strings"
)

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

// The authenticated key's name is its alias. It can resolve a target only
// when the key is bound all the way down to one existing session.
func (a *messageAPI) keyAliasTargetLocked(key accessKeyState, name string) (accessKeyBinding, *apiProblem) {
	current, active := a.currentKeyLocked(key)
	if !active || current.Name != name {
		return accessKeyBinding{}, &apiProblem{Status: 404, Code: "target_alias_not_found", Message: "密钥名称与 alias 不匹配"}
	}
	binding := current.Binding
	if binding == nil || binding.DeviceID == "" || binding.AIClient == "" || binding.ProjectPath == "" || binding.SessionID == "" {
		return accessKeyBinding{}, &apiProblem{Status: 400, Code: "alias_requires_bound_session", Message: "使用密钥名称作为 alias 须先将密钥绑定到具体会话"}
	}
	return *binding, nil
}

func (a *messageAPI) aliasMatchesJobLocked(key accessKeyState, name string, job *messageJob) bool {
	if name == "" {
		return job.TargetAlias == ""
	}
	binding, problem := a.keyAliasTargetLocked(key, name)
	return problem == nil && job.TargetAlias == name &&
		binding.DeviceID == job.DeviceID && binding.AIClient == job.AIClient &&
		binding.ProjectPath == job.ProjectPath && binding.SessionID == job.SessionID
}
