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
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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
// 字段顺序即 JSON 序列化顺序，且 HMAC 覆盖的是这段明文 base64url 之后的字符串
// （不是原始 JSON 字节），所以顺序不能随意调整（改了签名对不上）。
type dshCookieBody struct {
	Version   int    `json:"version"`
	Authority string `json:"authority"`
	IssuedAt  int64  `json:"issuedAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

// decodeDSHSecret 解码 .credentials.yaml 里 base64url 形式的签名密钥。
//
// host 端 storedSecret() 把 payload.secret 做 base64url 解码并要求恰好 32 字节，
// 再用解码后的字节做 HMAC key。直接把文件里的字符串当 key 会静默 401，
// 而且 401 只会表现为"cookie 无效"，排障时很难联想到编码层。
func decodeDSHSecret(secret string) ([]byte, error) {
	if secret == "" {
		return nil, errors.New("DSH cookie secret 为空")
	}
	key, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(key) != 32 {
		return nil, errors.New("DSH cookie secret 不是 32 字节 base64url")
	}
	return key, nil
}

// signDSHCookie 用 .credentials.yaml 里的 secret 自签一个浏览器会话 cookie，
// 返回可直接放进请求头 Cookie 的 "name=value"。
//
// 与 host 端 dsh-client-connection 的 encodeCookie 逐字节对齐：
//   - HMAC key 是 secret 的 base64url 解码结果（32 字节），不是文件里的文本；
//   - HMAC 覆盖 body 的 base64url 字符串，不是原始 JSON；
//   - cookie 值形如 v1.<body-b64url>.<sig-b64url>。
func signDSHCookie(secret, authority string, now time.Time, ttl time.Duration) (string, error) {
	if authority == "" {
		return "", errors.New("DSH cookie authority 为空")
	}
	if ttl <= 0 || ttl > dshCookieMaxAge {
		return "", fmt.Errorf("DSH cookie 有效期 %s 超出 (0, %s] 区间", ttl, dshCookieMaxAge)
	}
	key, err := decodeDSHSecret(secret)
	if err != nil {
		return "", err
	}
	issuedAt := now.UnixMilli()
	plain, err := json.Marshal(dshCookieBody{
		Version:   1,
		Authority: authority,
		IssuedAt:  issuedAt,
		ExpiresAt: issuedAt + ttl.Milliseconds(),
	})
	if err != nil {
		return "", fmt.Errorf("序列化 DSH cookie: %w", err)
	}
	body := dshBase64URL(plain)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(body))
	return dshCookieName(authority) + "=v1." + body + "." + dshBase64URL(mac.Sum(nil)), nil
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

// dshDesktopAppPath 是 DSH Desktop 的安装路径（能力探测 / 排障提示用）。
const dshDesktopAppPath = "/Applications/DSH Desktop.app"

// dshDesktopInstalled 报告本机是否装了 DSH Desktop。
//
// 只看安装不看运行：hostRunning 需要一次成功连接才能判定，两者在 /api/info 里
// 分开上报，"没装"与"装了但没开"对用户是两种不同的提示。
func dshDesktopInstalled() bool {
	_, err := os.Stat(dshDesktopAppPath)
	return err == nil
}

// defaultDSHHome 是 DSH Desktop 的默认 harness 主目录。
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
// 端点与每进程 token 都只在这里出现，所以它是 Desktop 端点发现的来源。
func defaultDSHLog() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Logs", "DSH Desktop", "harness.log")
}

// defaultDeepSeekHome 是 `dsh` CLI / web harness 的默认主目录（~/.dsh）。
//
// 与 Desktop 的 harness 主目录是两个独立根：各自有 .credentials.yaml 与 sessions/。
// 这也是 cookie 归属的判据——用某个 home 的 secret 签出来的 cookie 只对同 home
// 的 host 有效，因此扫端口时可以据此认领 endpoint。
func defaultDeepSeekHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dsh")
}

// deepSeekInstalled 报告本机是否存在 `dsh` CLI 的 harness 主目录。
//
// 与 Desktop 不同，CLI 没有 App bundle 可查；主目录（或其中的凭据文件）存在
// 就说明这台机器用 CLI 跑过 harness。只看安装不看运行，两者分开上报。
func deepSeekInstalled(home string) bool {
	if home == "" {
		return false
	}
	if st, err := os.Stat(home); err == nil && st.IsDir() {
		return true
	}
	return false
}

// dshHarnessSpec 描述一个 DSH 家族 harness 的接入参数。
//
// Desktop 与 CLI 的差异集中在“端点从哪来”和“home 在哪”，协议与凭据模型完全一致；
// 因此两个 assistant 共用同一套客户端实现，只在装配时传不同 spec。
type dshHarnessSpec struct {
	// assistant 是该 harness 在 Fleet 里的 assistant 标识（dsh / deepseek）。
	assistant string
	home      string
	logPath   string
	// endpointOverride 形如 127.0.0.1:43129，显式指定后跳过自动发现。
	endpointOverride string
	// autoDiscover 在没有日志时扫描 loopback 监听端口，用 home 的 secret 认领 host。
	// Desktop 有固定日志，CLI 只在终端打印 URL，因此只有 CLI 需要它。
	autoDiscover bool
}

// connectDSH 按「显式 endpoint → 日志端点 → 监听端口发现 → token/secret 认证」建立客户端。
//
// endpointOverride 非空时跳过自动发现，供显式配置与排障使用。
// 两条凭据路径都失败才返回错误；错误里带上两条路径各自的原因，否则排障时只能看到一半。
func connectDSH(ctx context.Context, spec dshHarnessSpec) (*dshClient, dshEndpoint, dshAuthMode, error) {
	endpoint, err := discoverDSHEndpoint(ctx, spec)
	if err != nil {
		return nil, dshEndpoint{}, dshAuthNone, err
	}

	baseURL := fmt.Sprintf("http://%s", dshAuthority(endpoint.Port))
	authority := dshAuthority(endpoint.Port)

	if endpoint.Token != "" {
		cookie, err := dshExchangeToken(ctx, baseURL, endpoint.Token)
		if err == nil {
			return newDSHClient(baseURL, cookie), endpoint, dshAuthToken, nil
		}
		// token 路径失败（多半是日志被轮转或 token 已随进程换代）时退回自签。
		if secret, secretErr := dshCookieSecret(spec.home); secretErr == nil {
			if cookie, signErr := signDSHCookie(secret, authority, time.Now(), dshCookieMaxAge); signErr == nil {
				return newDSHClient(baseURL, cookie), endpoint, dshAuthSecret, nil
			}
		}
		return nil, endpoint, dshAuthNone, fmt.Errorf("token 与 secret 两条凭据路径都失败: %v", err)
	}

	secret, err := dshCookieSecret(spec.home)
	if err != nil {
		return nil, endpoint, dshAuthNone, fmt.Errorf("%w: 没有启动 token，且 %v", errDSHAuthFailed, err)
	}
	cookie, err := signDSHCookie(secret, authority, time.Now(), dshCookieMaxAge)
	if err != nil {
		return nil, endpoint, dshAuthNone, err
	}
	return newDSHClient(baseURL, cookie), endpoint, dshAuthSecret, nil
}

// discoverDSHEndpoint 按 spec 决定端点来源。
func discoverDSHEndpoint(ctx context.Context, spec dshHarnessSpec) (dshEndpoint, error) {
	if spec.endpointOverride != "" {
		host, portText, ok := strings.Cut(spec.endpointOverride, ":")
		if !ok || host == "" {
			return dshEndpoint{}, fmt.Errorf("DSH endpoint 配置不合法: %q", spec.endpointOverride)
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port <= 0 || port > 65535 {
			return dshEndpoint{}, fmt.Errorf("DSH endpoint 端口不合法: %q", spec.endpointOverride)
		}
		return dshEndpoint{Port: port}, nil
	}

	if spec.logPath != "" {
		if fromLog, err := dshEndpointFromLog(spec.logPath); err == nil {
			return fromLog, nil
		} else if !spec.autoDiscover {
			return dshEndpoint{}, fmt.Errorf("%w: %v", errDSHHostUnavailable, err)
		}
	}

	if spec.autoDiscover {
		fromListeners, err := discoverDSHListeners(ctx, spec.home)
		if err != nil {
			return dshEndpoint{}, err
		}
		return fromListeners, nil
	}

	return dshEndpoint{}, fmt.Errorf("%w: 没有可用的端点来源", errDSHHostUnavailable)
}

// dshLoopbackPorts 返回本机所有 loopback TCP 监听端口，供测试替换。
var dshLoopbackPorts = listDSHLoopbackPorts

// listDSHLoopbackPorts 用 lsof 枚举 loopback 监听端口。
//
// CLI 的 `dsh --profile web` 只把带 token 的 URL 打到 stdout，没有 Desktop 那样的
// 日志文件；能稳定拿到的只有“它正在某个 loopback 端口上监听”。端口可能是 OS 分配的
// 临时端口，所以不能写死 43129。
func listDSHLoopbackPorts() ([]int, error) {
	// launchd 给的 PATH 通常含 /usr/sbin，但排障时可能在最小 PATH 下运行；
	// 逐个尝试常见位置，避免"明明装了 host 却总报扫描失败"。
	var out []byte
	var err error
	for _, bin := range []string{"lsof", "/usr/sbin/lsof", "/usr/bin/lsof"} {
		out, err = exec.Command(bin, "-nP", "-iTCP", "-sTCP:LISTEN", "-Fn").Output()
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "n") {
			continue
		}
		addr := strings.TrimPrefix(line, "n")
		host, portText, ok := strings.Cut(addr, ":")
		if !ok {
			continue
		}
		switch host {
		case "127.0.0.1", "[::1]", "0.0.0.0", "*":
		default:
			continue
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		seen[port] = true
	}
	ports := make([]int, 0, len(seen))
	for port := range seen {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports, nil
}

// probeDSHHome 判断某个 loopback 端口是否是 home 对应的 DSH host。
//
// 两步都要成立：/manifest.webmanifest 是 DSH Web UI 的特征端点（其它本地服务极少
// 同时返回 manifest+json），而 / 用 home 的 secret 自签 cookie 后返回 200 才证明
// 这个 host 属于该 home——不同 home 的 host 会用各自的 secret 验签，必然 401。
func probeDSHHome(ctx context.Context, home string, port int) bool {
	secret, err := dshCookieSecret(home)
	if err != nil {
		return false
	}
	cookie, err := signDSHCookie(secret, dshAuthority(port), time.Now(), dshCookieMaxAge)
	if err != nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()
	client := &http.Client{Timeout: 1200 * time.Millisecond}

	manifest, err := http.NewRequestWithContext(probeCtx, http.MethodGet, fmt.Sprintf("http://%s/manifest.webmanifest", dshAuthority(port)), nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(manifest)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "manifest") {
		return false
	}

	root, err := http.NewRequestWithContext(probeCtx, http.MethodGet, fmt.Sprintf("http://%s/", dshAuthority(port)), nil)
	if err != nil {
		return false
	}
	root.Header.Set("Cookie", cookie)
	resp, err = client.Do(root)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// discoverDSHListeners 扫描 loopback 监听端口，返回 home 认领到的 host。
//
// 多个端口并发探测，取端口号最小的匹配项，保证同一台机器重复调用结果稳定。
func discoverDSHListeners(ctx context.Context, home string) (dshEndpoint, error) {
	ports, err := dshLoopbackPorts()
	if err != nil {
		return dshEndpoint{}, fmt.Errorf("%w: 扫描本机监听端口失败: %v", errDSHHostUnavailable, err)
	}
	matched := make([]int, 0, 1)
	results := make(chan int, len(ports))
	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	for _, port := range ports {
		go func(port int) {
			if probeDSHHome(probeCtx, home, port) {
				results <- port
				return
			}
			results <- 0
		}(port)
	}
	for range ports {
		if port := <-results; port > 0 {
			matched = append(matched, port)
		}
	}
	if len(matched) == 0 {
		return dshEndpoint{}, fmt.Errorf("%w: 没有用 %s 的凭据认领到 DSH host", errDSHHostUnavailable, home)
	}
	sort.Ints(matched)
	return dshEndpoint{Port: matched[0]}, nil
}
