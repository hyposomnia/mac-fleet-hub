package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDSHPermissionsReadAndSwitch(t *testing.T) {
	for _, preset := range []string{"read-only", "workspace-write", "danger-full-access", "custom-review"} {
		t.Run(preset, func(t *testing.T) {
			host := newDSHTestHost(t)
			current := "workspace-write"
			writes := 0
			host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
				switch endpoint {
				case "permissionPresets/catalog":
					return map[string]any{"options": []any{map[string]any{"value": preset, "name": preset}}}, nil
				case "session/projections":
					req := args["request"].(map[string]any)
					if req["sessionId"] != "session-permission" {
						t.Errorf("wrong session: %v", req)
					}
					return map[string]any{"values": map[string]any{"permissions": map[string]any{"currentValue": current}}}, nil
				case "commands/execute":
					if args["agentId"] != "session-permission" || args["line"] != "/permission "+preset {
						t.Errorf("wrong command: %v", args)
					}
					writes++
					current = preset
					return map[string]any{"commandId": "cmd-1", "result": map[string]any{"kind": "success"}}, nil
				default:
					t.Errorf("unexpected endpoint %s", endpoint)
					return nil, nil
				}
			}
			b := newTestDSHBackend(t, host)
			if err := b.Settings(context.Background(), "dsh", "session-permission", preset); err != nil {
				t.Fatal(err)
			}
			state, err := b.Control(context.Background(), "dsh", "session-permission")
			raw, _ := json.Marshal(state)
			var got struct {
				ApprovalMode    string
				ApprovalOptions []struct{ Value string }
			}
			json.Unmarshal(raw, &got)
			if err != nil || got.ApprovalMode != preset || len(got.ApprovalOptions) != 1 || writes != 1 {
				t.Fatalf("state=%s writes=%d err=%v", raw, writes, err)
			}
		})
	}
}

func TestDSHPermissionsRejectFailures(t *testing.T) {
	for _, failure := range []string{"unknown", "catalog", "command", "missing-result", "mismatch"} {
		t.Run(failure, func(t *testing.T) {
			host := newDSHTestHost(t)
			writes := 0
			host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
				switch endpoint {
				case "permissionPresets/catalog":
					if failure == "catalog" {
						return nil, &dshRemoteError{Code: "unavailable", Message: "offline"}
					}
					return map[string]any{"options": []any{map[string]any{"value": "workspace-write", "name": "Workspace"}}}, nil
				case "commands/execute":
					writes++
					if failure == "command" {
						return map[string]any{"result": map[string]any{"kind": "error", "text": "denied"}}, nil
					}
					if failure == "missing-result" {
						return nil, nil
					}
					return map[string]any{"result": map[string]any{"kind": "success"}}, nil
				case "session/projections":
					return map[string]any{"values": map[string]any{"permissions": map[string]any{"currentValue": "read-only"}}}, nil
				}
				return nil, nil
			}
			b := newTestDSHBackend(t, host)
			preset := "workspace-write"
			if failure == "unknown" {
				preset = "not-advertised"
			}
			if err := b.Settings(context.Background(), "dsh", "session-x", preset); err == nil {
				t.Fatal("must reject")
			}
			if (failure == "unknown" || failure == "catalog") && writes != 0 {
				t.Fatal("must not write")
			}
		})
	}
}

func TestDSHPermissionsUnavailableDoesNotBreakControl(t *testing.T) {
	host := newDSHTestHost(t)
	host.callResult = func(string, map[string]any) (any, *dshRemoteError) {
		return nil, &dshRemoteError{Code: "gateway/not-found", Message: "unsupported"}
	}
	b := newTestDSHBackend(t, host)
	state, err := b.Control(context.Background(), "dsh", "session-x")
	if err != nil || state.WriterOwner != "dsh-host" {
		t.Fatalf("%+v %v", state, err)
	}
}

func TestDSHPermissionEmptyCatalogDisablesControl(t *testing.T) {
	host := newDSHTestHost(t)
	host.callResult = func(string, map[string]any) (any, *dshRemoteError) { return map[string]any{"options": []any{}}, nil }
	b := newTestDSHBackend(t, host)
	state, err := b.Control(context.Background(), "dsh", "session-x")
	if err != nil || len(state.ApprovalOptions) != 0 || state.ApprovalMode != "" || state.ApprovalError == "" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}
