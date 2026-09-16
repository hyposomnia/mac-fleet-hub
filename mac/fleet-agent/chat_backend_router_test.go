package main

import (
	"context"
	"errors"
	"testing"
)

// 装配守卫：agentChatBackend 必须同时满足 Codex 的会话目录三接口。
//
// /api/sessions、/api/sessions/action 与 cwdForSession 都是对 agentChatBackend 做类型
// 断言，而 newAgentChatBackend() 恒定返回 routingChatBackend。DSH 重构引入路由层时漏了
// 这三个转发，断言全部失败 → Codex 会话列表稳定 503 appserver_unavailable，但 chat 类
// 接口照常可用（所以聊天测试全绿也发现不了）。2026-09-16 全舰队实测复现。
//
// 这里断言真实装配点而不是手搭 router：漏实现时本测试失败，正是要拦的那种改动。
func TestAgentChatBackendSatisfiesCodexCatalogInterfaces(t *testing.T) {
	backend := newAgentChatBackend()
	if _, ok := backend.(codexThreadCatalog); !ok {
		t.Fatal("agentChatBackend 未实现 codexThreadCatalog：/api/sessions?assistant=codex 会稳定 503")
	}
	if _, ok := backend.(codexThreadManager); !ok {
		t.Fatal("agentChatBackend 未实现 codexThreadManager：/api/sessions/action 会稳定 503")
	}
	if _, ok := backend.(codexThreadReader); !ok {
		t.Fatal("agentChatBackend 未实现 codexThreadReader：cwdForSession 会返回 appserver_unavailable")
	}
}

// 未装配 Codex 后端时（router.codex == nil）三条路径都要给出稳定的
// errAppServerUnavailable，而不是 panic 或静默成功。
func TestRoutingBackendWithoutCodexReturnsUnavailable(t *testing.T) {
	router := &routingChatBackend{}
	ctx := context.Background()

	if _, err := router.ListThreads(ctx, codexThreadListOptions{}); !errors.Is(err, errAppServerUnavailable) {
		t.Fatalf("ListThreads err = %v, want errAppServerUnavailable", err)
	}
	if err := router.MutateThread(ctx, "session", "archive", ""); !errors.Is(err, errAppServerUnavailable) {
		t.Fatalf("MutateThread err = %v, want errAppServerUnavailable", err)
	}
	if _, err := router.ThreadCwd(ctx, "session"); !errors.Is(err, errAppServerUnavailable) {
		t.Fatalf("ThreadCwd err = %v, want errAppServerUnavailable", err)
	}
}
