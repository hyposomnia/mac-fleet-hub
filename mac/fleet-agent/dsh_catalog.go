package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var errDSHInvalidSessionName = errors.New("invalid DeepSeek session name")

// 原生操作只经 Desktop host 执行，不直接修改 DSH 的会话日志或 workspace 存储。
func (b *dshChatBackend) mutateSession(ctx context.Context, sessionID, action, value string) error {
	request := map[string]any{"sessionId": sessionID}
	var endpoint string
	switch action {
	case "pin", "unpin":
		return writeThreadPin(dshPinsPath(), sessionID, action == "pin")
	case "rename":
		name := strings.TrimSpace(value)
		if name == "" || len(name) > 500 {
			return errDSHInvalidSessionName
		}
		request["title"] = name
		endpoint = "session/rename"
	case "archive":
		endpoint = "workspace/archiveSession"
	case "delete":
		endpoint = "session/delete"
	default:
		// DSH 当前没有取消归档接口；不能用修改内部文件来伪造支持。
		return errDSHUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := b.ensure(ctx)
	if err != nil {
		return err
	}
	_, err = client.call(ctx, endpoint, map[string]any{"request": request})
	if err == nil && action == "delete" {
		return writeThreadPin(dshPinsPath(), sessionID, false)
	}
	return b.translateCallError(err)
}

func dshPinsPath() string {
	if strings.TrimSpace(cfg.ChatQueueFile) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cfg.ChatQueueFile), "dsh-thread-pins.json")
}

func (b *dshChatBackend) listSessions(ctx context.Context, archived bool, search string) ([]Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	sessions, err := b.dshListSessions(ctx)
	if err != nil {
		return nil, err
	}
	archiveIDs, err := b.archivedSessionIDs(ctx)
	if err != nil {
		return nil, err
	}
	pins := readThreadPins(dshPinsPath())
	needle := strings.ToLower(strings.TrimSpace(search))
	filtered := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		if archiveIDs[session.SessionID] != archived {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(session.Title+"\n"+session.Cwd), needle) {
			continue
		}
		session.Live = !archived
		session.Pinned = pins[session.SessionID]
		filtered = append(filtered, session)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Pinned != filtered[j].Pinned {
			return filtered[i].Pinned
		}
		return filtered[i].Mtime > filtered[j].Mtime
	})
	return filtered, nil
}

// workspace/follow 的首帧是权威归档集合；读完立即取消这条逻辑流，复用原 WS。
func (b *dshChatBackend) archivedSessionIDs(ctx context.Context) (map[string]bool, error) {
	client, err := b.ensure(ctx)
	if err != nil {
		return nil, err
	}
	stream, err := client.openStream(ctx, "workspace/follow", nil)
	if err != nil {
		return nil, b.translateCallError(err)
	}
	defer stream.Close()
	raw, err := stream.Recv(ctx)
	if err != nil {
		return nil, b.translateCallError(err)
	}
	var frame struct {
		Type  string `json:"type"`
		Value struct {
			ArchivedSessionIDs *[]string `json:"archivedSessionIds"`
		} `json:"value"`
	}
	if json.Unmarshal(raw, &frame) != nil || frame.Type != "baseline" || frame.Value.ArchivedSessionIDs == nil {
		return nil, fmt.Errorf("%w: workspace/follow 缺少归档 baseline", errDSHProtocolChanged)
	}
	ids := map[string]bool{}
	for _, id := range *frame.Value.ArchivedSessionIDs {
		ids[id] = true
	}
	return ids, nil
}
