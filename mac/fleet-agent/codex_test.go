package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 用临时 CodexHome 搭最小数据集，验证 scanCodexSessions 只列「desktop app 活跃会话」：
//   thread_source==user（排除 subagent 子代理线程）且未归档（rollout 不在 archived_sessions）。
//   标题优先 session_index 的 thread_name，否则取首条「非注入」user 文本；cwd 取自 session_meta。
func TestScanCodexActiveUserNonArchived(t *testing.T) {
	home := t.TempDir()
	cfg.CodexHome = home

	idA := "019e0000-0000-7000-8000-00000000000a"   // user + 在 index → index 标题
	idB := "019e0000-0000-7000-8000-00000000000b"   // user + 不在 index → 首条非注入 user 标题
	idSub := "019e0000-0000-7000-8000-00000000005b" // subagent → 排除
	idArc := "019e0000-0000-7000-8000-0000000000ac" // user 但已归档 → 排除

	os.WriteFile(filepath.Join(home, "session_index.jsonl"),
		[]byte(`{"id":"`+idA+`","thread_name":"索引标题","updated_at":"2026-06-18T08:18:37Z"}`+"\n"), 0644)

	dir := filepath.Join(home, "sessions", "2026", "06", "18")
	os.MkdirAll(dir, 0755)
	write := func(id, src string, msgs ...string) {
		var b strings.Builder
		b.WriteString(`{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/proj/` + id[len(id)-1:] + `","thread_source":"` + src + `","timestamp":"2026-06-18T08:18:37Z"}}` + "\n")
		for _, m := range msgs {
			mb, _ := json.Marshal(m)
			b.WriteString(`{"type":"response_item","payload":{"role":"user","content":` + string(mb) + `}}` + "\n")
		}
		os.WriteFile(filepath.Join(dir, "rollout-2026-06-18T08-18-37-"+id+".jsonl"), []byte(b.String()), 0644)
	}
	write(idA, "user", "<environment_context>\n  <cwd>x", "真实首条A")
	write(idB, "user", "<environment_context>\n  <cwd>x", "把本地ssh服务器设置上")
	write(idSub, "subagent", "真实首条sub")
	write(idArc, "user", "真实首条arc")
	// idArc 已归档：archived_sessions 放一个同 id 的 rollout（平铺）
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
		t.Errorf("subagent 子代理线程不应出现")
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
