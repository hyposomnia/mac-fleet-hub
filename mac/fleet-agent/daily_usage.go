package main

// Usage is read from the assistants' own records, including sessions not opened
// in Fleet. No transcript text, path, price estimate or credential leaves the Mac.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

type dailyModelUsage struct {
	Assistant         string `json:"assistant"`
	Model             string `json:"model"`
	InputTokens       int64  `json:"inputTokens"`  // includes cache reads/writes
	OutputTokens      int64  `json:"outputTokens"` // includes reasoning, not added twice
	CachedInputTokens int64  `json:"cachedInputTokens"`
	CacheWriteTokens  int64  `json:"cacheWriteTokens"`
	TotalTokens       int64  `json:"totalTokens"`
}
type dailyUsageReport struct {
	Date        string            `json:"date"`
	Timezone    string            `json:"timezone"`
	GeneratedAt int64             `json:"generatedAt"`
	Models      []dailyModelUsage `json:"models"`
	TotalTokens int64             `json:"totalTokens"`
	Partial     bool              `json:"partial"`
	Warnings    []string          `json:"warnings"`
}
type dailyUsageRecord struct {
	key   string
	usage dailyModelUsage
}
type dailyFileCache struct {
	size, mtime int64
	records     []dailyUsageRecord
	err         bool
}

var dailyUsageCache = struct {
	sync.Mutex
	day   string
	files map[string]dailyFileCache
}{}

func usageNumber(v map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if n, ok := v[key].(float64); ok && n >= 0 && n <= 1e15 {
			return int64(n)
		}
	}
	return 0
}
func usageObject(v map[string]any, key string) map[string]any {
	x, _ := v[key].(map[string]any)
	return x
}
func usageString(v map[string]any, key string) string { x, _ := v[key].(string); return x }
func usageTime(v any) time.Time {
	if s, ok := v.(string); ok {
		t, _ := time.Parse(time.RFC3339Nano, s)
		return t
	}
	if n, ok := v.(float64); ok {
		return time.UnixMilli(int64(n))
	}
	return time.Time{}
}
func normalizedDailyUsage(assistant, model string, v map[string]any) dailyModelUsage {
	u := dailyModelUsage{Assistant: assistant, Model: model,
		InputTokens: usageNumber(v, "input_tokens", "inputTokens"), OutputTokens: usageNumber(v, "output_tokens", "outputTokens"),
		CachedInputTokens: usageNumber(v, "cached_input_tokens", "cache_read_input_tokens", "cacheReadTokens"),
		CacheWriteTokens:  usageNumber(v, "cache_creation_input_tokens")}
	if u.Model == "" {
		u.Model = "unknown"
	}
	if assistant == "claude" || assistant == "dsh" {
		u.InputTokens += u.CachedInputTokens + u.CacheWriteTokens
	}
	u.TotalTokens = u.InputTokens + u.OutputTokens
	return u
}

// Read just the leading top-level type. Rollouts put type before their payload;
// do not search arbitrary message text for a type marker. A very large image or
// tool result must not prevent later accounting events from being read.
func usageRowType(prefix []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(prefix))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return ""
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return ""
		}
		if key == "type" {
			var typ string
			if decoder.Decode(&typ) == nil {
				return typ
			}
			return ""
		}
		var skip json.RawMessage
		if decoder.Decode(&skip) != nil {
			return ""
		}
	}
	return ""
}

func usageLogSplit(assistant string) bufio.SplitFunc {
	discarding := false
	return func(data []byte, eof bool) (int, []byte, error) {
		if !discarding && len(data) >= 4096 && bytes.IndexByte(data, '\n') < 0 {
			typ := usageRowType(data[:min(len(data), 4096)])
			switch assistant {
			case "codex":
				discarding = typ != "" && typ != "event_msg" && typ != "turn_context" && typ != "session_meta"
			case "claude":
				discarding = typ != "" && typ != "assistant"
			case "dsh":
				discarding = typ != "" && typ != "session" && typ != "assistant/message" && typ != "assistant/chunk" && typ != "request/header" && typ != "request/context"
			}
		}
		if discarding {
			if i := bytes.IndexByte(data, '\n'); i >= 0 {
				discarding = false
				return i + 1, nil, nil
			}
			return len(data), nil, nil
		}
		return bufio.ScanLines(data, eof)
	}
}

