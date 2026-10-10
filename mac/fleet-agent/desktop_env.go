// Shared readiness diagnostics and legacy state-file compatibility.
// Desktop connection intent is owned by the main shared runtime, not a watchdog.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	desktopEnvReadyzTimeout = 2 * time.Second
	appServerStateOK        = "ok"
	appServerStateFailed    = "failed"
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
