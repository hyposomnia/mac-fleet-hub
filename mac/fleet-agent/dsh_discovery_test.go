package main

import (
	"os"
	"path/filepath"
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
	const (
		secret    = "test-secret-do-not-use"
		authority = "127.0.0.1:43129"
		want      = "dsh-auth-HI6DuTLbaVd4how-w57FxST3l-HEDuPslIWbs23FcR0=" +
			"v1.eyJ2ZXJzaW9uIjoxLCJhdXRob3JpdHkiOiIxMjcuMC4wLjE6NDMxMjkiLCJpc3N1ZWRBdCI6MTcwMDAwMDAwMDAwMCwiZXhwaXJlc0F0IjoxNzAwMDg2NDAwMDAwfQ" +
			".NBp35Zh7gW49rL0y-LIrp01lEiA8aEri8FkwhlxvKFA"
	)
	now := time.UnixMilli(1700000000000)

	got, err := signDSHCookie(secret, authority, now, 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 期望值由独立实现（node crypto）算出：body 明文的字段顺序也必须一致，
	// 因为 HMAC 覆盖的是那段 base64url 里的原始字节。
	if got != want {
		t.Fatalf("cookie = %q\nwant     %q", got, want)
	}
}

func TestSignDSHCookieRejectsBadTTL(t *testing.T) {
	now := time.UnixMilli(1700000000000)

	if _, err := signDSHCookie("s", "127.0.0.1:43129", now, dshCookieMaxAge+time.Second); err == nil {
		t.Fatal("ttl beyond the host-side 30d bound must be rejected")
	}
	if _, err := signDSHCookie("s", "127.0.0.1:43129", now, 0); err == nil {
		t.Fatal("zero ttl must be rejected")
	}
	if _, err := signDSHCookie("s", "127.0.0.1:43129", now, -time.Hour); err == nil {
		t.Fatal("negative ttl must be rejected")
	}
	if _, err := signDSHCookie("", "127.0.0.1:43129", now, time.Hour); err == nil {
		t.Fatal("empty secret must be rejected")
	}
	// 恰好 30 天是允许的（host 端只校验上界）。
	if _, err := signDSHCookie("s", "127.0.0.1:43129", now, dshCookieMaxAge); err != nil {
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
