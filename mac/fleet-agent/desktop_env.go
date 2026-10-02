// desktop_env.go —— R5 看门狗 + shared app-server 状态文件。
//
// shared 模式下 GUI 域被注入 CODEX_APP_SERVER_WS_URL，ChatGPT.app 启动即连这个 loopback
// WebSocket。一旦 shared app-server 挂掉而变量还留着，Desktop 就会被指向一个死端口，
// 每次启动直接失败（connect ECONNREFUSED）。看门狗周期性探测 /readyz，连续失败到阈值
// 就摘掉该变量（只摘一次、只记状态迁移），恢复后再补回一次。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	desktopEnvDefaultInterval      = 30 * time.Second
	desktopEnvMinInterval          = 5 * time.Second
	desktopEnvDefaultFailThreshold = 3
	desktopEnvReadyzTimeout        = 2 * time.Second

	// 状态文件里的 state 取值。与 shell 侧监督包装（mac/codex-keeper-launch.sh）
	// 保持同一套词汇：ok / failed。
	appServerStateOK     = "ok"
	appServerStateFailed = "failed"
)

// jsonFlag 兼容两种写法：shell 写出的裸数字 1/0，以及 Go 写出的 true/false。
type jsonFlag bool

func (f *jsonFlag) UnmarshalJSON(b []byte) error {
	switch strings.TrimSpace(string(b)) {
	case "true", "1", `"1"`, `"true"`:
		*f = true
	default:
		*f = false
	}
	return nil
}

func (f jsonFlag) MarshalJSON() ([]byte, error) {
	if f {
		return []byte("1"), nil
	}
	return []byte("0"), nil
}

// appServerState 是熔断/看门狗状态文件的内容。shell 侧监督包装还会写入
// codexBin / listen，这里读出来展示、写回时原样保留。
type appServerState struct {
	State             string   `json:"state"`
	LastError         string   `json:"lastError"`
	UpdatedAt         string   `json:"updatedAt"`
	ClearedDesktopEnv jsonFlag `json:"clearedDesktopEnv"`
	CodexBin          string   `json:"codexBin,omitempty"`
	Listen            string   `json:"listen,omitempty"`
}

// appServerStateHealthy：state 为空或 ok/ready 都视为健康（兼容 keeper 与看门狗两套写法）。
func appServerStateHealthy(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "", appServerStateOK, "ready":
		return true
	default:
		return false
	}
}

