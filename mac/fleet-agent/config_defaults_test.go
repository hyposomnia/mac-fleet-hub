package main

import (
	"os"
	"reflect"
	"regexp"
	"testing"
)

func TestSetupMacBracesVariablesBeforeNonASCIIText(t *testing.T) {
	script, err := os.ReadFile("../setup-mac.sh")
	if err != nil {
		t.Fatal(err)
	}
	unsafeExpansion := regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*[^\x00-\x7F]`)
	if match := unsafeExpansion.Find(script); match != nil {
		t.Fatalf("setup-mac.sh has an unbraced variable before non-ASCII text: %q", match)
	}
}

func TestLoadConfigDefaultsToSharedCodexWebSocket(t *testing.T) {
	t.Setenv("HOME", "/Users/tester")
	t.Setenv("FLEET_CODEX_APPSERVER_MODE", "")
	t.Setenv("FLEET_CODEX_APPSERVER_SOCK", "")
	t.Setenv("FLEET_CODEX_DESKTOP_WS_URL", "")
	t.Setenv("FLEET_CODEX_DESKTOP_SHARED_DAEMON", "")

	config := loadConfig()
	if config.CodexMode != "shared" {
		t.Fatalf("CodexMode got %q want shared", config.CodexMode)
	}
	if !config.CodexDesktopShare {
		t.Fatal("CodexDesktopShare got false want true")
	}
	wantLaunchctl := [][]string{
		{"unsetenv", codexDesktopLocalDaemonEnv},
		{"setenv", codexDesktopWebSocketEnv, codexSharedWebSocketEndpoint},
	}
	operations, err := codexDesktopLaunchctlOperations(config, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if got := launchctlArgs(operations); !reflect.DeepEqual(got, wantLaunchctl) {
		t.Fatalf("default launchctl operations got %#v want %#v", got, wantLaunchctl)
	}
}

func TestLoadConfigAllowsExplicitIsolatedCodexDaemon(t *testing.T) {
	t.Setenv("HOME", "/Users/tester")
	t.Setenv("FLEET_CODEX_APPSERVER_MODE", "isolated")
	t.Setenv("FLEET_CODEX_APPSERVER_SOCK", "/Users/tester/.macfleet/codex-app-server.sock")
	t.Setenv("FLEET_CODEX_DESKTOP_WS_URL", "")
	t.Setenv("FLEET_CODEX_DESKTOP_SHARED_DAEMON", "0")

	config := loadConfig()
	if config.CodexMode != "isolated" {
		t.Fatalf("CodexMode got %q want isolated", config.CodexMode)
	}
	if config.CodexDesktopShare {
		t.Fatal("CodexDesktopShare got true want false")
	}
	wantLaunchctl := [][]string{
		{"unsetenv", codexDesktopLocalDaemonEnv},
		{"unsetenv", codexDesktopWebSocketEnv},
	}
	operations, err := codexDesktopLaunchctlOperations(config, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if got := launchctlArgs(operations); !reflect.DeepEqual(got, wantLaunchctl) {
		t.Fatalf("isolated launchctl operations got %#v want %#v", got, wantLaunchctl)
	}
}
