package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var deviceIDPattern = regexp.MustCompile(`^m[1-9][0-9]*$`)

type deviceAccess struct {
	path    string
	mu      sync.Mutex
	binding deviceBinding
	lease   time.Time
	scope   context.Context
	cancel  context.CancelFunc
	timer   *time.Timer
	client  *http.Client
}

func newDeviceAccess(path string) *deviceAccess {
	return &deviceAccess{path: path, client: deviceHTTPClient()}
}

func sameDeviceAuthorization(first, second deviceBinding) bool {
	first.Complete = false
	second.Complete = false
	first.DeviceName = ""
	second.DeviceName = ""
	return first == second
}

func (access *deviceAccess) deny() {
	if access.cancel != nil {
		access.cancel()
	}
	if access.timer != nil {
		access.timer.Stop()
	}
	access.lease = time.Time{}
}

func (access *deviceAccess) close() {
	access.mu.Lock()
	defer access.mu.Unlock()
	access.deny()
	access.client.CloseIdleConnections()
}

func (access *deviceAccess) refresh(ctx context.Context) {
	binding, err := readDeviceBinding(access.path)
	access.mu.Lock()
	if err != nil || binding.Locked || (access.binding.DeviceID != "" && !sameDeviceAuthorization(access.binding, binding)) {
		access.deny()
	}
	access.binding = binding
	access.mu.Unlock()
	if err != nil || binding.Locked {
		return
	}
	request, _ := http.NewRequestWithContext(ctx, "GET", binding.Origin+"/api/device/status", nil)
	request.Header.Set("Authorization", "Bearer "+binding.DeviceToken)
	response, err := access.client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 500 || response.StatusCode == 429 {
		return
	}
	var status struct {
		ID    string `json:"device_id"`
		Email string `json:"owner_email"`
		Name  string `json:"device_name"`
		Lease int64  `json:"lease_until"`
	}
	err = json.NewDecoder(http.MaxBytesReader(nil, response.Body, 16<<10)).Decode(&status)
	access.mu.Lock()
	defer access.mu.Unlock()
	if !sameDeviceAuthorization(access.binding, binding) {
		return
	}
	if response.StatusCode != 200 || err != nil || status.ID != binding.DeviceID || status.Email != binding.OwnerEmail || status.Lease <= time.Now().Unix() || status.Lease > time.Now().Add(46*time.Second).Unix() {
		access.deny()
		return
	}
	// Names are presentation metadata, never part of the authorization identity.
	// Synchronize only after validating the device, owner and lease.
	if status.Name != "" {
		syncDeviceName(access.path, binding, status.Name)
		access.binding.DeviceName = status.Name
	}
	if access.scope == nil || access.scope.Err() != nil {
		access.scope, access.cancel = context.WithCancel(context.Background())
	}
	access.lease = time.Unix(status.Lease, 0)
	if access.timer != nil {
		access.timer.Stop()
	}
	scopeCancel := access.cancel
	access.timer = time.AfterFunc(time.Until(access.lease), scopeCancel)
}

func (access *deviceAccess) run(ctx context.Context) {
	go access.watchLocalBinding(ctx)
	access.refresh(ctx)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer access.close()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			access.refresh(ctx)
		}
	}
}

func (access *deviceAccess) watchLocalBinding(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			binding, err := readDeviceBinding(access.path)
			access.mu.Lock()
			if err != nil || binding.Locked || !sameDeviceAuthorization(binding, access.binding) {
				access.deny()
			}
			access.mu.Unlock()
		}
	}
}

func (access *deviceAccess) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/health" && (request.Method == "GET" || request.Method == "HEAD") {
			writer.Header().Set("Content-Type", "text/plain")
			writer.Write([]byte("ok"))
			return
		}
		current, err := readDeviceBinding(access.path)
		access.mu.Lock()
		if err != nil || current.Locked || !sameDeviceAuthorization(current, access.binding) {
			access.deny()
		}
		binding, scope := access.binding, access.scope
		allowed := err == nil && !current.Locked && scope != nil && scope.Err() == nil && time.Now().Before(access.lease) && request.Header.Get("X-Fleet-Device-ID") == binding.DeviceID && subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fleet-Device-Token")), []byte(binding.ProxyToken)) == 1
		access.mu.Unlock()
		if !allowed {
			http.Error(writer, "device authorization required", 403)
			return
		}
		ctx, cancel := context.WithCancel(request.Context())
		defer cancel()
		stop := context.AfterFunc(scope, cancel)
		defer stop()
		request = request.Clone(ctx)
		for key := range request.Header {
			if strings.HasPrefix(strings.ToLower(key), "x-fleet-") {
				request.Header.Del(key)
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func localServiceProxy(port int) http.Handler {
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	proxy.Transport = &http.Transport{Proxy: nil, ResponseHeaderTimeout: 15 * time.Second}
	return proxy
}

func registerDeviceServices(mux *http.ServeMux, binding deviceBinding) {
	terminalPort, filesPort := binding.TerminalPort, binding.FilesPort
	if terminalPort == 0 {
		terminalPort = 7681
	}
	if filesPort == 0 {
		filesPort = 8080
	}
	mux.Handle("/"+binding.DeviceID+"/term/", localServiceProxy(terminalPort))
	mux.Handle("/"+binding.DeviceID+"/files/", localServiceProxy(filesPort))
}

// Re-read under the existing login/logout lock so metadata cannot overwrite a
// concurrent revocation, new association, or installation completion.
func syncDeviceName(path string, expected deviceBinding, name string) {
	lock, err := lockDeviceState(path)
	if err != nil {
		return
	}
	defer lock.Close()
	current, err := readDeviceBinding(path)
	if err != nil || !sameDeviceAuthorization(current, expected) || current.DeviceName == name {
		return
	}
	current.DeviceName = name
	_ = writePrivateJSON(path, current)
}
