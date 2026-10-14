package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func modelCatalogFixture() map[string]any {
	return map[string]any{
		"default": map[string]any{"provider": "route-a", "model": "shared", "reasoningEffort": "high"},
		"groups": []any{
			map[string]any{"id": "route-a", "name": "Provider A", "models": []any{
				map[string]any{"id": "shared", "name": "Shared A", "reasoning": map[string]any{
					"defaultEffort": "high", "efforts": []any{map[string]any{"id": "high", "name": "High", "description": "More reasoning"}},
				}},
			}},
			map[string]any{"id": "route-b", "name": "Provider B", "models": []any{
				map[string]any{"id": "shared", "name": "Shared B"},
				map[string]any{"id": "other/model", "name": "Other", "description": "Other description"},
			}},
		},
		"failures": []any{},
	}
}

func modelHost(t *testing.T) (*dshTestHost, *dshChatBackend) {
	t.Helper()
	host := newDSHTestHost(t)
	host.streamReply = func(endpoint, streamID string, send func(map[string]any)) {
		if endpoint == "session/follow" {
			send(map[string]any{"type": "item", "streamId": streamID, "value": map[string]any{
				"type": "snapshot", "cursor": 1, "hasMore": false, "records": []any{},
			}})
		}
	}
	host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
		switch endpoint {
		case "session/modelCatalog":
			if len(args) != 0 {
				t.Errorf("modelCatalog args must be empty: %v", args)
			}
			return modelCatalogFixture(), nil
		case "session/create":
			return map[string]any{"sessionId": "session-models"}, nil
		case "session/list":
			return map[string]any{"items": []any{map[string]any{
				"sessionId": "session-models", "projections": map[string]any{"values": map[string]any{
					"modelSelection": map[string]any{
						"lastUsed": map[string]any{"provider": "route-a", "model": "shared", "reasoningEffort": "high"},
						"next":     map[string]any{"provider": "route-b", "model": "shared"},
					},
				}},
			}}}, nil
		default:
			return map[string]any{}, nil
		}
	}
	return host, newTestDSHBackend(t, host)
}

func TestDSHModelsStartIncludesEveryProvider(t *testing.T) {
	_, backend := modelHost(t)
	result, err := backend.Start(context.Background(), "dsh", "/tmp/models", "default")
	if err != nil {
		t.Fatal(err)
	}
	values := []string{}
	for _, option := range result.Models {
		values = append(values, option.Value)
	}
	if !reflect.DeepEqual(values, []string{"route-a/shared", "route-b/shared", "route-b/other%2Fmodel"}) {
		t.Fatalf("catalog values = %v", values)
	}
	if result.Model != "route-a/shared" || result.Effort != "high" {
		t.Fatalf("default = %+v", result)
	}
	if result.Models[0].DefaultEffort != "high" || len(result.Models[0].SupportedEfforts) != 1 {
		t.Fatalf("reasoning missing: %+v", result.Models[0])
	}
	if result.Models[0].DisplayName == result.Models[1].DisplayName {
		t.Fatal("provider identities are indistinguishable")
	}
}

func TestDSHModelsResumeUsesNextWithoutChangingHistoryModel(t *testing.T) {
	_, backend := modelHost(t)
	result, err := backend.Resume(context.Background(), "dsh", "session-models", "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 3 || result.Model != "route-b/shared" {
		t.Fatalf("resume = %+v", result)
	}
	model, effort := backend.sessionModel(backend.session("session-models"))
	if model != "shared" || effort != "high" {
		t.Fatalf("history model/effort = %q/%q", model, effort)
	}
}

func TestDSHModelsInputSelectsProviderBeforePrompt(t *testing.T) {
	host, backend := modelHost(t)
	var calls []string
	var request map[string]any
	var mu sync.Mutex
	base := host.callResult
	host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
		mu.Lock()
		calls = append(calls, endpoint)
		mu.Unlock()
		if endpoint == "session/selectModel" {
			request = args["request"].(map[string]any)
			return map[string]any{"selected": map[string]any{"provider": "route-b", "model": "other/model", "reasoningEffort": "max"}}, nil
		}
		return base(endpoint, args)
	}
	_, err := backend.Input(context.Background(), "dsh", "session-models", "hello", nil, nil, ChatTurnOptions{Model: "route-b/other%2Fmodel", Effort: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"session/modelCatalog", "session/selectModel", "session/prompt"}) {
		t.Fatalf("calls = %v", calls)
	}
	if !reflect.DeepEqual(request, map[string]any{"sessionId": "session-models", "provider": "route-b", "model": "other/model", "reasoningEffort": "max"}) {
		t.Fatalf("selection = %v", request)
	}
}

