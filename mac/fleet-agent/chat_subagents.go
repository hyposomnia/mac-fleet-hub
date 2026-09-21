package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const maxCodexSubagentPages = 5

// ChatSubagent is the stable, read-only dashboard projection of a Codex child
// thread. The browser never needs the collaboration tool wire format.
type ChatSubagent struct {
	ThreadID       string `json:"threadId"`
	ParentThreadID string `json:"parentThreadId,omitempty"`
	Name           string `json:"name"`
	AgentPath      string `json:"agentPath,omitempty"`
	Nickname       string `json:"nickname,omitempty"`
	Role           string `json:"role,omitempty"`
	Status         string `json:"status"`
	Depth          int    `json:"depth,omitempty"`
	CreatedAt      int64  `json:"createdAt,omitempty"`
	UpdatedAt      int64  `json:"updatedAt,omitempty"`
}

type ChatSubagentPage struct {
	Items []ChatSubagent `json:"items"`
}

type chatSubagentReader interface {
	Subagents(context.Context, string, string) (ChatSubagentPage, error)
}

type codexSubagentSource struct {
	ThreadSpawn struct {
		ParentThreadID string `json:"parent_thread_id"`
		Depth          int    `json:"depth"`
		AgentPath      string `json:"agent_path"`
		AgentNickname  string `json:"agent_nickname"`
		AgentRole      string `json:"agent_role"`
	} `json:"thread_spawn"`
}

func codexThreadSubagentSource(raw json.RawMessage) codexSubagentSource {
	var source map[string]json.RawMessage
	if json.Unmarshal(raw, &source) != nil {
		return codexSubagentSource{}
	}
	value := source["subAgent"]
	if len(value) == 0 {
		value = source["subagent"]
	}
	var parsed codexSubagentSource
	_ = json.Unmarshal(value, &parsed)
	return parsed
}

func codexSubagentName(agentPath, nickname, role, threadID string) string {
	name := strings.Trim(strings.TrimPrefix(strings.TrimSpace(agentPath), "/root/"), "/")
	if name == "root" {
		name = ""
	}
	for _, candidate := range []string{name, strings.TrimSpace(nickname), strings.TrimSpace(role)} {
		if candidate != "" {
			return candidate
		}
	}
	if len(threadID) > 8 {
		return "subagent-" + threadID[len(threadID)-8:]
	}
	return "subagent"
}

func codexSubagentStatus(threadStatus string, runtime codexRolloutTaskState, runtimeKnown bool) string {
	if runtimeKnown && runtime.turnID != "" {
		if !runtime.terminal {
			return "running"
		}
		status := strings.ToLower(strings.TrimSpace(runtime.status))
		switch {
		case status == "completed" || status == "complete":
			return "completed"
		case strings.Contains(status, "fail") || strings.Contains(status, "error"):
			return "failed"
		default:
			return "interrupted"
		}
	}
	status := strings.ToLower(strings.TrimSpace(threadStatus))
	if codexStatusIsRunning(status) || status == "pendinginit" || status == "pending_init" {
		return "running"
	}
	if strings.Contains(status, "fail") || strings.Contains(status, "error") {
		return "failed"
	}
	return "unknown"
}

func (b *codexChatBackend) Subagents(ctx context.Context, assistant, parentThreadID string) (ChatSubagentPage, error) {
	if normAssistant(assistant) != "codex" {
		return ChatSubagentPage{}, errUnsupportedChatAssistant
	}
	parentThreadID = strings.TrimSpace(parentThreadID)
	if parentThreadID == "" {
		return ChatSubagentPage{}, fmt.Errorf("missing parent thread id")
	}
	rpc, err := b.ensure(ctx)
	if err != nil {
		return ChatSubagentPage{}, err
	}

	items := make([]ChatSubagent, 0)
	seen := map[string]bool{}
	cursor := ""
	for pageIndex := 0; pageIndex < maxCodexSubagentPages; pageIndex++ {
		params := map[string]interface{}{
			"limit": 100, "cursor": nil, "sortKey": "created_at", "sortDirection": "asc",
			"archived": false, "useStateDbOnly": true, "ancestorThreadId": parentThreadID,
		}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, callErr := rpc.call(ctx, "thread/list", params)
		if callErr != nil {
			return ChatSubagentPage{}, callErr
		}
		var wire codexThreadListWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return ChatSubagentPage{}, fmt.Errorf("decode Codex subagent list: %w", err)
		}
		for _, thread := range wire.Data {
			threadID := codexThreadID(thread)
			if threadID == "" || seen[threadID] || !codexThreadIsInternalSubagent(thread) {
				continue
			}
			seen[threadID] = true
			source := codexThreadSubagentSource(thread.Source)
			parentID := strings.TrimSpace(thread.ParentThreadID)
			if parentID == "" {
				parentID = strings.TrimSpace(source.ThreadSpawn.ParentThreadID)
			}
			agentPath := strings.TrimSpace(source.ThreadSpawn.AgentPath)
			nickname := strings.TrimSpace(thread.AgentNickname)
			if nickname == "" {
				nickname = strings.TrimSpace(source.ThreadSpawn.AgentNickname)
			}
			role := strings.TrimSpace(thread.AgentRole)
			if role == "" {
				role = strings.TrimSpace(source.ThreadSpawn.AgentRole)
			}
			runtime, runtimeKnown := codexCurrentRolloutTaskState(threadID)
			items = append(items, ChatSubagent{
				ThreadID: threadID, ParentThreadID: parentID,
				Name: codexSubagentName(agentPath, nickname, role, threadID), AgentPath: agentPath,
				Nickname: nickname, Role: role,
				Status: codexSubagentStatus(codexThreadStatus(thread.Status), runtime, runtimeKnown),
				Depth:  source.ThreadSpawn.Depth, CreatedAt: thread.CreatedAt, UpdatedAt: thread.UpdatedAt,
			})
		}
		if wire.NextCursor == nil || strings.TrimSpace(*wire.NextCursor) == "" {
			break
		}
		cursor = *wire.NextCursor
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt == items[j].CreatedAt {
			return items[i].Name < items[j].Name
		}
		return items[i].CreatedAt < items[j].CreatedAt
	})
	return ChatSubagentPage{Items: items}, nil
}

func handleChatSubagents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	assistant := normAssistant(r.URL.Query().Get("assistant"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
	if assistant != "codex" {
		writeErr(w, http.StatusNotImplemented, "unsupported_assistant", "子 Agent 目前仅支持 ChatGPT。")
		return
	}
	if sessionID == "" || len(sessionID) > 200 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	reader, ok := agentChatBackend.(chatSubagentReader)
	if !ok {
		writeChatErr(w, errAppServerUnavailable)
		return
	}
	page, err := reader.Subagents(r.Context(), assistant, sessionID)
	if err != nil {
		writeChatErr(w, err)
		return
	}
	writeJSON(w, page)
}
