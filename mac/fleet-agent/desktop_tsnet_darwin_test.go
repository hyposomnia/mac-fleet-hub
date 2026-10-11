//go:build fleet_desktop && darwin

package main

import (
	"errors"
	"net"
	"syscall"
	"testing"

	"tailscale.com/net/netmon"
	"tailscale.com/net/netns"
	"tailscale.com/util/eventbus"
)

func TestDesktopMeshUsesSystemRouteWithoutBindingPhysicalInterface(t *testing.T) {
	netns.SetDisableBindConnToInterface(t.Logf, false)
	t.Cleanup(func() { netns.SetDisableBindConnToInterface(t.Logf, false) })
	_, err := createDesktopMesh(desktopMeshOptions{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	monitor, err := netmon.New(eventbus.New(), t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	dialer := netns.FromDialer(t.Logf, monitor, &net.Dialer{}).(*net.Dialer)
	for _, address := range []string{"198.51.100.10:443", "[2001:db8::10]:443"} {
		raw := &interfaceBindingProbe{}
		if err := dialer.Control("tcp", address, raw); err != nil {
			t.Errorf("system route for %s was overridden: %v", address, err)
		}
		if raw.calls != 0 {
			t.Errorf("bound %s to an interface instead of using its system route", address)
		}
	}
}

type interfaceBindingProbe struct{ calls int }

func (probe *interfaceBindingProbe) Control(func(uintptr)) error {
	probe.calls++
	return errors.New("unexpected interface binding")
}
func (*interfaceBindingProbe) Read(func(uintptr) bool) error  { return syscall.EBADF }
func (*interfaceBindingProbe) Write(func(uintptr) bool) error { return syscall.EBADF }
