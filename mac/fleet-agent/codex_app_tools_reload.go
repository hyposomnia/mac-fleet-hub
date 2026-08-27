package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var findCodexDesktopAppToolsPipe = codexDesktopAppToolsPipe
var codexAnyPhysicalActiveTurn = systemCodexAnyPhysicalActiveTurn

type codexMCPServerStatus struct {
	Name          string                     `json:"name"`
	RuntimeStatus string                     `json:"runtimeStatus"`
	Tools         map[string]json.RawMessage `json:"tools"`
}

// reloadCodexAppTools restarts only MCP runtimes. The shared app-server and
// all of its client connections remain alive, so a Desktop restart can rotate
// its native app-tools pipe without recreating the thread writer process.
func (b *codexChatBackend) reloadCodexAppTools(ctx context.Context, pipePath string) error {
	rpc, err := b.ensure(ctx)
	if err != nil {
		return err
	}
	if _, err := rpc.call(ctx, "config/mcpServer/reload", nil); err != nil {
		return fmt.Errorf("reload Codex MCP servers: %w", err)
	}
	raw, err := rpc.call(ctx, "mcpServerStatus/list", map[string]interface{}{
		"cursor": nil,
		"limit":  100,
		"detail": "full",
	})
	if err != nil {
		return fmt.Errorf("read Codex MCP server status: %w", err)
	}
	var result struct {
		Data []codexMCPServerStatus `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode Codex MCP server status: %w", err)
	}
	for _, server := range result.Data {
		if server.Name != "codex_app" {
			continue
		}
		if len(server.Tools) == 0 {
			return fmt.Errorf("Codex app tools runtime has no tools")
		}
		// Without threadId app-server may omit runtimeStatus even though the
		// global inventory is live. A concrete non-connected status is fatal;
		// a populated tool catalog with null status is sufficient here.
		if server.RuntimeStatus != "" && server.RuntimeStatus != "connected" {
			return fmt.Errorf("Codex app tools runtime is %q", server.RuntimeStatus)
		}
		b.mu.Lock()
		b.appToolsPipe = pipePath
		b.mu.Unlock()
		return nil
	}
	return fmt.Errorf("Codex app tools MCP is missing")
}

func (b *codexChatBackend) hasTurnOrInteractionInProgress() bool {
	b.mu.Lock()
	if len(b.pending) > 0 {
		b.mu.Unlock()
		return true
	}
	for _, turnID := range b.lastTurn {
		if turnID != "" {
			b.mu.Unlock()
			return true
		}
	}
	b.mu.Unlock()
	return codexAnyPhysicalActiveTurn()
}

func systemCodexAnyPhysicalActiveTurn() bool {
	lockPaths, _ := filepath.Glob(filepath.Join(cfg.CodexHome, "thread-writer-locks", "*.lock"))
	for _, lockPath := range lockPaths {
		sessionID := strings.TrimSuffix(filepath.Base(lockPath), ".lock")
		if sessionID == "" || codexThreadWriterProcessOwner(sessionID) == "" {
			continue
		}
		state, known := codexCurrentRolloutTaskState(sessionID)
		if known && state.turnID != "" && !state.terminal {
			return true
		}
		if _, err := os.Stat(lockPath); err == nil && !known {
			// A live writer without readable rollout evidence is unknown; do not
			// reload a process-global MCP runtime under it.
			return true
		}
	}
	return false
}

func (b *codexChatBackend) runCodexAppToolsPipeWatcher(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		pipePath, err := "", fmt.Errorf("Codex Desktop app tools are unavailable")
		if codexDesktopAppToolsSupported() {
			pipePath, err = findCodexDesktopAppToolsPipe()
		}
		if err == nil {
			b.mu.Lock()
			loadedPipe := b.appToolsPipe
			b.mu.Unlock()
			if pipePath != loadedPipe && !b.hasTurnOrInteractionInProgress() {
				reloadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				reloadErr := b.reloadCodexAppTools(reloadCtx, pipePath)
				cancel()
				if reloadErr != nil {
					log.Printf("Codex Desktop app tools reload pending: %v", reloadErr)
				} else {
					log.Printf("Codex Desktop app tools connected (%s)", filepath.Base(pipePath))
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
