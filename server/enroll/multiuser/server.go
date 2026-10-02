package multiuser

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Options struct {
	StateDir, StaticDir, Origin, KeyFile, LoginServer string
	Key                                               []byte
	Now                                               func() time.Time
	Network                                           Network
	Application                                       func(User) http.Handler
	PublicAPI                                         http.Handler
	ProxyURL                                          func(Device, string) string
	AgentPort, TerminalPort, FilesPort                int
	MeshProxy                                         string
}

type scope struct {
	context context.Context
	cancel  context.CancelFunc
}
type Server struct {
	options     Options
	db          *sql.DB
	mu          sync.Mutex
	scopeMu     sync.Mutex
	scopes      map[string]scope
	root        context.Context
	cancel      context.CancelFunc
	authSlots   chan struct{}
	failMu      sync.Mutex
	failures    map[string][]time.Time
	networkSync atomic.Int64
}

func New(options Options) (*Server, error) {
	if _, err := NewDeviceTransport(options.MeshProxy); err != nil {
		return nil, err
	}
	origin, err := url.Parse(options.Origin)
	if err != nil || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.User != nil {
		return nil, errors.New("FLEET_ORIGIN 必须是完整服务 origin")
	}
	if origin.Scheme != "https" && !(origin.Scheme == "http" && net.ParseIP(origin.Hostname()).IsLoopback()) {
		return nil, errors.New("HTTP 仅允许 loopback 本地验证")
	}
	if options.StateDir == "" {
		return nil, errors.New("缺少状态目录")
	}
	if len(options.Key) == 0 {
		if options.KeyFile == "" {
			options.KeyFile = filepath.Join(options.StateDir, "encryption.key")
		}
		if err = os.MkdirAll(filepath.Dir(options.KeyFile), 0700); err != nil {
			return nil, err
		}
		options.Key, err = os.ReadFile(options.KeyFile)
		if errors.Is(err, os.ErrNotExist) {
			if existing, statErr := os.Stat(filepath.Join(options.StateDir, "fleet.sqlite")); statErr == nil && existing.Size() > 0 {
				return nil, errors.New("已有数据库的加密 key 丢失，请恢复原 key，不能生成替代 key")
			}
			options.Key = make([]byte, 32)
			if _, err = rand.Read(options.Key); err != nil {
				return nil, err
			}
			file, createErr := os.OpenFile(options.KeyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if createErr != nil {
				return nil, createErr
			}
			_, err = file.Write(options.Key)
			file.Close()
		}
		if err != nil {
			return nil, err
		}
		if err = os.Chmod(options.KeyFile, 0600); err != nil {
			return nil, err
		}
	}
	if len(options.Key) != 32 {
		return nil, errors.New("加密 key 必须是 32 字节")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.AgentPort == 0 {
		options.AgentPort = 7682
	}
	if options.TerminalPort == 0 {
		options.TerminalPort = 7681
	}
	if options.FilesPort == 0 {
		options.FilesPort = 8080
	}
	root, cancel := context.WithCancel(context.Background())
	server := &Server{options: options, root: root, cancel: cancel, scopes: map[string]scope{}, authSlots: make(chan struct{}, 4), failures: map[string][]time.Time{}}
	if err = server.openStore(); err != nil {
		cancel()
		if server.db != nil {
			server.db.Close()
		}
		return nil, err
	}
	var encrypted []byte
	err = server.db.QueryRow("SELECT totp FROM users LIMIT 1").Scan(&encrypted)
	if err == nil {
		if _, err = server.unseal(encrypted); err != nil {
			err = errors.New("加密 key 与已有数据库不匹配，请恢复原 key")
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		server.Close()
		return nil, err
	}
	return server, nil
}

func (server *Server) Close() error   { server.cancel(); return server.db.Close() }
func (server *Server) now() time.Time { return server.options.Now() }
func (server *Server) ScopeContext(userID int64, deviceID string) context.Context {
	key := intString(userID) + "/" + deviceID
	server.scopeMu.Lock()
	defer server.scopeMu.Unlock()
	if existing, ok := server.scopes[key]; ok {
		return existing.context
	}
	ctx, cancel := context.WithCancel(server.root)
	server.scopes[key] = scope{ctx, cancel}
	return ctx
}
func (server *Server) cancelScopes(userID int64, deviceID string) {
	server.scopeMu.Lock()
	defer server.scopeMu.Unlock()
	for key, value := range server.scopes {
		if strings.HasPrefix(key, intString(userID)+"/") && (deviceID == "" || key == intString(userID)+"/"+deviceID) {
			value.cancel()
			delete(server.scopes, key)
		}
	}
}

func respond(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	json.NewEncoder(writer).Encode(value)
}
func reject(writer http.ResponseWriter, status int, message string) {
	respond(writer, status, map[string]string{"error": message})
}
func decode(writer http.ResponseWriter, request *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	if err := decoder.Decode(value); err != nil {
		reject(writer, 400, "请求格式错误")
		return false
	}
	return true
}
func (server *Server) failure(writer http.ResponseWriter, err error) {
	log.Printf("fleet-server: %v", err)
	reject(writer, 500, "服务暂时不可用")
}
func unsafeMethod(method string) bool {
	return method != "GET" && method != "HEAD" && method != "OPTIONS"
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Referrer-Policy", "same-origin")
	path := request.URL.Path
	if path == "/healthz" {
		if err := server.db.PingContext(request.Context()); err != nil {
			reject(writer, 503, "unavailable")
		} else {
			writer.Write([]byte("ok"))
		}
		return
	}
	if path == "/readyz" {
		lastSync := server.networkSync.Load()
		if server.options.Network == nil || lastSync == 0 || server.now().Unix()-lastSync > 45 {
			reject(writer, 503, "Headscale 网络策略未就绪")
		} else {
			writer.Write([]byte("ok"))
		}
		return
	}
	if strings.HasPrefix(path, "/api/v1/messages") {
		if server.options.PublicAPI != nil {
			server.options.PublicAPI.ServeHTTP(writer, request)
		} else {
			reject(writer, 401, "访问密钥无效")
		}
		return
	}
	if path == "/enroll/join" || path == "/join" {
		reject(writer, 410, "请通过浏览器确认设备绑定")
		return
	}
	if path == "/enroll/agent-config" {
		respond(writer, 200, map[string]int{"idleSec": 1800})
		return
	}
	if strings.HasPrefix(path, "/api/device/") {
		server.handleDeviceAuth(writer, request)
		return
	}
	if strings.HasPrefix(path, "/api/auth/") {
		if unsafeMethod(request.Method) && request.Header.Get("Origin") != server.options.Origin {
			reject(writer, 403, "请求来源不合法")
			return
		}
		server.handleAuth(writer, request)
		return
	}
	if path == "/api/enrollment/start" || path == "/api/enrollment/claim" || path == "/api/enrollment/complete" {
		if request.Method != "POST" {
			reject(writer, 405, "仅支持 POST")
			return
		}
		server.handleEnrollment(writer, request, User{})
		return
	}
	if path == "/auth" || strings.HasPrefix(path, "/auth/") {
		server.serveFile(writer, request, "auth.html")
		return
	}
	if path == "/theme.js" || path == "/auth-client.js" || path == "/account.js" || path == "/account.css" || path == "/style.css" || strings.HasPrefix(path, "/icons/") {
		server.serveFile(writer, request, path[1:])
		return
	}
	if path == "/enroll/bootstrap.sh" || path == "/enroll/mac-bundle.tar.gz" || strings.HasPrefix(path, "/enroll/dist/") {
		reject(writer, 404, "请由 nginx 分发安装包")
		return
	}
	session, user, err := server.authenticate(request, true)
	if err != nil {
		if !strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/m") {
			http.Redirect(writer, request, "/auth?next="+url.QueryEscape(request.URL.RequestURI()), 303)
		} else {
			reject(writer, 401, "请重新登录")
		}
		return
	}
	if unsafeMethod(request.Method) {
		match := devicePath.FindStringSubmatch(path)
		embedded := len(match) > 0 && (match[2] == "files" || match[2] == "dsh" || (match[2] == "api" && strings.HasPrefix(match[3], "/dsh-native/")))
		validToken := request.Header.Get("X-CSRF-Token") == session.CSRF
		if embedded {
			validToken = request.Header.Get("Sec-Fetch-Site") == "same-origin"
		}
		if request.Header.Get("Origin") != server.options.Origin || !validToken {
			reject(writer, 403, "请求来源或安全令牌不合法")
			return
		}
	}
	if strings.HasPrefix(path, "/m") && server.handleProxy(writer, request, session, user) {
		return
	}
	if path == "/api/enrollment/confirm" {
		if request.Method != "POST" {
			reject(writer, 405, "仅支持 POST")
			return
		}
		server.handleEnrollment(writer, request, user)
		return
	}
	if path == "/api/enrollment/preview" {
		server.enrollmentPreview(writer, request)
		return
	}
	if strings.HasPrefix(path, "/api/admin/") {
		if user.Role != "admin" {
			reject(writer, 403, "需要管理员权限")
			return
		}
		server.handleAdmin(writer, request, user)
		return
	}
	if path == "/api/devices" || strings.HasPrefix(path, "/api/devices/") || path == "/api/nodes.json" || path == "/api/names" || path == "/api/settings" {
		server.handleDevices(writer, request, user)
		return
	}
	if strings.HasPrefix(path, "/api/") {
		if server.options.Application != nil {
			handler := server.options.Application(user)
			if handler != nil {
				handler.ServeHTTP(writer, request)
				return
			}
		}
		reject(writer, 404, "接口不存在")
		return
	}
	switch path {
	case "/account", "/account/":
		server.serveFile(writer, request, "account.html")
	case "/admin", "/admin/":
		if user.Role != "admin" {
			reject(writer, 403, "需要管理员权限")
			return
		}
		server.serveFile(writer, request, "admin.html")
	case "/enroll/confirm", "/enroll/confirm/":
		server.serveFile(writer, request, "enroll.html")
	case "/", "/index.html":
		server.serveFile(writer, request, "index.html")
	default:
		if strings.Contains(path, "..") || strings.HasPrefix(path, "/enroll/") {
			reject(writer, 404, "不存在")
			return
		}
		http.FileServer(http.Dir(server.options.StaticDir)).ServeHTTP(writer, request)
	}
}

func (server *Server) serveFile(writer http.ResponseWriter, request *http.Request, name string) {
	http.ServeFile(writer, request, filepath.Join(server.options.StaticDir, name))
}
