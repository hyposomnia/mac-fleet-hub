package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type desktopMesh interface {
	Up(context.Context) error
	Listen(string, string) (net.Listener, error)
	Close() error
}
type desktopMeshOptions struct{ Directory, Hostname, ControlURL, AuthKey string }
type desktopNetworkConfig struct {
	Schema     int    `json:"schema"`
	Origin     string `json:"origin"`
	DeviceID   string `json:"device_id"`
	ControlURL string `json:"control_url"`
}
type desktopNetwork struct {
	mu        sync.Mutex
	directory string
	create    func(desktopMeshOptions) (desktopMesh, error)
	mesh      desktopMesh
	config    desktopNetworkConfig
}

var createDesktopMesh = func(desktopMeshOptions) (desktopMesh, error) {
	return nil, errors.New("此二进制未包含图形化组网组件")
}

func newDesktopNetwork(directory string, create func(desktopMeshOptions) (desktopMesh, error)) *desktopNetwork {
	return &desktopNetwork{directory: directory, create: create}
}

func (network *desktopNetwork) Join(ctx context.Context, grant pairingGrant, _ bool) error {
	config := desktopNetworkConfig{Schema: 1, Origin: grant.Origin, DeviceID: grant.DeviceID, ControlURL: grant.LoginServer}
	return network.connect(ctx, config, grant.AuthKey)
}

func (network *desktopNetwork) Resume(ctx context.Context, binding deviceBinding) error {
	var config desktopNetworkConfig
	if err := readPrivateJSON(filepath.Join(network.directory, "network.json"), &config); err != nil {
		return err
	}
	if binding.Locked || !binding.Complete || binding.Origin != config.Origin || binding.DeviceID != config.DeviceID {
		return errors.New("设备授权与网络状态不匹配")
	}
	return network.connect(ctx, config, "")
}

func (network *desktopNetwork) connect(ctx context.Context, config desktopNetworkConfig, key string) error {
	network.mu.Lock()
	defer network.mu.Unlock()
	if config.Schema != 1 || !deviceIDPattern.MatchString(config.DeviceID) {
		return errors.New("网络配置无效")
	}
	if _, err := validateFleetOrigin(config.Origin); err != nil {
		return err
	}
	if _, err := validateFleetOrigin(config.ControlURL); err != nil {
		return err
	}
	if network.mesh != nil {
		if network.config != config {
			return errors.New("请先解除当前设备关联")
		}
		return nil
	}
	digest := sha256.Sum256([]byte(config.Origin + "/" + config.DeviceID))
	directory := filepath.Join(network.directory, fmt.Sprintf("mesh-%x", digest[:12]))
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := privateInfo(directory, true); err != nil {
		return err
	}
	mesh, err := network.create(desktopMeshOptions{Directory: directory, Hostname: "mac" + strings.TrimPrefix(config.DeviceID, "m"), ControlURL: config.ControlURL, AuthKey: key})
	if err != nil {
		return err
	}
	if err = mesh.Up(ctx); err != nil {
		mesh.Close()
		return errors.New("设备网络接入失败，请检查服务器连接后重试")
	}
	if err = writePrivateJSON(filepath.Join(network.directory, "network.json"), config); err != nil {
		mesh.Close()
		return err
	}
	network.mesh = mesh
	network.config = config
	return nil
}

func (network *desktopNetwork) Listen(port int) (net.Listener, error) {
	network.mu.Lock()
	defer network.mu.Unlock()
	if network.mesh == nil || port < 1 || port > 65535 {
		return nil, errors.New("设备网络尚未就绪")
	}
	return network.mesh.Listen("tcp", ":"+strconv.Itoa(port))
}

func (network *desktopNetwork) Close() {
	network.mu.Lock()
	defer network.mu.Unlock()
	if network.mesh != nil {
		network.mesh.Close()
		network.mesh = nil
	}
}
