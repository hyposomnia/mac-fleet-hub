package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCodexListThreadsUsesDesktopQueryAndFiltersOnlyHiddenSources(t *testing.T) {
	previousCfg := cfg
	cfg.CodexHome = t.TempDir()
	t.Cleanup(func() { cfg = previousCfg })

	rpc := newFakeRPCConn()
	rpc.reply["thread/list"] = json.RawMessage(`{
		"data":[
			{"id":"visible","cwd":"/repo","preview":"Visible","createdAt":1,"updatedAt":2,"recencyAt":3,"status":{"type":"notLoaded"}},
			{"id":"ephemeral","cwd":"/repo","preview":"Ephemeral","ephemeral":true,"createdAt":1,"updatedAt":2},
			{"id":"ambient","cwd":"/repo","preview":"Ambient","threadSource":"ambient_suggestions","createdAt":1,"updatedAt":2}
		],
		"nextCursor":"next"
	}`)
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	page, err := backend.ListThreads(context.Background(), codexThreadListOptions{
		Limit: 50, Search: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].SessionID != "visible" || page.NextCursor != "next" {
		t.Fatalf("page got %+v", page)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "thread/list" {
		t.Fatalf("calls got %+v", rpc.calls)
	}
	got := mapFromParams(t, rpc.calls[0].params)
	want := map[string]interface{}{
		"limit":          float64(50),
		"cursor":         nil,
		"sortKey":        "recency_at",
		"modelProviders": nil,
		"sourceKinds":    []interface{}{},
		"archived":       false,
		"useStateDbOnly": true,
		"searchTerm":     "needle",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("thread/list params got %#v want %#v", got, want)
	}
}

func TestCodexThreadCwdUsesThreadReadWithoutTurns(t *testing.T) {
	rpc := newFakeRPCConn()
	rpc.reply["thread/read"] = json.RawMessage(`{"thread":{"id":"thread-1","cwd":"/repo"}}`)
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return rpc, func() {}, nil
	})

	cwd, err := backend.ThreadCwd(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if cwd != "/repo" {
		t.Fatalf("cwd got %q", cwd)
	}
	if len(rpc.calls) != 1 || rpc.calls[0].method != "thread/read" {
		t.Fatalf("calls got %+v", rpc.calls)
	}
	want := map[string]interface{}{"threadId": "thread-1", "includeTurns": false}
	if got := mapFromParams(t, rpc.calls[0].params); !reflect.DeepEqual(got, want) {
		t.Fatalf("params got %#v want %#v", got, want)
	}
}

func TestCodexMutateThreadUsesAppServerMethods(t *testing.T) {
	tests := []struct {
		action string
		value  string
		method string
		params map[string]interface{}
	}{
		{action: "rename", value: "New name", method: "thread/name/set", params: map[string]interface{}{"threadId": "thread-1", "name": "New name"}},
		{action: "archive", method: "thread/archive", params: map[string]interface{}{"threadId": "thread-1"}},
		{action: "unarchive", method: "thread/unarchive", params: map[string]interface{}{"threadId": "thread-1"}},
	}
	for _, test := range tests {
		t.Run(test.action, func(t *testing.T) {
			rpc := newFakeRPCConn()
			backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
				return rpc, func() {}, nil
			})
			if err := backend.MutateThread(context.Background(), "thread-1", test.action, test.value); err != nil {
				t.Fatal(err)
			}
			if len(rpc.calls) != 1 || rpc.calls[0].method != test.method {
				t.Fatalf("calls got %+v", rpc.calls)
			}
			if got := mapFromParams(t, rpc.calls[0].params); !reflect.DeepEqual(got, test.params) {
				t.Fatalf("params got %#v want %#v", got, test.params)
			}
		})
	}
}

func TestCodexPinStatePersistsOutsideAppServer(t *testing.T) {
	previousCfg := cfg
	cfg.CodexHome = t.TempDir()
	t.Cleanup(func() { cfg = previousCfg })
	backend := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) {
		return newFakeRPCConn(), func() {}, nil
	})

	if err := backend.MutateThread(context.Background(), "thread-1", "pin", ""); err != nil {
		t.Fatal(err)
	}
	if !readCodexThreadPins()["thread-1"] {
		t.Fatal("thread pin was not persisted")
	}
	info, err := os.Stat(filepath.Join(cfg.CodexHome, "fleet-thread-pins.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("pin file mode got %o", info.Mode().Perm())
	}
	if err := backend.MutateThread(context.Background(), "thread-1", "unpin", ""); err != nil {
		t.Fatal(err)
	}
	if readCodexThreadPins()["thread-1"] {
		t.Fatal("thread pin was not removed")
	}
}
