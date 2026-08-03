package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const codexDesktopThreadPageSize = 50

var codexCatalogCallTimeout = 10 * time.Second

type codexThreadListOptions struct {
	Cursor   string
	Search   string
	Archived bool
	Limit    int
}

type codexThreadPage struct {
	Sessions   []Session
	NextCursor string
}

type codexThreadCatalog interface {
	ListThreads(context.Context, codexThreadListOptions) (codexThreadPage, error)
}

type codexThreadManager interface {
	MutateThread(context.Context, string, string, string) error
}

type codexThreadReader interface {
	ThreadCwd(context.Context, string) (string, error)
}

type codexThreadListWire struct {
	Data       []codexThreadWire `json:"data"`
	NextCursor *string           `json:"nextCursor"`
}

type codexThreadWire struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"sessionId"`
	Cwd          string          `json:"cwd"`
	Name         *string         `json:"name"`
	Preview      string          `json:"preview"`
	Source       json.RawMessage `json:"source"`
	ThreadSource json.RawMessage `json:"threadSource"`
	Ephemeral    bool            `json:"ephemeral"`
	CreatedAt    int64           `json:"createdAt"`
	UpdatedAt    int64           `json:"updatedAt"`
	RecencyAt    *int64          `json:"recencyAt"`
	Status       json.RawMessage `json:"status"`
	GitInfo      *struct {
		Branch string `json:"branch"`
	} `json:"gitInfo"`
}

func (b *codexChatBackend) ListThreads(ctx context.Context, opts codexThreadListOptions) (codexThreadPage, error) {
	rpc, err := b.ensure(ctx)
	if err != nil {
		return codexThreadPage{}, err
	}
	limit := opts.Limit
	if limit <= 0 || limit > 100 {
		limit = codexDesktopThreadPageSize
	}
	params := map[string]interface{}{
		"limit":          limit,
		"cursor":         nil,
		"sortKey":        "recency_at",
		"modelProviders": nil,
		"sourceKinds":    []string{},
		"archived":       opts.Archived,
		"useStateDbOnly": true,
	}
	if opts.Cursor != "" {
		params["cursor"] = opts.Cursor
	}
	if search := strings.TrimSpace(opts.Search); search != "" {
		params["searchTerm"] = search
	}
	raw, err := rpc.call(ctx, "thread/list", params)
	if err != nil && codexRecencySortUnsupported(err) {
		// recency_at was added after updated_at. Keep the compatibility branch
		// narrow and explicit instead of falling back to local SQLite guesses.
		params["sortKey"] = "updated_at"
		raw, err = rpc.call(ctx, "thread/list", params)
	}
	if err != nil {
		return codexThreadPage{}, err
	}
	var wire codexThreadListWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return codexThreadPage{}, fmt.Errorf("decode Codex thread/list: %w", err)
	}
	page := codexThreadPage{Sessions: make([]Session, 0, len(wire.Data))}
	if wire.NextCursor != nil {
		page.NextCursor = *wire.NextCursor
	}
	for _, thread := range wire.Data {
		if thread.Ephemeral || codexThreadSource(thread.ThreadSource) == "ambient_suggestions" {
			continue
		}
		session, ok := codexSessionFromThread(thread)
		if ok {
			page.Sessions = append(page.Sessions, session)
		}
	}
	pins := readCodexThreadPins()
	for index := range page.Sessions {
		page.Sessions[index].Pinned = pins[page.Sessions[index].SessionID]
	}
	sort.SliceStable(page.Sessions, func(left, right int) bool {
		if page.Sessions[left].Pinned != page.Sessions[right].Pinned {
			return page.Sessions[left].Pinned
		}
		return page.Sessions[left].Mtime > page.Sessions[right].Mtime
	})
	return page, nil
}

func (b *codexChatBackend) MutateThread(ctx context.Context, sessionID, action, value string) error {
	rpc, err := b.ensure(ctx)
	if err != nil {
		return err
	}
	switch action {
	case "rename":
		name := strings.TrimSpace(value)
		if name == "" || len(name) > 500 {
			return fmt.Errorf("invalid thread name")
		}
		_, err = rpc.call(ctx, "thread/name/set", map[string]interface{}{"threadId": sessionID, "name": name})
	case "archive":
		_, err = rpc.call(ctx, "thread/archive", map[string]string{"threadId": sessionID})
	case "unarchive":
		_, err = rpc.call(ctx, "thread/unarchive", map[string]string{"threadId": sessionID})
	case "delete":
		_, err = rpc.call(ctx, "thread/delete", map[string]string{"threadId": sessionID})
		if err == nil {
			err = writeCodexThreadPin(sessionID, false)
		}
	case "pin":
		err = writeCodexThreadPin(sessionID, true)
	case "unpin":
		err = writeCodexThreadPin(sessionID, false)
	default:
		return fmt.Errorf("unsupported thread action")
	}
	return err
}

func (b *codexChatBackend) ThreadCwd(ctx context.Context, sessionID string) (string, error) {
	rpc, err := b.ensure(ctx)
	if err != nil {
		return "", err
	}
	raw, err := rpc.call(ctx, "thread/read", map[string]interface{}{"threadId": sessionID, "includeTurns": false})
	if err != nil {
		return "", err
	}
	var response struct {
		Thread struct {
			Cwd string `json:"cwd"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("decode Codex thread/read: %w", err)
	}
	return response.Thread.Cwd, nil
}

