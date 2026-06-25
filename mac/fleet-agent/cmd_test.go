package main

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// F1：Bypass连接 = claude 带 --dangerously-skip-permissions。这是安全边界，
// 用单测钉死「普通连接不带、bypass 才带」与标志位置，避免回归。
func TestClaudeResumeCmd(t *testing.T) {
	cfg = Config{ClaudeBin: "claude"} // 代理默认关，proxyEnvPrefix() 为空

	normal := claudeResumeCmd("abc-123", false)
	want := "claude --resume 'abc-123'"
	if normal != want {
		t.Fatalf("normal resume: got %q want %q", normal, want)
	}

	bypass := claudeResumeCmd("abc-123", true)
	wantB := "claude --resume 'abc-123' --dangerously-skip-permissions"
	if bypass != wantB {
		t.Fatalf("bypass resume: got %q want %q", bypass, wantB)
	}
}

func TestClaudeNewCmd(t *testing.T) {
	cfg = Config{ClaudeBin: "claude"}

	if got := claudeNewCmd(false); got != "claude" {
		t.Fatalf("normal new: got %q want %q", got, "claude")
	}
	if got := claudeNewCmd(true); got != "claude --dangerously-skip-permissions" {
		t.Fatalf("bypass new: got %q want %q", got, "claude --dangerously-skip-permissions")
	}
}

// tmux -f <conf> 必须排在 new-session 之前，否则被当作 new-session 的参数而失效——
// conf 在 server 启动时加载 history-limit/mouse，错位则网页终端退回「只一屏、不能滚」。
// 钉死：① conf 非空时 -f 紧贴开头且在 new-session 前；② conf 为空时不带 -f。
func TestTmuxNewSessionArgs(t *testing.T) {
	args := tmuxNewSessionArgs("/home/u/.macfleet-tmux.conf", "fleet-abc", "cd '/x'; exec claude")
	if len(args) < 3 || args[0] != "-f" || args[1] != "/home/u/.macfleet-tmux.conf" || args[2] != "new-session" {
		t.Fatalf("conf 非空：-f 须在 new-session 前，got %v", args)
	}
	if args[len(args)-3] != "sh" || args[len(args)-2] != "-c" {
		t.Fatalf("尾部应是 sh -c <full>，got %v", args)
	}

	bare := tmuxNewSessionArgs("", "fleet-abc", "x")
	if bare[0] != "new-session" {
		t.Fatalf("conf 为空：不应带 -f，got %v", bare)
	}
}

// pty 耗尽判定是 open/new 500 → 精确提示的核心分支，钉死边界：
// 达上限才算耗尽；探测失败(max==0)绝不误判，否则会把所有 tmux 错误都谎报成 pty 耗尽。
func TestPtyExhausted(t *testing.T) {
	cases := []struct {
		used, max int
		want      bool
	}{
		{511, 511, true},  // 正好达上限
		{527, 511, true},  // 超上限（本次事故现场）
		{510, 511, false}, // 还差一个
		{0, 0, false},     // 两项都探测失败 → 不判耗尽
		{600, 0, false},   // 上限探测失败 → 不判耗尽
	}
	for _, c := range cases {
		if got := ptyExhausted(c.used, c.max); got != c.want {
			t.Errorf("ptyExhausted(%d,%d)=%v want %v", c.used, c.max, got, c.want)
		}
	}
}

// httpTmuxErr 按 pty 是否耗尽分流到 503/500，且各自带正确 error 类型与可读 message。
func TestHttpTmuxErr(t *testing.T) {
	orig := ptyUsage
	defer func() { ptyUsage = orig }()

	// 耗尽 → 503 + pty_exhausted，文案含 used/max
	ptyUsage = func() (int, int) { return 527, 511 }
	rec := httptest.NewRecorder()
	httpTmuxErr(rec, errors.New("create session failed"))
	if rec.Code != 503 {
		t.Fatalf("exhausted: code got %d want 503", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("exhausted: bad json: %v", err)
	}
	if body["error"] != "pty_exhausted" {
		t.Fatalf("exhausted: error got %q want pty_exhausted", body["error"])
	}
	if !strings.Contains(body["message"], "527/511") {
		t.Fatalf("exhausted: message missing 527/511: %q", body["message"])
	}

	// 未耗尽 → 500 + tmux_failed，回传原始 err 文本
	ptyUsage = func() (int, int) { return 10, 511 }
	rec = httptest.NewRecorder()
	httpTmuxErr(rec, errors.New("boom-xyz"))
	if rec.Code != 500 {
		t.Fatalf("normal: code got %d want 500", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("normal: bad json: %v", err)
	}
	if body["error"] != "tmux_failed" || !strings.Contains(body["message"], "boom-xyz") {
		t.Fatalf("normal: body=%+v", body)
	}
}

// tailWaiting 钉死「等你回答/授权」判据（实证：两个等待会话末条都是 assistant + stop_reason=tool_use）：
// 只看尾部最后一条可解析记录；assistant+tool_use=等待，end_turn / 工具已出结果 / 截断行都不算。
func TestTailWaiting(t *testing.T) {
	asstWait := `{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"AskUserQuestion"}]}}`
	asstDone := `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text"}]}}`
	userResult := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result"}]}}`
	attachment := `{"type":"attachment"}`

	cases := []struct {
		name string
		tail string
		want bool
	}{
		{"asking/permission 末条 tool_use", asstWait, true},
		{"轮次正常结束 end_turn", asstDone, false},
		{"工具已出结果 user.tool_result", userResult, false},
		{"末条是工具执行后的 attachment", asstWait + "\n" + attachment, false},
		{"多行尾部 + 末条等待 + 末尾换行", asstDone + "\n" + asstWait + "\n", true},
		{"尾部首行被截断也能跳过取下一条", "ble}\n" + asstWait, true},
		{"空", "", false},
	}
	for _, c := range cases {
		if got := tailWaiting([]byte(c.tail)); got != c.want {
			t.Errorf("%s: tailWaiting=%v want %v", c.name, got, c.want)
		}
	}
}
