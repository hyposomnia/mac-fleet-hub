package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in only: consumes one model call and creates/archives a disposable target.
// Exercise the production generator and name protection rather than a copy of
// the protocol. Never modify any pre-existing user conversation.
func TestCodexNativeTitleSpike(t *testing.T) {
	if os.Getenv("FLEET_TITLE_SPIKE") != "1" {
		t.Skip("manual ephemeral title probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	connector := func(ctx context.Context) (codexRPCConn, func(), error) {
		return connectCodexAppServerSocket(ctx, filepath.Join(os.Getenv("HOME"), ".macfleet/codex-app-server.sock"))
	}
	rpc, cleanup, err := connector(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	title, err := generateCodexTitleOnRPC(ctx, rpc, "我想让一个网页管理多台 Mac 的终端、文件和 AI 会话，并在手机上查看任务状态。")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("AI title:", title)
	raw, err := rpc.call(ctx, "thread/start", map[string]interface{}{"cwd": "/private/tmp", "ephemeral": false, "threadSource": "thread_title", "approvalPolicy": "never", "permissions": ":read-only"})
	if err != nil {
		t.Fatal(err)
	}
	var target struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err = json.Unmarshal(raw, &target); err != nil || target.Thread.ID == "" {
		t.Fatalf("target: %v", err)
	}
	id := target.Thread.ID
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := rpc.call(cleanup, "thread/archive", map[string]interface{}{"threadId": id}); err != nil {
			t.Error("archive:", err)
		}
		_, _ = rpc.call(cleanup, "thread/unsubscribe", map[string]interface{}{"threadId": id})
	}()
	b := newCodexChatBackend(connector)
	req := &codexTitleRequest{started: true}
	b.titleRequests[id] = req
	if err = b.applyGeneratedTitle(ctx, id, req, title); err != nil {
		t.Fatal(err)
	}
	raw, err = rpc.call(ctx, "thread/read", map[string]interface{}{"threadId": id, "includeTurns": false})
	if err != nil {
		t.Fatal(err)
	}
	var read struct {
		Thread struct {
			Name string `json:"name"`
		} `json:"thread"`
	}
	if err = json.Unmarshal(raw, &read); err != nil || read.Thread.Name != title {
		t.Fatalf("readback: %q %v", read.Thread.Name, err)
	}
	t.Log("production generator + guarded thread/name/set + thread/read: title matches")
}
