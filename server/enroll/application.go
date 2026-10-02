package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fleet-enroll/multiuser"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type applicationRegistry interface {
	Users() ([]multiuser.User, error)
	Devices(int64) ([]multiuser.Device, error)
	AuthorizedDevice(int64, string) (multiuser.Device, error)
	ProxyCredential(int64, string) (string, error)
	ScopeContext(int64, string) context.Context
}

type userApplications struct {
	server      applicationRegistry
	stateDir    string
	mu          sync.Mutex
	apis        map[int64]*messageAPI
	ctx         context.Context
	cancel      context.CancelFunc
	runners     sync.WaitGroup
	closeOnce   sync.Once
	agentPort   int
	concurrency int
	meshProxy   string
}

func newUserApplications(stateDir string) *userApplications {
	ctx, cancel := context.WithCancel(context.Background())
	return &userApplications{
		stateDir: stateDir, apis: map[int64]*messageAPI{}, ctx: ctx, cancel: cancel,
		agentPort: envInt("ENROLL_AGENT_PORT", 7682), concurrency: envInt("ENROLL_MESSAGE_CONCURRENCY", 4),
	}
}

func (apps *userApplications) Start() error {
	if apps.server == nil {
		return errors.New("application registry is not configured")
	}
	users, err := apps.server.Users()
	if err != nil {
		return err
	}
	for _, user := range users {
		if _, err := apps.application(user.ID); err != nil {
			return err
		}
	}
	return nil
}

func (apps *userApplications) application(owner int64) (*messageAPI, error) {
	apps.mu.Lock()
	defer apps.mu.Unlock()
	if owner <= 0 || apps.server == nil || apps.ctx.Err() != nil {
		return nil, errors.New("application unavailable")
	}
	if api := apps.apis[owner]; api != nil {
		return api, nil
	}
	api, err := newMessageAPIAt(filepath.Join(apps.stateDir, "users", strconv.FormatInt(owner, 10)))
	if err != nil {
		return nil, err
	}
	api.agentPort = apps.agentPort
	api.maxConcurrent = apps.concurrency
	if api.maxConcurrent < 1 {
		api.maxConcurrent = 1
	}
	api.baseContext = apps.ctx
	api.resolveDeviceFunc = func(input string) (resolvedDevice, *apiProblem) {
		return apps.resolveDevice(owner, input)
	}
	api.scopeJob = func(ctx context.Context, job *messageJob) (context.Context, context.CancelFunc, error) {
		return apps.jobContext(ctx, owner, job)
	}
	api.scopeCallback = func(ctx context.Context) (context.Context, context.CancelFunc, error) {
		return apps.ownerContext(ctx, owner)
	}
	transport, err := multiuser.NewDeviceTransport(apps.meshProxy)
	if err != nil {
		return nil, err
	}
	api.client.Transport = &applicationTransport{apps: apps, owner: owner, api: api, base: transport}
	api.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	apps.apis[owner] = api
	apps.runners.Add(1)
	go func() {
		defer apps.runners.Done()
		api.run(apps.ctx)
	}()
	return api, nil
}

func (apps *userApplications) activeOwner(owner int64) error {
	if apps.server == nil || apps.ctx.Err() != nil {
		return errors.New("application unavailable")
	}
	users, err := apps.server.Users()
	if err != nil {
		return err
	}
	for _, user := range users {
		if user.ID == owner && user.Status == "active" {
			return nil
		}
	}
	return errors.New("owner is not active")
}

func applicationContext(ctx context.Context, scopes ...context.Context) (context.Context, context.CancelFunc) {
	merged, cancel := context.WithCancel(ctx)
	stops := make([]func() bool, 0, len(scopes))
	for _, scope := range scopes {
		stops = append(stops, context.AfterFunc(scope, cancel))
		if scope.Err() != nil {
			cancel()
		}
	}
	return merged, func() {
		for _, stop := range stops {
			stop()
		}
		cancel()
	}
}

