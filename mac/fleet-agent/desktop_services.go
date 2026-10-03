package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type desktopServiceCommand struct {
	Program    string
	Args       []string
	Persistent bool
}

func desktopServiceCommands(binding deviceBinding, bin, state, home string) ([]desktopServiceCommand, error) {
	if !deviceIDPattern.MatchString(binding.DeviceID) || binding.DeviceID != "m"+binding.Index {
		return nil, errors.New("设备编号无效")
	}
	seen := map[int]bool{}
	for _, port := range []int{binding.AgentPort, binding.TerminalPort, binding.FilesPort} {
		if port < 1 || port > 65535 || seen[port] {
			return nil, errors.New("服务端口无效或冲突")
		}
		seen[port] = true
	}
	var password [24]byte
	if _, err := rand.Read(password[:]); err != nil {
		return nil, err
	}
	database := filepath.Join(state, "files.db")
	files := filepath.Join(bin, "filebrowser")
	return []desktopServiceCommand{
		{files, []string{"-d", database, "config", "init"}, false},
		{files, []string{"-d", database, "users", "add", "admin", hex.EncodeToString(password[:]), "--perm.admin"}, false},
		{files, []string{"-d", database, "config", "set", "--auth.method=noauth", "--baseURL", "/" + binding.DeviceID + "/files", "--root", home}, false},
		{files, []string{"-a", "127.0.0.1", "-p", strconv.Itoa(binding.FilesPort), "-d", database, "-r", home, "-b", "/" + binding.DeviceID + "/files"}, true},
		{filepath.Join(bin, "ttyd"), []string{"-i", "127.0.0.1", "-p", strconv.Itoa(binding.TerminalPort), "-b", "/" + binding.DeviceID + "/term", "-W", "-t", "disableLeaveAlert=true", "--url-arg", filepath.Join(bin, "fleet-attach")}, true},
	}, nil
}

func desktopResources() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(filepath.Dir(executable)), "Resources"), nil
}

func desktopInitializeFiles(commands []desktopServiceCommand, state string, run func(desktopServiceCommand) error) error {
	database := filepath.Join(state, "files.db")
	if _, err := os.Lstat(database); err == nil {
		if err := privateInfo(database, false); err != nil {
			return err
		}
		return run(commands[2])
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(state, ".files-init-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	stagedDatabase := filepath.Join(stage, "files.db")
	for _, command := range commands {
		command.Args = append([]string(nil), command.Args...)
		command.Args[1] = stagedDatabase
		if err := run(command); err != nil {
			return err
		}
	}
	if err := os.Chmod(stagedDatabase, 0600); err != nil {
		return err
	}
	return os.Rename(stagedDatabase, database)
}

func startDesktopServices(ctx context.Context, binding deviceBinding, state string, failed chan<- error) (context.CancelFunc, error) {
	resources, err := desktopResources()
	if err != nil {
		return nil, err
	}
	bin := filepath.Join(resources, "bin")
	for _, name := range []string{"ttyd", "tmux", "filebrowser", "fleet-attach"} {
		info, err := os.Stat(filepath.Join(bin, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return nil, fmt.Errorf("应用组件 %s 缺失，请重新安装完整应用", name)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	commands, err := desktopServiceCommands(binding, bin, state, home)
	if err != nil {
		return nil, err
	}
	for _, port := range []int{binding.FilesPort, binding.TerminalPort} {
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			return nil, fmt.Errorf("本机端口 %d 已被占用", port)
		}
		listener.Close()
	}
	if err := privateInfo(state, true); err != nil {
		return nil, err
	}
	logPath := filepath.Join(state, "services.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := privateInfo(logPath, false); err != nil {
		logFile.Close()
		return nil, err
	}
	scope, cancelScope := context.WithCancel(ctx)
	var processes sync.WaitGroup
	var closeOnce sync.Once
	cancel := func() {
		cancelScope()
		processes.Wait()
		closeOnce.Do(func() { logFile.Close() })
	}
	if err := desktopInitializeFiles(commands[:3], state, func(command desktopServiceCommand) error {
		return exec.CommandContext(scope, command.Program, command.Args...).Run()
	}); err != nil {
		cancel()
		return nil, errors.New("文件服务初始化失败")
	}
	for _, command := range commands[3:] {
		process := exec.CommandContext(scope, command.Program, command.Args...)
		process.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")
		if !command.Persistent {
			if err := process.Run(); err != nil {
				cancel()
				return nil, errors.New("文件服务初始化失败")
			}
			continue
		}
		process.Stdout = logFile
		process.Stderr = logFile
		if err := process.Start(); err != nil {
			cancel()
			return nil, errors.New("后台组件启动失败")
		}
		processes.Add(1)
		go func() {
			defer processes.Done()
			err := process.Wait()
			if scope.Err() == nil {
				select {
				case failed <- fmt.Errorf("后台组件退出: %s (%v)", filepath.Base(process.Path), err):
				default:
				}
			}
		}()
	}
	deadline := time.Now().Add(8 * time.Second)
	for _, port := range []int{binding.FilesPort, binding.TerminalPort} {
		for {
			connection, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 200*time.Millisecond)
			if err == nil {
				connection.Close()
				break
			}
			if time.Now().After(deadline) || scope.Err() != nil {
				cancel()
				return nil, errors.New("后台组件健康检查失败")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return cancel, nil
}
