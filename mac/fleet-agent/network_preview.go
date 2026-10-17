package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/net/publicsuffix"
)

const networkPreviewAPI = "/api/network-preview/"

var networkPreviewPrefixPattern = regexp.MustCompile(`^/network-preview/[A-Za-z0-9_-]{32,128}/$`)

type previewJar struct {
	jar  http.CookieJar
	used time.Time
}
type networkPreviewProxy struct {
	transport *http.Transport
	mu        sync.Mutex
	jars      map[string]previewJar
}

func newNetworkPreviewProxy() http.Handler {
	return &networkPreviewProxy{transport: &http.Transport{Proxy: http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 60 * time.Second}, jars: map[string]previewJar{}}
}

func parsePreviewOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(u.Host, "\\\r\n\t ") {
		return nil, fmt.Errorf("invalid HTTP(S) origin")
	}
	u.Path = ""
	return u, nil
}

func (p *networkPreviewProxy) jar(id string) http.CookieJar {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for key, item := range p.jars {
		if now.Sub(item.used) > 8*time.Hour {
			delete(p.jars, key)
		}
	}
	item, ok := p.jars[id]
	if !ok {
		// Bound memory even when many authenticated previews are opened.
		if len(p.jars) >= 256 {
			var oldest string
			at := now
			for key, candidate := range p.jars {
				if candidate.used.Before(at) {
					oldest, at = key, candidate.used
				}
			}
			delete(p.jars, oldest)
		}
		item.jar, _ = cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	}
	item.used = now
	p.jars[id] = item
	return item.jar
}

func previewOriginString(u *url.URL) string { return u.Scheme + "://" + u.Host }