func (apps *userApplications) ownerContext(ctx context.Context, owner int64) (context.Context, context.CancelFunc, error) {
	if err := apps.activeOwner(owner); err != nil {
		return nil, nil, err
	}
	merged, cancel := applicationContext(ctx, apps.ctx, apps.server.ScopeContext(owner, ""))
	if err := apps.activeOwner(owner); err != nil {
		cancel()
		return nil, nil, err
	}
	if err := merged.Err(); err != nil {
		cancel()
		return nil, nil, err
	}
	return merged, cancel, nil
}

type applicationDeviceContextKey struct{}
type applicationDevicePin struct{ ID, NodeID, IP string }

func (apps *userApplications) jobContext(ctx context.Context, owner int64, job *messageJob) (context.Context, context.CancelFunc, error) {
	if job == nil {
		return nil, nil, errors.New("message job is missing")
	}
	pin := applicationDevicePin{ID: job.DeviceID, NodeID: job.DeviceNodeID, IP: job.DeviceIP}
	ctx = context.WithValue(ctx, applicationDeviceContextKey{}, pin)
	return apps.deviceContext(ctx, owner, pin)
}

func pinLegacyMessageJobs(raw []byte, owner int64, devices []multiuser.Device) ([]byte, error) {
	byID := map[string]multiuser.Device{}
	nodes := map[string]bool{}
	addresses := map[string]bool{}
	for _, device := range devices {
		if owner <= 0 || device.UserID != owner || device.Index <= 0 || device.ID != "m"+strconv.Itoa(device.Index) ||
			device.Status != "active" || device.NodeID == "" || net.ParseIP(device.IP) == nil ||
			nodes[device.NodeID] || addresses[device.IP] {
			return nil, errors.New("旧消息迁移需要唯一且已验证的 owner/设备/节点映射")
		}
		if _, exists := byID[device.ID]; exists {
			return nil, errors.New("旧消息迁移的设备编号不唯一")
		}
		byID[device.ID] = device
		nodes[device.NodeID], addresses[device.IP] = true, true
	}
	var disk messageStoreDisk
	if err := json.Unmarshal(raw, &disk); err != nil {
		return nil, fmt.Errorf("读取旧消息: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errors.New("旧消息存储须为 JSON 对象")
	}
	var jobs []json.RawMessage
	if err := json.Unmarshal(fields["jobs"], &jobs); err != nil {
		return nil, fmt.Errorf("读取旧消息列表: %w", err)
	}
	changed := false
	for index, job := range disk.Jobs {
		if job == nil || (job.Status != messageQueued && job.Status != messageRunning) {
			continue
		}
		device, exists := byID[job.DeviceID]
		if !exists || job.ID == "" || job.DeviceIP != device.IP || (job.DeviceNodeID != "" && job.DeviceNodeID != device.NodeID) {
			return nil, fmt.Errorf("旧消息 %q 的设备身份与已验证映射不一致", job.ID)
		}
		if job.DeviceNodeID != "" {
			continue
		}
		var jobFields map[string]json.RawMessage
		if err := json.Unmarshal(jobs[index], &jobFields); err != nil {
			return nil, err
		}
		jobFields["device_node_id"], _ = json.Marshal(device.NodeID)
		jobs[index], _ = json.Marshal(jobFields)
		changed = true
	}
	if !changed {
		return raw, nil
	}
	fields["jobs"], _ = json.Marshal(jobs)
	return json.Marshal(fields)
}

func (apps *userApplications) authorizedPin(owner int64, pin applicationDevicePin) error {
	if pin.ID == "" || pin.NodeID == "" || net.ParseIP(pin.IP) == nil {
		return errors.New("device identity is missing")
	}
	device, err := apps.server.AuthorizedDevice(owner, pin.ID)
	if err != nil {
		return err
	}
	if device.UserID != owner || device.ID != pin.ID || device.Status != "active" || device.NodeID != pin.NodeID || device.IP != pin.IP {
		return errors.New("device authorization changed")
	}
	return nil
}

func (apps *userApplications) deviceContext(ctx context.Context, owner int64, pin applicationDevicePin) (context.Context, context.CancelFunc, error) {
	merged, cancel, err := apps.ownerContext(ctx, owner)
	if err != nil {
		return nil, nil, err
	}
	scoped, release := applicationContext(merged, apps.server.ScopeContext(owner, pin.ID))
	cleanup := func() { release(); cancel() }
	if err := apps.authorizedPin(owner, pin); err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := scoped.Err(); err != nil {
		cleanup()
		return nil, nil, err
	}
	return scoped, cleanup, nil
}

func (apps *userApplications) resolveDevice(owner int64, input string) (resolvedDevice, *apiProblem) {
	notFound := &apiProblem{Status: 404, Code: "device_not_found", Message: "找不到指定设备"}
	if err := apps.activeOwner(owner); err != nil {
		return resolvedDevice{}, notFound
	}
	devices, err := apps.server.Devices(owner)
	if err != nil {
		return resolvedDevice{}, &apiProblem{Status: 503, Code: "device_unavailable", Message: "设备列表暂时不可用"}
	}
	input = strings.TrimSpace(input)
	var byName []resolvedDevice
	for _, device := range devices {
		pin := applicationDevicePin{ID: device.ID, NodeID: device.NodeID, IP: device.IP}
		if device.UserID != owner || apps.authorizedPin(owner, pin) != nil {
			continue
		}
		name := strings.TrimSpace(device.Name)
		if name == "" {
			name = "Mac " + strconv.Itoa(device.Index)
		}
		resolved := resolvedDevice{ID: device.ID, Name: name, IP: device.IP, NodeID: device.NodeID}
		if strings.EqualFold(device.ID, input) {
			return resolved, nil
		}
		if strings.EqualFold(name, input) {
			byName = append(byName, resolved)
		}
	}
	if len(byName) == 1 {
		return byName[0], nil
	}
	if len(byName) > 1 {
		candidates := make([]map[string]string, 0, len(byName))
		for _, device := range byName {
			candidates = append(candidates, map[string]string{"id": device.ID, "name": device.Name})
		}
		return resolvedDevice{}, &apiProblem{Status: 409, Code: "ambiguous_device", Message: "设备显示名称不唯一，请改用设备 ID", Details: map[string]interface{}{"candidates": candidates}}
	}
	return resolvedDevice{}, notFound
}

type applicationTransport struct {
	apps  *userApplications
	owner int64
	api   *messageAPI
	base  http.RoundTripper
}

func (transport *applicationTransport) CloseIdleConnections() {
	if base, ok := transport.base.(interface{ CloseIdleConnections() }); ok {
		base.CloseIdleConnections()
	}
}

func (transport *applicationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	pin, ok := request.Context().Value(applicationDeviceContextKey{}).(applicationDevicePin)
	if !ok || request.URL.Scheme != "http" || request.URL.User != nil ||
		request.URL.Host != net.JoinHostPort(pin.IP, strconv.Itoa(transport.api.agentPort)) ||
		!strings.HasPrefix(request.URL.Path, "/api/") {
		return nil, errors.New("agent request is outside device scope")
	}
	ctx, cancel, err := transport.apps.deviceContext(request.Context(), transport.owner, pin)
	if err != nil {
		return nil, err
	}
	credential, err := transport.apps.server.ProxyCredential(transport.owner, pin.ID)
	if err != nil || credential == "" {
		cancel()
		return nil, errors.New("device credential unavailable; upgrade and reassociate")
	}
	outbound := request.Clone(ctx)
	for key := range outbound.Header {
		if strings.HasPrefix(strings.ToLower(key), "x-fleet-") || strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "Cookie") {
			outbound.Header.Del(key)
		}
	}
	outbound.Header.Set("X-Fleet-Device-ID", pin.ID)
	outbound.Header.Set("X-Fleet-Device-Token", credential)
	response, err := transport.base.RoundTrip(outbound)
	if err != nil {
		cancel()
		return nil, err
	}
	response.Body = &applicationResponseBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type applicationResponseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (body *applicationResponseBody) Close() error {
	defer body.once.Do(body.cancel)
	return body.ReadCloser.Close()
}

func (apps *userApplications) Handler(user multiuser.User) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		ctx, cancel, err := apps.ownerContext(request.Context(), user.ID)
		if err != nil {
			writeAPIProblem(writer, &apiProblem{Status: 401, Code: "unauthorized", Message: "请重新登录"})
			return
		}
		defer cancel()
		api, err := apps.application(user.ID)
		if err != nil {
			writeAPIProblem(writer, &apiProblem{Status: 503, Code: "application_unavailable", Message: "自动化服务暂时不可用"})
			return
		}
		request = request.Clone(ctx)
		request.URL.Path = strings.TrimPrefix(request.URL.Path, "/api")
		if strings.HasPrefix(request.URL.Path, "/message-records/settings/access-key") {
			request.URL.Path = strings.TrimPrefix(request.URL.Path, "/message-records")
		}
		switch path := request.URL.Path; {
		case path == "/automation/access-keys" || strings.HasPrefix(path, "/automation/access-keys/"):
			api.handleAccessKeys(writer, request)
		case path == "/message-records" || path == "/automation/message-records":
			api.handleMessageRecords(writer, request)
		case path == "/settings/access-key" || path == "/settings/access-key/rotate":
			api.handleAccessKey(writer, request)
		default:
			http.NotFound(writer, request)
		}
	})
}

