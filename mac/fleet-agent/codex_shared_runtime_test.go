package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedRuntimeChecksLiveServerBeforeRevivingHistoricalTurn(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		err            error
		fleet          bool
		wantRunning    bool
	}{
		{name: "interrupted history with idle live thread", response: `{"thread":{"id":"thread-1","status":{"type":"idle"}}}`},
		{name: "real Desktop turn", response: `{"thread":{"id":"thread-1","status":{"type":"active"}}}`, wantRunning: true},
		{name: "unavailable server", err: errors.New("offline"), wantRunning: true},
		{name: "missing status is not idle proof", response: `{"thread":{"id":"thread-1"}}`, wantRunning: true},
		{name: "other thread is not idle proof", response: `{"thread":{"id":"another-thread","status":{"type":"idle"}}}`, wantRunning: true},
		{name: "new Fleet lease outranks historical record", response: `{"thread":{"id":"thread-1","status":{"type":"idle"}}}`, fleet: true, wantRunning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldCfg, oldState := cfg, codexActiveRolloutTaskState
			t.Cleanup(func() { cfg, codexActiveRolloutTaskState = oldCfg, oldState })
			cfg.CodexMode = "shared"
			codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) {
				return codexRolloutTaskState{turnID: "turn-old", status: "inProgress"}, true
			}
			rpc := newFakeRPCConn()
			rpc.reply["thread/read"] = json.RawMessage(tc.response)
			if tc.err != nil {
				rpc.errs["thread/read"] = []error{tc.err}
			}
			b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return rpc, func() {}, nil })
			b.loadedThreads["thread-1"] = true
			b.lastTurn["thread-1"], b.turnOwners["thread-1"], b.writerOwners["thread-1"] = "turn-old", "desktop", "desktop"
			if tc.fleet {
				b.lastTurn["thread-1"], b.turnOwners["thread-1"], b.writerOwners["thread-1"] = "turn-new", "fleet", "fleet"
			}
			got, err := b.Control(context.Background(), "codex", "thread-1")
			if err != nil {
				t.Fatal(err)
			}
			if (got.Status == "running") != tc.wantRunning {
				t.Fatalf("control = %+v, want running=%v", got, tc.wantRunning)
			}
			if !tc.wantRunning && (got.ActiveTurnID != "" || got.TurnOwner != "" || got.WriterOwner != "") {
				t.Fatalf("idle retained ownership: %+v", got)
			}
			if tc.fleet && (got.ActiveTurnID != "turn-new" || got.TurnOwner != "fleet") {
				t.Fatalf("new Fleet lease lost: %+v", got)
			}
			for _, call := range rpc.calls {
				if call.method != "thread/read" {
					t.Fatalf("status check mutated/loaded thread: %s", call.method)
				}
				params := mapFromParams(t, call.params)
				if params["includeTurns"] != false {
					t.Fatal("status check should not hydrate history")
				}
			}
		})
	}
}

func TestSharedSessionListDoesNotReviveInterruptedHistory(t *testing.T) {
	oldCfg, oldBackend := cfg, agentChatBackend
	t.Cleanup(func() { cfg, agentChatBackend = oldCfg, oldBackend })
	cfg.CodexMode = "shared"
	rpc := newFakeRPCConn()
	rpc.reply["thread/read"] = json.RawMessage(`{"thread":{"id":"thread-1","status":{"type":"idle"}}}`)
	agentChatBackend = &routingChatBackend{codex: newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return rpc, func() {}, nil })}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-old"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sessions := []Session{{SessionID: "thread-1", Assistant: "codex", Status: "active", Live: true}}
	markSessionRuntime("codex", sessions, nil, map[string]string{"thread-1": path})
	if sessions[0].Status != "idle" || sessions[0].Live {
		t.Fatalf("interrupted history still running: %+v", sessions[0])
	}
}
