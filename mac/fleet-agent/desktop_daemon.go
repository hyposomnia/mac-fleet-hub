package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type desktopRuntimeState struct {
	Phase      string                           `json:"phase"`
	Components map[string]desktopComponentState `json:"components,omitempty"`
	Error      string                           `json:"error,omitempty"`
}

func runDesktopDaemon() error {
	if err := configureDesktopEnvironment(); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	control, err := startDesktopControl(desktopStateDirectory(), bindingPath(), desktopDiskStatus)
	if err != nil {
		return err
	}
	defer control.Close()
	network := newDesktopNetwork(desktopStateDirectory(), createDesktopMesh)
	defer network.Close()
	failed := make(chan error, 4)
	var setupMu sync.Mutex
	var stopServices context.CancelFunc
	var serving atomic.Bool
	control.mu.Lock()
	control.idleCheck = func() error {
		if serving.Load() {
			return desktopCheckIdle()
		}
		return nil
	}
	control.mu.Unlock()
	setup := func(scope context.Context, binding deviceBinding) error {
		setupMu.Lock()
		defer setupMu.Unlock()
		if serving.Load() {
			return nil
		}
		if err := scope.Err(); err != nil {
			return err
		}
		control.SetRuntime("starting", "")
		stop, err := startDesktopServices(ctx, binding, desktopStateDirectory(), failed)
		if err != nil {
			control.SetRuntime("failed", err.Error())
			return err
		}
		stopServices = stop
		listener, err := network.Listen(binding.AgentPort)
		if err != nil {
			stop()
			return err
		}
		ready := make(chan struct{})
		go func() {
			err := serveAgent(listener, func() { serving.Store(true); close(ready) })
			if ctx.Err() == nil {
				failed <- err
			}
		}()
		select {
		case <-ready:
			control.SetRuntime("running", "")
			return nil
		case <-scope.Done():
			stop()
			listener.Close()
			return scope.Err()
		case <-time.After(30 * time.Second):
			stop()
			listener.Close()
			return errors.New("设备服务启动超时")
		}
	}
	pairing := newDesktopPairing(loginOptions{Path: bindingPath(), Join: network.Join, Setup: setup, Authorize: desktopOAuthAuthorization})
	defer func() {
		cancel()
		pairing.Cancel()
		setupMu.Lock()
		defer setupMu.Unlock()
		if stopServices != nil {
			stopServices()
		}
	}()
	control.SetPairing(pairing)
	control.SetRuntime("unbound", "")
	go func() {
		binding, err := readDeviceBinding(bindingPath())
		if os.IsNotExist(err) || err == nil && (!binding.Complete || binding.Locked) {
			return
		}
		if err != nil {
			control.SetRuntime("failed", "设备授权文件无法读取")
			return
		}
		_ = retryDesktopOperation(ctx, time.Second, 15*time.Second, func(ctx context.Context) error {
			current, err := readDeviceBinding(bindingPath())
			if err != nil || current.Locked || !sameDeviceAuthorization(current, binding) {
				return context.Canceled
			}
			control.SetRuntime("connecting", "")
			scope, stop := context.WithTimeout(ctx, 30*time.Second)
			defer stop()
			if err := network.Resume(scope, binding); err != nil {
				return err
			}
			return setup(scope, binding)
		}, func(err error) { control.SetRuntime("connecting", "正在重新连接："+err.Error()) }, control.retryRequests)
	}()
	select {
	case <-ctx.Done():
		return nil
	case err := <-failed:
		return err
	}
}

func (control *desktopControl) SetRuntime(phase, message string) {
	control.mu.Lock()
	defer control.mu.Unlock()
	control.runtime = desktopRuntimeState{Phase: phase, Error: message}
}

// Retries are bounded in frequency, not count: offline boot must heal without a
// process restart. Short waits also make resume from sleep responsive.
func retryDesktopOperation(ctx context.Context, initial, maximum time.Duration, operation func(context.Context) error, failed func(error), retrySignals ...<-chan struct{}) error {
	var retry <-chan struct{}
	if len(retrySignals) > 0 {
		retry = retrySignals[0]
	}
	delay := initial
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		failed(err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		case <-retry:
			timer.Stop()
			delay = initial / 2
		}
		delay *= 2
		if delay > maximum {
			delay = maximum
		}
	}
}
