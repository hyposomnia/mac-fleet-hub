package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

type desktopPairingState struct {
	Phase      string `json:"phase"`
	Attempt    string `json:"attempt"`
	Origin     string `json:"origin"`
	URL        string `json:"url,omitempty"`
	Code       string `json:"code,omitempty"`
	DeviceID   string `json:"device_id,omitempty"`
	OwnerEmail string `json:"owner_email,omitempty"`
	Error      string `json:"error,omitempty"`
}

type desktopPairing struct {
	mu      sync.Mutex
	state   desktopPairingState
	options loginOptions
	cancel  context.CancelFunc
	consent chan struct{}
}

func newDesktopPairing(options loginOptions) *desktopPairing {
	return &desktopPairing{options: options, state: desktopPairingState{Phase: "idle"}}
}

func (pairing *desktopPairing) Snapshot() desktopPairingState {
	pairing.mu.Lock()
	defer pairing.mu.Unlock()
	return pairing.state
}

func (pairing *desktopPairing) Start(origin string) error {
	origin, err := validateFleetOrigin(origin)
	if err != nil {
		return err
	}
	pairing.mu.Lock()
	defer pairing.mu.Unlock()
	if pairing.cancel != nil {
		return errors.New("设备关联正在进行")
	}
	if pairing.options.Join == nil || pairing.options.Setup == nil {
		return errors.New("图形化网络组件未就绪")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	pairing.cancel = cancel
	pairing.consent = make(chan struct{}, 1)
	pairing.state = desktopPairingState{Phase: "starting", Attempt: hex.EncodeToString(nonce[:]), Origin: origin}
	options := pairing.options
	options.NoOpen = true
	options.Browser = func(url, code string) {
		pairing.mu.Lock()
		defer pairing.mu.Unlock()
		pairing.state.Phase = "browser"
		pairing.state.URL = url
		pairing.state.Code = code
	}
	options.Confirm = func(grant pairingGrant) error {
		pairing.mu.Lock()
		pairing.state.Phase = "awaiting_confirmation"
		pairing.state.DeviceID = grant.DeviceID
		pairing.state.OwnerEmail = grant.OwnerEmail
		consent := pairing.consent
		pairing.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-consent:
			return ctx.Err()
		}
	}
	go func() {
		err := pairDevice(ctx, origin, options)
		pairing.mu.Lock()
		defer pairing.mu.Unlock()
		cancel()
		pairing.cancel = nil
		if err == nil {
			pairing.state.Phase = "complete"
		} else if errors.Is(err, context.Canceled) {
			pairing.state.Phase = "cancelled"
		} else {
			pairing.state.Phase = "failed"
			pairing.state.Error = err.Error()
		}
	}()
	return nil
}

func (pairing *desktopPairing) Confirm(expected desktopPairingState) error {
	pairing.mu.Lock()
	defer pairing.mu.Unlock()
	state := pairing.state
	if state.Phase != "awaiting_confirmation" || state.Attempt != expected.Attempt || state.Origin != expected.Origin || state.OwnerEmail != expected.OwnerEmail || state.DeviceID != expected.DeviceID || state.Code != expected.Code {
		return errors.New("关联状态已变化，请重新核对账号和设备")
	}
	pairing.state.Phase = "joining"
	pairing.consent <- struct{}{}
	return nil
}

func (pairing *desktopPairing) Cancel() {
	pairing.mu.Lock()
	defer pairing.mu.Unlock()
	if pairing.cancel != nil {
		pairing.cancel()
	}
}
