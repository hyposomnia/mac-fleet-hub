package main

import (
	"context"
	"encoding/json"
)

// routingChatBackend 按 assistant 把自绘聊天调用分派给各自的后端。
//
// 在加 DSH 之前，agentChatBackend 就是一个 Codex 后端，每个入口自己判断
// `assistant != "codex"` 再返回"不支持"。有了第二个自绘 assistant 之后，
// 分流必须在唯一的装配点发生，否则每个后端都要重复一遍不属于它的路由知识。
type routingChatBackend struct {
	codex *codexChatBackend
	dsh   *dshChatBackend // nil = 该机器未启用 DSH
}

// pick 返回某个 assistant 的后端；未启用的 assistant 落到不可用后端，
// 由它给出稳定的 errAppServerUnavailable，而不是 panic 或静默成功。
func (r *routingChatBackend) pick(assistant string) chatBackend {
	switch normAssistant(assistant) {
	case "dsh":
		if r.dsh != nil {
			return r.dsh
		}
		return unavailableChatBackend{}
	default:
		if r.codex != nil {
			return r.codex
		}
		return unavailableChatBackend{}
	}
}

func (r *routingChatBackend) Start(ctx context.Context, assistant, cwd, mode string) (ChatStartResult, error) {
	return r.pick(assistant).Start(ctx, assistant, cwd, mode)
}

func (r *routingChatBackend) Resume(ctx context.Context, assistant, sessionID, mode string) (ChatResumeResult, error) {
	return r.pick(assistant).Resume(ctx, assistant, sessionID, mode)
}

func (r *routingChatBackend) History(ctx context.Context, assistant, sessionID, cursor string) (ChatHistoryPage, error) {
	return r.pick(assistant).History(ctx, assistant, sessionID, cursor)
}

func (r *routingChatBackend) Skills(ctx context.Context, assistant, cwd string) ([]ChatSkill, error) {
	return r.pick(assistant).Skills(ctx, assistant, cwd)
}

func (r *routingChatBackend) Input(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment, skills []ChatSkill, opts ChatTurnOptions) (ChatInputResult, error) {
	return r.pick(assistant).Input(ctx, assistant, sessionID, text, images, skills, opts)
}

func (r *routingChatBackend) Steer(ctx context.Context, assistant, sessionID, clientMessageID, text string, images []ChatAttachment, skills []ChatSkill) (ChatInputResult, error) {
	return r.pick(assistant).Steer(ctx, assistant, sessionID, clientMessageID, text, images, skills)
}

func (r *routingChatBackend) Events(ctx context.Context, assistant, sessionID string) (<-chan ChatEvent, error) {
	return r.pick(assistant).Events(ctx, assistant, sessionID)
}

func (r *routingChatBackend) Respond(ctx context.Context, assistant, sessionID, requestID string, response json.RawMessage) error {
	return r.pick(assistant).Respond(ctx, assistant, sessionID, requestID, response)
}

func (r *routingChatBackend) Interrupt(ctx context.Context, assistant, sessionID string) error {
	return r.pick(assistant).Interrupt(ctx, assistant, sessionID)
}

func (r *routingChatBackend) Release(ctx context.Context, assistant, sessionID string) error {
	return r.pick(assistant).Release(ctx, assistant, sessionID)
}

func (r *routingChatBackend) Settings(ctx context.Context, assistant, sessionID, approvalMode string) error {
	return r.pick(assistant).Settings(ctx, assistant, sessionID, approvalMode)
}

func (r *routingChatBackend) Control(ctx context.Context, assistant, sessionID string) (ChatRuntimeState, error) {
	return r.pick(assistant).Control(ctx, assistant, sessionID)
}

// codexBackend 暴露 Codex 后端给 /api/info 这类需要读它内部状态的地方。
func (r *routingChatBackend) codexBackend() *codexChatBackend {
	if r == nil {
		return nil
	}
	return r.codex
}

// 下面三个方法转发 Codex 的**会话目录**能力（/api/sessions、/api/sessions/action
// 与 cwdForSession 项目解析）。它们必须由路由层显式实现，因为那三处调用点是对
// agentChatBackend 做类型断言（codexThreadCatalog / codexThreadManager /
// codexThreadReader），而装配点 newAgentChatBackend() 现在恒定返回 routingChatBackend：
// 少转发一个，整条 Codex 会话列表就稳定返回 appserver_unavailable，而 chat 类接口
// （Start/Resume/History/Skills…）照常可用，所以只看聊天功能不会发现。
//
// 2026-09-16 实测就是这个形态：四台 Mac 上 /api/sessions?assistant=claude 返回 200、
// assistant=codex 返回 503，dashboard 的 Codex 会话列表与项目分组全挂。
// 守卫见 TestAgentChatBackendSatisfiesCodexCatalogInterfaces。
func (r *routingChatBackend) ListThreads(ctx context.Context, opts codexThreadListOptions) (codexThreadPage, error) {
	if r == nil || r.codex == nil {
		return codexThreadPage{}, errAppServerUnavailable
	}
	return r.codex.ListThreads(ctx, opts)
}

func (r *routingChatBackend) MutateThread(ctx context.Context, sessionID, action, value string) error {
	if r == nil || r.codex == nil {
		return errAppServerUnavailable
	}
	return r.codex.MutateThread(ctx, sessionID, action, value)
}

func (r *routingChatBackend) ThreadCwd(ctx context.Context, sessionID string) (string, error) {
	if r == nil || r.codex == nil {
		return "", errAppServerUnavailable
	}
	return r.codex.ThreadCwd(ctx, sessionID)
}

func (r *routingChatBackend) Subagents(ctx context.Context, assistant, sessionID string) (ChatSubagentPage, error) {
	if r == nil || r.codex == nil || normAssistant(assistant) != "codex" {
		return ChatSubagentPage{}, errAppServerUnavailable
	}
	return r.codex.Subagents(ctx, assistant, sessionID)
}

// dshBackend 暴露 DSH 后端（可能为 nil）。
func (r *routingChatBackend) dshBackend() *dshChatBackend {
	if r == nil {
		return nil
	}
	return r.dsh
}
