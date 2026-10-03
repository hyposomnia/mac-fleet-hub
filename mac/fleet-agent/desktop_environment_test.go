package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopEnvironmentIsolatesOwnedStateWithoutChangingDesktop(t *testing.T) {
	state := desktopTestDirectory(t)
	values := desktopEnvironment(state, "/bundle/Resources", "/usr/bin:/bin")
	for _, name := range []string{"FLEET_BINDING_FILE", "FLEET_TMUX_CONF", "FLEET_CHAT_QUEUE_FILE", "FLEET_PROXY_FILE", "TMUX_TMPDIR"} {
		if !strings.HasPrefix(values[name], state+string(filepath.Separator)) {
			t.Fatalf("shared legacy state in %s: %s", name, values[name])
		}
	}
	if values["PATH"] != "/bundle/Resources/bin:/usr/bin:/bin" {
		t.Fatal("bundled tools are not preferred")
	}
	if _, exists := values["CODEX_APP_SERVER_WS_URL"]; exists {
		t.Fatal("changed existing Desktop environment")
	}
}
