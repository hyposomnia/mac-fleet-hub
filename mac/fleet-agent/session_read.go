package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Fleet read cursors belong to the Mac, rather than a particular browser's localStorage.
// The Desktop state is read-only: its own process owns and updates that file.
var sessionReadMu sync.Mutex

func sessionReadPath() string {
	if cfg.CodexHome == "" {
		return ""
	}
	return filepath.Join(cfg.CodexHome, "fleet-session-reads.json")
}

func sessionReadKey(assistant, sessionID string) string {
	return assistant + "\n" + sessionID
}

func readSessionCursorsLocked() map[string]int64 {
	cursors := map[string]int64{}
	if data, err := os.ReadFile(sessionReadPath()); err == nil {
		_ = json.Unmarshal(data, &cursors)
	}
	if cursors == nil {
		return map[string]int64{}
	}
	return cursors
}

func writeSessionCursor(assistant, sessionID string, activityAt int64) (int64, error) {
	if assistant != "codex" && assistant != "dsh" && assistant != "claude" {
		return 0, fmt.Errorf("invalid assistant")
	}
	if sessionID == "" || len(sessionID) > 512 || strings.ContainsAny(sessionID, "\r\n") ||
		activityAt < 0 || activityAt > time.Now().Add(5*time.Minute).UnixMilli() {
		return 0, fmt.Errorf("invalid session read cursor")
	}
	path := sessionReadPath()
	if path == "" {
		return 0, fmt.Errorf("session read path is not configured")
	}
	sessionReadMu.Lock()
	defer sessionReadMu.Unlock()
	cursors := readSessionCursorsLocked()
	key := sessionReadKey(assistant, sessionID)
	if activityAt <= cursors[key] {
		return cursors[key], nil
	}
	cursors[key] = activityAt
	data, err := json.Marshal(cursors)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return 0, err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".fleet-session-reads-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return 0, err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return 0, err
	}
	if err := temp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return 0, err
	}
	return activityAt, nil
}

func handleSessionRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Assistant  string `json:"assistant"`
		SessionID  string `json:"sessionId"`
		ActivityAt int64  `json:"activityAt"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "无效的已读请求")
		return
	}
	readAt, err := writeSessionCursor(req.Assistant, req.SessionID, req.ActivityAt)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "无法保存已读状态")
		return
	}
	writeJSON(w, map[string]int64{"readAt": readAt})
}

// Desktop stores unread IDs by account identity and execution host. Read only the
// current local host; remote hosts and a previous signed-in account are unrelated.
func desktopUnreadThreads() (map[string]bool, int64, bool) {
	path := filepath.Join(cfg.CodexHome, ".codex-global-state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false
	}
	var state struct {
		Read struct {
			Version          int                            `json:"version"`
			UnreadByIdentity map[string]map[string][]string `json:"unreadByIdentity"`
			LegacyMigration  struct {
				IdentityKey    string            `json:"identityKey"`
				AdoptedHostIDs map[string]string `json:"adoptedHostIds"`
			} `json:"legacyMigration"`
		} `json:"electron-thread-read-state-v1"`
	}
	if json.Unmarshal(data, &state) != nil || state.Read.Version != 1 {
		return nil, 0, false
	}
	identity := state.Read.LegacyMigration.IdentityKey
	if identity == "" && len(state.Read.UnreadByIdentity) == 1 {
		for key := range state.Read.UnreadByIdentity {
			identity = key
		}
	}
	hosts, ok := state.Read.UnreadByIdentity[identity]
	if !ok {
		return nil, 0, false
	}
	hostKey := state.Read.LegacyMigration.AdoptedHostIDs["local"]
	if hostKey == "" && len(hosts) == 1 {
		for key := range hosts {
			if strings.HasPrefix(key, "local:") {
				hostKey = key
			}
		}
	}
	ids, ok := hosts[hostKey]
	if !ok || !strings.HasPrefix(hostKey, "local:") {
		return nil, 0, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, false
	}
	unread := make(map[string]bool, len(ids))
	for _, id := range ids {
		unread[id] = true
	}
	return unread, info.ModTime().UnixMilli(), true
}

func applySessionReadState(sessions []Session) {
	sessionReadMu.Lock()
	cursors := readSessionCursorsLocked()
	sessionReadMu.Unlock()
	desktopUnread, desktopUpdatedAt, desktopAvailable := desktopUnreadThreads()
	for index := range sessions {
		session := &sessions[index]
		session.ReadAt = cursors[sessionReadKey(session.Assistant, session.SessionID)]
		if desktopAvailable && session.Assistant == "codex" {
			unread := desktopUnread[session.SessionID]
			// A stopped Desktop cannot have observed a newer reply. Its old absence
			// from the unread set must not erase Fleet's fresh notification.
			activity := session.Mtime
			if session.OutputEndedAt > activity {
				activity = session.OutputEndedAt
			}
			if unread || desktopUpdatedAt >= activity {
				session.DesktopUnread = &unread
			}
		}
	}
}
