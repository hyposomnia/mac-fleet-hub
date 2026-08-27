package main

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	codexDesktopLocalDaemonEnv = "CODEX_APP_SERVER_USE_LOCAL_DAEMON"
	codexDesktopWebSocketEnv   = "CODEX_APP_SERVER_WS_URL"
)

var runCodexDesktopLaunchctl = func(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput()
}

type codexDesktopLaunchctlOperation struct {
	Args []string
}

// codexDesktopLaunchctlOperations returns the complete GUI launchd environment
// mutation for future Codex.app launches. True shared mode points Desktop at
// the same loopback WebSocket app-server as Fleet. Every other mode removes
// both sharing mechanisms so stale launchctl state cannot override the mode.
func codexDesktopLaunchctlOperations(config Config, goos string) ([]codexDesktopLaunchctlOperation, error) {
	if goos != "darwin" {
		return nil, nil
	}
	operations := []codexDesktopLaunchctlOperation{
		{Args: []string{"unsetenv", codexDesktopLocalDaemonEnv}},
	}
	if config.CodexDesktopShare && normalizeCodexAppServerMode(config.CodexMode) == codexAppServerModeShared {
		endpoint := strings.TrimSpace(config.CodexDesktopURL)
		if endpoint == "" && (strings.HasPrefix(config.CodexSock, "ws://") || strings.HasPrefix(config.CodexSock, "wss://")) {
			endpoint = strings.TrimSpace(config.CodexSock)
		}
		if endpoint == "" {
			endpoint = codexSharedWebSocketEndpoint
		}
		endpoint, err := validateSharedCodexEndpoint(endpoint)
		if err != nil {
			return nil, err
		}
		return append(operations, codexDesktopLaunchctlOperation{
			Args: []string{"setenv", codexDesktopWebSocketEnv, endpoint},
		}), nil
	}
	return append(operations, codexDesktopLaunchctlOperation{
		Args: []string{"unsetenv", codexDesktopWebSocketEnv},
	}), nil
}

func configureCodexDesktopSharedDaemon(config Config, _ string, goos string) error {
	operations, err := codexDesktopLaunchctlOperations(config, goos)
	if err != nil {
		return err
	}
	var failures []string
	for _, operation := range operations {
		out, runErr := runCodexDesktopLaunchctl(operation.Args...)
		if runErr == nil {
			continue
		}
		detail := strings.TrimSpace(string(out))
		failure := fmt.Sprintf("launchctl %s: %v", strings.Join(operation.Args, " "), runErr)
		if detail != "" {
			failure += ": " + detail
		}
		failures = append(failures, failure)
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}
