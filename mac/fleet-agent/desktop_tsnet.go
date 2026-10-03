//go:build fleet_desktop

package main

import (
	"context"
	"tailscale.com/tsnet"
)

type nativeDesktopMesh struct{ *tsnet.Server }

func (mesh *nativeDesktopMesh) Up(ctx context.Context) error {
	_, err := mesh.Server.Up(ctx)
	return err
}

func init() {
	createDesktopMesh = func(options desktopMeshOptions) (desktopMesh, error) {
		return &nativeDesktopMesh{&tsnet.Server{Dir: options.Directory, Hostname: options.Hostname, ControlURL: options.ControlURL, AuthKey: options.AuthKey, Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}}, nil
	}
}
