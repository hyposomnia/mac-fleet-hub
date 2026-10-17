package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Use Desktop's supported tool channel. Desktop owns its read-state file; never
// edit that file behind the running process or send a fabricated app-server RPC.
func setCodexDesktopReadState(ctx context.Context, sessionID string, read bool) error {
	path, err := findCodexDesktopAppToolsPipe()
	if err != nil {
		return err
	}
	return callCodexDesktopReadState(ctx, path, sessionID, read)
}

func callCodexDesktopReadState(ctx context.Context, path, sessionID string, read bool) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	call := func(id int, method string, params any, output any) error {
		data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if err != nil {
			return err
		}
		frame := make([]byte, 4+len(data))
		binary.LittleEndian.PutUint32(frame, uint32(len(data)))
		copy(frame[4:], data)
		if _, err := io.Copy(conn, strings.NewReader(string(frame))); err != nil {
			return err
		}
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return err
		}
		size := binary.LittleEndian.Uint32(header[:])
		if size == 0 || size > 8<<20 {
			return fmt.Errorf("invalid Desktop tool response size")
		}
		data = make([]byte, int(size))
		if _, err := io.ReadFull(conn, data); err != nil {
			return err
		}
		var response struct {
			ID     int              `json:"id"`
			Error  *json.RawMessage `json:"error"`
			Result json.RawMessage  `json:"result"`
		}
		if json.Unmarshal(data, &response) != nil || response.ID != id || response.Error != nil {
			return fmt.Errorf("Desktop read-state request failed")
		}
		return json.Unmarshal(response.Result, output)
	}
	var catalog struct {
		Tools []struct{ Name, Namespace string } `json:"tools"`
	}
	if err := call(1, "tools/list", map[string]any{"threadStartKind": "all"}, &catalog); err != nil {
		return err
	}
	namespace := ""
	for _, tool := range catalog.Tools {
		if tool.Name == "set_thread_read_state" {
			namespace = tool.Namespace
			break
		}
	}
	if namespace == "" {
		return fmt.Errorf("Desktop does not expose set_thread_read_state")
	}
	var result struct {
		Success bool `json:"success"`
	}
	if err := call(2, "tools/call", map[string]any{
		"namespace": namespace, "tool": "set_thread_read_state", "callerSource": "codex",
		"hostId": "local", "threadId": sessionID, "callId": "fleet-read-state", "turnId": "fleet-read-state",
		"arguments": map[string]any{"threadId": sessionID, "hostId": "local", "source": "codex", "read": read},
	}, &result); err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("Desktop could not update thread read state")
	}
	return nil
}

func handleNativeSessionReadState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	var input struct {
		Assistant string `json:"assistant"`
		SessionID string `json:"sessionId"`
		Read      *bool  `json:"read"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil || input.Assistant != "codex" ||
		strings.TrimSpace(input.SessionID) == "" || len(input.SessionID) > 512 || strings.ContainsAny(input.SessionID, "\r\n\x00") || input.Read == nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "无效的会话阅读状态")
		return
	}
	err := setCodexDesktopReadState(r.Context(), input.SessionID, *input.Read)
	var nativeAt int64
	if err == nil && !*input.Read {
		// Global-state disk writes can follow the accepted tool call. Confirm
		// the unread snapshot before allowing a later Desktop read to clear it.
		ctx, cancel := context.WithTimeout(r.Context(), 750*time.Millisecond)
		defer cancel()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			unread, updatedAt, available := desktopUnreadThreads()
			if available && unread[input.SessionID] {
				nativeAt = updatedAt
				break
			}
			select {
			case <-ctx.Done():
				writeJSON(w, map[string]any{"nativeSynced": true, "nativeReadStateAt": 0})
				return
			case <-ticker.C:
			}
		}
	}
	writeJSON(w, map[string]any{"nativeSynced": err == nil, "nativeReadStateAt": nativeAt})
}
