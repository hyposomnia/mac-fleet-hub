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

// DSH 默认开启：装了 DSH Desktop 的机器只要 Desktop 一起来就该立刻能用，
// 不需要逐台配置。显式设 0 仍可让单机退出。
func TestLoadConfigEnablesDSHByDefault(t *testing.T) {
	t.Setenv("HOME", "/Users/tester")
	t.Setenv("FLEET_DSH_ENABLED", "")
	t.Setenv("FLEET_DSH_HOME", "")
	t.Setenv("FLEET_DSH_LOG", "")

	config := loadConfig()
	if !config.DSHEnabled {
		t.Fatal("DSHEnabled 默认应为 true（显式设 0 才退出）")
	}
	// 路径默认值必须是 DSH Desktop 的真实位置，而不是 DSH CLI 自己的 ~/.dsh——
	// 指错就看不到 Desktop 的会话，而且这件事不会自己报错。
	if want := "/Users/tester/Library/Application Support/dsh-desktop/harness"; config.DSHHome != want {
		t.Fatalf("DSHHome got %q want %q", config.DSHHome, want)
	}
	if want := "/Users/tester/Library/Logs/DSH Desktop/harness.log"; config.DSHLog != want {
		t.Fatalf("DSHLog got %q want %q", config.DSHLog, want)
	}

	t.Setenv("FLEET_DSH_ENABLED", "0")
	if loadConfig().DSHEnabled {
		t.Fatal("显式设 0 时应关闭")
	}
}