func (p *networkPreviewProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Macfleet-Preview-Version", "1")
	parts := strings.SplitN(strings.TrimPrefix(r.URL.EscapedPath(), networkPreviewAPI), "/", 2)
	if !strings.HasPrefix(r.URL.Path, networkPreviewAPI) || len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[0])
	target, targetErr := parsePreviewOrigin(string(decoded))
	root, rootErr := parsePreviewOrigin(r.Header.Get("X-Macfleet-Preview-Origin"))
	gate, gateErr := parsePreviewOrigin(r.Header.Get("X-Macfleet-Preview-Gateway"))
	prefix, id := r.Header.Get("X-Macfleet-Preview-Prefix"), r.Header.Get("X-Macfleet-Preview-ID")
	if err != nil || targetErr != nil || rootErr != nil || gateErr != nil || !networkPreviewPrefixPattern.MatchString(prefix) || len(id) != 64 {
		http.Error(w, "网页预览需要通过 Fleet 会话打开", 400)
		return
	}
	target.Path, err = url.PathUnescape("/" + parts[1])
	target.RawPath = "/" + parts[1]
	target.RawQuery = r.URL.RawQuery
	if err != nil {
		http.Error(w, "invalid preview path", 400)
		return
	}
	sameOrigin := previewOriginString(target) == previewOriginString(root)
	credentials := r.Header.Get("X-Macfleet-Preview-Credentials")
	withCookies := credentials != "omit" && (sameOrigin || credentials == "include")
	jar := p.jar(id)
	rewrite := func(raw string) string { return previewRewriteURL(raw, target, prefix) }
	proxy := &httputil.ReverseProxy{Transport: p.transport, FlushInterval: -1}
	proxy.Director = func(out *http.Request) {
		upstream := *target
		out.URL = &upstream
		out.Host = target.Host
		out.Header.Del("Cookie")
		out.Header.Del("Referer")
		for key := range out.Header {
			lower := strings.ToLower(key)
			if strings.HasPrefix(lower, "x-fleet-") || strings.HasPrefix(lower, "x-macfleet-") || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "sec-fetch-") || lower == "forwarded" {
				out.Header.Del(key)
			}
		}
		out.Header.Set("Accept-Encoding", "identity")
		out.Header["X-Forwarded-For"] = nil
		out.Header.Set("Origin", previewOriginString(root))
		out.Header.Set("Referer", previewOriginString(root)+"/")
		// These addresses are server bind addresses, not reachable destinations.
		// Preserve Host while connecting to the corresponding loopback interface.
		if ip := net.ParseIP(target.Hostname()); ip != nil && ip.IsUnspecified() {
			host := "127.0.0.1"
			if ip.To4() == nil {
				host = "::1"
			}
			port := target.Port()
			if port == "" {
				port = "80"
				if target.Scheme == "https" {
					port = "443"
				}
			}
			out.URL.Host = net.JoinHostPort(host, port)
		}
		if withCookies {
			for _, cookie := range jar.Cookies(target) {
				out.AddCookie(cookie)
			}
		}
		if r.Method == "OPTIONS" {
			headers := []string{}
			for _, key := range strings.Split(out.Header.Get("Access-Control-Request-Headers"), ",") {
				if !strings.EqualFold(strings.TrimSpace(key), "X-Fleet-Preview-Credentials") {
					headers = append(headers, strings.TrimSpace(key))
				}
			}
			out.Header.Set("Access-Control-Request-Headers", strings.Join(headers, ", "))
		}
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if withCookies {
			jar.SetCookies(target, resp.Cookies())
		}
		resp.Header.Del("Set-Cookie")
		allowed := sameOrigin || (resp.Header.Get("Access-Control-Allow-Origin") == previewOriginString(root) && (!withCookies || resp.Header.Get("Access-Control-Allow-Credentials") == "true")) || (resp.Header.Get("Access-Control-Allow-Origin") == "*" && !withCookies)
		resp.Header.Del("Access-Control-Allow-Origin")
		resp.Header.Del("Access-Control-Allow-Credentials")
		if allowed {
			resp.Header.Set("Access-Control-Allow-Origin", "null")
			resp.Header.Set("Access-Control-Allow-Credentials", "true")
			if sameOrigin {
				resp.Header.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
				resp.Header.Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
			} else if headers := resp.Header.Get("Access-Control-Allow-Headers"); headers != "" {
				resp.Header.Set("Access-Control-Allow-Headers", headers+", X-Fleet-Preview-Credentials")
			}
		}
		resp.Header.Set("Cache-Control", "no-store")
		resp.Header.Set("Referrer-Policy", "no-referrer")
		for _, key := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only", "X-Frame-Options", "Service-Worker-Allowed", "Clear-Site-Data", "Report-To", "Reporting-Endpoints", "Refresh"} {
			resp.Header.Del(key)
		}
		if location := resp.Header.Get("Location"); location != "" {
			resp.Header.Set("Location", rewrite(location))
		}
		if resp.StatusCode == http.StatusSwitchingProtocols {
			return nil
		}
		kind := strings.ToLower(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
		if r.Method == "HEAD" || (kind != "text/html" && kind != "text/css" && kind != "text/javascript" && kind != "application/javascript") {
			return nil
		}
		const limit = 16 << 20
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if len(body) > limit {
			return fmt.Errorf("preview document exceeds 16 MiB")
		}
		if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
			return fmt.Errorf("unsupported compressed preview document")
		}
		switch kind {
		case "text/html":
			reader, decodeErr := charset.NewReader(bytes.NewReader(body), resp.Header.Get("Content-Type"))
			if decodeErr != nil {
				return decodeErr
			}
			body, err = io.ReadAll(reader)
			if err != nil {
				return err
			}
			body, err = rewriteNetworkHTML(body, target, root, gate, prefix)
			resp.Header.Set("Content-Type", "text/html; charset=utf-8")
		case "text/css":
			body = []byte(rewriteNetworkCSS(string(body), rewrite))
		default:
			body = []byte(rewriteNetworkImports(string(body), rewrite))
		}
		if err != nil {
			return err
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("ETag")
		resp.Header.Del("Content-MD5")
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "该电脑无法连接这个网页，或网页预览响应无效。请确认服务仍在运行以及该电脑的网络连接。", http.StatusBadGateway)
	}
	// A virtual same-origin fetch does not need upstream CORS preflight. The
	// sandbox introduces a real preflight, which we answer without touching the app.
	if r.Method == "OPTIONS" && sameOrigin && r.Header.Get("Access-Control-Request-Method") != "" {
		w.Header().Set("Access-Control-Allow-Origin", "null")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
		w.WriteHeader(204)
		return
	}
	proxy.ServeHTTP(w, r)
}

func previewRewriteURL(raw string, base *url.URL, prefix string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, prefix) {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme == "data" || u.Scheme == "blob" {
		return raw
	}
	u = base.ResolveReference(u)
	if u.User != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ws" && u.Scheme != "wss") {
		return ""
	}
	if u.Scheme == "ws" {
		u.Scheme = "http"
	}
	if u.Scheme == "wss" {
		u.Scheme = "https"
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	result := prefix + base64.RawURLEncoding.EncodeToString([]byte(previewOriginString(u))) + path
	if u.RawQuery != "" {
		result += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		result += "#" + url.PathEscape(u.Fragment)
	}
	return result
}

var networkCSSURL = regexp.MustCompile(`(?i)url\(\s*(?:"([^"\r\n]*)"|'([^'\r\n]*)'|([^)'"\s][^)]*))\s*\)`)
var networkCSSImport = regexp.MustCompile(`(?i)(@import\s+)(["'])([^"'\r\n]+)["']`)
var networkJSImport = regexp.MustCompile(`(?m)(\b(?:from|import)\s*(?:\(\s*)?)(["'])([^"'\r\n]+)["']`)

