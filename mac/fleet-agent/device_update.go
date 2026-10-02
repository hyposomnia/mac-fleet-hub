package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func supportsDeviceAuthorization(data []byte) bool {
	var capabilities struct {
		Device  int `json:"device_authorization"`
		Browser int `json:"browser_pairing"`
		Proxy   int `json:"agent_proxy"`
	}
	return json.Unmarshal(data, &capabilities) == nil && capabilities.Device == 1 && capabilities.Browser == 1 && capabilities.Proxy == 1
}

func signedAgentTeam(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", path).Run(); err != nil {
		return "", errors.New("客户端签名验证失败")
	}
	output, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-d", "--verbose=4", path).CombinedOutput()
	if err != nil {
		return "", errors.New("无法读取客户端代码签名")
	}
	var team string
	identifier, developer := false, false
	for _, line := range strings.Split(string(output), "\n") {
		if line == "Identifier=com.macfleet.fleet-agent" {
			identifier = true
		}
		if strings.HasPrefix(line, "Authority=Developer ID Application:") {
			developer = true
		}
		if strings.HasPrefix(line, "TeamIdentifier=") {
			team = strings.TrimPrefix(line, "TeamIdentifier=")
		}
	}
	if !identifier || !developer || team == "" || team == "not set" {
		return "", errors.New("拒绝非正式 Developer ID 客户端")
	}
	return team, nil
}

func validateAgentUpdate(self string, data []byte) error {
	team, err := signedAgentTeam(self)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(self), ".fleet-agent-verify-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	file.Close()
	if err != nil {
		return err
	}
	if err = os.Chmod(file.Name(), 0700); err != nil {
		return err
	}
	nextTeam, err := signedAgentTeam(file.Name())
	if err != nil {
		return err
	}
	if nextTeam != team {
		return errors.New("更新客户端的签名团队与当前版本不同")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, file.Name(), "capabilities").Output()
	if err != nil || !supportsDeviceAuthorization(output) {
		return errors.New("拒绝降级：更新客户端必须保留逐用户设备授权")
	}
	return nil
}
