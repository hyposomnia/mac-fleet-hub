package main

import (
	"os"
	"path/filepath"
)

func desktopEnvironment(state, resources, searchPath string) map[string]string {
	return map[string]string{
		"FLEET_BINDING_FILE":    filepath.Join(state, "binding.json"),
		"FLEET_TMUX_CONF":       filepath.Join(state, "tmux.conf"),
		"FLEET_CHAT_QUEUE_FILE": filepath.Join(state, "chat-queue.json"),
		"FLEET_PROXY_FILE":      filepath.Join(state, "proxy.json"),
		"TMUX_TMPDIR":           filepath.Join(state, "tmux"),
		"PATH":                  filepath.Join(resources, "bin") + string(os.PathListSeparator) + searchPath,
	}
}

func configureDesktopEnvironment() error {
	resources, err := desktopResources()
	if err != nil {
		return err
	}
	state := desktopStateDirectory()
	for _, directory := range []string{state, filepath.Join(state, "tmux")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		if err := privateInfo(directory, true); err != nil {
			return err
		}
	}
	for name, value := range desktopEnvironment(state, resources, os.Getenv("PATH")) {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	return nil
}
