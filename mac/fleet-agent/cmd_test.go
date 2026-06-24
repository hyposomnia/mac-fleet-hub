package main

import "testing"

// F1：Bypass连接 = claude 带 --dangerously-skip-permissions。这是安全边界，
// 用单测钉死「普通连接不带、bypass 才带」与标志位置，避免回归。
func TestClaudeResumeCmd(t *testing.T) {
	cfg = Config{ClaudeBin: "claude"} // 代理默认关，proxyEnvPrefix() 为空

	normal := claudeResumeCmd("abc-123", false)
	want := "claude --resume 'abc-123'"
	if normal != want {
		t.Fatalf("normal resume: got %q want %q", normal, want)
	}

	bypass := claudeResumeCmd("abc-123", true)
	wantB := "claude --resume 'abc-123' --dangerously-skip-permissions"
	if bypass != wantB {
		t.Fatalf("bypass resume: got %q want %q", bypass, wantB)
	}
}

func TestClaudeNewCmd(t *testing.T) {
	cfg = Config{ClaudeBin: "claude"}

	if got := claudeNewCmd(false); got != "claude" {
		t.Fatalf("normal new: got %q want %q", got, "claude")
	}
	if got := claudeNewCmd(true); got != "claude --dangerously-skip-permissions" {
		t.Fatalf("bypass new: got %q want %q", got, "claude --dangerously-skip-permissions")
	}
}
