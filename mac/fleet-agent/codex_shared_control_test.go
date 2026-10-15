package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func sharedDesktopControlBackend(t *testing.T) (*codexChatBackend, *fakeRPCConn) {
	t.Helper()
	oldCfg, oldState := cfg, codexActiveRolloutTaskState
	t.Cleanup(func() { cfg, codexActiveRolloutTaskState = oldCfg, oldState })
	cfg.CodexMode, cfg.CodexHome = "shared", t.TempDir()
	codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) {
		return codexRolloutTaskState{turnID: "turn-desktop", status: "inProgress"}, true
	}
	rpc := newFakeRPCConn()
	rpc.reply["thread/read"] = json.RawMessage(`{"thread":{"id":"thread-1","status":{"type":"active"}}}`)
	rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1","status":{"type":"active"},"turns":[{"id":"turn-desktop","status":"inProgress"}]}}`)
	rpc.reply["turn/steer"] = json.RawMessage(`{"turnId":"turn-desktop"}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return rpc, func() {}, nil })
	b.loadedThreads["thread-1"] = true
	b.lastTurn["thread-1"], b.turnOwners["thread-1"], b.writerOwners["thread-1"] = "turn-desktop", "desktop", "desktop"
	return b, rpc
}

func TestSharedDesktopTurnControlsReachSameServer(t *testing.T) {
	for _, operation := range []string{"steer", "interrupt", "settings", "respond", "release"} {
		t.Run(operation, func(t *testing.T) {
			b, rpc := sharedDesktopControlBackend(t)
			var err error
			wantMethod := "turn/" + operation
			switch operation {
			case "steer":
				_, err = b.Steer(context.Background(), "codex", "thread-1", "follow-1", "continue", nil, nil)
			case "interrupt":
				err = b.Interrupt(context.Background(), "codex", "thread-1")
			case "settings":
				err = b.Settings(context.Background(), "codex", "thread-1", "on-request")
				wantMethod = "thread/settings/update"
			case "respond":
				b.pending["42"] = pendingCodexRequest{id: json.RawMessage(`42`), method: "item/commandExecution/requestApproval", sessionID: "thread-1", params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-desktop"}`)}
				err = b.Respond(context.Background(), "codex", "thread-1", "42", json.RawMessage(`{"decision":"accept"}`))
				wantMethod = "response:42"
			case "release":
				err = b.Release(context.Background(), "codex", "thread-1")
				wantMethod = "turn/interrupt"
			}
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, call := range rpc.calls {
				if call.method != wantMethod {
					continue
				}
				found = true
				if operation == "steer" || operation == "interrupt" || operation == "release" {
					params := mapFromParams(t, call.params)
					key := "turnId"
					if operation == "steer" {
						key = "expectedTurnId"
					}
					if params["threadId"] != "thread-1" || params[key] != "turn-desktop" {
						t.Fatalf("control targeted wrong thread/turn: %+v", params)
					}
				}
			}
			if !found {
				t.Fatalf("missing %s: %v", wantMethod, methods(rpc.calls))
			}
		})
	}
}

func TestSharedActiveInputWaitsForTurnWithoutWriterTakeover(t *testing.T) {
	b, rpc := sharedDesktopControlBackend(t)
	_, err := b.Input(context.Background(), "codex", "thread-1", "next", nil, nil, ChatTurnOptions{})
	if !errors.Is(err, errFleetChatTurnRunning) {
		t.Fatalf("active shared turn should wait for completion: %v", err)
	}
	for _, call := range rpc.calls {
		if call.method == "turn/start" || call.method == "turn/interrupt" {
			t.Fatalf("next-turn input changed active turn: %s", call.method)
		}
	}
}

func TestSharedQueueDeliveryCanSteerDesktopTurn(t *testing.T) {
	b, rpc := sharedDesktopControlBackend(t)
	result, err := (backendChatQueueSender{backend: b}).Deliver(ChatQueueItem{
		Assistant: "codex", SessionID: "thread-1", ClientMessageID: "follow-1", Text: "continue",
		DeliveryMode: chatDeliveryAuto, Options: ChatTurnOptions{ApprovalMode: "on-request"},
	})
	if err != nil || result.TurnID != "turn-desktop" || result.Delivery != chatDeliverySteer {
		t.Fatalf("shared queued followup was blocked: result=%+v err=%v", result, err)
	}
	for _, call := range rpc.calls {
		if call.method == "turn/start" || call.method == "turn/interrupt" {
			t.Fatalf("followup replaced active shared turn: %s", call.method)
		}
	}
}

func TestSharedControlSnapshotExposesSharedRuntime(t *testing.T) {
	b, _ := sharedDesktopControlBackend(t)
	oldBackend, oldQueue := agentChatBackend, agentChatQueue
	t.Cleanup(func() { agentChatBackend, agentChatQueue = oldBackend, oldQueue })
	agentChatBackend = b
	var err error
	agentChatQueue, err = openChatQueue(filepath.Join(t.TempDir(), "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	control, err := buildChatSessionControlSnapshot(context.Background(), "codex", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(control)
	var fields map[string]interface{}
	_ = json.Unmarshal(raw, &fields)
	if fields["sharedRuntime"] != true || fields["turnOwner"] != "desktop" || fields["turnPhase"] != "running" {
		t.Fatalf("shared control lost runtime capability or turn origin: %s", raw)
	}
}

func TestSharedFullAccessAppliesToDesktopRequests(t *testing.T) {
	b, rpc := sharedDesktopControlBackend(t)
	b.pending["41"] = pendingCodexRequest{id: json.RawMessage(`41`), method: "item/commandExecution/requestApproval", sessionID: "thread-1", params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-desktop"}`)}
	if err := b.Settings(context.Background(), "codex", "thread-1", "full-access"); err != nil {
		t.Fatal(err)
	}
	if _, pending := b.pending["41"]; pending {
		t.Fatal("existing shared approval remained blocked")
	}
	if !b.autoApprove(rpc, rpcNotification{ID: json.RawMessage(`42`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-desktop"}`)}) {
		t.Fatal("shared full-access request was blocked by turn origin")
	}
	b.pending["43"] = pendingCodexRequest{id: json.RawMessage(`43`), method: "item/commandExecution/requestApproval", sessionID: "thread-1"}
	if err := b.Respond(context.Background(), "codex", "another-thread", "43", json.RawMessage(`{"decision":"accept"}`)); !errors.Is(err, errChatRequestNotFound) {
		t.Fatalf("request identity validation lost: %v", err)
	}
}
