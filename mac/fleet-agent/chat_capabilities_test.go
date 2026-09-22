package main

import "testing"

// TestChatCapabilities 锁住"哪些 assistant 支持自绘聊天"这一唯一真源。
//
// 存在的理由：自绘链路原本靠 16 处硬编码 `assistant != "codex"` 守卫，加第三个
// assistant 时每一处都要改，且容易漏。能力化之后只在这里判定。
func TestChatCapabilities(t *testing.T) {
	codexAll := assistantCapabilities{SelfDraw: true, Queue: true, ApprovalModes: true}
	// dsh 有自绘面与队列，但没有会话级权限预设：权限由 host 自己的设置决定。
	dshAll := assistantCapabilities{SelfDraw: true, Queue: true}
	none := assistantCapabilities{}

	// dsh 的能力受 FLEET_DSH_ENABLED 控制：关着的时候必须是 false，
	// 否则未启用的机器上会冒出一个点了没反应的入口。
	prev := cfg.DSHEnabled
	t.Cleanup(func() { cfg.DSHEnabled = prev })

	cfg.DSHEnabled = true
	dshEnabled := chatCapabilities("dsh")
	if dshEnabled != dshAll {
		t.Fatalf("启用时 chatCapabilities(dsh) = %+v, want %+v", dshEnabled, dshAll)
	}
	if dshEnabled.ApprovalModes {
		t.Fatal("DSH 不得声明 ApprovalModes：Settings 明确返回 errDSHUnsupported")
	}
	if got := chatCapabilities("DSH"); got != dshAll {
		t.Fatalf("大小写不敏感失败: %+v", got)
	}

	cfg.DSHEnabled = false
	if got := chatCapabilities("dsh"); got != none {
		t.Fatalf("未启用时 chatCapabilities(dsh) = %+v, want 空能力", got)
	}

	cases := []struct {
		name      string
		assistant string
		want      assistantCapabilities
	}{
		{"codex 小写", "codex", codexAll},
		{"codex 大写", "Codex", codexAll},
		{"codex 带空格", "  codex  ", codexAll},
		{"claude 仍走终端，不得自绘", "claude", none},
		{"claude 大写", "Claude", none},
		{"空串回退 claude", "", none},
		{"未知 assistant", "gemini", none},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := chatCapabilities(tc.assistant)
			if got != tc.want {
				t.Fatalf("chatCapabilities(%q) = %+v, want %+v", tc.assistant, got, tc.want)
			}
		})
	}
}
