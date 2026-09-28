package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSessionReadSharedCursorAndDesktopState(t *testing.T) {
	old := cfg
	cfg.CodexHome = t.TempDir()
	t.Cleanup(func() { cfg = old })
	activity := time.Now().Add(-time.Minute).UnixMilli()
	desktopPath := filepath.Join(cfg.CodexHome, ".codex-global-state.json")
	state := `{"electron-thread-read-state-v1":{"version":1,"legacyMigration":{"identityKey":"current","adoptedHostIds":{"local":"local:host"}},"unreadByIdentity":{"current":{"local:host":["unread"]},"other":{"local:host":["read"]}}}}`
	if err := os.WriteFile(desktopPath, []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	later := time.UnixMilli(activity + 1000)
	if err := os.Chtimes(desktopPath, later, later); err != nil {
		t.Fatal(err)
	}
	sessions := []Session{
		{Assistant: "codex", SessionID: "unread", Mtime: activity},
		{Assistant: "codex", SessionID: "read", Mtime: activity},
		{Assistant: "dsh", SessionID: "read", Mtime: activity},
	}
	applySessionReadState(sessions)
	if sessions[0].DesktopUnread == nil || !*sessions[0].DesktopUnread ||
		sessions[1].DesktopUnread == nil || *sessions[1].DesktopUnread ||
		sessions[2].DesktopUnread != nil {
		t.Fatalf("desktop unread state was not scoped to current identity and Codex: %+v", sessions)
	}

	body := `{"assistant":"codex","sessionId":"unread","activityAt":` + strconv.FormatInt(activity, 10) + `}`
	rr := httptest.NewRecorder()
	handleSessionRead(rr, httptest.NewRequest(http.MethodPost, "/api/sessions/read", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("mark read: %d %s", rr.Code, rr.Body.String())
	}
	// Every browser sees this cursor on the next session listing, even if Desktop
	// has not yet removed the ID from its own unread set.
	fresh := []Session{{Assistant: "codex", SessionID: "unread", Mtime: activity}}
	applySessionReadState(fresh)
	if fresh[0].ReadAt != activity || fresh[0].DesktopUnread == nil || !*fresh[0].DesktopUnread {
		t.Fatalf("shared cursor missing: %+v", fresh[0])
	}
	if info, err := os.Stat(sessionReadPath()); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("cursor file permissions: %v %v", info, err)
	}
	// An out-of-order request must not move the shared read cursor backwards.
	if got, err := writeSessionCursor("codex", "unread", activity-1); err != nil || got != activity {
		t.Fatalf("cursor regressed: %d %v", got, err)
	}
}

func TestDesktopStateDoesNotClearActivityAfterLastDesktopUpdate(t *testing.T) {
	old := cfg
	cfg.CodexHome = t.TempDir()
	t.Cleanup(func() { cfg = old })
	path := filepath.Join(cfg.CodexHome, ".codex-global-state.json")
	data := `{"electron-thread-read-state-v1":{"version":1,"legacyMigration":{"identityKey":"current","adoptedHostIds":{"local":"local:host"}},"unreadByIdentity":{"current":{"local:host":[]}}}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	sessions := []Session{{Assistant: "codex", SessionID: "new", OutputEndedAt: time.Now().UnixMilli()}}
	applySessionReadState(sessions)
	if sessions[0].DesktopUnread != nil {
		t.Fatalf("stale Desktop state erased a new reply: %+v", sessions[0])
	}
}
