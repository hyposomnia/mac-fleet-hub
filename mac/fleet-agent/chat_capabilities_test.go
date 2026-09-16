package main

import "testing"

// TestChatCapabilities 锁住"哪些 assistant 支持自绘聊天"这一唯一真源。
//
// 存在的理由：自绘链路原本靠 16 处硬编码 `assistant != "codex"` 守卫，加第三个
// assistant 时每一处都要改，且容易漏。能力化之后只在这里判定。
func TestChatCapabilities(t *testing.T) {
	all := assistantCapabilities{SelfDraw: true, Queue: true}
	none := assistantCapabilities{}

	cases := []struct {
		name      string
		assistant string
		want      assistantCapabilities
	}{
		{"codex 小写", "codex", all},
		{"codex 大写", "Codex", all},
		{"codex 带空格", "  codex  ", all},
		{"dsh 尚未接入，必须先为 false", "dsh", none},
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
