package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func usageTestDay(t *testing.T) (time.Time, time.Time) {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 17, 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1)
}
func TestDailyUsageCodexCumulativeAndQuotaDuplicates(t *testing.T) {
	start, end := usageTestDay(t)
	rows := `{"type":"session_meta","payload":{"id":"thread"}}
{"type":"turn_context","payload":{"model":"gpt-5"}}
{"timestamp":"2026-10-16T15:59:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":20},"last_token_usage":{"input_tokens":100,"output_tokens":20}}}}
{"timestamp":"2026-10-16T16:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":250,"output_tokens":40,"cached_input_tokens":100},"last_token_usage":{"input_tokens":150,"output_tokens":20,"cached_input_tokens":100}}}}
{"timestamp":"2026-10-16T16:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":250,"output_tokens":40,"cached_input_tokens":100},"last_token_usage":{"input_tokens":150,"output_tokens":20,"cached_input_tokens":100}}}}
{"type":"turn_context","payload":{"model":"gpt-6"}}
{"timestamp":"2026-10-17T12:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":450,"output_tokens":70,"cached_input_tokens":200},"last_token_usage":{"input_tokens":200,"output_tokens":30,"cached_input_tokens":100}}}}
{"timestamp":"2026-10-17T16:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":600,"output_tokens":100},"last_token_usage":{"input_tokens":150,"output_tokens":30}}}}`
	got, err := readDailyUsage(context.Background(), strings.NewReader(rows), "codex", "file", start, end)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	total := int64(0)
	for _, row := range got {
		total += row.usage.TotalTokens
		if row.usage.CachedInputTokens != 100 {
			t.Fatal(row)
		}
	}
	if total != 400 {
		t.Fatalf("got %d want 400 (input incl cached + output)", total)
	}
}
func TestDailyUsageClaudeDeduplicatesBlocksAndIncludesCache(t *testing.T) {
	start, end := usageTestDay(t)
	rows := `{"timestamp":"2026-10-17T00:00:00Z","type":"assistant","requestId":"r","message":{"id":"m","model":"sonnet","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}
{"timestamp":"2026-10-17T00:00:00Z","type":"assistant","requestId":"r","message":{"id":"m","model":"sonnet","usage":{"input_tokens":10,"output_tokens":15,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}
{"timestamp":"2026-10-17T00:00:00Z","type":"user","message":{"usage":{"input_tokens":999}}}
{"timestamp":"2026-10-17T00:00`
	got, err := readDailyUsage(context.Background(), strings.NewReader(rows), "claude", "file", start, end)
	if err != nil || len(got) != 1 || got[0].usage.TotalTokens != 145 || got[0].usage.InputTokens != 130 {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestDailyUsageDSHPackedSessionAndMessageDedup(t *testing.T) {
	start, end := usageTestDay(t)
	rows := `{"type":"session","id":"s"}
{"type":"request/header","data":{"header":{"config":{"model":"deepseek-flash"}}}}
{"type":"assistant/chunk","time":1792195200000,"data":{"turn":1,"step":2,"chunk":{"type":"usage","usage":{"inputTokens":10,"outputTokens":20,"cacheReadTokens":30,"reasoningTokens":10,"totalTokens":60}}}}
{"type":"assistant/message","time":1792195200001,"data":{"turn":1,"step":2,"usage":{"inputTokens":10,"outputTokens":20,"cacheReadTokens":30,"reasoningTokens":10,"totalTokens":60}}}`
	got, err := readDailyUsage(context.Background(), strings.NewReader(rows), "dsh", "f", start, end)
	if err != nil || len(got) != 1 || got[0].usage.TotalTokens != 60 || got[0].usage.Model != "deepseek-flash" {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestDailyUsageFileCacheInvalidatesAndCompressedSubagentsCount(t *testing.T) {
	start, _ := usageTestDay(t)
	root := t.TempDir()
	file := filepath.Join(root, "session.v4.jsonl.zstd")
	write := func(output string) {
		t.Helper()
		z, err := zstd.NewWriter(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer z.Close()
		data := `{"type":"session","id":"subagent"}` + "\n" + `{"type":"request/context","data":{"model":"deepseek"}}` + "\n" + `{"type":"assistant/message","time":1792195200000,"data":{"turn":1,"step":1,"usage":{"inputTokens":10,"outputTokens":` + output + `}}}`
		if err := os.WriteFile(file, z.EncodeAll([]byte(data), nil), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("20")
	homes := map[string][]string{"dsh": {root}, "claude": {filepath.Join(root, "not-installed")}}
	first := collectDailyUsage(context.Background(), start.Location(), start.Add(time.Hour), homes)
	if first.Partial || first.TotalTokens != 30 {
		t.Fatalf("%+v", first)
	}
	write("300")
	second := collectDailyUsage(context.Background(), start.Location(), start.Add(time.Hour), homes)
	if second.TotalTokens != 310 {
		t.Fatalf("stale cache %+v", second)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if got := collectDailyUsage(context.Background(), start.Location(), start.Add(time.Hour), homes); got.TotalTokens != 0 {
		t.Fatalf("deleted record retained %+v", got)
	}
}
func TestDailyUsageDSTBoundaryAndCancellation(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	if end.Sub(start) != 25*time.Hour {
		t.Fatal("fixture is not DST fall-back day")
	}
	rows := `{"timestamp":"2026-11-02T04:30:00Z","type":"assistant","message":{"id":"m","model":"opus","usage":{"input_tokens":10}}}`
	got, err := readDailyUsage(context.Background(), strings.NewReader(rows), "claude", "f", start, end)
	if err != nil || len(got) != 1 {
		t.Fatalf("DST last hour lost %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readDailyUsage(ctx, strings.NewReader(rows), "claude", "f", start, end); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestDailyUsageSkipsLargeNonAccountingRecords(t *testing.T) {
	start, end := usageTestDay(t)
	body := `{"timestamp":"2026-10-17T00:00:00Z","type":"response_item","payload":{"type":"message","text":"` + strings.Repeat("x", 17<<20) + `"}}` + "\n"
	rows := body + `{"type":"turn_context","payload":{"model":"gpt-5"}}` + "\n" + `{"timestamp":"2026-10-17T01:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"output_tokens":20}}}}`
	got, err := readDailyUsage(context.Background(), strings.NewReader(rows), "codex", "f", start, end)
	if err != nil || len(got) != 1 || got[0].usage.TotalTokens != 120 {
		t.Fatalf("usage after large record lost: %+v %v", got, err)
	}
}

// Opt-in read-only smoke check; never logs transcripts, session IDs or paths.
func TestDailyUsageLocalSmoke(t *testing.T) {
	if os.Getenv("FLEET_USAGE_SMOKE") != "1" {
		t.Skip("opt-in real host accounting")
	}
	c := loadConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	started := time.Now()
	got := collectDailyUsage(ctx, time.Local, started, map[string][]string{
		"claude": {filepath.Join(c.ClaudeHome, "projects")},
		"codex":  {filepath.Join(c.CodexHome, "sessions"), filepath.Join(c.CodexHome, "archived_sessions")},
		"dsh":    {filepath.Join(c.DSHHome, "sessions")},
	})
	t.Logf("date=%s models=%d totalTokens=%d partial=%t warnings=%v elapsed=%s", got.Date, len(got.Models), got.TotalTokens, got.Partial, got.Warnings, time.Since(started).Round(time.Millisecond))
	if got.Partial {
		start := time.Date(started.Year(), started.Month(), started.Day(), 0, 0, 0, 0, time.Local)
		for _, root := range []string{filepath.Join(c.CodexHome, "sessions"), filepath.Join(c.CodexHome, "archived_sessions")} {
			filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
					return nil
				}
				st, e := d.Info()
				if e != nil || st.ModTime().Before(start) {
					return nil
				}
				f, e := os.Open(path)
				if e != nil {
					t.Log("codex log not readable")
					return nil
				}
				defer f.Close()
				_, e = readDailyUsage(context.Background(), f, "codex", "smoke", start, start.AddDate(0, 0, 1))
				if e != nil {
					t.Logf("codex scan error (no filename): %v; file bytes=%d", e, st.Size())
				}
				return nil
			})
		}
		t.Fatal("local usage scan incomplete")
	}
}
