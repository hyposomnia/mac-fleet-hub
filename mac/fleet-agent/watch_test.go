package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// evalWatch 应只在出现非 cli 的 entrypoint（Claude Desktop 等外部客户端）时判外部写入；
// CLI 自身的合法 DAG 分叉（同父多子 / 多叶子）不得误报。
func TestEvalWatchEntrypoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")

	// 注册时文件已有一条 cli 历史行（offset 跳过它）
	base := `{"uuid":"a","parentUuid":null,"entrypoint":"cli","type":"user"}` + "\n"
	if err := os.WriteFile(path, []byte(base), 0644); err != nil {
		t.Fatal(err)
	}
	const sid = "fleet-x"
	watchers = map[string]*watcher{}
	info, _ := os.Stat(path)
	watchers[sid] = &watcher{path: path, offset: info.Size()}

	// 1) 追加纯 CLI 行——含一个「合法分叉」（两个子节点挂同一父 a，多叶子）：不得判外部
	append := `{"uuid":"b","parentUuid":"a","entrypoint":"cli","type":"assistant"}` + "\n" +
		`{"uuid":"c","parentUuid":"a","entrypoint":"cli","type":"system","isApiErrorMessage":true}` + "\n" +
		`{"uuid":"d","parentUuid":"x-old-leaf","entrypoint":"cli","type":"user"}` + "\n"
	appendTo(t, path, append)
	if evalWatch(sid) {
		t.Fatalf("纯 cli 写入（含分叉/孤儿父）被误判为外部")
	}

	// 2) 再追加一条 claude-desktop 行：应判外部
	appendTo(t, path, `{"uuid":"e","parentUuid":"d","entrypoint":"claude-desktop","type":"user"}`+"\n")
	if !evalWatch(sid) {
		t.Fatalf("claude-desktop 写入未被判为外部")
	}

	// 3) external 一旦为真应保持（粘滞）
	if !evalWatch(sid) {
		t.Fatalf("external 状态未粘滞")
	}
}

func TestHandleReloadReturnsTmuxError(t *testing.T) {
	originalStart := startTmuxSession
	originalKill := killTmuxSession
	originalPtyUsage := ptyUsage
	watchMu.Lock()
	originalWatchers := watchers
	failedWatch := &watcher{assistant: "claude", sessionID: "reload-session", mode: "auto"}
	watchers = map[string]*watcher{"fleet-reload-test": failedWatch}
	watchMu.Unlock()
	t.Cleanup(func() {
		startTmuxSession = originalStart
		killTmuxSession = originalKill
		ptyUsage = originalPtyUsage
		watchMu.Lock()
		watchers = originalWatchers
		watchMu.Unlock()
	})
	startTmuxSession = func(name, cwd, command string) error { return errors.New("reload failed") }
	killTmuxSession = func(string) {}
	ptyUsage = func() (int, int) { return 1, 511 }

	req := httptest.NewRequest(http.MethodPost, "/api/reload", strings.NewReader(`{"sid":"fleet-reload-test"}`))
	recorder := httptest.NewRecorder()
	handleReload(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status got %d body %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "tmux_failed" || !strings.Contains(body["message"], "reload failed") {
		t.Fatalf("body got %#v", body)
	}
	watchMu.Lock()
	gotWatch := watchers["fleet-reload-test"]
	watchMu.Unlock()
	if gotWatch != failedWatch {
		t.Fatal("failed reload replaced the existing watcher")
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}
