package multiuser

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var devicePath = regexp.MustCompile(`^/(m[1-9][0-9]*)/(api|term|files|dsh)(/.*)?$`)

func (server *Server) handleProxy(writer http.ResponseWriter, request *http.Request, session sessionState, user User) bool {
	match := devicePath.FindStringSubmatch(request.URL.Path)
	if len(match) == 0 {
		return false
	}
	device, err := server.AuthorizedDevice(user.ID, match[1])
	if err != nil {
		reject(writer, 404, "设备不存在")
		return true
	}
	credential, err := server.ProxyCredential(user.ID, device.ID)
	if err != nil || credential == "" {
		reject(writer, 503, "设备需要升级并重新关联")
		return true
	}
	if origin := request.Header.Get("Origin"); origin != "" && origin != server.options.Origin {
		reject(writer, 403, "请求来源不合法")
		return true
	}
	kind, suffix := match[2], match[3]
	if kind == "dsh" && suffix == "" {
		http.Redirect(writer, request, server.options.Origin+"/"+device.ID+"/dsh/", 308)
		return true
	}
	port := server.options.AgentPort
	upstreamPath := "/api" + suffix
	switch kind {
	case "term":
		upstreamPath = "/" + device.ID + "/term" + suffix
	case "files":
		upstreamPath = "/" + device.ID + "/files" + suffix
	case "dsh":
		upstreamPath = "/api/dsh-native" + suffix
	}
	address := "http://" + net.JoinHostPort(device.IP, fmt.Sprint(port))
	if server.options.ProxyURL != nil {
		address = server.options.ProxyURL(device, kind)
	}
	target, err := url.Parse(address)
	if err != nil || target.Host == "" {
		reject(writer, 503, "设备地址不可用")
		return true
	}
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	stop := context.AfterFunc(server.ScopeContext(user.ID, device.ID), cancel)
	defer stop()
	stopUser := context.AfterFunc(server.ScopeContext(user.ID, ""), cancel)
	defer stopUser()
	sessionKey := "session/" + intString(user.ID) + "/" + session.Hash
	server.scopeMu.Lock()
	sessionScope, exists := server.scopes[sessionKey]
	if !exists {
		scopeContext, scopeCancel := context.WithCancel(server.root)
		sessionScope = scope{scopeContext, scopeCancel}
		server.scopes[sessionKey] = sessionScope
	}
	server.scopeMu.Unlock()
	stopSession := context.AfterFunc(sessionScope.context, cancel)
	defer stopSession()
	duration := time.Duration(session.Expires-server.now().Unix()) * time.Second
	timer := time.AfterFunc(duration, cancel)
	defer timer.Stop()
	currentDevice, authorizationErr := server.AuthorizedDevice(user.ID, device.ID)
	_, _, sessionErr := server.authenticate(request, true)
	if authorizationErr != nil || sessionErr != nil || currentDevice.NodeID != device.NodeID || currentDevice.IP != device.IP {
		reject(writer, 404, "设备访问授权已失效")
		return true
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(outbound *http.Request) {
		outbound.URL.Scheme = target.Scheme
		outbound.URL.Host = target.Host
		outbound.URL.Path = upstreamPath
		outbound.URL.RawPath = ""
		outbound.Host = target.Host
		for key := range outbound.Header {
			lower := strings.ToLower(key)
			if lower == "cookie" || lower == "authorization" || lower == "x-csrf-token" || strings.HasPrefix(lower, "x-fleet-") || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-remote-") || lower == "remote-user" || lower == "forwarded" {
				outbound.Header.Del(key)
			}
		}
		outbound.Header.Set("Origin", server.options.Origin)
		outbound.Header.Set("X-Fleet-Device-ID", device.ID)
		outbound.Header.Set("X-Fleet-Device-Token", credential)
	}
	transport, err := NewDeviceTransport(server.options.MeshProxy)
	if err != nil {
		reject(writer, 503, "设备网络配置不可用")
		return true
	}
	proxy.Transport = transport
	defer proxy.Transport.(*http.Transport).CloseIdleConnections()
	proxy.FlushInterval = -1
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Header.Del("Set-Cookie")
		response.Header.Set("Cache-Control", "no-store")
		if location := response.Header.Get("Location"); location != "" {
			parsed, err := url.Parse(location)
			if err != nil {
				return err
			}
			if parsed.IsAbs() && parsed.Host != target.Host {
				return fmt.Errorf("untrusted upstream redirect")
			}
			if parsed.IsAbs() {
				parsed.Scheme = ""
				parsed.Host = ""
			}
			if !strings.HasPrefix(parsed.Path, "/"+device.ID+"/") {
				parsed.Path = "/" + device.ID + "/" + kind + "/" + strings.TrimPrefix(parsed.Path, "/")
			}
			response.Header.Set("Location", parsed.String())
		}
		return nil
	}
	proxy.ErrorHandler = func(response http.ResponseWriter, _ *http.Request, _ error) {
		reject(response, 502, "设备暂时不可达")
	}
	proxy.ServeHTTP(writer, request.WithContext(ctx))
	return true
}
