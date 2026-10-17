package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseDSHWebLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want dshEndpoint
		ok   bool
	}{
		{
			// Desktop 自己的 harness.log 里就是这个形态（[stdout] 前缀 + 完整 URL）。
			name: "真实日志行",
			line: "[stdout] dsh web: http://127.0.0.1:43129/?token=abc-DEF_123",
			want: dshEndpoint{Port: 43129, Token: "abc-DEF_123"},
			ok:   true,
		},
		{
			name: "没有 [stdout] 前缀",
			line: "dsh web: http://127.0.0.1:3080/?token=xyz",
			want: dshEndpoint{Port: 3080, Token: "xyz"},
			ok:   true,
		},
		{
			// 进程 token 只在启动时打印一次；日志被轮转时仍应从 CPU 拿到端口，
			// 再走 .credentials.yaml 自签 cookie。
			name: "没有 token 也要拿到端口",
			line: "dsh web: http://127.0.0.1:43129/",
			want: dshEndpoint{Port: 43129},
			ok:   true,
		},
		{
			name: "无关的 endpoint 行",
			line: "[desktop] endpoint http://127.0.0.1:43129",
			want: dshEndpoint{},
			ok:   false,
		},
		{
			name: "URL 畸形",
			line: "dsh web: ://not a url",
			want: dshEndpoint{},
			ok:   false,
		},
		{
			name: "缺端口",
			line: "dsh web: http://127.0.0.1/?token=x",
			want: dshEndpoint{},
			ok:   false,
		},
		{name: "空行", line: "", want: dshEndpoint{}, ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseDSHWebLine(tc.line)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (line=%q)", ok, tc.ok, tc.line)
			}
			if got != tc.want {
				t.Fatalf("endpoint = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDSHEndpointFromLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harness.log")
	content := "" +
		"[desktop] starting\n" +
		"[stdout] dsh web: http://127.0.0.1:43129/?token=first\n" +
		"[harness-node] runtime node=v24\n" +
		"[stdout] dsh web: http://127.0.0.1:43129/?token=second\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := dshEndpointFromLog(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Desktop 重启后会追加新的 dsh web: 行，必须取最后一条（token 每进程不同）。
	if got.Token != "second" || got.Port != 43129 {
		t.Fatalf("endpoint = %+v, want port 43129 token second", got)
	}

	if _, err := dshEndpointFromLog(filepath.Join(dir, "missing.log")); err == nil {
		t.Fatal("missing log file should error")
	}

	noMatch := filepath.Join(dir, "nomatch.log")
	if err := os.WriteFile(noMatch, []byte("[desktop] nothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := dshEndpointFromLog(noMatch); err == nil {
		t.Fatal("log without a dsh web: line should error")
	}
}

func TestDSHCookieName(t *testing.T) {
	// 该值由独立实现（node crypto）算出，且与线上实测的 cookie 名逐字一致。
	const want = "dsh-auth-HI6DuTLbaVd4how-w57FxST3l-HEDuPslIWbs23FcR0"
	if got := dshCookieName("127.0.0.1:43129"); got != want {
		t.Fatalf("dshCookieName = %q, want %q", got, want)
	}
	// authority 参与哈希：换端口必须换 cookie 名，否则 host 端校验不过。
	if dshCookieName("127.0.0.1:43130") == want {
		t.Fatal("cookie name must depend on the authority")
	}
}

func TestSignDSHCookie(t *testing.T) {
	// host 端的密钥是 payload.secret 做 base64url 解码后的 32 字节，
	// 测试值就是 32 字节 "0123456789abcdef0123456789abcdef" 的 base64url。
	const (
		secret    = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"
		authority = "127.0.0.1:43129"
		want      = "dsh-auth-HI6DuTLbaVd4how-w57FxST3l-HEDuPslIWbs23FcR0=" +
			"v1.eyJ2ZXJzaW9uIjoxLCJhdXRob3JpdHkiOiIxMjcuMC4wLjE6NDMxMjkiLCJpc3N1ZWRBdCI6MTcwMDAwMDAwMDAwMCwiZXhwaXJlc0F0IjoxNzAwMDg2NDAwMDAwfQ" +
			".aW_5VblJqENiKYyOAiGp9pNB8BxTqgiU6zKWvbuq7kQ"
	)
	now := time.UnixMilli(1700000000000)

	got, err := signDSHCookie(secret, authority, now, 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 期望值由独立实现（node crypto）算出，并与真实 host 的 encodeCookie 对齐：
	// HMAC key 是解码后的 32 字节，HMAC 覆盖 body 的 base64url 字符串。
	if got != want {
		t.Fatalf("cookie = %q\nwant     %q", got, want)
	}
}

func TestSignDSHCookieRejectsBadTTL(t *testing.T) {
	now := time.UnixMilli(1700000000000)

	const secret = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"

	if _, err := signDSHCookie(secret, "127.0.0.1:43129", now, dshCookieMaxAge+time.Second); err == nil {
		t.Fatal("ttl beyond the host-side 30d bound must be rejected")
	}
	if _, err := signDSHCookie(secret, "127.0.0.1:43129", now, 0); err == nil {
		t.Fatal("zero ttl must be rejected")
	}
	if _, err := signDSHCookie(secret, "127.0.0.1:43129", now, -time.Hour); err == nil {
		t.Fatal("negative ttl must be rejected")
	}
	if _, err := signDSHCookie("", "127.0.0.1:43129", now, time.Hour); err == nil {
		t.Fatal("empty secret must be rejected")
	}
	// 恰好 30 天是允许的（host 端只校验上界）。
	if _, err := signDSHCookie(secret, "127.0.0.1:43129", now, dshCookieMaxAge); err != nil {
		t.Fatalf("exactly max age should be allowed, got %v", err)
	}
}

func TestParseCredentialsSecret(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
		ok   bool
	}{
		{
			name: "真实结构",
			data: "version: 1\n" +
				"records:\n" +
				"  client-connection/browser-session:\n" +
				"    kind: grant\n" +
				"    payload:\n" +
				"      version: 1\n" +
				"      secret: test-secret-do-not-use\n",
			want: "test-secret-do-not-use",
			ok:   true,
		},
		{
			name: "带引号的值",
			data: "version: 1\nrecords:\n  client-connection/browser-session:\n" +
				"    payload:\n      secret: \"quoted-secret\"\n",
			want: "quoted-secret",
			ok:   true,
		},
		{
			name: "没有 browser-session 记录",
			data: "version: 1\nrecords: {}\n",
			want: "",
			ok:   false,
		},
		{
			name: "有记录但缺 secret",
			data: "version: 1\nrecords:\n  client-connection/browser-session:\n    payload:\n      version: 1\n",
			want: "",
			ok:   false,
		},
		{name: "空文件", data: "", want: "", ok: false},
		{
			// 同名 key 出现在别的记录里时不能被误取。
			name: "只在别的记录里有 secret",
			data: "version: 1\nrecords:\n  other/thing:\n    payload:\n      secret: wrong\n",
			want: "",
			ok:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseCredentialsSecret([]byte(tc.data))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tc.ok, got)
			}
			if got != tc.want {
				t.Fatalf("secret = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDSHExchangeToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "good-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("dsh web authentication required; reopen the URL printed by dsh web."))
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: "dsh-auth-abc", Value: "v1.body.sig", Path: "/",
			HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 2592000,
		})
		w.Header().Set("Location", "/")
		w.WriteHeader(http.StatusSeeOther)
	}))
	defer srv.Close()

	ctx := context.Background()
	got, err := dshExchangeToken(ctx, srv.URL, "good-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "dsh-auth-abc=v1.body.sig" {
		t.Fatalf("cookie = %q, want dsh-auth-abc=v1.body.sig", got)
	}

	// 错误 token 是 401，必须报 errDSHAuthFailed 而不是把 401 页面当成功。
	if _, err := dshExchangeToken(ctx, srv.URL, "bad-token"); !errors.Is(err, errDSHAuthFailed) {
		t.Fatalf("err = %v, want errDSHAuthFailed", err)
	}
}

