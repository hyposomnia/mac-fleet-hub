package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 用临时 CodexHome 搭一个最小数据集：session_index.jsonl（desktop app 权威会话列表）
// + 两个 rollout（一个在 index 内、一个不在）。验证 scanCodexSessions：
//   ① 只返回 index 内的会话（不在册的 rollout 不混入）；
//   ② 标题取 index 的 thread_name（不取 rollout 里注入的 # AGENTS.md/env 文本）；
//   ③ cwd 从匹配 rollout 的 session_meta 补全。
func TestScanCodexDrivesFromDesktopIndex(t *testing.T) {
	home := t.TempDir()
	cfg.CodexHome = home

	idA := "019e0000-0000-7000-8000-00000000000a"
	idB := "019e0000-0000-7000-8000-00000000000b" // 不在 index → 应被排除
	os.WriteFile(filepath.Join(home, "session_index.jsonl"),
		[]byte(`{"id":"`+idA+`","thread_name":"真实标题","updated_at":"2026-05-18T08:18:37.970572Z"}`+"\n"), 0644)

	dir := filepath.Join(home, "sessions", "2026", "05", "18")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "rollout-2026-05-18T08-18-37-"+idA+".jsonl"), []byte(
		`{"type":"session_meta","payload":{"id":"`+idA+`","cwd":"/Users/x/proj","timestamp":"2026-05-18T08:18:37.970572Z"}}`+"\n"+
			`{"type":"response_item","payload":{"role":"user","content":"# AGENTS.md instructions for /Users/x/proj"}}`+"\n"), 0644)
	os.WriteFile(filepath.Join(dir, "rollout-2026-05-18T09-00-00-"+idB+".jsonl"), []byte(
		`{"type":"session_meta","payload":{"id":"`+idB+`","cwd":"/Users/x/junk"}}`+"\n"), 0644)

	got := scanCodexSessions()
	if len(got) != 1 {
		t.Fatalf("应只返回 index 内的 1 个会话（排除未在册 rollout），得到 %d", len(got))
	}
	s := got[0]
	if s.SessionID != idA {
		t.Fatalf("sessionId=%s want %s", s.SessionID, idA)
	}
	if s.Title != "真实标题" {
		t.Fatalf("title 应取 index thread_name，得到 %q", s.Title)
	}
	if s.Cwd != "/Users/x/proj" {
		t.Fatalf("cwd 应取 rollout session_meta，得到 %q", s.Cwd)
	}
	if s.Assistant != "codex" {
		t.Fatalf("assistant=%q", s.Assistant)
	}
}

func TestCodexIDFromName(t *testing.T) {
	id := "019e865e-55cc-7362-9cd4-77b6fdf68509"
	if got := codexIDFromName("rollout-2026-06-02T11-26-29-" + id + ".jsonl"); got != id {
		t.Fatalf("codexIDFromName=%q want %q", got, id)
	}
	if got := codexIDFromName("garbage.jsonl"); got != "" {
		t.Fatalf("非法文件名应返回空，得到 %q", got)
	}
}
