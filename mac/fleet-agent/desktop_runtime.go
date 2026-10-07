package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type desktopStatus struct {
	Schema     int                   `json:"schema"`
	PID        int                   `json:"pid"`
	Version    string                `json:"version"`
	Settings   desktopSettings       `json:"settings"`
	Binding    *desktopBindingStatus `json:"binding,omitempty"`
	DiskAccess desktopDiskAccess     `json:"disk_access"`
	Pairing    *desktopPairingState  `json:"pairing,omitempty"`
	Runtime    desktopRuntimeState   `json:"runtime"`
}

type desktopControl struct {
	directory   string
	bindingFile string
	probe       func() desktopDiskAccess
	mu          sync.Mutex
	disk        desktopDiskAccess
	listener    net.Listener
	server      *http.Server
	lock        *os.File
	closeOnce   sync.Once
	pairing     *desktopPairing
	runtime     desktopRuntimeState
	idleCheck   func() error
}

func startDesktopControl(directory, bindingFile string, probe func() desktopDiskAccess) (*desktopControl, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	if err := privateInfo(directory, true); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(directory, "control.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			lock.Close()
		}
	}()
	if err = privateInfo(lockPath, false); err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.New("后台管理服务已运行")
	}
	socket := filepath.Join(directory, "control.sock")
	if info, statErr := os.Lstat(socket); statErr == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
			err = errors.New("拒绝覆盖不安全的管理端点")
			return nil, err
		}
		if err = os.Remove(socket); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(statErr) {
		err = statErr
		return nil, err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(socket, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	control := &desktopControl{directory: directory, bindingFile: bindingFile, probe: probe, listener: listener, lock: lock}
	control.disk = probe()
	control.server = &http.Server{Handler: control, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	go control.server.Serve(listener)
	return control, nil
}

func (control *desktopControl) Close() {
	control.closeOnce.Do(func() {
		control.mu.Lock()
		if control.pairing != nil {
			control.pairing.Cancel()
		}
		control.mu.Unlock()
		control.server.Close()
		control.listener.Close()
		control.lock.Close()
	})
}

func desktopReply(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	json.NewEncoder(writer).Encode(value)
}

func (control *desktopControl) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Origin") != "" {
		desktopReply(writer, 403, map[string]string{"error": "拒绝浏览器管理请求"})
		return
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	if request.URL.Path == "/settings" || request.URL.Path == "/pair/start" || request.URL.Path == "/pair/confirm" {
		if !desktopMaintenance.Enter() {
			desktopReply(writer, http.StatusConflict, map[string]string{"error": "设备正在维护，请稍后重试"})
			return
		}
		defer desktopMaintenance.Leave()
	}
	switch request.URL.Path {
	case "/status":
		if request.Method != http.MethodGet {
			desktopReply(writer, 405, map[string]string{"error": "请求方法不支持"})
			return
		}
		settings, err := loadDesktopSettings(filepath.Join(control.directory, "settings.json"))
		if err != nil {
			desktopReply(writer, 500, map[string]string{"error": "无法读取本机设置"})
			return
		}
		status := desktopStatus{Schema: 1, PID: os.Getpid(), Version: version, Settings: settings, DiskAccess: control.disk}
		status.Runtime = control.runtime
		if control.pairing != nil {
			state := control.pairing.Snapshot()
			status.Pairing = &state
		}
		binding, err := readDeviceBinding(control.bindingFile)
		if err == nil {
			public := desktopPublicBinding(binding)
			status.Binding = &public
		} else if !os.IsNotExist(err) {
			desktopReply(writer, 500, map[string]string{"error": "设备授权文件不安全或损坏"})
			return
		}
		desktopReply(writer, 200, status)
	case "/disk/recheck":
		if request.Method != http.MethodPost {
			desktopReply(writer, 405, map[string]string{"error": "请求方法不支持"})
			return
		}
		control.disk = control.probe()
		desktopReply(writer, 200, control.disk)
	case "/settings":
		if request.Method != http.MethodPut {
			desktopReply(writer, 405, map[string]string{"error": "请求方法不支持"})
			return
		}
		var settings desktopSettings
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&settings); err != nil || decoder.Decode(new(any)) != io.EOF {
			desktopReply(writer, 400, map[string]string{"error": "无效设置"})
			return
		}
		origin := settings.Origin
		var err error
		if origin != "" {
			origin, err = validateFleetOrigin(origin)
		}
		if err != nil {
			desktopReply(writer, 400, map[string]string{"error": "请输入有效的 HTTPS 服务地址"})
			return
		}
		binding, bindingErr := readDeviceBinding(control.bindingFile)
		if bindingErr == nil && binding.Origin != origin {
			desktopReply(writer, 409, map[string]string{"error": "请先解除设备关联，再更换服务器"})
			return
		}
		if bindingErr != nil && !os.IsNotExist(bindingErr) {
			desktopReply(writer, 409, map[string]string{"error": "请先修复设备授权文件"})
			return
		}
		settings.Origin = origin
		if err := saveDesktopSettings(filepath.Join(control.directory, "settings.json"), settings); err != nil {
			desktopReply(writer, 400, map[string]string{"error": "设置未保存，请检查版本与文件权限"})
			return
		}
		desktopReply(writer, 200, settings)
	case "/lifecycle/prepare", "/lifecycle/resume", "/logout":
		if request.Method != http.MethodPost {
			desktopReply(writer, 405, map[string]string{"error": "请求方法不支持"})
			return
		}
		var err error
		switch request.URL.Path {
		case "/lifecycle/prepare":
			if control.pairing != nil {
				phase := control.pairing.Snapshot().Phase
				if phase == "starting" || phase == "browser" || phase == "awaiting_confirmation" || phase == "joining" {
					desktopReply(writer, 409, map[string]string{"error": "请先完成或取消设备关联"})
					return
				}
			}
			err = desktopMaintenance.Prepare(func() error {
				if control.idleCheck != nil {
					return control.idleCheck()
				}
				return nil
			})
		case "/lifecycle/resume":
			desktopMaintenance.Resume()
		case "/logout":
			err = logoutDevice(context.Background(), control.bindingFile)
		}
		if err != nil {
			desktopReply(writer, 409, map[string]string{"error": err.Error()})
			return
		}
		desktopReply(writer, 200, map[string]any{"pid": os.Getpid(), "ok": true})
	case "/pair/start", "/pair/confirm", "/pair/cancel":
		if request.Method != http.MethodPost {
			desktopReply(writer, 405, map[string]string{"error": "请求方法不支持"})
			return
		}
		if control.pairing == nil {
			desktopReply(writer, 503, map[string]string{"error": "图形化网络组件尚未就绪"})
			return
		}
		var expected desktopPairingState
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8192))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&expected) != nil || decoder.Decode(new(any)) != io.EOF {
			desktopReply(writer, 400, map[string]string{"error": "无效的配对操作"})
			return
		}
		var err error
		switch request.URL.Path {
		case "/pair/start":
			settings, loadErr := loadDesktopSettings(filepath.Join(control.directory, "settings.json"))
			err = loadErr
			if err == nil {
				err = control.pairing.Start(settings.Origin)
			}
		case "/pair/confirm":
			err = control.pairing.Confirm(expected)
		case "/pair/cancel":
			control.pairing.Cancel()
		}
		if err != nil {
			desktopReply(writer, 409, map[string]string{"error": err.Error()})
			return
		}
		desktopReply(writer, 200, control.pairing.Snapshot())
	default:
		desktopReply(writer, 404, map[string]string{"error": "操作不支持"})
	}
}

func (control *desktopControl) SetPairing(pairing *desktopPairing) {
	control.mu.Lock()
	defer control.mu.Unlock()
	control.pairing = pairing
}
