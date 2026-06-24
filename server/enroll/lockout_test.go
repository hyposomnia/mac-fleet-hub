package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 防爆破锁定必须按来源 IP 分桶：某个 IP 连错被锁，不能波及其他 IP，
// 否则任意匿名调用者只需发几个错误码即可把整个入网端点锁死（自伤式 DoS）。
func TestHandleJoin_LockoutIsPerIP(t *testing.T) {
	// 用 RFC 向量作 secret，把 secretFile 指向临时文件，使 loadSecret 成功、
	// 从而能走到 onFail 计数逻辑（错误码必走 401）。
	dir := t.TempDir()
	sf := filepath.Join(dir, "totp.secret")
	if err := os.WriteFile(sf, []byte(rfcSecret), 0600); err != nil {
		t.Fatal(err)
	}
	old := secretFile
	secretFile = sf
	t.Cleanup(func() { secretFile = old })

	post := func(ip, code string) int {
		body := strings.NewReader(`{"code":"` + code + `","index":"1"}`)
		r := httptest.NewRequest(http.MethodPost, "/join", body)
		r.Header.Set("X-Forwarded-For", ip)
		w := httptest.NewRecorder()
		handleJoin(w, r)
		return w.Code
	}

	// IP-A 连错 maxFails 次：每次都应返回 401（锁定检查在计数之前）。
	for i := 0; i < maxFails; i++ {
		if got := post("10.0.0.1", "000000"); got != 401 {
			t.Fatalf("IP-A 第 %d 次错误码应得 401，实得 %d", i+1, got)
		}
	}
	// 越过阈值后，IP-A 再来应被锁定 429。
	if got := post("10.0.0.1", "000000"); got != 429 {
		t.Fatalf("IP-A 超过阈值应锁定 429，实得 %d", got)
	}
	// 关键断言：IP-B 不应受 IP-A 锁定波及，仍应是验证码错误 401，而非 429。
	if got := post("10.0.0.2", "000000"); got != 401 {
		t.Fatalf("IP-B 不应被 IP-A 的锁定波及，应得 401，实得 %d", got)
	}
}
