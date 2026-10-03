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
	Phase string `json:"phase"`
	Error string `json:"error,omitempty"`
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
	pairing := newDesktopPairing(loginOptions{Path: bindingPath(), Join: network.Join, Setup: setup})
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
		control.SetRuntime("connecting", "")
		scope, stop := context.WithTimeout(ctx, 2*time.Minute)
		defer stop()
		if err := network.Resume(scope, binding); err != nil {
			control.SetRuntime("failed", err.Error())
			return
		}
		if err := setup(scope, binding); err != nil {
			control.SetRuntime("failed", err.Error())
		}
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
