package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

const dshNativeInternalPrefix = "/api/dsh-native/"

// dshNativeTarget is intentionally server-side only. The browser is authenticated by
// Fleet/Authelia; the DSH host cookie never leaves fleet-agent.
type dshNativeTarget struct {
	BaseURL string
	Cookie  string
}

type dshNativeResolver func(context.Context) (dshNativeTarget, error)

type dshNativeProxy struct {
	webPrefix string
	resolve   dshNativeResolver
	onFailure func(error)
}

func newDSHNativeProxy(macIndex string, resolve dshNativeResolver, onFailure func(error)) http.Handler {
	validIndex := true
	if macIndex == "" {
		validIndex = false
	}
	for _, r := range macIndex {
		if r < '0' || r > '9' {
			validIndex = false
			break
		}
	}
	if !validIndex {
		resolve = func(context.Context) (dshNativeTarget, error) {
			return dshNativeTarget{}, fmt.Errorf("invalid Mac index %q", macIndex)
		}
	}
	return &dshNativeProxy{
		webPrefix: "/m" + macIndex + "/dsh",
		resolve:   resolve,
		onFailure: onFailure,
	}
}

func (p *dshNativeProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, dshNativeInternalPrefix) {
		http.NotFound(w, r)
		return
	}
	if p.resolve == nil {
		http.Error(w, "DeepSeek Harness 不可用", http.StatusServiceUnavailable)
		return
	}

	target, err := p.resolve(r.Context())
	if err != nil {
		http.Error(w, "DeepSeek Harness 未启动或无法连接", http.StatusServiceUnavailable)
		return
	}
	upstream, err := url.Parse(target.BaseURL)
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		http.Error(w, "DeepSeek Harness 端点无效", http.StatusServiceUnavailable)
		return
	}

	request := r.Clone(r.Context())
	request.URL = cloneURL(r.URL)
	request.URL.Path = "/" + strings.TrimPrefix(r.URL.Path, dshNativeInternalPrefix)
	request.URL.RawPath = ""

	proxy := httputil.NewSingleHostReverseProxy(upstream)
	baseDirector := proxy.Director
	proxy.Director = func(out *http.Request) {
		baseDirector(out)
		out.Host = upstream.Host
		out.Header.Set("Cookie", target.Cookie)
		out.Header.Set("Accept-Encoding", "identity")
		if out.Header.Get("Origin") != "" {
			out.Header.Set("Origin", upstream.Scheme+"://"+upstream.Host)
		}
		out.Header.Del("Forwarded")
		out.Header.Del("X-Forwarded-Host")
		out.Header.Del("X-Forwarded-Proto")
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		// DSH's browser credential authenticates only the loopback hop. Fleet's browser
		// remains authenticated exclusively by Authelia.
		resp.Header.Del("Set-Cookie")
		if location := resp.Header.Get("Location"); location != "" {
			resp.Header.Set("Location", rewriteDSHNativeLocation(location, upstream, p.webPrefix))
		}

		contentType := strings.ToLower(resp.Header.Get("Content-Type"))
		var transform func([]byte) ([]byte, error)
		switch {
		case strings.Contains(contentType, "text/html"):
			transform = func(body []byte) ([]byte, error) { return rewriteDSHNativeHTML(body, p.webPrefix), nil }
		case strings.Contains(contentType, "manifest+json") || strings.HasSuffix(resp.Request.URL.Path, "/manifest.webmanifest"):
			transform = func(body []byte) ([]byte, error) { return rewriteDSHNativeManifest(body, p.webPrefix) }
		default:
			return nil
		}

		const maxRewriteBody = 16 << 20
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxRewriteBody+1))
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		if len(body) > maxRewriteBody {
			return fmt.Errorf("DSH 原生 UI 响应超过 %d bytes", maxRewriteBody)
		}
		body, err = transform(body)
		if err != nil {
			return err
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("ETag")
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		if p.onFailure != nil {
			p.onFailure(err)
		}
		http.Error(w, "DeepSeek Harness 连接已断开", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, request)
}

func cloneURL(in *url.URL) *url.URL {
	out := *in
	return &out
}

func rewriteDSHNativeLocation(location string, upstream *url.URL, prefix string) string {
	u, err := url.Parse(location)
	if err != nil {
		return location
	}
	if u.IsAbs() && !strings.EqualFold(u.Host, upstream.Host) {
		return location
	}
	if u.Path == "" {
		u.Path = "/"
	}
	if !strings.HasPrefix(u.Path, prefix+"/") {
		u.Scheme = ""
		u.Host = ""
		u.Path = prefix + "/" + strings.TrimPrefix(u.Path, "/")
	}
	return u.String()
}

func rewriteDSHNativeHTML(body []byte, prefix string) []byte {
	s := string(body)
	base := `<base href="` + prefix + `/">`
	s = strings.Replace(s, `<base href="/">`, base, 1)
	for _, root := range []string{"/plugins/", "/assets/", "/api/"} {
		for _, quote := range []string{`"`, `'`} {
			s = strings.ReplaceAll(s, quote+root, quote+prefix+root)
		}
	}
	for _, asset := range []string{"/dsh-desktop-logo.png", "/manifest.webmanifest"} {
		for _, quote := range []string{`"`, `'`} {
			s = strings.ReplaceAll(s, quote+asset, quote+prefix+asset)
		}
	}

	shim := dshNativePathShim(prefix)
	if strings.Contains(s, base) {
		s = strings.Replace(s, base, base+shim, 1)
	} else if i := strings.Index(strings.ToLower(s), "<head>"); i >= 0 {
		i += len("<head>")
		s = s[:i] + base + shim + s[i:]
	}
	return []byte(s)
}