func (apps *userApplications) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	path := strings.TrimPrefix(request.URL.Path, "/api")
	if path != "/v1/messages" && !strings.HasPrefix(path, "/v1/messages/") {
		http.NotFound(writer, request)
		return
	}
	header := strings.TrimSpace(request.Header.Get("Authorization"))
	if len(header) < 8 || !strings.EqualFold(header[:7], "Bearer ") {
		writeAPIProblem(writer, &apiProblem{Status: 401, Code: "invalid_access_key", Message: "访问密钥无效或已撤销"})
		return
	}
	if err := apps.Start(); err != nil {
		writeAPIProblem(writer, &apiProblem{Status: 503, Code: "application_unavailable", Message: "自动化服务暂时不可用"})
		return
	}
	users, err := apps.server.Users()
	if err != nil {
		writeAPIProblem(writer, &apiProblem{Status: 503, Code: "application_unavailable", Message: "自动化服务暂时不可用"})
		return
	}
	provided := hashString(strings.TrimSpace(header[7:]))
	var matched *messageAPI
	var owner int64
	for _, user := range users {
		if user.Status != "active" {
			continue
		}
		api, err := apps.application(user.ID)
		if err != nil {
			writeAPIProblem(writer, &apiProblem{Status: 503, Code: "application_unavailable", Message: "自动化服务暂时不可用"})
			return
		}
		api.mu.Lock()
		found := false
		for _, key := range api.keys {
			if key.Hash != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(key.Hash)) == 1 {
				found = true
			}
		}
		api.mu.Unlock()
		if found {
			if matched != nil {
				writeAPIProblem(writer, &apiProblem{Status: 401, Code: "invalid_access_key", Message: "访问密钥无效或已撤销"})
				return
			}
			matched, owner = api, user.ID
		}
	}
	if matched == nil {
		writeAPIProblem(writer, &apiProblem{Status: 401, Code: "invalid_access_key", Message: "访问密钥无效或已撤销"})
		return
	}
	ctx, cancel, err := apps.ownerContext(request.Context(), owner)
	if err != nil {
		writeAPIProblem(writer, &apiProblem{Status: 401, Code: "invalid_access_key", Message: "访问密钥无效或已撤销"})
		return
	}
	defer cancel()
	request = request.Clone(ctx)
	request.URL.Path = path
	matched.handleMessages(writer, request)
}

func (apps *userApplications) Close() {
	apps.closeOnce.Do(func() {
		apps.mu.Lock()
		apps.cancel()
		apis := make([]*messageAPI, 0, len(apps.apis))
		for _, api := range apps.apis {
			apis = append(apis, api)
		}
		apps.mu.Unlock()
		apps.runners.Wait()
		for _, api := range apis {
			api.workers.Wait()
			api.client.CloseIdleConnections()
		}
	})
}
