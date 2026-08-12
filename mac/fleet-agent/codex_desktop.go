package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

const codexDesktopLocalDaemonEnv = "CODEX_APP_SERVER_USE_LOCAL_DAEMON"

var runCodexDesktopLaunchctl = func(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput()
}

// codexDesktopSharedDaemonLaunchctlArgs returns the GUI launchd environment
// mutation needed for future Codex.app launches. Codex Desktop only supports
// its default $CODEX_HOME/app-server-control socket, so custom Fleet homes or
// sockets must leave Desktop on its own stdio app-server.
func codexDesktopSharedDaemonLaunchctlArgs(config Config, home, goos string) []string {
	if goos != "darwin" {
		return nil
	}
	defaultCodexHome := filepath.Join(home, ".codex")
	defaultSocket := filepath.Join(defaultCodexHome, "app-server-control", "app-server-control.sock")
	mode := normalizeCodexAppServerMode(config.CodexMode)
	compatibleSocket := strings.TrimSpace(config.CodexSock) == "" || filepath.Clean(config.CodexSock) == defaultSocket
	compatible := config.CodexDesktopShare && mode == codexAppServerModeShared &&
		filepath.Clean(config.CodexHome) == defaultCodexHome && compatibleSocket
	if compatible {
		return []string{"setenv", codexDesktopLocalDaemonEnv, "1"}
	}
	return []string{"unsetenv", codexDesktopLocalDaemonEnv}
}

func configureCodexDesktopSharedDaemon(config Config, home, goos string) error {
	args := codexDesktopSharedDaemonLaunchctlArgs(config, home, goos)
	if len(args) == 0 {
		return nil
	}
	out, err := runCodexDesktopLaunchctl(args...)
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		return fmt.Errorf("launchctl %s: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, detail)
}
