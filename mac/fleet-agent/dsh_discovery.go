package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dshEndpoint 是 DSH Desktop 的 harness host 端点。
//
// host 只监听 loopback，端口优先 43129（dev 版 43130），被占用时会退回 OS 分配的临时端口，
// 所以端口必须从运行痕迹里发现，不能写死。Token 是每进程随机、不落盘的启动凭据，
// 只在 harness.log 里出现一次；拿不到它时退回用 .credentials.yaml 的 secret 自签 cookie。
type dshEndpoint struct {
	Port  int
	Token string
}

// dshWebLineRe 与 Desktop 主进程自己抓取启动 URL 用的正则保持逐字一致，
// 这样上游改格式时两边会一起失效、不会静默漂移。
var dshWebLineRe = regexp.MustCompile(`\bdsh web:\s*(\S+)`)

// parseDSHWebLine 从一行日志里解析端点。
//
// 返回 ok 表示"这是一条 dsh web: 端点行且带合法端口"；Token 允许为空——
// 日志被轮转或只回放了部分输出时，端口仍然有用（可配合自签 cookie 使用）。
func parseDSHWebLine(line string) (dshEndpoint, bool) {
	m := dshWebLineRe.FindStringSubmatch(line)
	if m == nil {
		return dshEndpoint{}, false
	}
	u, err := url.Parse(m[1])
	if err != nil {
		return dshEndpoint{}, false
	}
	portText := u.Port()
	if portText == "" {
		return dshEndpoint{}, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return dshEndpoint{}, false
	}
	return dshEndpoint{Port: port, Token: u.Query().Get("token")}, true
}

// dshEndpointFromLog 从日志尾部向前找最后一条端点行。
//
// 必须取最后一条：Desktop 每次重启都会追加新的 dsh web: 行，而 token 是每进程一份的，
// 取错一条就会拿到已失效的凭据。
func dshEndpointFromLog(path string) (dshEndpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return dshEndpoint{}, fmt.Errorf("读取 DSH harness 日志失败: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if ep, ok := parseDSHWebLine(lines[i]); ok {
			return ep, nil
		}
	}
	return dshEndpoint{}, fmt.Errorf("%s 中没有 dsh web: 端点行", path)
}

// dshCookieMaxAge 与 host 端 cookieMaxAgeDays 的默认值一致。
//
// host 在验签时会拒绝 issuedAt/expiresAt 跨度超过该上界的 cookie，
// 所以自签时不能图省事签一个超长有效期。
const dshCookieMaxAge = 30 * 24 * time.Hour

// dshBrowserSessionKey 是 .credentials.yaml 里浏览器会话签名密钥的记录键。
const dshBrowserSessionKey = "client-connection/browser-session"

func dshBase64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// dshCookieName 复刻 host 的 cookie 命名：dsh-auth- + base64url(sha256(authority))。
//
// authority 参与哈希，因此换端口就是换 cookie 名；用错端口签出来的 cookie
// 会被 host 当作"没有凭据"直接 401。
func dshCookieName(authority string) string {
	sum := sha256.Sum256([]byte(authority))
	return "dsh-auth-" + dshBase64URL(sum[:])
}

