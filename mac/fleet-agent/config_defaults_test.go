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

func TestLoadConfigDefaultsToIsolatedCodexDaemon(t *testing.T) {
	t.Setenv("HOME", "/Users/tester")
	t.Setenv("FLEET_CODEX_APPSERVER_MODE", "")
	t.Setenv("FLEET_CODEX_DESKTOP_SHARED_DAEMON", "")

	config := loadConfig()
	if config.CodexMode != "isolated" {
		t.Fatalf("CodexMode got %q want isolated", config.CodexMode)
	}
	if config.CodexDesktopShare {
		t.Fatal("CodexDesktopShare got true want false")
	}
	wantLaunchctl := []string{"unsetenv", codexDesktopLocalDaemonEnv}
	if got := codexDesktopSharedDaemonLaunchctlArgs(config, "/Users/tester", "darwin"); !reflect.DeepEqual(got, wantLaunchctl) {
		t.Fatalf("default launchctl args got %#v want %#v", got, wantLaunchctl)
	}
}

func TestLoadConfigAllowsExplicitSharedCodexDaemon(t *testing.T) {
	t.Setenv("HOME", "/Users/tester")
	t.Setenv("FLEET_CODEX_APPSERVER_MODE", "shared")
	t.Setenv("FLEET_CODEX_DESKTOP_SHARED_DAEMON", "1")

	config := loadConfig()
	if config.CodexMode != "shared" {
		t.Fatalf("CodexMode got %q want shared", config.CodexMode)
	}
	if !config.CodexDesktopShare {
		t.Fatal("CodexDesktopShare got false want true")
	}
	wantLaunchctl := []string{"setenv", codexDesktopLocalDaemonEnv, "1"}
	if got := codexDesktopSharedDaemonLaunchctlArgs(config, "/Users/tester", "darwin"); !reflect.DeepEqual(got, wantLaunchctl) {
		t.Fatalf("shared launchctl args got %#v want %#v", got, wantLaunchctl)
	}
}
