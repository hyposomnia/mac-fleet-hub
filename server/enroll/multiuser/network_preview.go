package multiuser

// Network previews are capabilities scoped to an existing login, device binding
// and page origin. They deliberately do not use the Fleet browser cookie: the
// rendered application runs in an opaque sandbox, including on direct navigation.
import (
	"context"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const networkPreviewPrefix = "/network-preview/"

type networkPreviewContextKey struct{}
type networkPreviewContext struct {
	Origin, Prefix, ID string
}

func previewURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || strings.ContainsAny(u.Host, "\\\r\n\t ") {
		return nil, fmt.Errorf("仅支持不含账号密码的 HTTP(S) 地址")
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}

func previewOrigin(u *url.URL) string { return u.Scheme + "://" + u.Host }

var networkPreviewPage = template.Must(template.New("network-preview").Parse(`<!doctype html>
<html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Name}} · 网页预览</title><style>
html,body{margin:0;height:100%;font:14px system-ui;color:#202923;background:#fafafa}body{display:flex;flex-direction:column}
header{display:flex;align-items:center;gap:16px;padding:10px 16px;border-bottom:1px solid #8883}span{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}a{color:inherit}iframe{flex:1;width:100%;border:0}
</style><header><span title="{{.URL}}">通过 {{.Name}} 访问 · {{.URL}}</span><a href="{{.URL}}" target="_blank" rel="noopener noreferrer">在当前浏览器打开</a></header>
<iframe title="{{.Name}}上的网页" sandbox="allow-scripts allow-forms allow-downloads allow-popups allow-top-navigation-by-user-activation" referrerpolicy="no-referrer" src="{{.Source}}"></iframe></html>`))

func (server *Server) openNetworkPreview(w http.ResponseWriter, r *http.Request, session sessionState, user User) {
	if r.Method != http.MethodGet {
		reject(w, 405, "仅支持 GET")
		return
	}
	u, err := previewURL(r.URL.Query().Get("url"))
	if err != nil {
		reject(w, 400, err.Error())
		return
	}
	device, err := server.AuthorizedDevice(user.ID, r.URL.Query().Get("mac"))
	if err != nil {
		reject(w, 404, "设备不存在")
		return
	}
	token, binding := randomToken(), randomToken()
	now := server.now().Unix()
	server.db.Exec("DELETE FROM network_previews WHERE expires_at<=? OR session_hash NOT IN (SELECT token_hash FROM sessions)", now)
	var count int
	server.db.QueryRow("SELECT COUNT(*) FROM network_previews WHERE session_hash=?", session.Hash).Scan(&count)
	if count >= 128 {
		reject(w, 429, "打开的预览过多，请稍后重试")
		return
	}
	expires := server.now().Add(8 * time.Hour).Unix()
	if expires > session.Expires {
		expires = session.Expires
	}
	_, err = server.db.Exec("INSERT INTO network_previews(token_hash,binding_hash,session_hash,device_id,node_id,ip,origin,expires_at) VALUES(?,?,?,?,?,?,?,?)", digest(token), digest(binding), session.Hash, device.ID, device.NodeID, device.IP, previewOrigin(u), expires)
	if err != nil {
		server.failure(w, err)
		return
	}
	prefix := networkPreviewPrefix + token + "/"
	http.SetCookie(w, &http.Cookie{Name: "fleet_preview", Value: binding, Path: prefix, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode, MaxAge: int(expires - now)})
	source := prefix + base64.RawURLEncoding.EncodeToString([]byte(previewOrigin(u))) + u.EscapedPath()
	if u.RawQuery != "" {
		source += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		source += "#" + url.PathEscape(u.Fragment)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-src 'self'; frame-ancestors 'self'; base-uri 'none'")
	_ = networkPreviewPage.Execute(w, map[string]any{"Name": device.Name, "URL": u.String(), "Source": source})
}

func (server *Server) networkPreviewSessionValid(session sessionState) bool {
	var count int
	err := server.db.QueryRow(`SELECT COUNT(*) FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.user_id=? AND s.stage='full' AND s.expires_at>? AND u.status='active'`, session.Hash, session.UserID, server.now().Unix()).Scan(&count)
	return err == nil && count == 1
}

func (server *Server) handleNetworkPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	// No Fleet endpoint is exposed through this route, even when preview scripts
	// send a Fleet cookie or forge internal forwarding headers.
	parts := strings.SplitN(strings.TrimPrefix(r.URL.EscapedPath(), networkPreviewPrefix), "/", 3)
	if len(parts) != 3 || len(parts[0]) > 128 {
		reject(w, 404, "预览不存在")
		return
	}
	originBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	u, parseErr := previewURL(string(originBytes))
	if err != nil || parseErr != nil || u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		reject(w, 400, "预览地址无效")
		return
	}
	var session sessionState
	var previewExpires int64
	var deviceID, nodeID, ip, origin, bindingHash string
	err = server.db.QueryRow(`SELECT p.session_hash,s.user_id,s.expires_at,p.device_id,p.node_id,p.ip,p.origin,p.expires_at,p.binding_hash FROM network_previews p JOIN sessions s ON s.token_hash=p.session_hash WHERE p.token_hash=? AND p.expires_at>?`, digest(parts[0]), server.now().Unix()).Scan(&session.Hash, &session.UserID, &session.Expires, &deviceID, &nodeID, &ip, &origin, &previewExpires, &bindingHash)
	if err != nil || !server.networkPreviewSessionValid(session) {
		reject(w, 401, "预览已过期，请从会话重新打开链接")
		return
	}
	// CORS preflights carry no cookies. They expose no application body and
	// authorize no subsequent request without the HttpOnly browser binding.
	preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
	binding, bindingErr := r.Cookie("fleet_preview")
	if !preflight && (bindingErr != nil || digest(binding.Value) != bindingHash) {
		reject(w, 401, "请在打开预览的原浏览器中访问，或从会话重新打开链接")
		return
	}
	device, err := server.AuthorizedDevice(session.UserID, deviceID)
	if err != nil || device.NodeID != nodeID || device.IP != ip {
		reject(w, 404, "设备访问授权已失效")
		return
	}
	if requestOrigin := r.Header.Get("Origin"); requestOrigin != "" && requestOrigin != "null" && requestOrigin != server.options.Origin {
		reject(w, 403, "请求来源不合法")
		return
	}
	// A real document navigation changes the virtual origin. Mint a separate
	// capability instead of broadening the previous document's CORS authority.
	// Sec-Fetch-Dest cannot be set by page JavaScript; a fetch cannot mint one.
	destination := r.Header.Get("Sec-Fetch-Dest")
	if previewOrigin(u) != origin && r.Method == http.MethodGet && (destination == "iframe" || destination == "document") && bindingErr == nil {
		var count int
		server.db.QueryRow("SELECT COUNT(*) FROM network_previews WHERE session_hash=?", session.Hash).Scan(&count)
		if count >= 128 {
			reject(w, 429, "打开的预览过多，请稍后重试")
			return
		}
		token := randomToken()
		_, err = server.db.Exec("INSERT INTO network_previews(token_hash,binding_hash,session_hash,device_id,node_id,ip,origin,expires_at) VALUES(?,?,?,?,?,?,?,?)", digest(token), bindingHash, session.Hash, deviceID, nodeID, ip, previewOrigin(u), previewExpires)
		if err != nil {
			server.failure(w, err)
			return
		}
		prefix := networkPreviewPrefix + token + "/"
		http.SetCookie(w, &http.Cookie{Name: "fleet_preview", Value: binding.Value, Path: prefix, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode, MaxAge: int(previewExpires - server.now().Unix())})
		next := prefix + parts[1] + "/" + parts[2]
		if r.URL.RawQuery != "" {
			next += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	user, err := server.user(session.UserID)
	if err != nil {
		reject(w, 401, "请重新登录")
		return
	}
	session.Stage = "full"
	meta := networkPreviewContext{Origin: origin, Prefix: networkPreviewPrefix + parts[0] + "/", ID: digest(parts[0])}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(previewExpires-server.now().Unix())*time.Second)
	defer cancel()
	request := r.Clone(context.WithValue(ctx, networkPreviewContextKey{}, meta))
	request.URL.Path, err = url.PathUnescape("/" + deviceID + "/api/network-preview/" + parts[1] + "/" + parts[2])
	if err != nil {
		reject(w, 400, "预览路径无效")
		return
	}
	request.URL.RawPath = "/" + deviceID + "/api/network-preview/" + parts[1] + "/" + parts[2]
	request.Header.Set("Origin", server.options.Origin)
	server.handleProxy(w, request, session, user)
}
