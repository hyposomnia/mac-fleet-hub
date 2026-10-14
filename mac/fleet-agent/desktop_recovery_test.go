package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopRecoveryRetriesUntilConnected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	attempts := 0
	err := retryDesktopOperation(ctx, time.Millisecond, 4*time.Millisecond, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("offline")
		}
		return nil
	}, func(error) {})
	if err != nil || attempts != 3 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}
func TestDesktopRecoveryCancellationStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := retryDesktopOperation(ctx, time.Hour, time.Hour, func(context.Context) error { attempts++; cancel(); return errors.New("offline") }, func(error) {})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestDesktopComponentRecoversWithoutStoppingSibling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recovered := make(chan struct{})
	sibling := make(chan struct{})
	go superviseDesktopComponent(ctx, time.Millisecond, func(ctx context.Context) error { close(sibling); <-ctx.Done(); return ctx.Err() }, func(error) {})
	attempts := 0
	go superviseDesktopComponent(ctx, time.Millisecond, func(ctx context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("component crashed")
		}
		close(recovered)
		<-ctx.Done()
		return ctx.Err()
	}, func(error) {})
	select {
	case <-recovered:
	case <-time.After(time.Second):
		t.Fatal("component did not recover")
	}
	select {
	case <-sibling:
	case <-time.After(time.Second):
		t.Fatal("sibling not running")
	}
	if ctx.Err() != nil {
		t.Fatal("component failure cancelled healthy sibling")
	}
}

func TestNativeMissingComponentsDoNotPreventAgentStartup(t *testing.T) {
	state := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	stop, err := startDesktopServicesAt(context.Background(), deviceBinding{DeviceID: "m1", Index: "1", AgentPort: 7682, TerminalPort: 7681, FilesPort: 8080}, state, t.TempDir())
	if err != nil {
		t.Fatalf("missing optional components prevented startup: %v", err)
	}
	stop()
	desktopFilesPort.Store(0)
	desktopTerminalPort.Store(0)
}

func TestDesktopPortConflictChoosesAnotherLocalPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	occupied := listener.Addr().(*net.TCPAddr).Port
	chosen, err := availableDesktopPort(occupied)
	if err != nil || chosen == occupied || chosen == 0 {
		t.Fatalf("chosen=%d occupied=%d err=%v", chosen, occupied, err)
	}
}

func TestDesktopChildWithoutOwnedListenerStaysUnavailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var target atomic.Int64
	target.Store(-1)
	err := runDesktopComponent(ctx, exec.CommandContext(ctx, "/bin/sleep", "10"), 12345, &target, func(context.Context, int, int) bool { return false }, func() { t.Error("published child without listener") })
	if err == nil || target.Load() != -1 {
		t.Fatalf("unverified child published: port=%d err=%v", target.Load(), err)
	}
}
func TestDesktopChildLosingListenerIsWithdrawn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var target atomic.Int64
	target.Store(-1)
	checks := 0
	err := runDesktopComponent(ctx, exec.CommandContext(ctx, "/bin/sleep", "10"), 12345, &target, func(context.Context, int, int) bool { checks++; return checks == 1 }, func() {
		if target.Load() != 12345 {
			t.Error("verified child not published")
		}
	})
	if err == nil || target.Load() != -1 || checks < 2 {
		t.Fatalf("ownership loss not withdrawn: %d %d %v", target.Load(), checks, err)
	}
}

func TestDesktopRetryNowSkipsBackoffWithoutConcurrentJoin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	wake := make(chan struct{}, 1)
	attempts := 0
	err := retryDesktopOperation(ctx, time.Hour, time.Hour, func(context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("offline")
		}
		return nil
	}, func(error) { wake <- struct{}{} }, wake)
	if err != nil || attempts != 2 {
		t.Fatalf("retry-now attempts=%d err=%v", attempts, err)
	}
}