var codexPinsMu sync.Mutex

func codexPinsPath() string {
	if strings.TrimSpace(cfg.CodexHome) == "" {
		return ""
	}
	return filepath.Join(cfg.CodexHome, "fleet-thread-pins.json")
}

func readCodexThreadPins() map[string]bool {
	codexPinsMu.Lock()
	defer codexPinsMu.Unlock()
	return readCodexThreadPinsLocked()
}

func readCodexThreadPinsLocked() map[string]bool {
	pins := map[string]bool{}
	path := codexPinsPath()
	if path == "" {
		return pins
	}
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &pins)
	}
	return pins
}

func writeCodexThreadPin(sessionID string, pinned bool) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("invalid thread id")
	}
	codexPinsMu.Lock()
	defer codexPinsMu.Unlock()
	path := codexPinsPath()
	if path == "" {
		return fmt.Errorf("Codex home is not configured")
	}
	pins := readCodexThreadPinsLocked()
	if pinned {
		pins[sessionID] = true
	} else {
		delete(pins, sessionID)
	}
	data, err := json.MarshalIndent(pins, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".fleet-thread-pins-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func codexRecencySortUnsupported(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "recency_at") &&
		(strings.Contains(message, "invalid") || strings.Contains(message, "unknown") || strings.Contains(message, "unsupported"))
}

func codexThreadSource(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return ""
}

func codexSessionFromThread(thread codexThreadWire) (Session, bool) {
	id := strings.TrimSpace(thread.ID)
	if id == "" {
		id = strings.TrimSpace(thread.SessionID)
	}
	if id == "" {
		return Session{}, false
	}
	title := ""
	if thread.Name != nil {
		title = strings.TrimSpace(*thread.Name)
	}
	if title == "" {
		title = strings.TrimSpace(thread.Preview)
	}
	if title == "" {
		title = "(无标题)"
	}
	recency := thread.UpdatedAt
	if thread.RecencyAt != nil && *thread.RecencyAt > 0 {
		recency = *thread.RecencyAt
	}
	if recency <= 0 {
		recency = thread.CreatedAt
	}
	status, flags := codexRuntimeStatus(thread.Status)
	source := codexSessionSource(thread.Source)
	session := Session{
		SessionID: id,
		Assistant: "codex",
		Cwd:       thread.Cwd,
		Title:     trim(title),
		Mtime:     recency * 1000,
		Live:      status == "active",
		Waiting:   flags["waitingOnApproval"] || flags["waitingOnUserInput"],
		Status:    status,
		Source:    source,
	}
	if thread.GitInfo != nil {
		session.GitBranch = thread.GitInfo.Branch
	}
	return session, true
}

func codexRuntimeStatus(raw json.RawMessage) (string, map[string]bool) {
	var status struct {
		Type        string   `json:"type"`
		ActiveFlags []string `json:"activeFlags"`
	}
	if json.Unmarshal(raw, &status) != nil {
		var value string
		_ = json.Unmarshal(raw, &value)
		status.Type = value
	}
	flags := make(map[string]bool, len(status.ActiveFlags))
	for _, flag := range status.ActiveFlags {
		flags[flag] = true
	}
	if status.Type == "" {
		status.Type = "notLoaded"
	}
	return status.Type, flags
}

func codexSessionSource(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var object struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &object)
	return object.Type
}

func codexListThreads(ctx context.Context, opts codexThreadListOptions) (codexThreadPage, error) {
	if !codexExecutableAvailable() && !codexControlSocketAvailable() {
		return codexThreadPage{Sessions: []Session{}}, nil
	}
	catalog, ok := agentChatBackend.(codexThreadCatalog)
	if !ok {
		return codexThreadPage{}, errAppServerUnavailable
	}
	return catalog.ListThreads(ctx, opts)
}

func codexExecutableAvailable() bool {
	bin := strings.TrimSpace(cfg.CodexBin)
	if bin == "" {
		bin = "codex"
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

func codexControlSocketAvailable() bool {
	socketPath, err := codexAppServerSocketPath()
	if err != nil {
		return false
	}
	info, err := os.Stat(socketPath)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func codexAllThreads(ctx context.Context, archived bool) ([]Session, error) {
	var sessions []Session
	cursor := ""
	for pageNo := 0; pageNo < 20; pageNo++ {
		page, err := codexListThreads(ctx, codexThreadListOptions{
			Cursor: cursor, Archived: archived, Limit: 100,
		})
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, page.Sessions...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}
	return sessions, nil
}