func TestDSHModelsInputFailureDoesNotPrompt(t *testing.T) {
	for _, kind := range []string{"unknown", "catalog", "selection"} {
		t.Run(kind, func(t *testing.T) {
			host, backend := modelHost(t)
			prompted := false
			base := host.callResult
			host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
				if endpoint == "session/prompt" {
					prompted = true
				}
				if endpoint == "session/modelCatalog" && kind == "catalog" || endpoint == "session/selectModel" && kind == "selection" {
					return nil, &dshRemoteError{Code: "session/model-unavailable", Message: "unavailable"}
				}
				return base(endpoint, args)
			}
			model := "route-b/shared"
			if kind == "unknown" {
				model = "unknown/shared"
			}
			_, err := backend.Input(context.Background(), "dsh", "session-models", "hello", nil, nil, ChatTurnOptions{Model: model})
			if err == nil || prompted {
				t.Fatalf("err=%v prompted=%v", err, prompted)
			}
		})
	}
}

func TestDSHModelsResumeCatalogErrorKeepsHistoryAndReportsError(t *testing.T) {
	host, backend := modelHost(t)
	base := host.callResult
	host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
		if endpoint == "session/modelCatalog" {
			return nil, &dshRemoteError{Code: "gateway/signature-invalid", Message: "changed"}
		}
		return base(endpoint, args)
	}
	result, err := backend.Resume(context.Background(), "dsh", "session-models", "default")
	if err != nil && !errors.Is(err, errDSHProtocolChanged) {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatalf("catalog failure must preserve history: %v", err)
	}
	raw, _ := json.Marshal(result)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if body["modelsError"] == nil || body["modelsError"] == "" {
		t.Fatalf("missing catalog failure: %s", raw)
	}
}

func TestDSHModelsPartialProviderFailureIsVisible(t *testing.T) {
	host, backend := modelHost(t)
	base := host.callResult
	host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
		if endpoint == "session/modelCatalog" {
			catalog := modelCatalogFixture()
			catalog["failures"] = []any{map[string]any{"id": "failed-provider", "name": "Provider C", "message": "upstream detail"}}
			return catalog, nil
		}
		return base(endpoint, args)
	}
	result, err := backend.Resume(context.Background(), "dsh", "session-models", "default")
	if err != nil || len(result.Models) != 3 || result.ModelsError != "以下提供商的模型目录不可用：Provider C" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestDSHModelsMalformedCatalogDoesNotMasqueradeAsComplete(t *testing.T) {
	for _, catalog := range []any{nil, map[string]any{}, map[string]any{"groups": []any{map[string]any{"id": "a"}}}, map[string]any{"groups": []any{map[string]any{"id": "a", "models": []any{map[string]any{"id": ""}}}}}} {
		host, backend := modelHost(t)
		base := host.callResult
		host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
			if endpoint == "session/modelCatalog" {
				return catalog, nil
			}
			return base(endpoint, args)
		}
		result, err := backend.Resume(context.Background(), "dsh", "session-models", "default")
		if err != nil || len(result.Models) != 0 || result.ModelsError == "" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
}

func TestDSHModelsEmptyCatalogReportsNoAvailableModels(t *testing.T) {
	host, backend := modelHost(t)
	base := host.callResult
	host.callResult = func(endpoint string, args map[string]any) (any, *dshRemoteError) {
		if endpoint == "session/modelCatalog" {
			return map[string]any{"groups": []any{}, "failures": []any{}}, nil
		}
		return base(endpoint, args)
	}
	result, err := backend.Resume(context.Background(), "dsh", "session-models", "default")
	if err != nil || result.ModelsError != "DSH 当前没有可用模型" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
