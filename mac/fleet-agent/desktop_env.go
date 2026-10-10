// Read-only shared app-server readiness diagnostics.
package main

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const desktopEnvReadyzTimeout = 2 * time.Second

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
