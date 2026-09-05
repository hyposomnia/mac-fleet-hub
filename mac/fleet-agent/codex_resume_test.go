package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestCodexSharedResumePreservesLatestTurnOwnership(t *testing.T) {
	previousCfg, previousState := cfg, codexActiveRolloutTaskState
	t.Cleanup(func() {
		cfg, codexActiveRolloutTaskState = previousCfg, previousState
	})
	cfg.CodexMode = "shared"
	cfg.CodexHome = t.TempDir()
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) {
		return codexRolloutTaskState{turnID: "turn-current", status: "inProgress"}, true
	}
	for _, layout := range []struct {
		name  string
		turns string
	}{
		{"embedded chronological history", `[{"id":"turn-old","status":"completed"},{"id":"turn-current","status":"inProgress"}]`},
		{"paged newest first history", `[]`},
	} {
		for _, owner := range []string{"fleet", "desktop"} {
			t.Run(layout.name+"/"+owner, func(t *testing.T) {
				rpc := newFakeRPCConn()
				rpc.reply["thread/read"] = json.RawMessage(`{"thread":{"id":"thread-resume"}}`)
				rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-resume","status":{"type":"idle"}}}`)
				rpc.reply["thread/items/list"] = json.RawMessage(`{"data":[]}`)
				rpc.reply["turn/start"] = json.RawMessage(`{"turn":{"id":"turn-current"}}`)
				rpc.reply["turn/steer"] = json.RawMessage(`{"turnId":"turn-current"}`)
				b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
					return rpc, func() {}, nil
				})
				if owner == "fleet" {
					if _, err := b.Input(context.Background(), "codex", "thread-resume", "start", nil, nil, ChatTurnOptions{}); err != nil {
						t.Fatal(err)
					}
				}
				rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-resume","status":{"type":"active"},"turns":` + layout.turns + `}}`)
				rpc.reply["thread/turns/list"] = json.RawMessage(`{"data":[{"id":"turn-current","status":"inProgress"},{"id":"turn-old","status":"completed"}]}`)
				// Opening the conversation and returning to the browser foreground
				// both resume it before refreshing control and accepting followups.
				for i := 0; i < 2; i++ {
					res, err := b.Resume(context.Background(), "codex", "thread-resume", "default")
					if err != nil {
						t.Fatal(err)
					}
					if res.ActiveTurnID != "turn-current" || res.TurnOwner != owner || res.WriterOwner != owner {
						t.Fatalf("resume %d lost latest turn ownership: %+v", i, res)
					}
					control, err := b.Control(context.Background(), "codex", "thread-resume")
					if err != nil {
						t.Fatal(err)
					}
					if control.ActiveTurnID != "turn-current" || control.TurnOwner != owner || control.WriterOwner != owner {
						t.Fatalf("control after resume %d changed ownership: %+v", i, control)
					}
				}
				_, err := b.Steer(context.Background(), "codex", "thread-resume", "followup", "continue", nil, nil)
				if owner == "fleet" && err != nil {
					t.Fatalf("Fleet followup was blocked after resume: %v", err)
				}
				if owner == "desktop" && !errors.Is(err, errExternalChatTurn) {
					t.Fatalf("Desktop followup must remain protected, got %v", err)
				}
			})
		}
	}
}
