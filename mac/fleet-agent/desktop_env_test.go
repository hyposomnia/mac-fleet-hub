package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 摘除后写出的状态文件必须可解析，且字段/权限符合约定。
func TestWriteAppServerStateJSONParsable(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state", "app-server.json")
	if err := writeAppServerState(statePath, appServerStateFailed, "readyz 连续 3 次探测失败", true); err != nil {
		t.Fatalf("writeAppServerState: %v", err)
	}

	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("状态文件不存在: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("状态文件权限=%04o want 0600", info.Mode().Perm())
	}
	if dirInfo, err := os.Stat(filepath.Dir(statePath)); err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("状态目录权限不符: %v %v", dirInfo.Mode().Perm(), err)
	}

	b, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("状态文件不是合法 JSON: %v\n%s", err, b)
	}
	for _, key := range []string{"state", "lastError", "updatedAt", "clearedDesktopEnv"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("状态文件缺少字段 %q: %s", key, b)
		}
	}
	if raw["state"] != appServerStateFailed {
		t.Fatalf("state 字段不符: %s", b)
	}
	// shell 侧监督包装用裸数字 1/0 表示该布尔位，Go 侧必须写出同一种格式。
	if raw["clearedDesktopEnv"] != float64(1) {
		t.Fatalf("clearedDesktopEnv 应为 1（与 keeper 一致）: %s", b)
	}
	if _, err := time.Parse(time.RFC3339, raw["updatedAt"].(string)); err != nil {
		t.Fatalf("updatedAt 不是 RFC3339: %v", err)
	}

	state, err := readAppServerState(statePath)
	if err != nil {
		t.Fatalf("readAppServerState: %v", err)
	}
	if state.State != appServerStateFailed || !state.ClearedDesktopEnv || state.LastError == "" {
		t.Fatalf("读回内容不符: %+v", state)
	}

	entries, err := os.ReadDir(filepath.Dir(statePath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("残留临时文件: %v", entries)
	}
}

// shell 侧 keeper 写出的状态文件（ok/failed + 裸数字 1/0 + 额外字段）必须能读，
// 且看门狗写回状态时不许抹掉 codexBin/listen 诊断字段。
func TestAppServerStateCompatibleWithKeeperFormat(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state", "app-server.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	keeperJSON := `{"state":"failed","lastError":"keeper 连续启动失败","updatedAt":"2026-09-30T12:00:00Z","clearedDesktopEnv":1,"codexBin":"/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex","listen":"ws://127.0.0.1:47682/rpc"}`
	if err := os.WriteFile(statePath, []byte(keeperJSON+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := readAppServerState(statePath)
	if err != nil {
		t.Fatalf("readAppServerState: %v", err)
	}
	if state.State != appServerStateFailed || !state.ClearedDesktopEnv {
		t.Fatalf("keeper 格式解析不符: %+v", state)
	}
	if state.CodexBin == "" || state.Listen == "" {
		t.Fatalf("未读出 codexBin/listen: %+v", state)
	}
	if appServerStateHealthy(state.State) {
		t.Fatalf("failed 不应视为健康")
	}

	// 看门狗恢复时只更新状态位，诊断字段必须保留。
	if err := writeAppServerState(statePath, appServerStateOK, "", false); err != nil {
		t.Fatal(err)
	}
	again, err := readAppServerState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if again.State != appServerStateOK || again.ClearedDesktopEnv {
		t.Fatalf("状态未更新: %+v", again)
	}
	if again.CodexBin != state.CodexBin || again.Listen != state.Listen {
		t.Fatalf("诊断字段被抹掉: %+v", again)
	}
}

// 状态文件路径必须与 shell 侧监督包装一致：默认 ~/Library/Application Support/macfleet/state，
// FLEET_STATE_DIR 设了就跟它走。
func TestAppServerStatePathMatchesKeeperContract(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLEET_STATE_DIR", dir)
	if got := appServerStatePath(); got != filepath.Join(dir, "app-server.json") {
		t.Fatalf("FLEET_STATE_DIR 覆盖失效: %q", got)
	}

	t.Setenv("FLEET_STATE_DIR", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("无法确定 home 目录")
	}
	want := filepath.Join(home, "Library", "Application Support", "macfleet", "state", "app-server.json")
	if got := appServerStatePath(); got != want {
		t.Fatalf("默认状态文件路径=%q want %q", got, want)
	}
}

func TestReadyzURLNormalization(t *testing.T) {
	cases := map[string]string{
		"ws://127.0.0.1:47682/rpc": "http://127.0.0.1:47682/readyz",
		"wss://127.0.0.1:47682":    "http://127.0.0.1:47682/readyz",
		"127.0.0.1:47682":          "http://127.0.0.1:47682/readyz",
		"ws://127.0.0.1/rpc":       "http://127.0.0.1:" + sharedEndpointPort() + "/readyz",
		"":                         "",
	}
	for in, want := range cases {
		if got := readyzURL(in); got != want {
			t.Errorf("readyzURL(%q)=%q want %q", in, got, want)
		}
	}
}

func TestProbeReadyz(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if !probeReadyz(ok.URL, time.Second) {
		t.Fatalf("2xx 应判定 ready: %s", ok.URL)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	if probeReadyz(bad.URL, time.Second) {
		t.Fatalf("503 不应判定 ready: %s", bad.URL)
	}

	if probeReadyz("ws://127.0.0.1:1/rpc", 200*time.Millisecond) {
		t.Fatal("无监听端口不应判定 ready")
	}
	if probeReadyz("", time.Second) {
		t.Fatal("空端点不应判定 ready")
	}
}
