package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func desktopMayConfigureEnvironment() bool {
	return os.Getenv("FLEET_DESKTOP_MANAGED") != "1"
}

func desktopRequest(ctx context.Context, directory, action string, input io.Reader, output io.Writer) error {
	method, endpoint := http.MethodGet, "/status"
	switch action {
	case "status":
	case "disk-recheck":
		method, endpoint = http.MethodPost, "/disk/recheck"
	case "settings":
		method, endpoint = http.MethodPut, "/settings"
	case "pair-start":
		method, endpoint = http.MethodPost, "/pair/start"
	case "pair-confirm":
		method, endpoint = http.MethodPost, "/pair/confirm"
	case "pair-cancel":
		method, endpoint = http.MethodPost, "/pair/cancel"
	case "prepare-stop":
		method, endpoint = http.MethodPost, "/lifecycle/prepare"
	case "resume":
		method, endpoint = http.MethodPost, "/lifecycle/resume"
	case "logout":
		method, endpoint = http.MethodPost, "/logout"
	default:
		return errors.New("不支持的本机管理操作")
	}
	if err := privateInfo(directory, true); err != nil {
		return errors.New("后台尚未运行或本机状态目录不安全")
	}
	socket := filepath.Join(directory, "control.sock")
	info, err := os.Lstat(socket)
	if err != nil {
		return errors.New("后台尚未运行")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		return errors.New("管理端点不安全")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, method, "http://local"+endpoint, input)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return errors.New("无法连接后台服务")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 32769))
	if err != nil || len(data) > 32768 {
		return errors.New("后台响应无效")
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &failure) != nil || failure.Error == "" {
			return fmt.Errorf("后台拒绝操作 (HTTP %d)", response.StatusCode)
		}
		return errors.New(failure.Error)
	}
	_, err = output.Write(data)
	return err
}

func runDesktopCommand(args []string) int {
	if len(args) != 1 {
		return done(errors.New("请指定本机管理操作"))
	}
	if args[0] == "autostart-start" {
		return done(desktopAutostartStart(desktopStateDirectory(), func(arguments ...string) error {
			_, err := runCmd("/bin/launchctl", arguments...)
			return err
		}))
	}
	return done(desktopRequest(context.Background(), desktopStateDirectory(), args[0], os.Stdin, os.Stdout))
}

func desktopAutostartStart(directory string, launch func(...string) error) error {
	if err := privateInfo(directory, true); err != nil {
		return err
	}
	definition := filepath.Join(directory, "agent.plist")
	if err := privateInfo(definition, false); err != nil {
		return err
	}
	if launch("print", svcTarget()) == nil {
		return errors.New("检测到旧版 Fleet 后台，本次不会启动或替换它")
	}
	if launch("print", svcDomain()+"/com.macfleet.desktop-agent") == nil {
		return nil
	}
	return launch("bootstrap", svcDomain(), definition)
}
