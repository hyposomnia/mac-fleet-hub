package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type deviceBinding struct {
	Origin       string `json:"origin"`
	DeviceID     string `json:"device_id"`
	DeviceName   string `json:"device_name,omitempty"`
	OwnerEmail   string `json:"owner_email"`
	DeviceToken  string `json:"device_token"`
	ProxyToken   string `json:"proxy_token"`
	Index        string `json:"index"`
	AgentPort    int    `json:"agent_port"`
	TerminalPort int    `json:"terminal_port"`
	FilesPort    int    `json:"files_port"`
	Locked       bool   `json:"locked"`
	Complete     bool   `json:"complete"`
}

func bindingPath() string {
	home, _ := os.UserHomeDir()
	return envOr("FLEET_BINDING_FILE", filepath.Join(home, ".macfleet", "binding.json"))
}

func lockDeviceState(path string) (*os.File, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	if err := privateInfo(directory, true); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(directory, "authorization.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = privateInfo(lockPath, false); err != nil {
		file.Close()
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("另一个 login/logout 正在运行，请先完成该操作")
	}
	return file, nil
}

func privateInfo(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("不安全的私有文件权限: %s", path)
	}
	return nil
}

func writePrivateJSON(path string, value any) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := privateInfo(directory, true); err != nil {
		return err
	}
	if err := privateInfo(path, false); err != nil && !os.IsNotExist(err) {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".binding-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}

func readPrivateJSON(path string, value any) error {
	if err := privateInfo(filepath.Dir(path), true); err != nil {
		return err
	}
	if err := privateInfo(path, false); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, file, 32<<10)).Decode(value)
}

func readDeviceBinding(path string) (deviceBinding, error) {
	var binding deviceBinding
	if err := readPrivateJSON(path, &binding); err != nil {
		return binding, err
	}
	if _, err := validateFleetOrigin(binding.Origin); err != nil {
		return binding, err
	}
	if len(binding.DeviceToken) < 40 || len(binding.ProxyToken) < 40 || binding.DeviceToken == binding.ProxyToken || !deviceIDPattern.MatchString(binding.DeviceID) {
		return binding, errors.New("设备凭据损坏，请重新关联")
	}
	return binding, nil
}

func validateFleetOrigin(value string) (string, error) {
	origin, err := url.Parse(value)
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") || origin.Opaque != "" {
		return "", errors.New("请输入服务网页地址，不是 Headscale 控制面地址")
	}
	if origin.Scheme != "https" && !(origin.Scheme == "http" && net.ParseIP(origin.Hostname()).IsLoopback()) {
		return "", errors.New("服务地址必须使用 HTTPS；HTTP 只允许本地验证")
	}
	origin.Path = ""
	return origin.String(), nil
}

func deviceHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 8 * time.Second}}
}
