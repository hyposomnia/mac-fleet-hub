package main

import (
	"testing"
	"time"
)

// RFC 6238 测试向量（SHA1，secret = ASCII "12345678901234567890" → base32）。
// T=59s 的 8 位 TOTP 是 94287082，取后 6 位 = 287082。
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestVerifyTOTP_RFCVector(t *testing.T) {
	if !verifyTOTP(rfcSecret, "287082", 59) {
		t.Fatal("RFC 向量 287082@59s 应通过")
	}
	// T=1111111109 → 8 位 07081804 → 6 位 081804
	if !verifyTOTP(rfcSecret, "081804", 1111111109) {
		t.Fatal("RFC 向量 081804@1111111109s 应通过")
	}
}

func TestVerifyTOTP_Skew(t *testing.T) {
	// 287082 是 step=1(T=59→step=1) 的码；T 落在 step=2(60..89) 时应仍接受（±1 窗）。
	if !verifyTOTP(rfcSecret, "287082", 75) {
		t.Fatal("±1 时间窗内应接受上一窗的码")
	}
	// 但偏移到 step=3（90..119）就应拒绝
	if verifyTOTP(rfcSecret, "287082", 100) {
		t.Fatal("超出 ±1 窗应拒绝")
	}
}

// 核心安全修复：失败锁定必须 per-IP，刷错码的攻击者不能连带锁住机主。
func TestLockout_PerIP(t *testing.T) {
	ipStates = map[string]*ipState{} // 重置
	now := time.Unix(1700000000, 0)
	attacker, owner := "203.0.113.7", "198.51.100.9"

	for i := 0; i < maxFails; i++ {
		failIP(attacker, now)
	}
	if !lockedIP(attacker, now) {
		t.Fatal("攻击者 IP 连续失败后应被锁定")
	}
	if lockedIP(owner, now) {
		t.Fatal("机主 IP 不应被攻击者的失败连带锁定（per-IP 隔离）")
	}

	// 滑动衰减：超过 lockMin 后旧锁/计数过期，机主仍可入网
	later := now.Add(time.Duration(lockMin+1) * time.Minute)
	if lockedIP(attacker, later) {
		t.Fatal("超过锁定窗后应自动解锁")
	}
}

func TestLockout_SuccessClears(t *testing.T) {
	ipStates = map[string]*ipState{}
	now := time.Unix(1700000000, 0)
	ip := "203.0.113.50"
	failIP(ip, now)
	failIP(ip, now)
	okIP(ip) // 成功后清零
	for i := 0; i < maxFails-1; i++ {
		failIP(ip, now)
	}
	if lockedIP(ip, now) {
		t.Fatal("成功清零后，不足阈值的新失败不应锁定")
	}
}

func TestVerifyTOTP_Bad(t *testing.T) {
	if verifyTOTP(rfcSecret, "000000", 59) {
		t.Fatal("错误码应拒绝")
	}
	if verifyTOTP(rfcSecret, "12345", 59) {
		t.Fatal("非 6 位应拒绝")
	}
	if verifyTOTP("not-base32!!", "287082", 59) {
		t.Fatal("非法 secret 应拒绝")
	}
}
