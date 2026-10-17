package main

import (
	"os"
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

// dsh CLI / web harness 与 Desktop 是两个独立根：默认开启，home 指 ~/.dsh，
// 且不继承 Desktop 的日志路径（CLI 只在终端打印 URL，端点靠端口发现）。
func TestLoadConfigDefaultsForDeepSeekHarness(t *testing.T) {
	t.Setenv("HOME", "/Users/tester")
	t.Setenv("FLEET_DEEPSEEK_ENABLED", "")
	t.Setenv("FLEET_DEEPSEEK_HOME", "")
	t.Setenv("FLEET_DEEPSEEK_LOG", "")
	t.Setenv("FLEET_DEEPSEEK_ENDPOINT", "")

	config := loadConfig()
	if !config.DeepSeekEnabled {
		t.Fatal("DeepSeek harness 默认应为开启（显式设 0 才退出）")
	}
	if want := "/Users/tester/.dsh"; config.DeepSeekHome != want {
		t.Fatalf("DeepSeekHome got %q want %q", config.DeepSeekHome, want)
	}
	if config.DeepSeekLog != "" {
		t.Fatalf("DeepSeekLog 默认应为空（不做日志发现），got %q", config.DeepSeekLog)
	}
	if config.DeepSeekEndpoint != "" {
		t.Fatalf("DeepSeekEndpoint 默认应为空，got %q", config.DeepSeekEndpoint)
	}

	t.Setenv("FLEET_DEEPSEEK_ENABLED", "0")
	if loadConfig().DeepSeekEnabled {
		t.Fatal("显式设 0 时应关闭")
	}
}
