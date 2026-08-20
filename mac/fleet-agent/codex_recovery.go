package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

var codexRPCCallTimeout = 20 * time.Second
var codexRecoveryConnectTimeout = 15 * time.Second

type recoveringCodexRPC struct {
	backend *codexChatBackend
	inner   codexRPCConn
}

func (r *recoveringCodexRPC) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	raw, err := r.callOnce(ctx, method, params)
	if err == nil || ctx.Err() != nil || !recoverableCodexRPCError(err) {
		return raw, err
	}

	replacement, recoveryErr := r.backend.replaceFailedRPC(r, err)
	if recoveryErr != nil {
		r.backend.scheduleSelfRestart(recoveryErr)
		return nil, fmt.Errorf("%w: %s: %v", errAgentRestarting, method, recoveryErr)
	}
	if !codexRPCMethodSafeToRetry(method) {
		return nil, fmt.Errorf("%w: %s", errAppServerRecovered, method)
	}

	next, ok := replacement.(*recoveringCodexRPC)
	if !ok {
		return replacement.call(ctx, method, params)
	}
	raw, err = next.callOnce(ctx, method, params)
	if err == nil || ctx.Err() != nil || !recoverableCodexRPCError(err) {
		return raw, err
	}
	failedRPC := codexRPCConn(next)
	if codexUsesIsolatedSidecar() {
		replacement, recoveryErr = r.backend.restartFailedSidecarRPC(next, err)
		if recoveryErr == nil {
			failedRPC = replacement
			if final, ok := replacement.(*recoveringCodexRPC); ok {
				raw, err = final.callOnce(ctx, method, params)
			} else {
				raw, err = replacement.call(ctx, method, params)
			}
			if err == nil || ctx.Err() != nil || !recoverableCodexRPCError(err) {
				return raw, err
			}
		} else {
			err = recoveryErr
		}
	}
	r.backend.resetRPCIf(failedRPC)
	r.backend.scheduleSelfRestart(err)
	return nil, fmt.Errorf("%w: %s: %v", errAgentRestarting, method, err)
}

func (r *recoveringCodexRPC) callOnce(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	timeout := codexRPCCallTimeout
	if method == "thread/list" {
		timeout = codexCatalogCallTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := r.inner.call(callCtx, method, params)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return nil, fmt.Errorf("%w: %s", errAppServerTimeout, method)
	}
	return raw, err
}

func (r *recoveringCodexRPC) notify(method string, params interface{}) error {
	return r.recoverWriteFailure(method, r.inner.notify(method, params))
}

func (r *recoveringCodexRPC) respond(id json.RawMessage, result interface{}) error {
	return r.recoverWriteFailure("response", r.inner.respond(id, result))
}

func (r *recoveringCodexRPC) recoverWriteFailure(method string, err error) error {
	if err == nil || !recoverableCodexRPCError(err) {
		return err
	}
	if _, recoveryErr := r.backend.replaceFailedRPC(r, err); recoveryErr != nil {
		r.backend.scheduleSelfRestart(recoveryErr)
		return fmt.Errorf("%w: %s: %v", errAgentRestarting, method, recoveryErr)
	}
	return fmt.Errorf("%w: %s", errAppServerRecovered, method)
}

func (r *recoveringCodexRPC) notifications() <-chan rpcNotification {
	return r.inner.notifications()
}

func (r *recoveringCodexRPC) terminateCommandDescendants() {
	if cleaner, ok := r.inner.(interface{ terminateCommandDescendants() }); ok {
		cleaner.terminateCommandDescendants()
	}
}

func codexRPCMethodSafeToRetry(method string) bool {
	switch method {
	case "thread/list", "thread/read", "thread/resume", "thread/items/list", "thread/turns/list", "skills/list", "model/list":
		return true
	default:
		return false
	}
}

func recoverableCodexRPCError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errAppServerTimeout) || errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{"broken pipe", "closed pipe", "connection reset", "file already closed"} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func (b *codexChatBackend) replaceFailedRPC(failed codexRPCConn, cause error) (codexRPCConn, error) {
	b.resetRPCIf(failed)
	recoveryCtx, cancel := context.WithTimeout(context.Background(), codexRecoveryConnectTimeout)
	defer cancel()
	rpc, err := b.ensure(recoveryCtx)
	if err != nil {
		return nil, fmt.Errorf("Codex app-server recovery after %v: %w", cause, err)
	}
	return rpc, nil
}

// restartFailedSidecarRPC replaces the process, rather than merely opening a
// new connection to the same unresponsive isolated app-server. launchd may
// return before the new Unix socket is ready, so connection attempts are kept
// inside the recovery deadline and must not independently restart fleet-agent.
func (b *codexChatBackend) restartFailedSidecarRPC(failed codexRPCConn, cause error) (codexRPCConn, error) {
	b.connectMu.Lock()
	defer b.connectMu.Unlock()
	b.resetRPCIf(failed)
	if err := restartCodexFleetSidecar(); err != nil {
		return nil, fmt.Errorf("restart Codex sidecar after %v: %w", cause, err)
	}

	recoveryCtx, cancel := context.WithTimeout(context.Background(), codexRecoveryConnectTimeout)
	defer cancel()

	for {
		b.mu.Lock()
		if b.rpc != nil {
			rpc := b.rpc
			b.mu.Unlock()
			return rpc, nil
		}
		b.mu.Unlock()

		inner, cleanup, err := b.connect(recoveryCtx)
		if err == nil {
			rpc := codexRPCConn(&recoveringCodexRPC{backend: b, inner: inner})
			b.mu.Lock()
			b.rpc = rpc
			b.cleanup = cleanup
			b.mu.Unlock()
			go b.dispatch(rpc, rpc.notifications())
			return rpc, nil
		}
		select {
		case <-recoveryCtx.Done():
			return nil, fmt.Errorf("reconnect Codex sidecar after %v: %w", cause, err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (b *codexChatBackend) resetRPCIf(failed codexRPCConn) {
	b.mu.Lock()
	if b.rpc != failed {
		b.mu.Unlock()
		return
	}
	cleanup := b.cleanup
	b.rpc = nil
	b.cleanup = nil
	b.loadedThreads = map[string]bool{}
	b.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}

func (b *codexChatBackend) scheduleSelfRestart(reason error) {
	b.restartOnce.Do(func() {
		b.restart(reason)
	})
}

func scheduleFleetAgentRestart(reason error) {
	log.Printf("Codex app-server recovery failed; fleet-agent will restart: %v", reason)
	go func() {
		time.Sleep(750 * time.Millisecond)
		os.Exit(1)
	}()
}