func TestDSHExchangeTokenRequiresToken(t *testing.T) {
	if _, err := dshExchangeToken(context.Background(), "http://127.0.0.1:1", ""); err == nil {
		t.Fatal("空 token 不该发起请求")
	}
}

func TestDSHCookieSecretFromHome(t *testing.T) {
	home := t.TempDir()
	if _, err := dshCookieSecret(home); err == nil {
		t.Fatal("missing .credentials.yaml should error")
	}

	data := "version: 1\nrecords:\n  client-connection/browser-session:\n" +
		"    kind: grant\n    payload:\n      version: 1\n      secret: home-secret\n"
	if err := os.WriteFile(filepath.Join(home, ".credentials.yaml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := dshCookieSecret(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "home-secret" {
		t.Fatalf("secret = %q, want home-secret", got)
	}
}

// 测试用 32 字节密钥（base64url）：与 host 端要求一致，不能是任意字符串。
const dshTestSecret = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"

func writeDSHCredentials(t *testing.T, home, secret string) {
	t.Helper()
	data := "version: 1\nrecords:\n  client-connection/browser-session:\n" +
		"    kind: grant\n    payload:\n      version: 1\n      secret: " + secret + "\n"
	if err := os.WriteFile(filepath.Join(home, ".credentials.yaml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func listenerPort(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// 端口发现必须用 home 的凭据认领 host：两个 host 都挂着 manifest，
// 只有用对应 secret 签出 cookie 才能拿到 200 的那个才是本 home 的。
func TestDiscoverDSHListenersClaimsMatchingHome(t *testing.T) {
	home := t.TempDir()
	writeDSHCredentials(t, home, dshTestSecret)

	mine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.webmanifest":
			w.Header().Set("Content-Type", "application/manifest+json")
			_, _ = w.Write([]byte(`{"name":"DeepSeek Harness"}`))
		case "/":
			if strings.HasPrefix(r.Header.Get("Cookie"), "dsh-auth-") {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte("<html></html>"))
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mine.Close()

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.webmanifest":
			w.Header().Set("Content-Type", "application/manifest+json")
			w.WriteHeader(http.StatusOK)
		default:
			// 别的 home 的 host 会用别的 secret 验签，cookie 不匹配就是 401。
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer other.Close()

	prev := dshLoopbackPorts
	dshLoopbackPorts = func() ([]int, error) {
		return []int{listenerPort(t, other.URL), listenerPort(t, mine.URL)}, nil
	}
	t.Cleanup(func() { dshLoopbackPorts = prev })

	got, err := discoverDSHListeners(context.Background(), home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Port != listenerPort(t, mine.URL) {
		t.Fatalf("endpoint port = %d, want %d", got.Port, listenerPort(t, mine.URL))
	}

	// 没有凭据的 home 不能认领任何 host（否则等于无鉴权）。
	emptyHome := t.TempDir()
	if _, err := discoverDSHListeners(context.Background(), emptyHome); err == nil {
		t.Fatal("home without credentials must not claim a host")
	}

	// 完全不匹配时给出可读错误。
	dshLoopbackPorts = func() ([]int, error) { return []int{listenerPort(t, other.URL)}, nil }
	if _, err := discoverDSHListeners(context.Background(), home); err == nil {
		t.Fatal("no matching listener should error")
	}
}

// 端点优先级：显式覆盖 > 日志 > 端口发现。
func TestDiscoverDSHEndpointPrecedence(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "harness.log")
	if err := os.WriteFile(logPath, []byte("dsh web: http://127.0.0.1:43129/?token=log-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := dshLoopbackPorts
	dshLoopbackPorts = func() ([]int, error) {
		t.Fatal("已有日志时不应扫描端口")
		return nil, nil
	}
	t.Cleanup(func() { dshLoopbackPorts = prev })

	got, err := discoverDSHEndpoint(context.Background(), dshHarnessSpec{
		assistant: "deepseek", home: dir, logPath: logPath, autoDiscover: true,
	})
	if err != nil || got.Port != 43129 || got.Token != "log-token" {
		t.Fatalf("log endpoint = %+v, err = %v", got, err)
	}

	got, err = discoverDSHEndpoint(context.Background(), dshHarnessSpec{
		assistant: "deepseek", endpointOverride: "127.0.0.1:9999", autoDiscover: true,
	})
	if err != nil || got.Port != 9999 {
		t.Fatalf("override endpoint = %+v, err = %v", got, err)
	}
}

func TestDeepSeekInstalled(t *testing.T) {
	home := t.TempDir()
	if !deepSeekInstalled(home) {
		t.Fatal("existing directory should count as installed")
	}
	if deepSeekInstalled(filepath.Join(home, "missing")) {
		t.Fatal("missing directory must not count as installed")
	}
	if deepSeekInstalled("") {
		t.Fatal("empty home must not count as installed")
	}
}
