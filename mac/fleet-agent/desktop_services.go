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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type desktopServiceCommand struct {
	Program string
	Args    []string
}

func desktopServiceCommands(binding deviceBinding, bin, state string) ([]desktopServiceCommand, error) {
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
		{files, []string{"-d", database, "config", "init"}},
		{files, []string{"-d", database, "users", "add", "admin", hex.EncodeToString(password[:]), "--perm.admin"}},
		{files, []string{"-d", database, "config", "set", "--auth.method=noauth", "--baseURL", "/" + binding.DeviceID + "/files", "--root", "/"}},
		{files, []string{"-a", "127.0.0.1", "-p", strconv.Itoa(binding.FilesPort), "-d", database, "-r", "/", "-b", "/" + binding.DeviceID + "/files"}},
		{filepath.Join(bin, "ttyd"), []string{"-i", "127.0.0.1", "-p", strconv.Itoa(binding.TerminalPort), "-b", "/" + binding.DeviceID + "/term", "-W", "-t", "disableLeaveAlert=true", "--url-arg", filepath.Join(bin, "fleet-attach")}},
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

// Zero selects the legacy configured port. Negative means the native component
// is unavailable; never proxy to a conflicting unrelated local service.
var desktopFilesPort atomic.Int64
var desktopTerminalPort atomic.Int64

func availableDesktopPort(preferred int) (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(preferred))
	if err != nil {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func superviseDesktopComponent(ctx context.Context, delay time.Duration, run func(context.Context) error, report func(error)) {
	_ = retryDesktopOperation(ctx, delay, 15*time.Second, func(ctx context.Context) error {
		err := run(ctx)
		if err == nil {
			err = errors.New("组件已退出，正在重新启动")
		}
		return err
	}, report)
}

func startDesktopServices(ctx context.Context, binding deviceBinding, state string, _ chan<- error) (context.CancelFunc, error) {
	resources, err := desktopResources()
	if err != nil {
		return nil, err
	}
	return startDesktopServicesAt(ctx, binding, state, filepath.Join(resources, "bin"))
}

func startDesktopServicesAt(ctx context.Context, binding deviceBinding, state, bin string) (context.CancelFunc, error) {
	if _, err := desktopServiceCommands(binding, bin, state); err != nil {
		return nil, err
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
	var workers sync.WaitGroup
	var once sync.Once
	desktopFilesPort.Store(-1)
	desktopTerminalPort.Store(-1)
	for _, files := range []bool{true, false} {
		workers.Add(1)
		go func(files bool) {
			defer workers.Done()
			target := &desktopTerminalPort
			name := "终端"
			if files {
				target = &desktopFilesPort
				name = "文件"
			}
			superviseDesktopComponent(scope, time.Second, func(ctx context.Context) error {
				target.Store(-1)
				setDesktopComponent(name, "connecting", "")
				defer target.Store(-1)
				names := []string{"ttyd", "tmux", "fleet-attach"}
				preferred := binding.TerminalPort
				if files {
					names = []string{"filebrowser"}
					preferred = binding.FilesPort
				}
				for _, name := range names {
					info, err := os.Stat(filepath.Join(bin, name))
					if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
						return fmt.Errorf("应用组件 %s 缺失，请重新安装完整应用", name)
					}
				}
				port, err := availableDesktopPort(preferred)
				if err != nil {
					return err
				}
				local := binding
				if files {
					local.FilesPort = port
				} else {
					local.TerminalPort = port
				}
				commands, err := desktopServiceCommands(local, bin, state)
				if err != nil {
					return err
				}
				if files {
					initScope, stop := context.WithTimeout(ctx, 15*time.Second)
					err := desktopInitializeFiles(commands[:3], state, func(command desktopServiceCommand) error {
						return exec.CommandContext(initScope, command.Program, command.Args...).Run()
					})
					stop()
					if err != nil {
						return fmt.Errorf("文件服务初始化失败: %w", err)
					}
				}
				command := commands[4]
				if files {
					command = commands[3]
				}
				process := exec.CommandContext(ctx, command.Program, command.Args...)
				process.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")
				process.Stdout = logFile
				process.Stderr = logFile
				return runDesktopComponent(ctx, process, port, target, desktopChildOwnsListener, func() { setDesktopComponent(name, "running", "") })
			}, func(err error) {
				setDesktopComponent(name, "connecting", err.Error())
				fmt.Fprintf(logFile, "%s服务正在重试: %v\n", name, err)
			})
		}(files)
	}
	return func() { once.Do(func() { cancelScope(); workers.Wait(); logFile.Close() }) }, nil
}

// Only publish a listener owned by this exact child, never merely a reachable
// port. Reservations cannot be inherited by both third-party services.
func desktopChildOwnsListener(ctx context.Context, pid, port int) bool {
	scope, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	program := "/usr/sbin/lsof"
	if _, err := os.Stat(program); err != nil {
		var lookup error
		program, lookup = exec.LookPath("lsof")
		if lookup != nil {
			return false
		}
	}
	output, err := exec.CommandContext(scope, program, "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fp").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		if line == "p"+strconv.Itoa(pid) {
			return true
		}
	}
	return false
}

func runDesktopComponent(ctx context.Context, process *exec.Cmd, port int, target *atomic.Int64, owns func(context.Context, int, int) bool, ready func()) error {
	target.Store(-1)
	defer target.Store(-1)
	if err := process.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- process.Wait() }()
	stop := func() { _ = process.Process.Kill(); <-exited }
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	published := false
	for {
		select {
		case err := <-exited:
			return err
		case <-ctx.Done():
			stop()
			return ctx.Err()
		case <-deadline.C:
			stop()
			return errors.New("组件尚未建立监听，正在重试")
		case <-ticker.C:
			verified := owns(ctx, process.Process.Pid, port)
			if verified && !published {
				published = true
				deadline.Stop()
				target.Store(int64(port))
				ready()
				ticker.Reset(time.Second)
			}
			if !verified && published {
				target.Store(-1)
				stop()
				return errors.New("组件监听已丢失，正在重新连接")
			}
		}
	}
}

var desktopComponents = struct {
	sync.Mutex
	states map[string]desktopComponentState
}{states: make(map[string]desktopComponentState)}

type desktopComponentState struct {
	Phase string `json:"phase"`
	Error string `json:"error,omitempty"`
}

func setDesktopComponent(name, phase, message string) {
	key := "terminal"
	if name == "文件" {
		key = "files"
	}
	desktopComponents.Lock()
	defer desktopComponents.Unlock()
	desktopComponents.states[key] = desktopComponentState{Phase: phase, Error: message}
}
func desktopComponentSnapshot() map[string]desktopComponentState {
	desktopComponents.Lock()
	defer desktopComponents.Unlock()
	result := make(map[string]desktopComponentState, len(desktopComponents.states))
	for key, value := range desktopComponents.states {
		result[key] = value
	}
	return result
}