// appServerStatePath 是默认状态文件：~/Library/Application Support/macfleet/state/app-server.json。
// FLEET_STATE_DIR 显式覆盖时与 shell 侧监督包装保持同一目录。
func appServerStatePath() string {
	if dir := strings.TrimSpace(os.Getenv("FLEET_STATE_DIR")); dir != "" {
		return filepath.Join(dir, "app-server.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, "Library", "Application Support", "macfleet", "state", "app-server.json")
}

// launchctlRun 是 launchctl 调用的可替换入口（测试注入假实现，不真改 GUI 域环境）。
var launchctlRun = runCmd

func launchctlGetenv(key string) (string, error) {
	out, err := launchctlRun("launchctl", "getenv", key)
	return strings.TrimSpace(out), err
}

func launchctlSetenv(key, value string) error {
	_, err := launchctlRun("launchctl", "setenv", key, value)
	return err
}

func launchctlUnsetenv(key string) error {
	_, err := launchctlRun("launchctl", "unsetenv", key)
	return err
}

// desktopEnvWatchdog：shared app-server 的 GUI 域环境看门狗。
// 所有外部副作用（探测 / 读写环境变量）都通过字段注入，便于测试。
type desktopEnvWatchdog struct {
	probe         func() bool
	getenv        func() (string, error)
	setenv        func(string) error
	unsetenv      func() error
	desiredURL    string
	statePath     string
	interval      time.Duration // 默认 30s，下限 5s
	failThreshold int           // 默认 3
	logf          func(format string, args ...any)

	failures       int
	cleared        bool // 本轮故障是否已摘除过变量
	clearAttempted bool // 本轮故障是否已尝试过摘除（避免每天 tick 重试刷屏）
	lastLog        string
}

// newDesktopEnvWatchdog 用真实 readyz 探测与 launchctl 构造看门狗。
func newDesktopEnvWatchdog(desiredURL, statePath string) *desktopEnvWatchdog {
	endpoint := strings.TrimSpace(desiredURL)
	if endpoint == "" {
		endpoint = codexSharedWebSocketEndpoint
	}
	return &desktopEnvWatchdog{
		probe:         func() bool { return probeReadyz(endpoint, desktopEnvReadyzTimeout) },
		getenv:        func() (string, error) { return launchctlGetenv(codexDesktopWebSocketEnv) },
		setenv:        func(value string) error { return launchctlSetenv(codexDesktopWebSocketEnv, value) },
		unsetenv:      func() error { return launchctlUnsetenv(codexDesktopWebSocketEnv) },
		desiredURL:    endpoint,
		statePath:     statePath,
		interval:      desktopEnvDefaultInterval,
		failThreshold: desktopEnvDefaultFailThreshold,
		logf:          log.Printf,
	}
}

func (w *desktopEnvWatchdog) effectiveInterval() time.Duration {
	if w.interval <= 0 {
		return desktopEnvDefaultInterval
	}
	if w.interval < desktopEnvMinInterval {
		return desktopEnvMinInterval
	}
	return w.interval
}

func (w *desktopEnvWatchdog) effectiveThreshold() int {
	if w.failThreshold <= 0 {
		return desktopEnvDefaultFailThreshold
	}
	return w.failThreshold
}

func (w *desktopEnvWatchdog) emit(format string, args ...any) {
	if w.logf != nil {
		w.logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// emitOnce 只在状态迁移（key 变化）时打日志，避免每个 tick 刷屏。
func (w *desktopEnvWatchdog) emitOnce(key, format string, args ...any) {
	if w.lastLog == key {
		return
	}
	w.lastLog = key
	w.emit(format, args...)
}

// Run 周期探测直到 ctx 取消。
func (w *desktopEnvWatchdog) Run(ctx context.Context) {
	interval := w.effectiveInterval()
	threshold := w.effectiveThreshold()
	w.emit("Desktop 环境看门狗已启动：每 %s 探测 %s 的 /readyz，连续 %d 次失败即摘除 GUI 域 %s",
		interval, strings.TrimSpace(w.desiredURL), threshold, codexDesktopWebSocketEnv)

	w.check()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.emit("Desktop 环境看门狗已退出")
			return
		case <-ticker.C:
			w.check()
		}
	}
}

// check 执行一次探测：成功清零计数并在「此前摘除过」时恢复一次；失败累加，
// 达到阈值且 GUI 域确实非空时摘除一次，并把状态写进 statePath。
func (w *desktopEnvWatchdog) check() {
	if w.probe == nil {
		return
	}

	if w.probe() {
		w.failures = 0
		if !w.cleared {
			w.emitOnce(appServerStateOK, "shared app-server readyz 正常")
			return
		}
		if w.setenv == nil {
			w.cleared = false
			return
		}
		if err := w.setenv(w.desiredURL); err != nil {
			w.emit("shared app-server 已恢复，但重新注入 GUI 域 %s 失败: %v", codexDesktopWebSocketEnv, err)
			return
		}
		w.cleared = false
		w.clearAttempted = false
		w.lastLog = "" // 允许后续再次记录状态迁移
		w.emit("shared app-server 已恢复，重新注入 GUI 域 %s=%s", codexDesktopWebSocketEnv, w.desiredURL)
		_ = writeAppServerState(w.statePath, appServerStateOK, "", false)
		return
	}

	w.failures++
	if w.failures < w.effectiveThreshold() || w.clearAttempted {
		return
	}
	if w.getenv == nil || w.unsetenv == nil {
		w.clearAttempted = true
		return
	}

	value, err := w.getenv()
	if err != nil {
		w.clearAttempted = true
		w.emit("读取 GUI 域 %s 失败: %v", codexDesktopWebSocketEnv, err)
		return
	}
	if strings.TrimSpace(value) == "" {
		w.clearAttempted = true
		w.emitOnce("empty", "shared app-server 连续 %d 次未 ready，但 GUI 域未注入 %s，无需摘除",
			w.failures, codexDesktopWebSocketEnv)
		return
	}

	w.clearAttempted = true
	if err := w.unsetenv(); err != nil {
		w.emit("摘除 GUI 域 %s 失败: %v", codexDesktopWebSocketEnv, err)
		return
	}
	w.cleared = true
	lastError := fmt.Sprintf("readyz 连续 %d 次探测失败", w.failures)
	w.emit("shared app-server 连续 %d 次未 ready，已摘除 GUI 域 %s（原值 %s），避免 Desktop 被指向死端口",
		w.failures, codexDesktopWebSocketEnv, value)
	_ = writeAppServerState(w.statePath, appServerStateFailed, lastError, true)
}

// readyzURL 把 shared WS/HTTP 端点归一成 http://host:port/readyz；无法解析返回空串。
func readyzURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "ws://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	host := parsed.Host
	if parsed.Port() == "" {
		host = net.JoinHostPort(parsed.Hostname(), sharedEndpointPort())
	}
	return "http://" + host + "/readyz"
}

// sharedEndpointPort 取默认 shared loopback 端口（来自 codexSharedWebSocketEndpoint）。
func sharedEndpointPort() string {
	if parsed, err := url.Parse(codexSharedWebSocketEndpoint); err == nil && parsed.Port() != "" {
		return parsed.Port()
	}
	return "47682"
}

// probeReadyz 探测 shared app-server 的 /readyz：HTTP 2xx 才算 ready。
func probeReadyz(raw string, timeout time.Duration) bool {
	target := readyzURL(raw)
	if target == "" {
		return false
	}
	if timeout <= 0 {
		timeout = desktopEnvReadyzTimeout
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(target)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<12))
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// writeAppServerState 原子写状态文件：目录 0700，同目录临时文件 + rename，文件 0600。
func writeAppServerState(path, state, lastError string, clearedDesktopEnv bool) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("状态文件路径为空")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	payload := appServerState{
		State:             state,
		LastError:         lastError,
		UpdatedAt:         time.Now().Format(time.RFC3339),
		ClearedDesktopEnv: jsonFlag(clearedDesktopEnv),
	}
	// 只更新状态，不覆盖 shell 监督包装写入的诊断字段（codexBin / listen）。
	if prev, err := readAppServerState(path); err == nil {
		payload.CodexBin = prev.CodexBin
		payload.Listen = prev.Listen
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".app-server-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 成功后已被 rename 走，这里是失败兜底
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// readAppServerState 读取状态文件；不存在时返回的 error 满足 os.IsNotExist。
func readAppServerState(path string) (appServerState, error) {
	var state appServerState
	b, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return state, err
	}
	return state, nil
}
