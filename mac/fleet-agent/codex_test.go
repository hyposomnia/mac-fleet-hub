package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 用临时 CodexHome 搭最小数据集，验证 scanCodexSessions 只列「desktop app 活跃会话」：
//   originator=="Codex Desktop"（排除命令行 codex-tui/codex_exec）、source 为字符串（排除
//   subagent 子代理，其 source 是对象）、且未归档（rollout 不在 archived_sessions）。
//   标题优先 session_index 的 thread_name，否则取首条「非注入」user 文本；cwd 取自 session_meta。
func TestScanCodexActiveDesktopOnly(t *testing.T) {
	home := t.TempDir()
	cfg.CodexHome = home

	idA := "019e0000-0000-7000-8000-00000000000a"   // Desktop + 在 index → index 标题
	idB := "019e0000-0000-7000-8000-00000000000b"   // Desktop + 不在 index → 首条非注入 user 标题
	idSub := "019e0000-0000-7000-8000-00000000005b" // Desktop 但 source 为对象（subagent）→ 排除
	idCli := "019e0000-0000-7000-8000-00000000c111" // codex-tui 命令行 → 排除
	idArc := "019e0000-0000-7000-8000-0000000000ac" // Desktop 但已归档 → 排除

	os.WriteFile(filepath.Join(home, "session_index.jsonl"),
		[]byte(`{"id":"`+idA+`","thread_name":"索引标题","updated_at":"2026-06-18T08:18:37Z"}`+"\n"), 0644)

	dir := filepath.Join(home, "sessions", "2026", "06", "18")
	os.MkdirAll(dir, 0755)
	// originator/source 直接拼进 session_meta；srcJSON 传字符串字面量 `"vscode"` 或对象 `{...}`
	write := func(id, originator, srcJSON string, msgs ...string) {
		var b strings.Builder
		b.WriteString(`{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/proj/` + id[len(id)-1:] + `","originator":"` + originator + `","source":` + srcJSON + `,"timestamp":"2026-06-18T08:18:37Z"}}` + "\n")
		for _, m := range msgs {
			mb, _ := json.Marshal(m)
			b.WriteString(`{"type":"response_item","payload":{"role":"user","content":` + string(mb) + `}}` + "\n")
		}
		os.WriteFile(filepath.Join(dir, "rollout-2026-06-18T08-18-37-"+id+".jsonl"), []byte(b.String()), 0644)
	}
	write(idA, "Codex Desktop", `"vscode"`, "<environment_context>\n  <cwd>x", "真实首条A")
	write(idB, "Codex Desktop", `"vscode"`, "<environment_context>\n  <cwd>x", "把本地ssh服务器设置上")
	write(idSub, "Codex Desktop", `{"subagent":{"agent_role":"worker"}}`, "真实首条sub")
	write(idCli, "codex-tui", `"cli"`, "真实首条cli")
	write(idArc, "Codex Desktop", `"vscode"`, "真实首条arc")
	os.MkdirAll(filepath.Join(home, "archived_sessions"), 0755)
	os.WriteFile(filepath.Join(home, "archived_sessions", "rollout-2026-06-01T00-00-00-"+idArc+".jsonl"), []byte("{}\n"), 0644)

	got := scanCodexSessions()
	byID := map[string]Session{}
	for _, s := range got {
		byID[s.SessionID] = s
	}
	if len(got) != 2 {
		t.Fatalf("应只返回 2 个活跃会话(A,B)，得到 %d: %+v", len(got), got)
	}
	if byID[idA].Title != "索引标题" {
		t.Errorf("A 标题应取 index thread_name，得到 %q", byID[idA].Title)
	}
	if byID[idB].Title != "把本地ssh服务器设置上" {
		t.Errorf("B 标题应取首条非注入 user，得到 %q", byID[idB].Title)
	}
	if _, ok := byID[idSub]; ok {
		t.Errorf("subagent 子代理（source 对象）不应出现")
	}
	if _, ok := byID[idCli]; ok {
		t.Errorf("命令行 codex-tui 会话不应出现")
	}
	if _, ok := byID[idArc]; ok {
		t.Errorf("已归档会话不应出现")
	}
	for _, s := range got {
		if s.Cwd == "" {
			t.Errorf("不应有空 cwd（会渲染成「未知项目」）: %+v", s)
		}
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
