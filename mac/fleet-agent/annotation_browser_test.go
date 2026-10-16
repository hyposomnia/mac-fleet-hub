//go:build annotation_uat

package main

// Local browser UAT uses real assets, queue, serializer and SSE. Only external
// providers, inventory and authentication are test doubles; no model is called.
import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAnnotationBrowserServer(t *testing.T) {
	cfg.DSHEnabled = true
	q, _ := openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	agentChatQueue = q
	var mu sync.Mutex
	histories := map[string][]ChatEvent{}
	subscribers := map[string][]chan ChatEvent{}
	initial := func(a, s string) []ChatEvent {
		if histories[a+s] == nil {
			histories[a+s] = []ChatEvent{
				newChatEvent("user_done", a, s, "initial", "u-initial", map[string]any{"text": "请审阅这份交互设计。"}),
				newChatEvent("assistant_done", a, s, "initial", "a-initial", map[string]any{"text": "注释让讨论直接关联到具体文字。\n\n先选中需要修改的句子，再补充你的意见。保存后，注释会作为输入框中的附件发送。\n\n1. 支持只引用原文。\n2. 主题色标记用于区分不同选文。\n\n[打开设计文档](/view?mac=m1&path=/annotation-demo.md) · [打开代码](/view?mac=m1&path=/annotation-demo.go)\n\n```text\n:codex-annotation{index=\"1\"}\n```"}),
			}
		}
		return append([]ChatEvent(nil), histories[a+s]...)
	}
	backend := fakeChatBackend{
		controlFn: func(context.Context, string, string) (ChatRuntimeState, error) {
			return ChatRuntimeState{Status: "idle", SharedRuntime: true}, nil
		},
		resumeFn: func(_ context.Context, a, s, _ string) (ChatResumeResult, error) {
			mu.Lock()
			defer mu.Unlock()
			return ChatResumeResult{SessionID: s, ThreadID: s, Status: "idle", History: ChatHistoryPage{Events: initial(a, s)}}, nil
		},
		historyFn: func(_ context.Context, a, s, _ string) (ChatHistoryPage, error) {
			mu.Lock()
			defer mu.Unlock()
			return ChatHistoryPage{Events: initial(a, s)}, nil
		},
		eventsFn: func(ctx context.Context, a, s string) (<-chan ChatEvent, error) {
			mu.Lock()
			defer mu.Unlock()
			ch := make(chan ChatEvent, 64)
			subscribers[a+s] = append(subscribers[a+s], ch)
			return ch, nil
		},
		steerFn: func(context.Context, string, string, string, string, []ChatAttachment, []ChatSkill) (ChatInputResult, error) {
			return ChatInputResult{}, errNoActiveChatTurn
		},
		inputFn: func(_ context.Context, a, s, text string, _ []ChatAttachment, _ []ChatSkill, o ChatTurnOptions) (ChatInputResult, error) {
			mu.Lock()
			defer mu.Unlock()
			initial(a, s)
			turn := fmt.Sprintf("turn-%d", time.Now().UnixNano())
			prefix := "codex"
			if a != "codex" {
				prefix = "fleet"
			}
			events := []ChatEvent{
				newChatEvent("turn_started", a, s, turn, "", map[string]any{}),
				newChatEvent("user_done", a, s, turn, "u-"+turn, map[string]any{"clientId": o.ClientUserMessageID, "text": text}),
				newChatEvent("assistant_done", a, s, turn, "a-"+turn, map[string]any{"text": "已根据意见调整。:" + prefix + "-annotation{index=\"1\"}\n\n相邻引用：:" + prefix + "-annotation{index=\"1\"}:" + prefix + "-annotation{index=\"2\"}\n\n`:" + prefix + "-annotation{index=\"1\"}` 保持代码原样。"}),
				newChatEvent("turn_done", a, s, turn, "", map[string]any{"status": "completed"}),
			}
			histories[a+s] = append(histories[a+s], events...)
			for _, ch := range subscribers[a+s] {
				for _, event := range events {
					select {
					case ch <- event:
					default:
					}
				}
			}
			return ChatInputResult{TurnID: turn}, nil
		},
	}
	agentChatBackend = backend
	worker := newChatQueueWorker(q, backendChatQueueSender{backend})
	agentChatQueueWorker = worker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Run(ctx)
	mux := http.NewServeMux()
	reply := func(path string, value any) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, value) })
	}
	reply("/api/auth/me", map[string]any{"user": map[string]any{"id": "annotation-uat", "email": "annotation@example.test"}, "csrf_token": "fixture"})
	reply("/api/devices", map[string]any{"devices": []any{map[string]any{"id": "m1", "name": "注释验收设备", "online": true, "status": "active"}}})
	reply("/api/names", map[string]string{"m1": "注释验收设备"})
	reply("/api/settings", map[string]any{})
	reply("/m1/api/info", map[string]any{"dsh": map[string]any{"enabled": true, "degraded": false}})
	reply("/m1/api/health", map[string]any{"ok": true})
	mux.HandleFunc("/m1/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		a := r.URL.Query().Get("assistant")
		writeJSON(w, map[string]any{"sessions": []any{map[string]any{"sessionId": "annotation-demo", "assistant": a, "title": "注释集成验收", "cwd": "/annotation-demo", "mtime": time.Now().UnixMilli()}, map[string]any{"sessionId": "annotation-other", "assistant": a, "title": "作用域隔离验收", "cwd": "/annotation-demo", "mtime": 1}}})
	})
	reply("/m1/api/projects", map[string]any{"projects": []any{map[string]string{"name": "annotation-demo", "cwd": "/annotation-demo"}}})
	mux.HandleFunc("/m1/api/chat/resume", handleChatResume)
	mux.HandleFunc("/m1/api/chat/history", handleChatHistory)
	mux.HandleFunc("/m1/api/chat/events", handleChatEvents)
	mux.HandleFunc("/m1/api/chat/queue", handleChatQueue)
	mux.HandleFunc("/m1/api/chat/skills", handleChatSkills)
	mux.HandleFunc("/m1/api/chat/subagents", handleChatSubagents)
	mux.HandleFunc("/m1/api/file/preview", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		kind := "markdown"
		content := "# 注释设计文档\n\n选中这段文档，可以将讨论关联到具体内容。\n\n保存后在共享输入框中管理，切换文件与会话时草稿继续保留。\n\n这是第二段，用于验证跨段选文。"
		if strings.HasSuffix(path, ".go") {
			kind = "text"
			content = "package main\n\nfunc annotation() string {\n    return \"选中文字，添加到对话\"\n}\n"
		}
		writeJSON(w, map[string]any{"kind": kind, "path": path, "name": filepath.Base(path), "content": content})
	})
	dashboard := filepath.Join("..", "..", "server", "dashboard")
	mux.HandleFunc("/view", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(dashboard, "index.html"))
	})
	mux.Handle("/", http.FileServer(http.Dir(dashboard)))
	t.Log("Browser harness: http://127.0.0.1:8873 (test doubles; no model calls)")
	server := &http.Server{Addr: "127.0.0.1:8873", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
