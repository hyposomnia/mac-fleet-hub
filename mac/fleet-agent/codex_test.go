package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 用临时 CodexHome 搭最小数据集，验证 scanCodexSessions 只列「desktop app 活跃会话」：
//
//	originator=="Codex Desktop"（排除命令行 codex-tui/codex_exec）、source 为字符串（排除
//	subagent 子代理，其 source 是对象）、且未归档（rollout 不在 archived_sessions）。
//	标题优先 session_index 的 thread_name，否则取首条「非注入」user 文本；cwd 取自 session_meta。
func TestScanCodexActiveDesktopOnly(t *testing.T) {
	home := t.TempDir()
	cfg.CodexHome = home

	idA := "019e0000-0000-7000-8000-00000000000a"   // Desktop + 在 index → index 标题
	idB := "019e0000-0000-7000-8000-00000000000b"   // Desktop + 不在 index → 首条非注入 user 标题
	idCmd := "019e0000-0000-7000-8000-0000000000cd" // Desktop + slash command envelope → 清洗成可读标题
	idIdxCmd := "019e0000-0000-7000-8000-00000000c0de"
	idSub := "019e0000-0000-7000-8000-00000000005b" // Desktop 但 source 为对象（subagent）→ 排除
	idCli := "019e0000-0000-7000-8000-00000000c111" // codex-tui 命令行 → 排除
	idArc := "019e0000-0000-7000-8000-0000000000ac" // Desktop 但已归档 → 排除

	var idx strings.Builder
	for _, row := range []struct {
		id, title string
	}{
		{idA, "索引标题"},
		{idIdxCmd, "<command-message>deploy</command-message>\n<command-name>/deploy</command-name>"},
	} {
		b, _ := json.Marshal(map[string]string{
			"id":          row.id,
			"thread_name": row.title,
			"updated_at":  "2026-06-18T08:18:37Z",
		})
		idx.Write(b)
		idx.WriteByte('\n')
	}
	os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(idx.String()), 0644)

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
	write(idB, "Codex Desktop", `"vscode"`, "<environment_context>\n  <cwd>x", "<turn_aborted>", "把本地ssh服务器设置上")
	write(idCmd, "Codex Desktop", `"vscode"`, "<environment_context>\n  <cwd>x", "<command-message>dev</command-message>\n<command-name>/dev</command-name>\n<command-args>修修 session 名字</command-args>")
	write(idIdxCmd, "Codex Desktop", `"vscode"`, "真实首条idx")
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
	if len(got) != 4 {
		t.Fatalf("应只返回 4 个活跃会话(A,B,Cmd,IdxCmd)，得到 %d: %+v", len(got), got)
	}
	if byID[idA].Title != "索引标题" {
		t.Errorf("A 标题应取 index thread_name，得到 %q", byID[idA].Title)
	}
	if byID[idB].Title != "把本地ssh服务器设置上" {
		t.Errorf("B 标题应取首条非注入 user，得到 %q", byID[idB].Title)
	}
	if byID[idCmd].Title != "修修 session 名字" {
		t.Errorf("slash command 标题应取 command-args，得到 %q", byID[idCmd].Title)
	}
	if byID[idIdxCmd].Title != "/deploy" {
		t.Errorf("index 里的 slash command 标题应清洗成 command-name，得到 %q", byID[idIdxCmd].Title)
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

func TestCodexRolloutMetaSkipsFallbackTitleWhenIndexHasTitle(t *testing.T) {
	dir := t.TempDir()
	id := "019e865e-55cc-7362-9cd4-77b6fdf68509"
	path := filepath.Join(dir, "rollout-2026-06-02T11-26-29-"+id+".jsonl")
	body := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/proj/a","originator":"Codex Desktop","source":"vscode","timestamp":"2026-06-18T08:18:37Z"}}`,
		`{"type":"response_item","payload":{"role":"user","content":"fallback title"}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	gotID, cwd, _, originator, srcStr, title := codexRolloutMeta(path, false)
	if gotID != id || cwd != "/proj/a" || originator != "Codex Desktop" || !srcStr {
		t.Fatalf("meta 解析异常: id=%q cwd=%q originator=%q srcStr=%v", gotID, cwd, originator, srcStr)
	}
	if title != "" {
		t.Fatalf("needTitle=false 时不应读取 fallback title，得到 %q", title)
	}

	_, _, _, _, _, title = codexRolloutMeta(path, true)
	if title != "fallback title" {
		t.Fatalf("needTitle=true 应保持 fallback title，得到 %q", title)
	}
}

func TestCodexThreadRowUsesIndexTitleAndRecencyTime(t *testing.T) {
	row := codexThreadRow{
		ID:           "019e865e-55cc-7362-9cd4-77b6fdf68509",
		Title:        "这是一条非常长的首条用户消息，不能直接当列表标题",
		Preview:      "预览",
		Cwd:          "/repo",
		GitBranch:    "main",
		Source:       "vscode",
		ThreadSource: "user",
		UpdatedAt:    100,
		UpdatedAtMs:  200_000,
		RecencyAtMs:  300_000,
	}
	idx := map[string]codexIdx{row.ID: {title: "短标题", mtime: 123}}

	got, ok := codexSessionFromThreadRow(row, idx)
	if !ok {
		t.Fatal("row should be accepted")
	}
	if got.Title != "短标题" {
		t.Fatalf("title got %q", got.Title)
	}
	if got.Mtime != 300_000 {
		t.Fatalf("mtime got %d", got.Mtime)
	}
	if got.Cwd != "/repo" || got.GitBranch != "main" {
		t.Fatalf("bad session: %+v", got)
	}
	if !got.Live {
		t.Fatalf("row with post-create activity should be active: %+v", got)
	}
}

func TestCodexThreadRowRejectsSubagents(t *testing.T) {
	for _, row := range []codexThreadRow{
		{ID: "019e865e-55cc-7362-9cd4-77b6fdf68509", Source: `{"subagent":{"other":"guardian"}}`, ThreadSource: "user"},
		{ID: "019e865e-55cc-7362-9cd4-77b6fdf68509", Source: "vscode", ThreadSource: "subagent"},
	} {
		if _, ok := codexSessionFromThreadRow(row, nil); ok {
			t.Fatalf("subagent row should be rejected: %+v", row)
		}
	}
}

func TestCodexThreadTitleSkipsSystemTitles(t *testing.T) {
	row := codexThreadRow{
		ID:      "019e865e-55cc-7362-9cd4-77b6fdf68509",
		Title:   "The user interrupted the previous turn on purpose. Any running work should stop.",
		Preview: "<codex_delegation>\n  <source_thread_id>abc</source_thread_id>",
	}
	if got := codexThreadTitle(row, nil); got != "(无标题)" {
		t.Fatalf("system title should be skipped, got %q", got)
	}
}

func TestCodexImportedHistoryWithoutActivityIsNotActive(t *testing.T) {
	row := codexThreadRow{
		ID:           "019e865e-55cc-7362-9cd4-77b6fdf68509",
		Title:        "导入历史",
		Source:       "vscode",
		ThreadSource: "user",
		CreatedAtMs:  100_000,
		UpdatedAtMs:  100_000,
		RecencyAtMs:  100_000,
	}
	got, ok := codexSessionFromThreadRow(row, nil)
	if !ok {
		t.Fatal("row should be accepted")
	}
	if got.Live {
		t.Fatalf("imported history without later activity should not be active: %+v", got)
	}
}

func TestCodexOpenedPtyIsActiveEvenWhenHistoryDormant(t *testing.T) {
	idOpen := "019e865e-55cc-7362-9cd4-77b6fdf68509"
	idHistory := "029e865e-55cc-7362-9cd4-77b6fdf68510"
	all := []Session{
		{SessionID: idOpen, Assistant: "codex", Live: false},
		{SessionID: idHistory, Assistant: "codex", Live: false},
	}
	markSessionRuntime("codex", all, map[string]bool{shortSidFor("codex", idOpen): true}, nil)
	if !all[0].Live || !all[0].Pty {
		t.Fatalf("opened Codex pty should remain active: %+v", all[0])
	}
	if all[1].Live || all[1].Pty {
		t.Fatalf("unopened Codex history should not be active: %+v", all[1])
	}
}
