//go:build fleet_desktop

package main

import (
	"context"
	"runtime"

	"tailscale.com/net/netns"
	"tailscale.com/tsnet"
)

type nativeDesktopMesh struct{ *tsnet.Server }

func (mesh *nativeDesktopMesh) Up(ctx context.Context) error {
	_, err := mesh.Server.Up(ctx)
	return err
}

func init() {
	createDesktopMesh = func(options desktopMeshOptions) (desktopMesh, error) {
		if runtime.GOOS == "darwin" {
			// tsnet installs no OS routes. Follow the host's route, including
			// private control servers reached through another VPN.
			netns.SetDisableBindConnToInterface(func(string, ...any) {}, true)
		}
		return &nativeDesktopMesh{&tsnet.Server{Dir: options.Directory, Hostname: options.Hostname, ControlURL: options.ControlURL, AuthKey: options.AuthKey, Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}}, nil
	}
}
