package main

import (
	"errors"
	"net/http"
	"sync"
	"time"
)

type desktopMaintenanceGate struct {
	operations sync.RWMutex
	mu         sync.Mutex
	prepared   bool
	timer      *time.Timer
	generation uint64
}

var desktopMaintenance desktopMaintenanceGate

func (gate *desktopMaintenanceGate) Enter() bool { return gate.operations.TryRLock() }
func (gate *desktopMaintenanceGate) Leave()      { gate.operations.RUnlock() }
func (gate *desktopMaintenanceGate) Prepare(check func() error) error {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.prepared {
		return errors.New("另一个停止或升级操作正在进行")
	}
	if !gate.operations.TryLock() {
		return errors.New("正在处理操作，请完成后再重启或升级")
	}
	if err := check(); err != nil {
		gate.operations.Unlock()
		return err
	}
	gate.prepared = true
	gate.generation++
	generation := gate.generation
	gate.timer = time.AfterFunc(90*time.Second, func() { gate.resumeGeneration(generation) })
	return nil
}
func (gate *desktopMaintenanceGate) Resume() {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.resumeLocked()
}
func (gate *desktopMaintenanceGate) resumeGeneration(generation uint64) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.generation == generation {
		gate.resumeLocked()
	}
}
func (gate *desktopMaintenanceGate) resumeLocked() {
	if gate.prepared {
		gate.prepared = false
		gate.timer.Stop()
		gate.operations.Unlock()
	}
}
func (gate *desktopMaintenanceGate) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead || request.Header.Get("Upgrade") != "" {
			if !gate.Enter() {
				http.Error(writer, "设备正在维护，请稍后重试", http.StatusServiceUnavailable)
				return
			}
			defer gate.Leave()
		}
		next.ServeHTTP(writer, request)
	})
}

func desktopCheckIdle() error {
	if agentChatQueue != nil {
		for _, item := range agentChatQueue.List("", "") {
			switch item.Status {
			case chatQueueSending, chatQueueSteering, chatQueueTakingOver, chatQueueRecovering, chatQueueTakeoverCheck:
				return errors.New("消息正在投递，请完成后再重启或升级")
			}
		}
	}
	backend, ok := agentChatBackend.(*routingChatBackend)
	if !ok {
		return nil
	}
	if backend.codex != nil {
		backend.codex.mu.Lock()
		defer backend.codex.mu.Unlock()
		for session, turn := range backend.codex.lastTurn {
			if turn != "" && backend.codex.turnOwners[session] != "desktop" {
				return errors.New("Fleet 会话正在运行，请完成后再重启或升级")
			}
		}
	}
	if backend.dsh != nil {
		backend.dsh.sessMu.Lock()
		defer backend.dsh.sessMu.Unlock()
		for _, session := range backend.dsh.sessions {
			if session.running {
				return errors.New("会话正在运行，请完成后再重启或升级")
			}
		}
	}
	return nil
}
