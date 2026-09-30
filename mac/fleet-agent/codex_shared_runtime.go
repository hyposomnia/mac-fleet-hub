package main

import (
	"context"
	"encoding/json"
	"time"
)

// An unfinished rollout can survive an interrupted turn without a terminal
// append. In shared mode the live listener can disprove that historical turn;
// unlike an isolated sidecar, its idle status describes Desktop's runtime too.
// Keep unavailable/unknown states conservative and never claim a writer here.
func (b *codexChatBackend) reconcileSharedRolloutState(ctx context.Context, sessionID string, state codexRolloutTaskState) codexRolloutTaskState {
	if !codexUsesSharedDaemon() || state.turnID == "" || state.terminal {
		return state
	}
	b.mu.Lock()
	cachedTurn := b.lastTurn[sessionID]
	fleetLease := b.ownsCurrentFleetTurnLocked(sessionID, state.turnID)
	b.mu.Unlock()
	if fleetLease {
		// A turn/start response may precede both the live status update and the
		// rollout append. Its explicit same-turn lease remains authoritative.
		return state
	}
	stamp, haveStamp := b.rolloutStamp(sessionID)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rpc, err := b.ensure(ctx)
	if err != nil {
		return state
	}
	raw, err := rpc.call(ctx, "thread/read", map[string]interface{}{
		"threadId": sessionID, "includeTurns": false,
	})
	if err != nil {
		return state
	}
	var response codexResumeWire
	if json.Unmarshal(raw, &response) != nil || response.Thread.ID != sessionID ||
		len(response.Thread.Status) == 0 || string(response.Thread.Status) == "null" {
		return state
	}
	// codexThreadStatus defaults malformed data to idle; require an explicit
	// idle value so a protocol change or partial reply cannot open write access.
	var status struct {
		Type string `json:"type"`
	}
	var statusText string
	_ = json.Unmarshal(response.Thread.Status, &status)
	_ = json.Unmarshal(response.Thread.Status, &statusText)
	if status.Type != "idle" && statusText != "idle" {
		return state
	}
	if haveStamp {
		current, ok := b.rolloutStamp(sessionID)
		if !ok || current != stamp {
			return state
		}
	}
	b.mu.Lock()
	changed := b.lastTurn[sessionID] != cachedTurn || b.ownsCurrentFleetTurnLocked(sessionID, state.turnID)
	b.mu.Unlock()
	if changed {
		return state
	}
	state.terminal = true
	state.status = "interrupted"
	return state
}

func (b *codexChatBackend) runtimeRolloutTaskState(ctx context.Context, sessionID string) (codexRolloutTaskState, bool) {
	state, known := codexActiveRolloutTaskState(sessionID)
	if known {
		state = b.reconcileSharedRolloutState(ctx, sessionID, state)
	}
	return state, known
}

func (b *codexChatBackend) currentLiveRolloutTaskState(ctx context.Context, sessionID string) (codexRolloutTaskState, bool) {
	state, known := codexCurrentRolloutTaskState(sessionID)
	if known {
		state = b.reconcileSharedRolloutState(ctx, sessionID, state)
	}
	return state, known
}
