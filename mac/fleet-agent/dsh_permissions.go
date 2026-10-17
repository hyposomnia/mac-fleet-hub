package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (b *dshChatBackend) permissionCatalog(ctx context.Context, client *dshClient) ([]ChatApprovalOption, error) {
	raw, err := client.call(ctx, "permissionPresets/catalog", map[string]any{})
	if err != nil {
		return nil, b.translateCallError(err)
	}
	var catalog struct {
		Options []ChatApprovalOption `json:"options"`
	}
	if json.Unmarshal(raw, &catalog) != nil || len(catalog.Options) == 0 {
		return nil, fmt.Errorf("%w: invalid permission catalog", errDSHProtocolChanged)
	}
	seen := map[string]bool{}
	for _, option := range catalog.Options {
		if strings.TrimSpace(option.Value) == "" || strings.TrimSpace(option.Value) != option.Value || strings.ContainsAny(option.Value, "\r\n") || seen[option.Value] {
			return nil, fmt.Errorf("%w: invalid permission option", errDSHProtocolChanged)
		}
		seen[option.Value] = true
	}
	return catalog.Options, nil
}

func (b *dshChatBackend) permissionSelection(ctx context.Context, client *dshClient, sessionID string) (string, error) {
	raw, err := client.call(ctx, "session/projections", map[string]any{"request": map[string]any{"sessionId": sessionID}})
	if err != nil {
		return "", b.translateCallError(err)
	}
	var projection struct {
		Values struct {
			Permissions struct {
				CurrentValue string `json:"currentValue"`
			} `json:"permissions"`
		} `json:"values"`
	}
	if json.Unmarshal(raw, &projection) != nil || strings.TrimSpace(projection.Values.Permissions.CurrentValue) == "" {
		return "", fmt.Errorf("%w: missing permission selection", errDSHProtocolChanged)
	}
	return projection.Values.Permissions.CurrentValue, nil
}

func (b *dshChatBackend) Settings(ctx context.Context, assistant, sessionID, preset string) error {
	client, err := b.ensure(ctx)
	if err != nil {
		return err
	}
	options, err := b.permissionCatalog(ctx, client)
	if err != nil {
		return err
	}
	found := false
	for _, option := range options {
		if option.Value == preset {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("DSH 未提供权限选项 %q", preset)
	}
	raw, err := client.call(ctx, "commands/execute", map[string]any{"agentId": sessionID, "line": "/permission " + preset, "submittedAttachments": []any{}})
	if err != nil {
		return b.translateCallError(err)
	}
	var result struct {
		Result struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Result.Kind == "" {
		return fmt.Errorf("%w: missing permission command result", errDSHProtocolChanged)
	}
	if result.Result.Kind != "success" {
		return fmt.Errorf("DSH 权限切换失败：%s", result.Result.Text)
	}
	current, err := b.permissionSelection(ctx, client, sessionID)
	if err != nil {
		return err
	}
	if current != preset {
		return fmt.Errorf("DSH 权限未生效：当前为 %s", current)
	}
	return nil
}