// Codex totals are cumulative across requests. last_token_usage is repeated by
// quota updates; summing it blindly double counts. Claude streamed blocks share
// a message ID; retain one (the largest complete usage) instead of adding blocks.
func readDailyUsage(ctx context.Context, reader io.Reader, assistant, fallbackID string, start, end time.Time) ([]dailyUsageRecord, error) {
	rows := map[string]dailyModelUsage{}
	model, session := "", fallbackID
	var previous dailyModelUsage
	haveTotal := false
	limited := &io.LimitedReader{R: reader, N: (512 << 20) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	scanner.Split(usageLogSplit(assistant))
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data := scanner.Bytes()
		// Skip message/tool bodies before JSON allocation. Only accounting and
		// model/session metadata are relevant, even in very large rollouts.
		if assistant == "codex" && !bytes.Contains(data, []byte(`"token_count"`)) && !bytes.Contains(data, []byte(`"turn_context"`)) && !bytes.Contains(data, []byte(`"session_meta"`)) {
			continue
		}
		if assistant == "claude" && !bytes.Contains(data, []byte(`"usage"`)) {
			continue
		}
		if assistant == "dsh" && !bytes.Contains(data, []byte(`"usage"`)) && !bytes.Contains(data, []byte(`"request/header"`)) && !bytes.Contains(data, []byte(`"request/context"`)) && !bytes.Contains(data, []byte(`"session"`)) {
			continue
		}
		var row map[string]any
		if json.Unmarshal(data, &row) != nil {
			continue
		} // concurrent partial last write
		typ := usageString(row, "type")
		at := usageTime(row["timestamp"])
		var u dailyModelUsage
		key := ""
		switch assistant {
		case "claude":
			if typ != "assistant" {
				continue
			}
			msg := usageObject(row, "message")
			id := usageString(msg, "id")
			if id == "" {
				id = usageString(row, "uuid")
			}
			if id == "" {
				continue
			}
			u = normalizedDailyUsage(assistant, usageString(msg, "model"), usageObject(msg, "usage"))
			key = id + ":" + usageString(row, "requestId")
		case "codex":
			p := usageObject(row, "payload")
			if typ == "session_meta" {
				if id := usageString(p, "id"); id != "" {
					session = id
				}
			}
			if typ == "turn_context" {
				model = usageString(p, "model")
			}
			if typ != "event_msg" || usageString(p, "type") != "token_count" {
				continue
			}
			info := usageObject(p, "info")
			u = normalizedDailyUsage(assistant, model, usageObject(info, "last_token_usage"))
			if totals := usageObject(info, "total_token_usage"); totals != nil {
				current := normalizedDailyUsage(assistant, model, totals)
				if haveTotal && current.InputTokens >= previous.InputTokens && current.OutputTokens >= previous.OutputTokens {
					u.InputTokens = current.InputTokens - previous.InputTokens
					u.OutputTokens = current.OutputTokens - previous.OutputTokens
					u.CachedInputTokens = max(int64(0), current.CachedInputTokens-previous.CachedInputTokens)
					u.TotalTokens = u.InputTokens + u.OutputTokens
				}
				previous, haveTotal = current, true
				key = fmt.Sprintf("%s:%d:%d:%d", session, current.InputTokens, current.OutputTokens, current.CachedInputTokens)
			} else {
				key = fmt.Sprintf("%s:%s:%d:%d", session, usageString(row, "timestamp"), u.InputTokens, u.OutputTokens)
			}
		case "dsh":
			if typ == "session" {
				if id := usageString(row, "id"); id != "" {
					session = id
				}
				continue
			}
			at = usageTime(row["time"])
			p := usageObject(row, "data")
			if typ == "request/header" {
				model = usageString(usageObject(usageObject(p, "header"), "config"), "model")
			}
			if typ == "request/context" {
				model = usageString(p, "model")
			}
			v := usageObject(p, "usage")
			if typ == "assistant/chunk" {
				v = usageObject(usageObject(p, "chunk"), "usage")
			}
			if (typ != "assistant/message" && typ != "assistant/chunk") || v == nil {
				continue
			}
			u = normalizedDailyUsage(assistant, model, v)
			key = fmt.Sprintf("%s:%d:%d", session, usageNumber(p, "turn"), usageNumber(p, "step"))
		}
		if at.Before(start) || !at.Before(end) || u.TotalTokens == 0 || key == "" {
			continue
		}
		if old, ok := rows[key]; !ok || u.TotalTokens > old.TotalTokens {
			rows[key] = u
		}
		if len(rows) > 200000 {
			return nil, fmt.Errorf("daily usage record limit exceeded")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("usage log exceeds scan limit")
	}
	out := make([]dailyUsageRecord, 0, len(rows))
	for key, u := range rows {
		out = append(out, dailyUsageRecord{assistant + ":" + key, u})
	}
	return out, nil
}

func collectDailyUsage(ctx context.Context, loc *time.Location, now time.Time, homes map[string][]string) dailyUsageReport {
	day := now.In(loc).Format("2006-01-02")
	start, _ := time.ParseInLocation("2006-01-02", day, loc)
	end := start.AddDate(0, 0, 1) // DST days are not always 24 hours
	out := dailyUsageReport{Date: day, Timezone: loc.String(), GeneratedAt: now.UnixMilli(), Models: []dailyModelUsage{}, Warnings: []string{}}
	for !dailyUsageCache.TryLock() {
		select {
		case <-ctx.Done():
			out.Partial = true
			out.Warnings = append(out.Warnings, "scan:busy")
			return out
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer dailyUsageCache.Unlock()
	cacheKey := day + "/" + loc.String()
	if dailyUsageCache.day != cacheKey {
		dailyUsageCache.day = cacheKey
		dailyUsageCache.files = map[string]dailyFileCache{}
	}
	seenFiles, records := map[string]bool{}, map[string]dailyModelUsage{}
	for assistant, roots := range homes {
		failed := false
		for _, root := range roots {
			err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if walkErr != nil {
					if os.IsNotExist(walkErr) && path == root {
						return nil
					}
					failed = true
					return nil
				}
				if d.Type()&os.ModeSymlink != 0 {
					return nil
				}
				if d.IsDir() {
					return nil
				}
				if !strings.HasSuffix(path, ".jsonl") && !(assistant == "dsh" && strings.HasSuffix(path, ".jsonl.zstd")) {
					return nil
				}
				st, err := d.Info()
				if err != nil {
					failed = true
					return nil
				}
				if st.ModTime().Before(start) {
					return nil
				}
				seenFiles[path] = true
				cached, ok := dailyUsageCache.files[path]
				if !ok || cached.size != st.Size() || cached.mtime != st.ModTime().UnixNano() {
					cached = dailyFileCache{size: st.Size(), mtime: st.ModTime().UnixNano()}
					f, err := os.Open(path)
					if err == nil {
						var r io.Reader = f
						var z *zstd.Decoder
						if strings.HasSuffix(path, ".zstd") {
							z, err = zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20))
							if err == nil {
								r = z
							}
						}
						if err == nil {
							cached.records, err = readDailyUsage(ctx, r, assistant, filepath.Base(path), start, end)
						}
						if z != nil {
							z.Close()
						}
						f.Close()
					}
					cached.err = err != nil
					if !cached.err {
						dailyUsageCache.files[path] = cached
					}
				}
				failed = failed || cached.err
				for _, record := range cached.records {
					if prev, ok := records[record.key]; !ok || record.usage.TotalTokens > prev.TotalTokens {
						records[record.key] = record.usage
					}
				}
				return nil
			})
			if err != nil {
				failed = true
			}
		}
		if failed {
			out.Partial = true
			out.Warnings = append(out.Warnings, assistant+":incomplete")
		}
	}
	for path := range dailyUsageCache.files {
		if !seenFiles[path] {
			delete(dailyUsageCache.files, path)
		}
	}
	models := map[string]dailyModelUsage{}
	for _, u := range records {
		key := u.Assistant + "/" + u.Model
		total := models[key]
		total.Assistant = u.Assistant
		total.Model = u.Model
		total.InputTokens += u.InputTokens
		total.OutputTokens += u.OutputTokens
		total.CachedInputTokens += u.CachedInputTokens
		total.CacheWriteTokens += u.CacheWriteTokens
		total.TotalTokens += u.TotalTokens
		models[key] = total
	}
	for _, u := range models {
		out.Models = append(out.Models, u)
		out.TotalTokens += u.TotalTokens
	}
	sort.Slice(out.Models, func(i, j int) bool {
		if out.Models[i].TotalTokens == out.Models[j].TotalTokens {
			return out.Models[i].Assistant+out.Models[i].Model < out.Models[j].Assistant+out.Models[j].Model
		}
		return out.Models[i].TotalTokens > out.Models[j].TotalTokens
	})
	sort.Strings(out.Warnings)
	return out
}

func handleDailyUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	zone := r.URL.Query().Get("timezone")
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		http.Error(w, "invalid timezone", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	homes := map[string][]string{
		"claude": {filepath.Join(cfg.ClaudeHome, "projects")},
		"codex":  {filepath.Join(cfg.CodexHome, "sessions"), filepath.Join(cfg.CodexHome, "archived_sessions")},
		"dsh":    {filepath.Join(cfg.DSHHome, "sessions")},
	}
	writeJSON(w, collectDailyUsage(ctx, loc, time.Now(), homes))
}