// dshCookieBody 是 cookie 里承载的明文 JSON。
//
// 字段顺序即 JSON 序列化顺序，且 HMAC 覆盖的就是这段明文的 base64url 字节，
// 所以顺序不能随意调整（改了签名对不上）。
type dshCookieBody struct {
	Version   int    `json:"version"`
	Authority string `json:"authority"`
	IssuedAt  int64  `json:"issuedAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

// signDSHCookie 用 .credentials.yaml 里的 secret 自签一个浏览器会话 cookie，
// 返回可直接放进请求头 Cookie 的 "name=value"。
func signDSHCookie(secret, authority string, now time.Time, ttl time.Duration) (string, error) {
	if secret == "" {
		return "", errors.New("DSH cookie secret 为空")
	}
	if authority == "" {
		return "", errors.New("DSH cookie authority 为空")
	}
	if ttl <= 0 || ttl > dshCookieMaxAge {
		return "", fmt.Errorf("DSH cookie 有效期 %s 超出 (0, %s] 区间", ttl, dshCookieMaxAge)
	}
	issuedAt := now.UnixMilli()
	body, err := json.Marshal(dshCookieBody{
		Version:   1,
		Authority: authority,
		IssuedAt:  issuedAt,
		ExpiresAt: issuedAt + ttl.Milliseconds(),
	})
	if err != nil {
		return "", fmt.Errorf("序列化 DSH cookie: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return dshCookieName(authority) + "=v1." + dshBase64URL(body) + "." + dshBase64URL(mac.Sum(nil)), nil
}

// dshCookieSecret 读取 $DSH_HOME/.credentials.yaml 里的浏览器会话签名密钥。
//
// 该文件权限为 0600，属于当前用户；这仍是"同机同用户可读"的本地凭据，
// 不是官方集成契约，Desktop 升级可能改格式——所以它只是 token 路径的兜底。
func dshCookieSecret(home string) (string, error) {
	path := filepath.Join(home, ".credentials.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取 DSH 凭据失败: %w", err)
	}
	secret, ok := parseCredentialsSecret(data)
	if !ok {
		return "", fmt.Errorf("%s 中没有 %s 的 secret", path, dshBrowserSessionKey)
	}
	return secret, nil
}

// parseCredentialsSecret 从 .credentials.yaml 里取出 browser-session 的 secret。
//
// 刻意手写而不是引入 YAML 依赖：本项目只依赖 gorilla/websocket，为一个固定形状的
// 浅层文档再加一个第三方解析器不划算。代价是解析面窄——只认"records 下的
// client-connection/browser-session 记录里的 secret"，其它结构一律返回 false，
// 由调用方降级（这一步失败只影响兜底路径，主路径是 token 交换）。
func parseCredentialsSecret(data []byte) (string, bool) {
	recordsIndent := -1
	sessionIndent := -1

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		key, value, ok := splitYAMLKeyValue(trimmed)
		if !ok {
			continue
		}

		switch {
		case recordsIndent < 0:
			if key == "records" {
				recordsIndent = indent
			}
		case sessionIndent < 0:
			if indent <= recordsIndent {
				// 已经离开 records 段，目标记录不存在。
				return "", false
			}
			if key == dshBrowserSessionKey {
				sessionIndent = indent
			}
		default:
			if indent <= sessionIndent {
				// 离开目标记录，说明它下面没有 secret。
				return "", false
			}
			if key == "secret" && value != "" {
				return unquoteYAMLScalar(value), true
			}
		}
	}
	return "", false
}

// splitYAMLKeyValue 按第一个冒号切出键与值。
//
// 本例中出现的键（records、client-connection/browser-session、secret 等）都不含冒号，
// 值里的冒号（如 URL）在第一个冒号之后，因此按首个冒号切分是安全的。
func splitYAMLKeyValue(s string) (string, string, bool) {
	i := strings.Index(s, ":")
	if i <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(s[:i])
	if key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(s[i+1:]), true
}

func unquoteYAMLScalar(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// dshAuthMode 记录这次实际用上的凭据路径。
//
// 它不是实现细节：token 只在进程启动时打印一次且日志可能被轮转，secret 路径
// 则是同机同用户可读的文件。排障时必须能从 /api/info 看出走的是哪条。
type dshAuthMode string

const (
	dshAuthNone   dshAuthMode = "none"
	dshAuthToken  dshAuthMode = "token"
	dshAuthSecret dshAuthMode = "secret"
)

// dshExchangeToken 用启动 URL 里的 token 换取浏览器会话 cookie。
//
// 必须禁止跟随重定向：host 的成功响应是 303 + Set-Cookie 指向干净的 /，
// 一旦跟随就会把 Set-Cookie 丢掉，只剩一个没有凭据的 200 页面。
func dshExchangeToken(ctx context.Context, baseURL, token string) (string, error) {
	if token == "" {
		return "", errors.New("DSH 启动 token 为空")
	}
	target := strings.TrimRight(baseURL, "/") + "/?token=" + url.QueryEscape(token)
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", fmt.Errorf("构造 token 交换请求: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: %v", errDSHHostUnavailable, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	switch resp.StatusCode {
	case http.StatusOK, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect:
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", fmt.Errorf("%w: token 交换 HTTP %d", errDSHAuthFailed, resp.StatusCode)
	default:
		return "", fmt.Errorf("%w: token 交换 HTTP %d", errDSHProtocolChanged, resp.StatusCode)
	}

	for _, c := range resp.Cookies() {
		if strings.HasPrefix(c.Name, "dsh-auth-") && c.Value != "" {
			return c.Name + "=" + c.Value, nil
		}
	}
	return "", fmt.Errorf("%w: token 交换没有下发 dsh-auth cookie", errDSHAuthFailed)
}

// dshAuthority 是 host 侧 cookie 与信任栅栏使用的权威标识。
func dshAuthority(port int) string {
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// defaultDSHHome 是 DSH Desktop 的默认 harness 主目录。
//
// Desktop 用的是 join(app.getPath("userData"), "harness")，在 macOS 上就是这里。
// 注意它与 DSH CLI 自己的默认值（~/.dsh）不是同一个目录：不显式指定就看不到
// Desktop 的会话。
func defaultDSHHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "dsh-desktop", "harness")
}

// defaultDSHLog 是 Desktop 写 harness stdout/stderr 的日志路径。
//
// 端点与每进程 token 都只在这里出现，所以它是端点发现的唯一来源。
func defaultDSHLog() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Logs", "DSH Desktop", "harness.log")
}

// connectDSH 按「日志取端点 → token 换 cookie → secret 自签 cookie」建立客户端。
//
// endpointOverride 非空时跳过日志发现（形如 "127.0.0.1:43129"），供显式配置与排障使用。
// 两条凭据路径都失败才返回错误；错误里带上两条路径各自的原因，否则排障时只能看到一半。
func connectDSH(ctx context.Context, home, logPath, endpointOverride string) (*dshClient, dshEndpoint, dshAuthMode, error) {
	var endpoint dshEndpoint
	if endpointOverride != "" {
		host, portText, ok := strings.Cut(endpointOverride, ":")
		if !ok || host == "" {
			return nil, dshEndpoint{}, dshAuthNone, fmt.Errorf("DSH endpoint 配置不合法: %q", endpointOverride)
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port <= 0 || port > 65535 {
			return nil, dshEndpoint{}, dshAuthNone, fmt.Errorf("DSH endpoint 端口不合法: %q", endpointOverride)
		}
		endpoint = dshEndpoint{Port: port}
	} else {
		fromLog, err := dshEndpointFromLog(logPath)
		if err != nil {
			return nil, dshEndpoint{}, dshAuthNone, fmt.Errorf("%w: %v", errDSHHostUnavailable, err)
		}
		endpoint = fromLog
	}

	baseURL := fmt.Sprintf("http://%s", dshAuthority(endpoint.Port))
	authority := dshAuthority(endpoint.Port)

	if endpoint.Token != "" {
		cookie, err := dshExchangeToken(ctx, baseURL, endpoint.Token)
		if err == nil {
			return newDSHClient(baseURL, cookie), endpoint, dshAuthToken, nil
		}
		// token 路径失败（多半是日志被轮转或 token 已随进程换代）时退回自签。
		if secret, secretErr := dshCookieSecret(home); secretErr == nil {
			if cookie, signErr := signDSHCookie(secret, authority, time.Now(), dshCookieMaxAge); signErr == nil {
				return newDSHClient(baseURL, cookie), endpoint, dshAuthSecret, nil
			}
		}
		return nil, endpoint, dshAuthNone, fmt.Errorf("token 与 secret 两条凭据路径都失败: %v", err)
	}

	secret, err := dshCookieSecret(home)
	if err != nil {
		return nil, endpoint, dshAuthNone, fmt.Errorf("%w: 日志里没有 token，且 %v", errDSHAuthFailed, err)
	}
	cookie, err := signDSHCookie(secret, authority, time.Now(), dshCookieMaxAge)
	if err != nil {
		return nil, endpoint, dshAuthNone, err
	}
	return newDSHClient(baseURL, cookie), endpoint, dshAuthSecret, nil
}
