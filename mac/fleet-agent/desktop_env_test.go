package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const watchdogTestURL = "ws://127.0.0.1:47682/rpc"

// watchdogHarness 提供可断言的假 probe/getenv/setenv/unsetenv 与日志收集。
type watchdogHarness struct {
	mu       sync.Mutex
	ready    bool
	envValue string
	envErr   error
	unset    int
	setenv   []string
	getenv   int
	logs     []string
}

func newWatchdogHarness(t *testing.T, statePath string) (*desktopEnvWatchdog, *watchdogHarness) {
	t.Helper()
	h := &watchdogHarness{ready: false, envValue: watchdogTestURL}
	w := &desktopEnvWatchdog{
		probe: func() bool {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.ready
		},
		getenv: func() (string, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.getenv++
			return h.envValue, h.envErr
		},
		setenv: func(value string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.setenv = append(h.setenv, value)
			h.envValue = value
			return nil
		},
		unsetenv: func() error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.unset++
			h.envValue = ""
			return nil
		},
		desiredURL:    watchdogTestURL,
		statePath:     statePath,
		interval:      time.Millisecond, // 会被夹到下限 5s；测试直接调 check()
		failThreshold: 3,
		logf: func(format string, args ...any) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.logs = append(h.logs, fmt.Sprintf(format, args...))
		},
	}
	return w, h
}

func (h *watchdogHarness) setReady(ready bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = ready
}

func (h *watchdogHarness) counts() (unset, getenv int, setenv []string, logs []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.unset, h.getenv, append([]string(nil), h.setenv...), append([]string(nil), h.logs...)
}

func countLogs(logs []string, substr string) int {
	n := 0
	for _, line := range logs {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

// 达到阈值必须摘除一次，并且之后持续失败不许重复摘除/刷屏。
func TestWatchdogClearsOnceAtThresholdAndDoesNotSpam(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state", "app-server.json")
	w, h := newWatchdogHarness(t, statePath)

	for i := 0; i < 2; i++ {
		w.check()
	}
	if unset, _, _, _ := h.counts(); unset != 0 {
		t.Fatalf("未达阈值就摘除了 %d 次", unset)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("未达阈值不应写状态文件: %v", err)
	}

	for i := 0; i < 3; i++ {
		w.check()
	}
	unset, getenv, _, logs := h.counts()
	if unset != 1 {
		t.Fatalf("达到阈值应摘除 1 次，实际 %d", unset)
	}
	if getenv != 1 {
		t.Fatalf("达到阈值应只读一次环境变量，实际 %d", getenv)
	}
	if countLogs(logs, "已摘除") != 1 {
		t.Fatalf("应恰好记一条摘除日志: %v", logs)
	}

	logsAtThreshold := len(logs)
	for i := 0; i < 20; i++ {
		w.check()
	}
	unset, getenv, _, logs = h.counts()
	if unset != 1 || getenv != 1 {
		t.Fatalf("持续失败后重复动作：unset=%d getenv=%d", unset, getenv)
	}
	if len(logs) != logsAtThreshold {
		t.Fatalf("持续失败后仍在刷日志: +%d 条\n%v", len(logs)-logsAtThreshold, logs)
	}
}

// 恢复后必须 setenv 一次且只一次。
func TestWatchdogRestoresOnceAfterRecovery(t *testing.T) {
	w, h := newWatchdogHarness(t, filepath.Join(t.TempDir(), "app-server.json"))
	for i := 0; i < 3; i++ {
		w.check()
	}
	if unset, _, _, _ := h.counts(); unset != 1 {
		t.Fatalf("前置条件失败：unset=%d want 1", unset)
	}

	h.setReady(true)
	w.check()
	w.check()
	h.setReady(false)
	w.check()

	_, _, setenv, logs := h.counts()
	if len(setenv) != 1 || setenv[0] != watchdogTestURL {
		t.Fatalf("恢复应 setenv 一次且值为 %s，实际 %v", watchdogTestURL, setenv)
	}
	if countLogs(logs, "已恢复，重新注入") != 1 {
		t.Fatalf("恢复日志应恰好一条: %v", logs)
	}
}

// GUI 域本来就没注入变量时不许 unsetenv（无东西可摘）。
func TestWatchdogNoActionWhenEnvEmpty(t *testing.T) {
	w, h := newWatchdogHarness(t, filepath.Join(t.TempDir(), "app-server.json"))
	h.mu.Lock()
	h.envValue = ""
	h.mu.Unlock()

	for i := 0; i < 10; i++ {
		w.check()
	}
	unset, _, setenv, logs := h.counts()
	if unset != 0 {
		t.Fatalf("环境变量为空时不应 unsetenv，实际 %d 次", unset)
	}
	if len(setenv) != 0 {
		t.Fatalf("环境变量为空时不应 setenv，实际 %v", setenv)
	}
	if countLogs(logs, "无需摘除") != 1 {
		t.Fatalf("应只记一条「无需摘除」日志: %v", logs)
	}
}

// ctx 取消必须立刻退出（不能等下一个 tick）。
func TestWatchdogStopsOnContextCancel(t *testing.T) {
	w, h := newWatchdogHarness(t, filepath.Join(t.TempDir(), "app-server.json"))
	h.setReady(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后 Run 未退出")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Run 退出太慢: %s", elapsed)
	}
	_, _, _, logs := h.counts()
	if countLogs(logs, "已退出") != 1 {
		t.Fatalf("缺少退出日志: %v", logs)
	}
}

func TestWatchdogIntervalFloorAndDefaults(t *testing.T) {
	w := &desktopEnvWatchdog{interval: 0}
	if got := w.effectiveInterval(); got != desktopEnvDefaultInterval {
		t.Fatalf("默认 interval=%s want %s", got, desktopEnvDefaultInterval)
	}
	w.interval = time.Second
	if got := w.effectiveInterval(); got != desktopEnvMinInterval {
		t.Fatalf("下限 interval=%s want %s", got, desktopEnvMinInterval)
	}
	w.failThreshold = 0
	if got := w.effectiveThreshold(); got != desktopEnvDefaultFailThreshold {
		t.Fatalf("默认 threshold=%d want %d", got, desktopEnvDefaultFailThreshold)
	}
}

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

// 看门狗恢复时必须把状态文件写回 ok（同一份文件的另一套词汇是 keeper 的 failed）。
func TestWatchdogWritesOKStateOnRecovery(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state", "app-server.json")
	w, h := newWatchdogHarness(t, statePath)
	for i := 0; i < 3; i++ {
		w.check()
	}
	if state, err := readAppServerState(statePath); err != nil || state.State != appServerStateFailed || !state.ClearedDesktopEnv {
		t.Fatalf("摘除后状态文件不符: %+v err=%v", state, err)
	}

	h.setReady(true)
	w.check()
	state, err := readAppServerState(statePath)
	if err != nil {
		t.Fatalf("恢复后状态文件读取失败: %v", err)
	}
	if state.State != appServerStateOK || state.ClearedDesktopEnv {
		t.Fatalf("恢复后状态文件不符: %+v", state)
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
