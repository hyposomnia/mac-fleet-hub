package multiuser

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Read state belongs to the owner and device, not a browser or phone. Versions
// prevent a delayed automatic read from cancelling a newer manual unread mark.
type sessionReadState struct {
	ReadAt         int64 `json:"readAt"`
	MarkedUnread   bool  `json:"markedUnread"`
	Version        int64 `json:"readStateVersion"`
	NativeUnreadAt int64 `json:"-"`
}

func (server *Server) sessionReadLock(userID int64, deviceIndex int, assistant, sessionID string) *sync.Mutex {
	hash := fnv.New32a()
	fmt.Fprintf(hash, "%d/%d/%s/%s", userID, deviceIndex, assistant, sessionID)
	return &server.sessionReadMu[hash.Sum32()%uint32(len(server.sessionReadMu))]
}

func (server *Server) handleSessionReadState(w http.ResponseWriter, r *http.Request, user User, device Device, target *url.URL, credential string) {
	if r.Method != http.MethodPost {
		reject(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var input struct {
		Assistant       string `json:"assistant"`
		SessionID       string `json:"sessionId"`
		ActivityAt      int64  `json:"activityAt"`
		Unread          bool   `json:"unread"`
		Explicit        bool   `json:"explicit"`
		ExpectedVersion int64  `json:"expectedVersion"`
	}
	if !decode(w, r, &input) {
		return
	}
	if (input.Assistant != "codex" && input.Assistant != "dsh" && input.Assistant != "claude") ||
		strings.TrimSpace(input.SessionID) == "" || len(input.SessionID) > 512 || strings.ContainsAny(input.SessionID, "\r\n\x00") ||
		input.ActivityAt < 0 || input.ActivityAt > server.now().Add(5*time.Minute).UnixMilli() || input.ExpectedVersion < 0 {
		reject(w, http.StatusBadRequest, "无效的会话阅读状态")
		return
	}
	lock := server.sessionReadLock(user.ID, device.Index, input.Assistant, input.SessionID)
	lock.Lock()
	defer lock.Unlock()
	args := []any{user.ID, device.Index, input.Assistant, input.SessionID}
	var state sessionReadState
	err := server.db.QueryRowContext(r.Context(), "SELECT read_at,marked_unread,version,native_unread_at FROM session_read_states WHERE user_id=? AND device_index=? AND assistant=? AND session_id=?", args...).Scan(&state.ReadAt, &state.MarkedUnread, &state.Version, &state.NativeUnreadAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		server.failure(w, err)
		return
	}
	accepted := !state.MarkedUnread || input.Unread || (input.Explicit && input.ExpectedVersion == state.Version)
	if accepted {
		state.MarkedUnread = input.Unread
		state.NativeUnreadAt = 0
		if input.ActivityAt > state.ReadAt && !input.Unread {
			state.ReadAt = input.ActivityAt
		}
		state.Version++
		_, err = server.db.ExecContext(r.Context(), `INSERT INTO session_read_states(user_id,device_index,assistant,session_id,read_at,marked_unread,version) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(user_id,device_index,assistant,session_id) DO UPDATE SET read_at=excluded.read_at,marked_unread=excluded.marked_unread,version=excluded.version,native_unread_at=0`,
			user.ID, device.Index, input.Assistant, input.SessionID, state.ReadAt, state.MarkedUnread, state.Version)
		if err != nil {
			server.failure(w, err)
			return
		}
	}
	// Codex exposes this in Desktop App Tools, not the app-server thread API.
	// The durable gateway mark remains valid when Desktop/older agents are offline.
	nativeSynced := false
	if accepted && input.Assistant == "codex" && (input.Unread || input.Explicit) {
		var nativeAt int64
		nativeSynced, nativeAt = server.syncNativeSessionRead(r.Context(), target, credential, device.ID, input.SessionID, !input.Unread)
		if input.Unread && nativeSynced && nativeAt > 0 {
			if _, err := server.db.ExecContext(r.Context(), "UPDATE session_read_states SET native_unread_at=? WHERE user_id=? AND device_index=? AND assistant=? AND session_id=? AND version=?", nativeAt, user.ID, device.Index, input.Assistant, input.SessionID, state.Version); err != nil {
				server.failure(w, err)
				return
			}
		}
	}
	respond(w, http.StatusOK, struct {
		sessionReadState
		NativeSynced bool `json:"nativeSynced"`
	}{state, nativeSynced})
}

func (server *Server) syncNativeSessionRead(ctx context.Context, target *url.URL, credential, deviceID, sessionID string, read bool) (bool, int64) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	transport, err := NewDeviceTransport(server.options.MeshProxy)
	if err != nil {
		return false, 0
	}
	defer transport.CloseIdleConnections()
	address := *target
	address.Path, address.RawPath, address.RawQuery = "/api/sessions/read-state", "", ""
	body, _ := json.Marshal(map[string]any{"assistant": "codex", "sessionId": sessionID, "read": read})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address.String(), bytes.NewReader(body))
	if err != nil {
		return false, 0
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", server.options.Origin)
	request.Header.Set("X-Fleet-Device-ID", deviceID)
	request.Header.Set("X-Fleet-Device-Token", credential)
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false, 0
	}
	defer response.Body.Close()
	var result struct {
		NativeSynced      bool  `json:"nativeSynced"`
		NativeReadStateAt int64 `json:"nativeReadStateAt"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result) != nil {
		return false, 0
	}
	return result.NativeSynced, result.NativeReadStateAt
}

func (server *Server) applySessionReadStates(ctx context.Context, response *http.Response, userID int64, deviceIndex int) error {
	const limit = 16 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	response.Body.Close()
	if err != nil || len(data) > limit {
		return fmt.Errorf("invalid session list response")
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	var sessions []map[string]json.RawMessage
	if err := json.Unmarshal(result["sessions"], &sessions); err != nil {
		return err
	}
	rows, err := server.db.QueryContext(ctx, "SELECT assistant,session_id,read_at,marked_unread,version,native_unread_at FROM session_read_states WHERE user_id=? AND device_index=?", userID, deviceIndex)
	if err != nil {
		return err
	}
	defer rows.Close()
	states := map[string]sessionReadState{}
	for rows.Next() {
		var assistant, sessionID string
		var state sessionReadState
		if err := rows.Scan(&assistant, &sessionID, &state.ReadAt, &state.MarkedUnread, &state.Version, &state.NativeUnreadAt); err != nil {
			return err
		}
		states[assistant+"\n"+sessionID] = state
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, session := range sessions {
		var assistant, sessionID string
		json.Unmarshal(session["assistant"], &assistant)
		json.Unmarshal(session["sessionId"], &sessionID)
		// Never trust an upstream to supply gateway-owned manual state.
		delete(session, "markedUnread")
		delete(session, "readStateVersion")
		if state, found := states[assistant+"\n"+sessionID]; found {
			var desktopUnread *bool
			var desktopAt, activity, outputEndedAt int64
			json.Unmarshal(session["desktopUnread"], &desktopUnread)
			json.Unmarshal(session["desktopReadStateAt"], &desktopAt)
			json.Unmarshal(session["mtime"], &activity)
			json.Unmarshal(session["outputEndedAt"], &outputEndedAt)
			activity = max(activity, outputEndedAt)
			// Only a later native read may clear a mark that Desktop actually
			// acknowledged. A stale Desktop snapshot cannot erase the fallback.
			if assistant == "codex" && state.MarkedUnread && state.NativeUnreadAt > 0 &&
				desktopUnread != nil && !*desktopUnread && desktopAt > state.NativeUnreadAt {
				state, err = server.reconcileNativeSessionRead(ctx, userID, deviceIndex, assistant, sessionID, desktopAt, activity)
				if err != nil {
					return err
				}
			}
			var agentReadAt int64
			json.Unmarshal(session["readAt"], &agentReadAt)
			if state.ReadAt < agentReadAt {
				state.ReadAt = agentReadAt
			}
			session["readAt"], _ = json.Marshal(state.ReadAt)
			session["markedUnread"], _ = json.Marshal(state.MarkedUnread)
			session["readStateVersion"], _ = json.Marshal(state.Version)
		}
	}
	result["sessions"], err = json.Marshal(sessions)
	if err != nil {
		return err
	}
	data, err = json.Marshal(result)
	if err != nil {
		return err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	response.Header.Set("Content-Length", strconv.Itoa(len(data)))
	return nil
}

func (server *Server) reconcileNativeSessionRead(ctx context.Context, userID int64, deviceIndex int, assistant, sessionID string, desktopAt, activity int64) (sessionReadState, error) {
	// Serialize with manual mutations, including their native acknowledgement.
	// Re-read after locking: a newer manual mark may have superseded the list.
	lock := server.sessionReadLock(userID, deviceIndex, assistant, sessionID)
	lock.Lock()
	defer lock.Unlock()
	var state sessionReadState
	err := server.db.QueryRowContext(ctx, "SELECT read_at,marked_unread,version,native_unread_at FROM session_read_states WHERE user_id=? AND device_index=? AND assistant=? AND session_id=?", userID, deviceIndex, assistant, sessionID).Scan(&state.ReadAt, &state.MarkedUnread, &state.Version, &state.NativeUnreadAt)
	if err != nil || !state.MarkedUnread || state.NativeUnreadAt <= 0 || desktopAt <= state.NativeUnreadAt {
		return state, err
	}
	_, err = server.db.ExecContext(ctx, `UPDATE session_read_states SET marked_unread=0,read_at=MAX(read_at,?),version=version+1,native_unread_at=0
WHERE user_id=? AND device_index=? AND assistant=? AND session_id=? AND version=?`, activity, userID, deviceIndex, assistant, sessionID, state.Version)
	if err != nil {
		return state, err
	}
	err = server.db.QueryRowContext(ctx, "SELECT read_at,marked_unread,version,native_unread_at FROM session_read_states WHERE user_id=? AND device_index=? AND assistant=? AND session_id=?", userID, deviceIndex, assistant, sessionID).Scan(&state.ReadAt, &state.MarkedUnread, &state.Version, &state.NativeUnreadAt)
	return state, err
}
