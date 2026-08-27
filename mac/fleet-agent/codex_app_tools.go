package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const defaultCodexDesktopApp = "/Applications/ChatGPT.app"

// codexDesktopAppToolsPipe returns the protected browser-use socket currently
// held open by the signed ChatGPT main process. Desktop is the only component
// that creates a 0600 socket in this directory; browser-use sockets are 0755.
func codexDesktopAppToolsPipe() (string, error) {
	appPath := strings.TrimSpace(os.Getenv("FLEET_CODEX_DESKTOP_APP_PATH"))
	if appPath == "" {
		appPath = defaultCodexDesktopApp
	}
	wantCommand := filepath.Join(appPath, "Contents", "MacOS", "ChatGPT")
	// macOS pgrep compares a truncated process name for app bundles, even with
	// -f on some releases. Read ps once and accept only an exact executable,
	// same-uid command line with no arguments.
	processesOut, err := exec.Command("/bin/ps", "-axo", "pid=,uid=,command=").Output()
	if err != nil {
		return "", fmt.Errorf("Codex Desktop is not running: %w", err)
	}
	var pids []string
	for _, line := range strings.Split(string(processesOut), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[1] != fmt.Sprint(os.Getuid()) || fields[2] != wantCommand {
			continue
		}
		pids = append(pids, fields[0])
	}
	if len(pids) == 0 {
		return "", fmt.Errorf("Codex Desktop is not running")
	}

	var candidates []string
	for _, pid := range pids {
		commandOut, commandErr := exec.Command("/bin/ps", "-p", pid, "-o", "command=").Output()
		if commandErr != nil || strings.TrimSpace(string(commandOut)) != wantCommand {
			continue
		}
		lsofOut, lsofErr := exec.Command("/usr/sbin/lsof", "-n", "-P", "-a", "-p", pid, "-U", "-Fn").Output()
		if lsofErr != nil {
			continue
		}
		for _, line := range strings.Split(string(lsofOut), "\n") {
			if !strings.HasPrefix(line, "n/tmp/codex-browser-use/") {
				continue
			}
			path := strings.TrimPrefix(line, "n")
			if filepath.Dir(path) == "/tmp/codex-browser-use" && strings.HasSuffix(path, ".sock") {
				candidates = append(candidates, path)
			}
		}
	}
	return chooseCodexAppToolsPipe(candidates)
}

func chooseCodexAppToolsPipe(candidates []string) (string, error) {
	type socketCandidate struct {
		path    string
		modTime int64
	}
	seen := map[string]bool{}
	valid := make([]socketCandidate, 0, len(candidates))
	for _, path := range candidates {
		if seen[path] {
			continue
		}
		seen[path] = true
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0600 {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Getuid() {
			continue
		}
		valid = append(valid, socketCandidate{path: path, modTime: info.ModTime().UnixNano()})
	}
	if len(valid) == 0 {
		return "", fmt.Errorf("Codex Desktop app-tools socket is unavailable")
	}
	sort.Slice(valid, func(i, j int) bool {
		if valid[i].modTime == valid[j].modTime {
			return valid[i].path < valid[j].path
		}
		return valid[i].modTime < valid[j].modTime
	})
	return valid[0].path, nil
}

func codexDesktopAppToolsSupported() bool {
	appPath := strings.TrimSpace(os.Getenv("FLEET_CODEX_DESKTOP_APP_PATH"))
	if appPath == "" {
		appPath = defaultCodexDesktopApp
	}
	resources := filepath.Join(appPath, "Contents", "Resources")
	pluginDir := filepath.Join(resources, "plugins", "openai-bundled", "plugins", "codex-app-tools")
	for _, required := range []string{
		filepath.Join(resources, "cua_node", "bin", "node"),
		filepath.Join(pluginDir, "desktop-mcp.json"),
		filepath.Join(pluginDir, "server.mjs"),
		filepath.Join(pluginDir, "scripts", "launch_codex_app_tools_mcp"),
	} {
		info, err := os.Stat(required)
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}