func dshNativePathShim(prefix string) string {
	prefixJSON := strconv.Quote(prefix)
	return `<script>(()=>{` +
		`const p=globalThis.__MACFLEET_DSH_PREFIX__=` + prefixJSON + `;` +
		`const routed=(path)=>path===p||path.startsWith(p+"/");` +
		`const needs=(path)=>path==="/api"||path.startsWith("/api/")||path==="/plugins"||path.startsWith("/plugins/")||path==="/assets"||path.startsWith("/assets/")||path==="/manifest.webmanifest"||path==="/dsh-desktop-logo.png";` +
		`const httpOrigin=(u)=>(u.protocol==="ws:"?"http:":u.protocol==="wss:"?"https:":u.protocol)+"//"+u.host;` +
		`const route=(value)=>{try{const input=value instanceof URL?value.href:String(value);const u=new URL(input,location.href);if(httpOrigin(u)!==location.origin||routed(u.pathname)||!needs(u.pathname))return value;u.pathname=p+u.pathname;return value instanceof URL?u:u.href}catch(_){return value}};` +
		`const nativeFetch=window.fetch.bind(window);window.fetch=(input,init)=>{if(input instanceof Request){const next=route(input.url);return nativeFetch(next===input.url?input:new Request(next,input),init)}return nativeFetch(route(input),init)};` +
		`const NativeWebSocket=window.WebSocket;function FleetWebSocket(url,protocols){return protocols===undefined?new NativeWebSocket(route(url)):new NativeWebSocket(route(url),protocols)}Object.setPrototypeOf(FleetWebSocket,NativeWebSocket);FleetWebSocket.prototype=NativeWebSocket.prototype;window.WebSocket=FleetWebSocket;` +
		`const nativeOpen=window.XMLHttpRequest.prototype.open;window.XMLHttpRequest.prototype.open=function(method,url,...rest){return nativeOpen.call(this,method,route(url),...rest)};` +
		`if(window.EventSource){const NativeEventSource=window.EventSource;function FleetEventSource(url,options){return new NativeEventSource(route(url),options)}Object.setPrototypeOf(FleetEventSource,NativeEventSource);FleetEventSource.prototype=NativeEventSource.prototype;window.EventSource=FleetEventSource}` +
		`if(navigator.sendBeacon){const nativeBeacon=navigator.sendBeacon.bind(navigator);navigator.sendBeacon=(url,data)=>nativeBeacon(route(url),data)}` +
		`globalThis.__DSH_FILE_UPLOAD__={fetch:(input,init)=>window.fetch(route(input),init)};` +
		`})()</script>`
}

func rewriteDSHNativeManifest(body []byte, prefix string) ([]byte, error) {
	var manifest map[string]any
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("解析 DSH manifest: %w", err)
	}
	for _, key := range []string{"id", "start_url", "scope"} {
		if value, ok := manifest[key].(string); ok && strings.HasPrefix(value, "/") {
			manifest[key] = prefix + "/" + strings.TrimPrefix(value, "/")
		}
	}
	if icons, ok := manifest["icons"].([]any); ok {
		for _, raw := range icons {
			icon, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if src, ok := icon["src"].(string); ok && strings.HasPrefix(src, "/") {
				icon["src"] = prefix + "/" + strings.TrimPrefix(src, "/")
			}
		}
	}
	return json.Marshal(manifest)
}

func resolveDSHNativeTarget(ctx context.Context) (dshNativeTarget, error) {
	router, _ := agentChatBackend.(*routingChatBackend)
	backend := router.dshBackend()
	if backend == nil {
		return dshNativeTarget{}, errDSHHostUnavailable
	}
	client, err := backend.ensure(ctx)
	if err != nil {
		return dshNativeTarget{}, err
	}
	return dshNativeTarget{BaseURL: client.baseURL, Cookie: client.cookie}, nil
}

func markDSHNativeProxyFailure(err error) {
	router, _ := agentChatBackend.(*routingChatBackend)
	if backend := router.dshBackend(); backend != nil {
		backend.invalidateWithError(fmt.Errorf("%w: 原生 UI 代理失败: %v", errDSHHostUnavailable, err))
	}
}

func (b *dshChatBackend) invalidateWithError(err error) {
	if b == nil {
		return
	}
	b.connMu.Lock()
	client := b.client
	b.client = nil
	b.lastErr = err
	b.connMu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

// probeNativeUI makes /api/info reflect the actual local Web host instead of a
// stale log line or a connection that succeeded before Desktop was quit.
func (b *dshChatBackend) probeNativeUI(ctx context.Context) bool {
	client, err := b.ensure(ctx)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/manifest.webmanifest", nil)
	if err != nil {
		b.invalidateWithError(err)
		return false
	}
	req.Header.Set("Cookie", client.cookie)
	resp, err := client.http.Do(req)
	if err != nil {
		b.invalidateWithError(fmt.Errorf("%w: %v", errDSHHostUnavailable, err))
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		b.invalidateWithError(fmt.Errorf("%w: 原生 UI 探测 HTTP %d", errDSHHostUnavailable, resp.StatusCode))
		return false
	}
	b.connMu.Lock()
	b.lastErr = nil
	b.hostSeen = true
	b.connMu.Unlock()
	return true
}
