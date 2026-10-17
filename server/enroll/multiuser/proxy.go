package multiuser

import (
	"context"
	"fmt"
	"io"
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
	preview, isPreview := request.Context().Value(networkPreviewContextKey{}).(networkPreviewContext)
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
	server.watchSessionExpiry(ctx, cancel, session.Hash, session.Expires)
	currentDevice, authorizationErr := server.AuthorizedDevice(user.ID, device.ID)
	var sessionErr error
	if isPreview {
		if !server.networkPreviewSessionValid(session) {
			sessionErr = fmt.Errorf("preview login expired")
		}
	} else {
		_, _, sessionErr = server.authenticate(request, true)
	}
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
		if kind == "api" {
			outbound.URL.RawPath = strings.TrimPrefix(request.URL.EscapedPath(), "/"+device.ID)
		}
		outbound.Host = target.Host
		for key := range outbound.Header {
			lower := strings.ToLower(key)
			if lower == "cookie" || lower == "authorization" || lower == "x-csrf-token" || strings.HasPrefix(lower, "x-macfleet-preview-") || strings.HasPrefix(lower, "x-fleet-") || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-remote-") || lower == "remote-user" || lower == "forwarded" {
				outbound.Header.Del(key)
			}
		}
		outbound.Header.Set("Origin", server.options.Origin)
		outbound.Header.Set("X-Fleet-Device-ID", device.ID)
		outbound.Header.Set("X-Fleet-Device-Token", credential)
		if isPreview {
			if authorization := request.Header.Get("Authorization"); authorization != "" {
				outbound.Header.Set("Authorization", authorization)
			}
			if csrf := request.Header.Get("X-CSRF-Token"); csrf != "" {
				outbound.Header.Set("X-CSRF-Token", csrf)
			}
			outbound.Header.Set("X-Macfleet-Preview-Credentials", request.Header.Get("X-Fleet-Preview-Credentials"))
			outbound.Header.Set("X-Macfleet-Preview-Origin", preview.Origin)
			outbound.Header.Set("X-Macfleet-Preview-Prefix", preview.Prefix)
			outbound.Header.Set("X-Macfleet-Preview-ID", preview.ID)
			outbound.Header.Set("X-Macfleet-Preview-Gateway", server.options.Origin)
		}
	}
	transport, err := NewDeviceTransport(server.options.MeshProxy)
	if err != nil {
		reject(writer, 503, "设备网络配置不可用")
		return true
	}
	// Health probes should fail promptly; cold assistant initialization may take
	// longer. ResponseHeaderTimeout never limits an established SSE/WebSocket.
	if upstreamPath == "/api/health" {
		transport.ResponseHeaderTimeout = 5 * time.Second
	}
	proxy.Transport = transport
	defer proxy.Transport.(*http.Transport).CloseIdleConnections()
	proxy.FlushInterval = -1
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Header.Del("Set-Cookie")
		response.Header.Set("Cache-Control", "no-store")
		if isPreview {
			if request.Method == http.MethodOptions && request.Header.Get("Access-Control-Request-Method") != "" {
				response.Body.Close()
				response.Body = http.NoBody
				response.ContentLength = 0
				response.Header.Set("Content-Length", "0")
			}
			// This also protects direct navigation and SVG/HTML served with an
			// unexpected MIME type. Never grant allow-same-origin to arbitrary apps.
			response.Header.Del("Content-Security-Policy-Report-Only")
			response.Header.Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-downloads allow-popups allow-top-navigation-by-user-activation; default-src 'none'; script-src 'unsafe-inline' 'unsafe-eval' "+server.options.Origin+"; style-src 'unsafe-inline' "+server.options.Origin+"; connect-src "+server.options.Origin+" "+strings.Replace(server.options.Origin, "http", "ws", 1)+"; img-src data: blob: "+server.options.Origin+"; media-src blob: "+server.options.Origin+"; font-src data: "+server.options.Origin+"; frame-src "+server.options.Origin+"; form-action "+server.options.Origin+"; base-uri 'none'; frame-ancestors 'self'")
			response.Header.Set("Referrer-Policy", "no-referrer")
			if response.StatusCode == http.StatusNotFound && response.Header.Get("X-Macfleet-Preview-Version") != "1" {
				// Older devices have no forwarding endpoint; never fall back to
				// opening the viewer's loopback address.
				response.Header.Set("X-Fleet-Preview-Error", "device-upgrade-required")
				message := "这台电脑需要更新 Fleet 客户端才能打开宿主网络网页。请更新后重新打开链接。"
				response.Body.Close()
				response.Body = io.NopCloser(strings.NewReader(message))
				response.ContentLength = int64(len(message))
				response.Header.Set("Content-Length", fmt.Sprint(len(message)))
				response.Header.Set("Content-Type", "text/plain; charset=utf-8")
			}
			return nil
		}
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

// A connection does not renew a login. At its previous deadline, honor a renewal
// made by an authenticated request instead of closing a still-valid session.
func (server *Server) watchSessionExpiry(ctx context.Context, cancel context.CancelFunc, hash string, expires int64) {
	go func() {
		timer := time.NewTimer(time.Duration(expires-server.now().Unix()) * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				var current int64
				err := server.db.QueryRowContext(ctx, "SELECT expires_at FROM sessions WHERE token_hash=?", hash).Scan(&current)
				if err != nil || current <= server.now().Unix() {
					cancel()
					return
				}
				timer.Reset(time.Duration(current-server.now().Unix()) * time.Second)
			}
		}
	}()
}