func rewriteNetworkCSS(source string, rewrite func(string) string) string {
	source = networkCSSURL.ReplaceAllStringFunc(source, func(match string) string {
		parts := networkCSSURL.FindStringSubmatch(match)
		raw := parts[1] + parts[2] + parts[3]
		encoded, _ := json.Marshal(rewrite(strings.TrimSpace(raw)))
		return "url(" + string(encoded) + ")"
	})
	return networkCSSImport.ReplaceAllStringFunc(source, func(match string) string {
		parts := networkCSSImport.FindStringSubmatch(match)
		encoded, _ := json.Marshal(rewrite(parts[3]))
		return parts[1] + string(encoded)
	})
}

func rewriteNetworkImports(source string, rewrite func(string) string) string {
	return networkJSImport.ReplaceAllStringFunc(source, func(match string) string {
		parts := networkJSImport.FindStringSubmatch(match)
		raw := parts[3]
		if !strings.HasPrefix(raw, ".") && !strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "http:") && !strings.HasPrefix(raw, "https:") {
			return match
		}
		encoded, _ := json.Marshal(rewrite(raw))
		return parts[1] + string(encoded)
	})
}

func rewriteNetworkHTML(body []byte, base, root, gate *url.URL, prefix string) ([]byte, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// Honor a document's base before removing it; Fleet owns the actual URL base.
	var findBase func(*html.Node)
	findBase = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "base" {
			for _, a := range n.Attr {
				if a.Key == "href" {
					if u, e := url.Parse(a.Val); e == nil {
						base = base.ResolveReference(u)
					}
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findBase(c)
		}
	}
	findBase(doc)
	rewrite := func(raw string) string { return previewRewriteURL(raw, base, prefix) }
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			crossOriginLink := false
			if n.Data == "a" {
				for _, a := range n.Attr {
					if a.Key == "href" {
						u, e := url.Parse(a.Val)
						if e == nil && a.Val != "" && !strings.HasPrefix(a.Val, "#") {
							u = base.ResolveReference(u)
							crossOriginLink = previewOriginString(u) != previewOriginString(root) && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil
						}
					}
				}
			}
			attrs := n.Attr[:0]
			for _, a := range n.Attr {
				if n.Data == "base" || a.Key == "integrity" || a.Key == "nonce" || a.Key == "crossorigin" || (crossOriginLink && a.Key == "target") {
					continue
				}
				if n.Data == "meta" && strings.EqualFold(a.Key, "http-equiv") && (strings.EqualFold(a.Val, "Content-Security-Policy") || strings.EqualFold(a.Val, "refresh")) {
					n.Data = "template"
				}
				switch a.Key {
				case "href", "src", "action", "poster", "data", "formaction", "xlink:href":
					if n.Data == "a" && a.Key == "href" {
						if u, e := url.Parse(a.Val); e == nil && a.Val != "" && !strings.HasPrefix(a.Val, "#") {
							u = base.ResolveReference(u)
							if previewOriginString(u) != previewOriginString(root) && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil {
								a.Val = "/host-view?mac=" + url.QueryEscape("m"+cfg.MacIndex) + "&url=" + url.QueryEscape(u.String())
								break
							}
						}
					}
					a.Val = rewrite(a.Val)
				case "srcset":
					// Data URLs may contain commas. Leave those intact rather than
					// corrupting their bytes; ordinary URL candidates are rewritten.
					if !strings.Contains(a.Val, "data:") {
						candidates := strings.Split(a.Val, ",")
						for i, c := range candidates {
							fields := strings.Fields(c)
							if len(fields) > 0 {
								fields[0] = rewrite(fields[0])
								candidates[i] = strings.Join(fields, " ")
							}
						}
						a.Val = strings.Join(candidates, ", ")
					}
				case "style":
					a.Val = rewriteNetworkCSS(a.Val, rewrite)
				}
				attrs = append(attrs, a)
			}
			if crossOriginLink {
				attrs = append(attrs, html.Attribute{Key: "target", Val: "_top"})
			}
			n.Attr = attrs
			if n.Data == "script" || n.Data == "link" {
				n.Attr = append(n.Attr, html.Attribute{Key: "crossorigin", Val: "use-credentials"})
			}
			if n.Data == "script" || n.Data == "style" {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.TextNode {
						if n.Data == "style" {
							c.Data = rewriteNetworkCSS(c.Data, rewrite)
						} else {
							c.Data = rewriteNetworkImports(c.Data, rewrite)
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	config, _ := json.Marshal(map[string]string{"base": base.String(), "root": previewOriginString(root), "gateway": previewOriginString(gate), "prefix": prefix, "mac": "m" + cfg.MacIndex})
	script := &html.Node{Type: html.ElementNode, Data: "script"}
	script.AppendChild(&html.Node{Type: html.TextNode, Data: "(" + networkPreviewBootstrap + ")(" + string(config) + ");"})
	var inject func(*html.Node) bool
	inject = func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "head" {
			n.InsertBefore(script, n.FirstChild)
			return true
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if inject(c) {
				return true
			}
		}
		return false
	}
	inject(doc)
	var result bytes.Buffer
	err = html.Render(&result, doc)
	return result.Bytes(), err
}

// This adapter changes network destinations, never account authorization. It
// runs before application scripts; the response CSP keeps its origin opaque.
const networkPreviewBootstrap = `function(c){
const gate=new URL(c.gateway),base=new URL(c.base),prefix=c.prefix;
function unwrap(value){let u=new URL(String(value),base);if(u.origin===gate.origin&&u.pathname.startsWith(prefix)){let p=u.pathname.slice(prefix.length),i=p.indexOf('/');try{return new URL(atob(p.slice(0,i).replace(/-/g,'+').replace(/_/g,'/'))+p.slice(i)+u.search+u.hash)}catch{}}return u}
function route(value,ws=false){let text=String(value);if(text.startsWith('#')||/^(data:|blob:)/i.test(text))return text;let u=unwrap(text);if(!/^(https?:|wss?:)$/.test(u.protocol))throw new TypeError('Unsupported preview URL');if(ws&&(u.hostname===gate.hostname||u.hostname===base.hostname)){if(u.port===gate.port||!u.port)u.port=base.port;u.hostname=base.hostname;u.protocol=base.protocol}let scheme=u.protocol.replace('ws','http');let origin=scheme+'//'+u.host;let id=btoa(origin).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');return (ws?gate.origin.replace(/^http/,'ws'):gate.origin)+prefix+id+u.pathname+u.search+u.hash}
const nativeFetch=window.fetch.bind(window);
window.fetch=function(input,init){let req=input instanceof Request?input:null;let options={...(init||{})};let credentials=options.credentials||req?.credentials||'same-origin';let headers=new Headers(options.headers||req?.headers);headers.set('X-Fleet-Preview-Credentials',credentials);let target=route(req?req.url:input);if(req)input=new Request(target,req);else input=target;return nativeFetch(input,{...options,headers,credentials:'include',mode:'cors'})};
const open=XMLHttpRequest.prototype.open,send=XMLHttpRequest.prototype.send;
XMLHttpRequest.prototype.open=function(method,url,...rest){this.__fleetURL=url;return open.call(this,method,route(url),...rest)};
XMLHttpRequest.prototype.send=function(body){let include=this.withCredentials;this.withCredentials=true;this.setRequestHeader('X-Fleet-Preview-Credentials',include?'include':'same-origin');return send.call(this,body)};
for(const name of ['WebSocket','EventSource']){const Original=window[name];if(!Original)continue;window[name]=class extends Original{constructor(url,...args){super(route(url,name==='WebSocket'),...(name==='EventSource'?[{...(args[0]||{}),withCredentials:true}]:args))}}}
const nativeOpen=window.open;window.open=function(url,...args){return nativeOpen.call(window,route(url),...args)};
function attribute(name,value){return ['href','src','action','poster','formaction','data','xlink:href'].includes(name)?route(value):value}
const set=Element.prototype.setAttribute;Element.prototype.setAttribute=function(name,value){try{value=attribute(name.toLowerCase(),value)}catch{}return set.call(this,name,value)};
for(const [type,names] of [[HTMLImageElement,['src']],[HTMLScriptElement,['src']],[HTMLLinkElement,['href']],[HTMLAnchorElement,['href']],[HTMLFormElement,['action']],[HTMLIFrameElement,['src']],[HTMLSourceElement,['src']],[HTMLVideoElement,['src','poster']],[HTMLAudioElement,['src']]])for(const name of names){let d=Object.getOwnPropertyDescriptor(type.prototype,name);if(d?.set)Object.defineProperty(type.prototype,name,{...d,set(value){d.set.call(this,route(value))}})}
document.addEventListener('click',event=>{let a=event.target.closest?.('a[href]');if(!a||a.target==='_top')return;let raw=a.getAttribute('href');if(raw?.startsWith('#'))return;try{let u=unwrap(raw);if(u.origin!==c.root){set.call(a,'href',gate.origin+'/host-view?mac='+encodeURIComponent(c.mac)+'&url='+encodeURIComponent(u.href));set.call(a,'target','_top')}else set.call(a,'href',route(raw))}catch{event.preventDefault()}},true);
document.addEventListener('submit',event=>{try{let form=event.target;set.call(form,'action',route(form.getAttribute('action')||c.base))}catch{event.preventDefault()}},true);
}`
