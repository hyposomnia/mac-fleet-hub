package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexLastOutputEndMsUsesLatestTaskComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	data := strings.Join([]string{
		`{"timestamp":"2026-08-09T03:40:00Z","type":"event_msg","payload":{"type":"task_complete"}}`,
		`{"timestamp":"2026-08-09T03:50:00Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-08-09T03:59:40.123Z","type":"event_msg","payload":{"type":"task_complete"}}`,
		`{"timestamp":"2026-08-09T03:59:40.124Z","type":"event_msg","payload":{"type":"token_count"}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	want := parseTimeMs("2026-08-09T03:59:40.123Z")
	if got := codexLastOutputEndMs(path); got != want {
		t.Fatalf("output end got %d, want %d", got, want)
	}
}

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
	idFleet := "019e0000-0000-7000-8000-00000000f1ee" // mac-fleet-hub 自绘会话 → 保留
	idSub := "019e0000-0000-7000-8000-00000000005b"   // Desktop 但 source 为对象（subagent）→ 排除
	idCli := "019e0000-0000-7000-8000-00000000c111"   // codex-tui 命令行 → 排除
	idArc := "019e0000-0000-7000-8000-0000000000ac"   // Desktop 但已归档 → 排除

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
	write(idFleet, "mac_fleet_hub", `"vscode"`, "自绘新会话")
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
	if len(got) != 5 {
		t.Fatalf("应只返回 5 个活跃会话(A,B,Cmd,IdxCmd,Fleet)，得到 %d: %+v", len(got), got)
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
	if byID[idFleet].Title != "自绘新会话" {
		t.Errorf("mac-fleet-hub 自绘会话应保留，得到 %+v", byID[idFleet])
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
		CreatedAtMs:  100_000,
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

func TestCodexThreadRowRejectsObjectSourceSubagents(t *testing.T) {
	for _, row := range []codexThreadRow{
		{ID: "019e865e-55cc-7362-9cd4-77b6fdf68509", Source: `{"subagent":{"other":"guardian"}}`, ThreadSource: "user"},
		{ID: "019e865e-55cc-7362-9cd4-77b6fdf68509", Source: `{"subagent":{"other":"guardian"}}`, ThreadSource: "subagent"},
	} {
		if _, ok := codexSessionFromThreadRow(row, nil); ok {
			t.Fatalf("subagent row should be rejected: %+v", row)
		}
	}
}

func TestCodexThreadRowKeepsMigratedStringSourceThread(t *testing.T) {
	dir := t.TempDir()
	id := "019e865e-55cc-7362-9cd4-77b6fdf68509"
	path := filepath.Join(dir, "rollout-2026-06-02T11-26-29-"+id+".jsonl")
	body := `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/proj/a","originator":"Codex Desktop","source":"vscode","timestamp":"2026-06-18T08:18:37Z"}}`
	if err := os.WriteFile(path, []byte(body+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	row := codexThreadRow{
		ID:           id,
		Title:        "Emoji",
		Source:       "vscode",
		ThreadSource: "subagent",
		RolloutPath:  path,
		CreatedAtMs:  100_000,
		UpdatedAtMs:  105_000,
		RecencyAtMs:  105_000,
	}
	got, ok := codexSessionFromThreadRow(row, nil)
	if !ok {
		t.Fatal("migrated string-source thread should remain visible")
	}
	if got.Live {
		t.Fatalf("migrated thread without later activity should stay out of active: %+v", got)
	}
}

func TestScanCodexSQLiteKeepsMigratedStringSourceThread(t *testing.T) {
	home := t.TempDir()
	previousHome := cfg.CodexHome
	cfg.CodexHome = home
	t.Cleanup(func() { cfg.CodexHome = previousHome })

	userID := "019e865e-55cc-7362-9cd4-77b6fdf68501"
	migratedID := "019e865e-55cc-7362-9cd4-77b6fdf68502"
	writeRollout := func(id string) string {
		path := filepath.Join(home, "rollout-"+id+".jsonl")
		body := `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/repo","originator":"Codex Desktop","source":"vscode"}}`
		if err := os.WriteFile(path, []byte(body+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(path, "'", "''")
	}
	userRollout := writeRollout(userID)
	migratedRollout := writeRollout(migratedID)
	db := filepath.Join(home, "state_5.sqlite")
	schema := `create table threads (
id text, title text, preview text, cwd text, git_branch text, rollout_path text,
source text, thread_source text, archived integer, created_at integer,
created_at_ms integer, updated_at integer, updated_at_ms integer, recency_at_ms integer
);` +
		`insert into threads values ('` + userID + `','User','','/repo','main','` + userRollout + `','vscode','user',0,0,100000,0,300000,300000);` +
		`insert into threads values ('` + migratedID + `','Migrated','','/repo','main','` + migratedRollout + `','vscode','subagent',0,0,100000,0,200000,200000);`
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sqlite fixture: %v: %s", err, out)
	}

	got := scanCodexSQLiteSessions(nil)
	if len(got) != 2 {
		t.Fatalf("Desktop-visible user and migrated threads should both be returned, got %d: %+v", len(got), got)
	}
}

func TestCodexThreadRowRejectsNonDesktopSources(t *testing.T) {
	for _, source := range []string{"cli", "exec", "appServer", "unknown"} {
		t.Run(source, func(t *testing.T) {
			if _, ok := codexSessionFromThreadRow(codexThreadRow{
				ID: "019e865e-55cc-7362-9cd4-77b6fdf68509", Source: source,
			}, nil); ok {
				t.Fatalf("source %q should not appear in Desktop sessions", source)
			}
		})
	}
}

func TestCodexThreadRowFiltersVSCodePluginOriginator(t *testing.T) {
	for _, tc := range []struct {
		originator string
		want       bool
	}{
		{originator: "codex_vscode", want: false},
		{originator: "Codex Desktop", want: true},
		{originator: "codex-tui", want: false},
		{originator: "codex_exec", want: false},
	} {
		t.Run(tc.originator, func(t *testing.T) {
			dir := t.TempDir()
			id := "019e865e-55cc-7362-9cd4-77b6fdf68509"
			path := filepath.Join(dir, "rollout-2026-06-02T11-26-29-"+id+".jsonl")
			body := `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/proj/a","originator":"` + tc.originator + `","source":"vscode","timestamp":"2026-06-18T08:18:37Z"}}`
			if err := os.WriteFile(path, []byte(body+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			_, got := codexSessionFromThreadRow(codexThreadRow{
				ID:           id,
				Title:        "会话",
				Source:       "vscode",
				ThreadSource: "user",
				RolloutPath:  path,
			}, nil)
			if got != tc.want {
				t.Fatalf("originator %q visibility=%v want %v", tc.originator, got, tc.want)
			}
		})
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

func TestCodexThreadWithPromptIsActiveImmediately(t *testing.T) {
	row := codexThreadRow{
		ID:           "019e865e-55cc-7362-9cd4-77b6fdf68509",
		Preview:      "刚发送的第一条消息",
		Source:       "vscode",
		ThreadSource: "user",
		CreatedAtMs:  100_000,
		UpdatedAtMs:  101_000,
		RecencyAtMs:  101_000,
	}
	got, ok := codexSessionFromThreadRow(row, nil)
	if !ok {
		t.Fatal("thread with prompt should be accepted")
	}
	if !got.Live {
		t.Fatalf("thread with a real prompt should become active immediately: %+v", got)
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

func TestCodexRolloutLifecycleDrivesSessionListRuntime(t *testing.T) {
	previousBackend := agentChatBackend
	agentChatBackend = newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return nil, nil, errAppServerUnavailable
	})
	t.Cleanup(func() { agentChatBackend = previousBackend })
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-live"}}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	paths := map[string]string{"thread-1": path}
	active := []Session{{SessionID: "thread-1", Assistant: "codex", Status: "notLoaded"}}
	markSessionRuntime("codex", active, nil, paths)
	if !active[0].Live || active[0].Status != "active" {
		t.Fatalf("unfinished rollout should be active in the session list: %+v", active[0])
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString(
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-live"}}` + "\n")
	closeErr := f.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	idle := []Session{{SessionID: "thread-1", Assistant: "codex", Status: "notLoaded", Waiting: true}}
	markSessionRuntime("codex", idle, nil, paths)
	if idle[0].Live || idle[0].Status != "idle" || idle[0].Waiting {
		t.Fatalf("completed rollout should return the session list to idle: %+v", idle[0])
	}
}

func TestCodexSessionWaitingRequiresActionableFleetRequest(t *testing.T) {
	previousBackend := agentChatBackend
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return nil, nil, errAppServerUnavailable
	})
	agentChatBackend = backend
	t.Cleanup(func() { agentChatBackend = previousBackend })

	all := []Session{{SessionID: "thread-1", Assistant: "codex", Waiting: true, Status: "active"}}
	markSessionRuntime("codex", all, nil, nil)
	if all[0].Waiting {
		t.Fatalf("thread flag without a renderable Fleet request must not show waiting: %+v", all[0])
	}
	backend.pending["request-1"] = pendingCodexRequest{id: json.RawMessage(`61`), sessionID: "thread-1"}
	markSessionRuntime("codex", all, nil, nil)
	if !all[0].Waiting {
		t.Fatalf("actionable Fleet request must show waiting: %+v", all[0])
	}
}
