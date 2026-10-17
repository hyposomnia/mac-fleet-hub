package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestCodexSettingsRecoversMissingSharedThread(t *testing.T) {
	missing := errors.New("thread not found: thread-1")
	denied := errors.New("permission denied")
	for _, tc := range []struct {
		name         string
		mode         string
		updateErrors []error
		resumeError  error
		wantError    error
		wantMethods  []string
	}{
		{"recover", "shared", []error{missing}, nil, nil, []string{"thread/settings/update", "thread/resume", "thread/settings/update"}},
		{"bounded retry", "shared", []error{missing, missing}, nil, missing, []string{"thread/settings/update", "thread/resume", "thread/settings/update"}},
		{"resume failure", "shared", []error{missing}, denied, denied, []string{"thread/settings/update", "thread/resume"}},
		{"other error", "shared", []error{denied}, nil, denied, []string{"thread/settings/update"}},
		{"isolated does not claim writer", "isolated", []error{missing}, nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousCfg, previousState := cfg, codexActiveRolloutTaskState
			t.Cleanup(func() { cfg = previousCfg; codexActiveRolloutTaskState = previousState })
			cfg.CodexMode = tc.mode
			codexActiveRolloutTaskState = func(string) (codexRolloutTaskState, bool) { return codexRolloutTaskState{}, true }
			rpc := newFakeRPCConn()
			rpc.errs["thread/settings/update"] = tc.updateErrors
			rpc.reply["thread/resume"] = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
			if tc.resumeError != nil {
				rpc.errs["thread/resume"] = []error{tc.resumeError}
			}
			b := newCodexChatBackend(func(context.Context) (codexRPCConn, func(), error) { return rpc, func() {}, nil })
			b.loadedThreads["thread-1"] = true
			err := b.Settings(context.Background(), "codex", "thread-1", "full-access")
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("err=%v want=%v", err, tc.wantError)
			}
			if got := methods(rpc.calls); len(got) != len(tc.wantMethods) || (len(got) > 0 && !reflect.DeepEqual(got, tc.wantMethods)) {
				t.Fatalf("calls=%v want=%v", got, tc.wantMethods)
			}
			if tc.wantError == nil && b.approvalModes["thread-1"] != "full-access" {
				t.Fatal("successful settings not applied")
			}
			for _, call := range rpc.calls {
				params, ok := call.params.(map[string]interface{})
				if !ok {
					t.Fatalf("unexpected params: %#v", call.params)
				}
				if params["threadId"] != "thread-1" {
					t.Fatalf("wrong recovery target: %#v", params)
				}
			}
		})
	}
}
