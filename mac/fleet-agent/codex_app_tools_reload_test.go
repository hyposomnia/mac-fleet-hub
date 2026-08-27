package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReloadCodexAppToolsRequiresConnectedRuntimeWithTools(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["config/mcpServer/reload"] = json.RawMessage(`{}`)
	rpc.reply["mcpServerStatus/list"] = json.RawMessage(`{
		"data":[{"name":"codex_app","runtimeStatus":"connected","tools":{"create_thread":{}}}]
	}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	if err := b.reloadCodexAppTools(context.Background(), "/tmp/codex-browser-use/desktop.sock"); err != nil {
		t.Fatal(err)
	}
	if b.appToolsPipe != "/tmp/codex-browser-use/desktop.sock" {
		t.Fatalf("stored pipe got %q", b.appToolsPipe)
	}
	if len(rpc.calls) != 2 || rpc.calls[0].method != "config/mcpServer/reload" || rpc.calls[1].method != "mcpServerStatus/list" {
		t.Fatalf("calls got %#v", rpc.calls)
	}
}

func TestReloadCodexAppToolsRejectsFailedRuntime(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["config/mcpServer/reload"] = json.RawMessage(`{}`)
	rpc.reply["mcpServerStatus/list"] = json.RawMessage(`{
		"data":[{"name":"codex_app","runtimeStatus":"failed","tools":{}}]
	}`)
	b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})
	if err := b.reloadCodexAppTools(context.Background(), "/tmp/codex-browser-use/desktop.sock"); err == nil {
		t.Fatal("expected failed runtime to be rejected")
	}
}

func TestCodexAppToolsReloadWaitsForAnyTurnOrInteraction(t *testing.T) {
	previous := codexAnyPhysicalActiveTurn
	t.Cleanup(func() { codexAnyPhysicalActiveTurn = previous })
	codexAnyPhysicalActiveTurn = func() bool { return false }
	b := newCodexChatBackend(nil)
	b.lastTurn["desktop-thread"] = "desktop-turn"
	b.turnOwners["desktop-thread"] = "desktop"
	if !b.hasTurnOrInteractionInProgress() {
		t.Fatal("Desktop turn must block global MCP reload")
	}
	delete(b.lastTurn, "desktop-thread")
	b.pending["request-1"] = pendingCodexRequest{sessionID: "desktop-thread"}
	if !b.hasTurnOrInteractionInProgress() {
		t.Fatal("pending interaction must block global MCP reload")
	}
	delete(b.pending, "request-1")
	codexAnyPhysicalActiveTurn = func() bool { return true }
	if !b.hasTurnOrInteractionInProgress() {
		t.Fatal("external physical active turn must block global MCP reload")
	}
}
