package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// errDSHUnsupported 表示 DSH 侧 v1 尚未实现的写入面。
//
// 刻意报错而不是返回空成功：技能目录与权限预设的形状还需要在真实 host 上验证，
// 猜着实现会静默改用户的 DSH 配置或给出空的技能列表——两者都比明确的"暂不支持"更糟。
var errDSHUnsupported = errors.New("dsh_unsupported")

// dshBacklogLimit 限制没有订阅者时缓存的实时事件数，避免长会话把内存吃光。
const dshBacklogLimit = 512

// 关于 follow 快照的量级（实测，别凭直觉"优化"）：
//
// 不传 maxMessages 时 host 返回的尾部本来就是有界的——真实长会话实测 639 条记录 /
// 868 KB 帧。曾经试过显式传 maxMessages=200 "限制大小"，结果反而变成 1787 条记录 /
// 2271 KB（该参数不是简单的上界，语义与 records 数非线性）。所以这里保持不传，
// 让 host 用它自己的默认值。展开后的首页事件量（约 2.7 万条）与 session/page 的
// 向后分页都已实测可用，cursor 就是记录里最早的 seq。

// dshChatBackend 是 Fleet 侧的 DSH 客户端后端。
//
// shared 模式：它挂到 DSH Desktop 正在运行的 harness host 上，由 host 持有唯一的
// 持久化写入方。因此这里**没有** writer 租约，也绝不自己启动 DSH 进程。
type dshChatBackend struct {
	home             string
	logPath          string
	endpointOverride string

	connMu   sync.Mutex
	client   *dshClient
	endpoint dshEndpoint
	authMode dshAuthMode
	lastErr  error
	hostSeen bool

	eventMu       sync.Mutex
	waterfall     *dshStream
	clientID      string
	stopWaterfall context.CancelFunc

	sessMu   sync.Mutex
	sessions map[string]*dshSession

	subMu   sync.Mutex
	subs    map[string]map[chan ChatEvent]struct{}
	backlog map[string][]ChatEvent
}

// dshSession 是一个被订阅过的 DSH 会话的本地投影。
type dshSession struct {
	id      string
	cwd     string
	history []dshHistoryRecord
	hasMore bool
	cursor  int64
	oldest  int64

	running    bool
	activeTurn string

	// 该会话最近一次请求用的模型与思考档位（来自 request/header、request/context）。
	// dashboard 那条"模型使用数据"要显示它，而事件本身只带 usage 不带模型。
	model  string
	effort string

	stopFollow context.CancelFunc
	started    bool
}

func newDSHChatBackend(home, logPath, endpointOverride string) *dshChatBackend {
	return &dshChatBackend{
		home:             home,
		logPath:          logPath,
		endpointOverride: endpointOverride,
		sessions:         map[string]*dshSession{},
		subs:             map[string]map[chan ChatEvent]struct{}{},
		backlog:          map[string][]ChatEvent{},
	}
}

// ensure 返回可用的客户端，必要时（重新）建立连接。
//
// 不在这里做后台重连：DSH Desktop 重启会换端口与 token，恢复动作属于每一次调用，
// 由调用方在拿到 errDSHHostUnavailable 后自然触发重新发现。
func (b *dshChatBackend) ensure(ctx context.Context) (*dshClient, error) {
	b.connMu.Lock()
	if b.client != nil {
		client := b.client
		b.connMu.Unlock()
		return client, nil
	}
	b.connMu.Unlock()

	client, endpoint, mode, err := connectDSH(ctx, b.home, b.logPath, b.endpointOverride)
	if err != nil {
		b.connMu.Lock()
		b.lastErr = err
		b.connMu.Unlock()
		return nil, err
	}

	b.connMu.Lock()
	b.client = client
	b.endpoint = endpoint
	b.authMode = mode
	b.lastErr = nil
	b.hostSeen = true
	b.connMu.Unlock()
	return client, nil
}

// invalidate 丢弃当前连接，让下一次调用重新发现端点与凭据。
func (b *dshChatBackend) invalidate() {
	b.connMu.Lock()
	client := b.client
	b.client = nil
	b.connMu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

func (b *dshChatBackend) session(sessionID string) *dshSession {
	b.sessMu.Lock()
	defer b.sessMu.Unlock()
	s := b.sessions[sessionID]
	if s == nil {
		s = &dshSession{id: sessionID}
		b.sessions[sessionID] = s
	}
	return s
}

// ---------------- chatBackend 实现 ----------------

func (b *dshChatBackend) Start(ctx context.Context, assistant, cwd, mode string) (ChatStartResult, error) {
	client, err := b.ensure(ctx)
	if err != nil {
		return ChatStartResult{}, err
	}
	value, err := client.call(ctx, "session/create", map[string]any{
		"request": map[string]any{"cwd": cwd},
	})
	if err != nil {
		return ChatStartResult{}, b.translateCallError(err)
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(value, &created); err != nil || created.SessionID == "" {
		return ChatStartResult{}, fmt.Errorf("%w: session/create 未返回 sessionId", errDSHProtocolChanged)
	}

	s := b.session(created.SessionID)
	s.cwd = cwd
	b.startFollow(ctx, s)

	return ChatStartResult{SessionID: created.SessionID, Cwd: cwd}, nil
}

func (b *dshChatBackend) Resume(ctx context.Context, assistant, sessionID, mode string) (ChatResumeResult, error) {
	client, err := b.ensure(ctx)
	if err != nil {
		return ChatResumeResult{}, err
	}
	s := b.session(sessionID)
	history, err := b.loadSnapshot(ctx, client, s)
	if err != nil {
		return ChatResumeResult{}, err
	}
	b.startFollow(ctx, s)

	b.sessMu.Lock()
	running, activeTurn := s.running, s.activeTurn
	b.sessMu.Unlock()

	status := "idle"
	if running {
		status = "active"
	}
	model, effort := b.sessionModel(s)
	return ChatResumeResult{
		Model:        model,
		Effort:       effort,
		SessionID:    sessionID,
		ThreadID:     sessionID,
		Status:       status,
		ActiveTurnID: activeTurn,
		AccessMode:   chatAccessReadWrite,
		History:      history,
	}, nil
}

func (b *dshChatBackend) History(ctx context.Context, assistant, sessionID, cursor string) (ChatHistoryPage, error) {
	client, err := b.ensure(ctx)
	if err != nil {
		return ChatHistoryPage{}, err
	}
	s := b.session(sessionID)

	// 空 cursor = 第一页：直接用 follow 快照，避免为了首屏再多打一次 RPC。
	if strings.TrimSpace(cursor) == "" {
		return b.loadSnapshot(ctx, client, s)
	}

	throughSeq, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil {
		return ChatHistoryPage{}, fmt.Errorf("非法的 DSH 历史游标: %q", cursor)
	}
	value, err := client.call(ctx, "session/page", map[string]any{
		"request": map[string]any{
			"address":    dshSessionAddress(sessionID),
			"throughSeq": throughSeq,
		},
	})
	if err != nil {
		return ChatHistoryPage{}, b.translateCallError(err)
	}
	return dshHistoryPageFromRecords(sessionID, value)
}

func (b *dshChatBackend) Skills(ctx context.Context, assistant, cwd string) ([]ChatSkill, error) {
	// skills/list 是 stream 且请求形状尚未在真机验证。宁可明确报不支持，
	// 也不要返回空列表让用户以为"这台机器没有技能"。
	return nil, errDSHUnsupported
}

func (b *dshChatBackend) Input(ctx context.Context, assistant, sessionID, text string, images []ChatAttachment, skills []ChatSkill, opts ChatTurnOptions) (ChatInputResult, error) {
	// 交付模式由上层队列决定：普通输入一律走 queue（空闲时 host 会在轮次边界立刻领取，
	// 忙碌时排队到下一轮），steer 走 Steer 方法。
	return b.prompt(ctx, sessionID, text, images, opts.ClientUserMessageID, "queue")
}

func (b *dshChatBackend) Steer(ctx context.Context, assistant, sessionID, clientMessageID, text string, images []ChatAttachment, skills []ChatSkill) (ChatInputResult, error) {
	return b.prompt(ctx, sessionID, text, images, clientMessageID, "steer")
}

func (b *dshChatBackend) prompt(ctx context.Context, sessionID, text string, images []ChatAttachment, clientMessageID, mode string) (ChatInputResult, error) {
	client, err := b.ensure(ctx)
	if err != nil {
		return ChatInputResult{}, err
	}
	content := make([]map[string]any, 0, len(images)+1)
	if text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, attachment := range images {
		part, err := dshAttachmentPart(attachment)
		if err != nil {
			return ChatInputResult{}, err
		}
		content = append(content, part)
	}
	if len(content) == 0 {
		return ChatInputResult{}, errors.New("空消息：既没有文本也没有可发送的附件")
	}

	requestID := strings.TrimSpace(clientMessageID)
	if requestID == "" {
		requestID = newDSHID()
	}
	_, err = client.call(ctx, "session/prompt", map[string]any{
		"request": map[string]any{
			"requestId": requestID,
			"sessionId": sessionID,
			"mode":      mode,
			"content":   content,
		},
	})
	if err != nil {
		return ChatInputResult{}, b.translateCallError(err)
	}
	return ChatInputResult{}, nil
}

func (b *dshChatBackend) Events(ctx context.Context, assistant, sessionID string) (<-chan ChatEvent, error) {
	client, err := b.ensure(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := b.loadSnapshot(ctx, client, b.session(sessionID)); err != nil {
		return nil, err
	}
	b.startFollow(ctx, b.session(sessionID))

	b.subMu.Lock()
	backlog := b.backlog[sessionID]
	delete(b.backlog, sessionID)
	ch := make(chan ChatEvent, len(backlog)+64)
	for _, ev := range backlog {
		ch <- ev
	}
	if b.subs[sessionID] == nil {
		b.subs[sessionID] = map[chan ChatEvent]struct{}{}
	}
	b.subs[sessionID][ch] = struct{}{}
	b.subMu.Unlock()

	go func() {
		<-ctx.Done()
		b.subMu.Lock()
		if subs := b.subs[sessionID]; subs != nil {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(b.subs, sessionID)
			}
		}
		close(ch)
		b.subMu.Unlock()
	}()
	return ch, nil
}

func (b *dshChatBackend) Respond(ctx context.Context, assistant, sessionID, requestID string, response json.RawMessage) error {
	client, err := b.ensure(ctx)
	if err != nil {
		return err
	}
	eventID := strings.TrimSpace(requestID)
	clientID, err := b.ensureWaterfall(ctx, client)
	if err != nil {
		return err
	}
	outcome, err := dshApprovalOutcome(response)
	if err != nil {
		return err
	}
	_, err = client.call(ctx, "$events/result", map[string]any{
		"clientId": clientID,
		"eventId":  eventID,
		"outcome":  outcome,
	})
	if err != nil {
		return b.translateCallError(err)
	}
	return nil
}

func (b *dshChatBackend) Interrupt(ctx context.Context, assistant, sessionID string) error {
	client, err := b.ensure(ctx)
	if err != nil {
		return err
	}
	// host 内部用 keepInbox: true：只中止当前轮次，保留排队项，与 Fleet 既有语义一致。
	_, err = client.call(ctx, "session/cancel", map[string]any{
		"request": map[string]any{"sessionId": sessionID},
	})
	if err != nil {
		return b.translateCallError(err)
	}
	return nil
}

func (b *dshChatBackend) Release(ctx context.Context, assistant, sessionID string) error {
	// DSH 没有 writer 租约：一个 host 进程内每个会话只有一个 Agent，持久化写入方
	// 天然唯一。Fleet 侧无需（也无法）释放任何东西。
	return nil
}

func (b *dshChatBackend) Settings(ctx context.Context, assistant, sessionID, approvalMode string) error {
	// 权限预设要经 settings/mutate 写入 host 的 permission 命名空间，会改动用户的
	// 真实 DSH 配置；该路径的形状（preset 取值、revision 语义）尚未在真机验证，
	// 因此 v1 明确拒绝而不是猜。
	return errDSHUnsupported
}

func (b *dshChatBackend) Control(ctx context.Context, assistant, sessionID string) (ChatRuntimeState, error) {
	// WriterOwner 与本地是否跟踪过该会话无关：DSH 的写入方永远在 host 侧，
	// 一个 host 进程内每个会话只有一个 Agent。TurnOwner 留空表示没有排他归属——
	// DSH 的会话 inbox 是有序的，浏览器的 steer 与排队都安全。
	state := ChatRuntimeState{Status: "idle", WriterOwner: "dsh-host"}

	b.sessMu.Lock()
	defer b.sessMu.Unlock()
	s := b.sessions[sessionID]
	if s != nil && s.running {
		state.Status = "active"
		state.ActiveTurnID = s.activeTurn
	}
	return state, nil
}

// ---------------- 连接与流 ----------------

// ensureWaterfall 保证全局 $events 流在线，并返回 host 分配的 clientId。
//
// 审批与提问的回答案必须带上这个 clientId，所以它在整个进程生命周期内保持有效。
func (b *dshChatBackend) ensureWaterfall(ctx context.Context, client *dshClient) (string, error) {
	b.eventMu.Lock()
	if b.clientID != "" && b.waterfall != nil {
		id := b.clientID
		b.eventMu.Unlock()
		return id, nil
	}
	b.eventMu.Unlock()

	stream, err := client.openStream(ctx, "$events", nil)
	if err != nil {
		b.invalidate()
		return "", err
	}
	raw, err := stream.Recv(ctx)
	if err != nil {
		_ = stream.Close()
		return "", err
	}
	var ready struct {
		Type     string `json:"type"`
		ClientID string `json:"clientId"`
	}
	if json.Unmarshal(raw, &ready) != nil || ready.Type != "ready" || ready.ClientID == "" {
		_ = stream.Close()
		return "", fmt.Errorf("%w: $events 首帧不是 ready", errDSHProtocolChanged)
	}

	readCtx, cancel := context.WithCancel(context.Background())
	b.eventMu.Lock()
	b.waterfall = stream
	b.clientID = ready.ClientID
	b.stopWaterfall = cancel
	b.eventMu.Unlock()

	go b.runWaterfall(readCtx, stream)
	return ready.ClientID, nil
}

// runWaterfall 处理审批/提问的 waterfall 广播。
func (b *dshChatBackend) runWaterfall(ctx context.Context, stream *dshStream) {
	for {
		raw, err := stream.Recv(ctx)
		if err != nil {
			b.eventMu.Lock()
			if b.waterfall == stream {
				b.waterfall = nil
				b.clientID = ""
			}
			b.eventMu.Unlock()
			return
		}
		var frame struct {
			Type    string          `json:"type"`
			EventID string          `json:"eventId"`
			Event   string          `json:"event"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(raw, &frame) != nil {
			continue
		}
		switch frame.Type {
		case "approval/request", "user-questions/request":
			b.publishInteraction(frame.Type, frame.EventID, frame.Payload)
		case "cancel":
			// 另一个客户端（例如 Desktop GUI）先答了：必须把本地待答卡片收掉，
			// 否则用户会对着一个已经失效的审批按钮点。
			b.publish(frameSessionID(frame.Payload), []ChatEvent{
				newChatEvent("interaction_resolved", "dsh", frameSessionID(frame.Payload), "", frame.EventID,
					map[string]any{"requestId": frame.EventID, "response": map[string]any{"decision": "cancelled"}}),
			}...)
		}
	}
}

func (b *dshChatBackend) publishInteraction(kind, eventID string, payload json.RawMessage) {
	var data map[string]any
	_ = json.Unmarshal(payload, &data)
	if data == nil {
		data = map[string]any{}
	}
	sessionID, _ := data["sessionId"].(string)
	requestMethod := "item/commandExecution/requestApproval"
	eventType := "interaction_request"
	if kind == "user-questions/request" {
		requestMethod = "item/tool/requestUserInput"
	}
	body := map[string]any{
		"requestId":     eventID,
		"requestMethod": requestMethod,
		// DSH 的审批 outcome 只有 allowed-once / rejected：没有会话级授权，
		// 所以只允许"允许一次 / 拒绝"两个决定。
		"allowedDecisions": []string{"allow_once", "decline"},
		"raw":              data,
	}
	if questions, ok := data["questions"]; ok {
		body["questions"] = questions
	}
	if message, ok := data["message"].(string); ok {
		body["message"] = message
	}
	b.publish(sessionID, newChatEvent(eventType, "dsh", sessionID, "", eventID, body))
}

// startFollow 启动会话的实时订阅协程（幂等）。
func (b *dshChatBackend) startFollow(ctx context.Context, s *dshSession) {
	b.sessMu.Lock()
	if s.started {
		b.sessMu.Unlock()
		return
	}
	s.started = true
	followCtx, cancel := context.WithCancel(context.Background())
	s.stopFollow = cancel
	b.sessMu.Unlock()

	stream, err := b.openFollow(followCtx, s.id)
	if err != nil {
		b.sessMu.Lock()
		s.started = false
		b.sessMu.Unlock()
		cancel()
		return
	}
	go b.runFollow(followCtx, s, stream)
}

func (b *dshChatBackend) openFollow(ctx context.Context, sessionID string) (*dshStream, error) {
	client, err := b.ensure(ctx)
	if err != nil {
		return nil, err
	}
	// 参数名与嵌套形状取自生成产物：follow 的 args 只有 request.address。
	return client.openStream(ctx, "session/follow", map[string]any{
		"request": map[string]any{"address": dshSessionAddress(sessionID)},
	})
}

// runFollow 读取会话实时帧并投影给订阅者。
func (b *dshChatBackend) runFollow(ctx context.Context, s *dshSession, stream *dshStream) {
	defer func() {
		b.sessMu.Lock()
		s.started = false
		b.sessMu.Unlock()
	}()
	for {
		raw, err := stream.Recv(ctx)
		if err != nil {
			return
		}
		var frame struct {
			Type    string            `json:"type"`
			Records []json.RawMessage `json:"records"`
			Cursor  int64             `json:"cursor"`
			HasMore bool              `json:"hasMore"`
			Event   json.RawMessage   `json:"event"`
		}
		if json.Unmarshal(raw, &frame) != nil {
			continue
		}
		if frame.Type == "snapshot" {
			b.storeSnapshot(s, frame.Records, frame.Cursor, frame.HasMore)
			continue
		}
		rec := dshHistoryRecord{Type: frame.Type, Event: frame.Event}
		if model, effort, ok := dshModelFromRecord(rec); ok {
			b.sessMu.Lock()
			s.model, s.effort = model, effort
			b.sessMu.Unlock()
		}
		events := dshMapRecord(s.id, rec)
		model, effort := b.sessionModel(s)
		events = dshApplyModelContext(events, model, effort)
		b.trackTurnState(s, events)
		b.publish(s.id, events...)
	}
}

func (b *dshChatBackend) trackTurnState(s *dshSession, events []ChatEvent) {
	if len(events) == 0 {
		return
	}
	b.sessMu.Lock()
	defer b.sessMu.Unlock()
	for _, ev := range events {
		switch ev.Type {
		case "turn_started":
			s.running = true
			s.activeTurn = ev.TurnID
		case "turn_done":
			s.running = false
			s.activeTurn = ""
		}
	}
}

func (b *dshChatBackend) publish(sessionID string, events ...ChatEvent) {
	if len(events) == 0 {
		return
	}
	b.subMu.Lock()
	defer b.subMu.Unlock()
	subs := b.subs[sessionID]
	if len(subs) == 0 {
		backlog := append(b.backlog[sessionID], events...)
		if len(backlog) > dshBacklogLimit {
			backlog = backlog[len(backlog)-dshBacklogLimit:]
		}
		b.backlog[sessionID] = backlog
		return
	}
	for ch := range subs {
		for _, ev := range events {
			// 阻塞投递而不是丢弃：丢一条 delta 就是永久缺字。
			// 订阅者退出时会从 subs 摘除，因此这里不会永久卡住。
			ch <- ev
		}
	}
}

// loadSnapshot 拉一次会话快照并缓存为历史首页。
func (b *dshChatBackend) loadSnapshot(ctx context.Context, client *dshClient, s *dshSession) (ChatHistoryPage, error) {
	stream, err := client.openStream(ctx, "session/follow", map[string]any{
		"request": map[string]any{"address": dshSessionAddress(s.id)},
	})
	if err != nil {
		b.invalidate()
		return ChatHistoryPage{}, err
	}
	defer stream.Close()

	raw, err := stream.Recv(ctx)
	if err != nil {
		return ChatHistoryPage{}, err
	}
	var frame struct {
		Type    string            `json:"type"`
		Cursor  int64             `json:"cursor"`
		HasMore bool              `json:"hasMore"`
		Header  json.RawMessage   `json:"header"`
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil || frame.Type != "snapshot" {
		return ChatHistoryPage{}, fmt.Errorf("%w: follow 首帧不是 snapshot", errDSHProtocolChanged)
	}
	b.storeSnapshot(s, frame.Records, frame.Cursor, frame.HasMore)
	model, effort := b.sessionModel(s)
	if model == "" {
		// 窗口里没有 request/header 时退回会话投影，否则模型永远是空的。
		if m, e, ok := b.dshSessionModelSelection(ctx, client, s.id); ok {
			b.sessMu.Lock()
			s.model, s.effort = m, e
			b.sessMu.Unlock()
			model, effort = m, e
		}
	}
	return dshHistoryPageOfRecords(s, model, effort), nil
}

func (b *dshChatBackend) storeSnapshot(s *dshSession, records []json.RawMessage, cursor int64, hasMore bool) {
	parsed := make([]dshHistoryRecord, 0, len(records))
	model, effort := "", ""
	for _, raw := range records {
		var rec dshHistoryRecord
		if json.Unmarshal(raw, &rec) != nil {
			continue
		}
		parsed = append(parsed, rec)
		// 顺序扫描，留下最后一次请求用的模型（同一个快照里可能跨多个回合）。
		if m, e, ok := dshModelFromRecord(rec); ok {
			model, effort = m, e
		}
	}
	b.sessMu.Lock()
	if model != "" {
		s.model, s.effort = model, effort
	}
	// 快照可能比本地缓存更旧（重连后），只在更长时替换，避免历史倒退。
	if len(parsed) >= len(s.history) {
		s.history = parsed
	}
	s.cursor = cursor
	s.hasMore = hasMore
	s.oldest = dshRecordSeq(parsed)
	b.sessMu.Unlock()
}

func (b *dshChatBackend) translateCallError(err error) error {
	if errors.Is(err, errDSHHostUnavailable) || errors.Is(err, errDSHAuthFailed) {
		// 端点或凭据已失效（Desktop 重启）：丢连接，让下一次调用重新发现。
		b.invalidate()
	}
	return err
}

// Status 供 /api/info 报告 DSH 能力，不做任何新连接。
func (b *dshChatBackend) Status() (dshEndpoint, dshAuthMode, error, bool) {
	b.connMu.Lock()
	defer b.connMu.Unlock()
	return b.endpoint, b.authMode, b.lastErr, b.hostSeen
}

// Close 释放在途流（agent 退出或配置变更时调用）。
func (b *dshChatBackend) Close() {
	b.eventMu.Lock()
	stop := b.stopWaterfall
	b.waterfall = nil
	b.clientID = ""
	b.stopWaterfall = nil
	b.eventMu.Unlock()
	if stop != nil {
		stop()
	}

	b.sessMu.Lock()
	stops := make([]context.CancelFunc, 0, len(b.sessions))
	for _, s := range b.sessions {
		if s.stopFollow != nil {
			stops = append(stops, s.stopFollow)
		}
	}
	b.sessMu.Unlock()
	for _, stop := range stops {
		stop()
	}
	b.invalidate()
}

// ---------------- 小工具 ----------------

// dshSessionAddress 是 session/follow 与 session/page 的地址参数。
func dshSessionAddress(sessionID string) map[string]any {
	return map[string]any{"kind": "session", "sessionId": sessionID}
}

// dshVisibleSessionID 判断一个会话 id 是否属于用户在 GUI 里看得到的那一类。
//
// 只有 GUI 与 headless 创建的会话用 `session-<uuid>`；子代理会话是裸 uuid，
// ACP automation 会话也是裸 uuid。它们都不该出现在 Fleet 的会话列表里。
func dshVisibleSessionID(id string) bool {
	return strings.HasPrefix(id, "session-")
}

// dshListSessions 通过 host 的 session/list 取会话列表并投影成 Fleet 的 Session。
func (b *dshChatBackend) dshListSessions(ctx context.Context) ([]Session, error) {
	client, err := b.ensure(ctx)
	if err != nil {
		return nil, err
	}
	value, err := client.call(ctx, "session/list", map[string]any{"_request": map[string]any{}})
	if err != nil {
		return nil, b.translateCallError(err)
	}
	var listing struct {
		Items []struct {
			SessionID   string `json:"sessionId"`
			Cwd         string `json:"cwd"`
			UpdatedAt   int64  `json:"updatedAt"`
			Running     bool   `json:"running"`
			Blank       bool   `json:"blank"`
			Projections struct {
				Values struct {
					Title string `json:"title"`
				} `json:"values"`
			} `json:"projections"`
		} `json:"items"`
	}
	if err := json.Unmarshal(value, &listing); err != nil {
		return nil, fmt.Errorf("%w: session/list 返回无法解析", errDSHProtocolChanged)
	}

	out := make([]Session, 0, len(listing.Items))
	for _, item := range listing.Items {
		if !dshVisibleSessionID(item.SessionID) {
			continue
		}
		status := "idle"
		if item.Running {
			status = "active"
		}
		out = append(out, Session{
			SessionID:  item.SessionID,
			Assistant:  "dsh",
			Cwd:        item.Cwd,
			Title:      item.Projections.Values.Title,
			Mtime:      item.UpdatedAt,
			Live:       !item.Blank,
			Status:     status,
			ProjectCwd: item.Cwd,
		})
	}
	return out, nil
}

// dshHistoryPageFromRecords 把 session/page 的返回投影成历史分页。
func dshHistoryPageFromRecords(sessionID string, value json.RawMessage) (ChatHistoryPage, error) {
	var page struct {
		Records []json.RawMessage `json:"records"`
		HasMore bool              `json:"hasMore"`
	}
	if err := json.Unmarshal(value, &page); err != nil {
		return ChatHistoryPage{}, fmt.Errorf("%w: session/page 返回无法解析", errDSHProtocolChanged)
	}
	out := ChatHistoryPage{}
	for _, raw := range page.Records {
		var rec dshHistoryRecord
		if json.Unmarshal(raw, &rec) != nil {
			continue
		}
		out.Events = append(out.Events, dshMapRecord(sessionID, rec)...)
	}
	if page.HasMore {
		if seq := dshRecordSeqOfRaw(page.Records); seq > 0 {
			out.NextCursor = strconv.FormatInt(seq, 10)
		}
	}
	return out, nil
}

// dshSessionModelSelection 从 session/list 的会话投影里取当前模型与档位。
//
// 为什么不能只靠 request/header：那类事件在很久以前（每个请求一条），而快照窗口
// 只覆盖最近的消息，历史会话里根本取不到——模型会整段空着，dashboard 那条
// "模型使用数据"就只剩 token 没有名字。
func (b *dshChatBackend) dshSessionModelSelection(ctx context.Context, client *dshClient, sessionID string) (string, string, bool) {
	value, err := client.call(ctx, "session/list", map[string]any{"_request": map[string]any{}})
	if err != nil {
		return "", "", false
	}
	var listing struct {
		Items []struct {
			SessionID   string `json:"sessionId"`
			Projections struct {
				Values struct {
					// 真实形状是嵌套的：
					// modelSelection = {lastUsed:{provider,model,reasoningEffort}, next:{…}}
					ModelSelection struct {
						LastUsed struct {
							Model           string `json:"model"`
							ReasoningEffort string `json:"reasoningEffort"`
						} `json:"lastUsed"`
						Next struct {
							Model           string `json:"model"`
							ReasoningEffort string `json:"reasoningEffort"`
						} `json:"next"`
					} `json:"modelSelection"`
				} `json:"values"`
			} `json:"projections"`
		} `json:"items"`
	}
	if json.Unmarshal(value, &listing) != nil {
		return "", "", false
	}
	for _, item := range listing.Items {
		if item.SessionID != sessionID {
			continue
		}
		sel := item.Projections.Values.ModelSelection
		// 优先"上次实际用的"：next 只是待选项，没跑过时两者相同。
		if sel.LastUsed.Model != "" {
			return sel.LastUsed.Model, sel.LastUsed.ReasoningEffort, true
		}
		if sel.Next.Model != "" {
			return sel.Next.Model, sel.Next.ReasoningEffort, true
		}
		return "", "", false
	}
	return "", "", false
}

// sessionModel 读取会话当前记录的模型与档位（加锁，供发布前注入用）。
func (b *dshChatBackend) sessionModel(s *dshSession) (string, string) {
	b.sessMu.Lock()
	defer b.sessMu.Unlock()
	return s.model, s.effort
}

func dshHistoryPageOfRecords(s *dshSession, model, effort string) ChatHistoryPage {
	out := ChatHistoryPage{}
	for _, rec := range s.history {
		out.Events = append(out.Events, dshApplyModelContext(dshMapRecord(s.id, rec), model, effort)...)
	}
	if s.hasMore && s.oldest > 0 {
		out.NextCursor = strconv.FormatInt(s.oldest, 10)
	}
	return out
}

// dshRecordSeq 返回一批记录里最早的 seq，用作向后分页游标。
func dshRecordSeq(records []dshHistoryRecord) int64 {
	for _, rec := range records {
		var ev dshSessionEvent
		if json.Unmarshal(rec.Event, &ev) == nil && ev.Seq > 0 {
			return ev.Seq
		}
		var row dshPackedChunkRow
		if json.Unmarshal(rec.Event, &row) == nil && row.Seq > 0 {
			return row.Seq
		}
	}
	return 0
}

func dshRecordSeqOfRaw(records []json.RawMessage) int64 {
	parsed := make([]dshHistoryRecord, 0, len(records))
	for _, raw := range records {
		var rec dshHistoryRecord
		if json.Unmarshal(raw, &rec) == nil {
			parsed = append(parsed, rec)
		}
	}
	return dshRecordSeq(parsed)
}

// dshImagePart 把 Fleet 的附件转成 DSH 的图片内容块。
//
// 只发送 host 明确接受的四种 MIME；其它类型返回 nil（调用方跳过），
// 不为了"尽量发出去"而编造 mediaType。
func dshImagePart(att ChatAttachment) (map[string]any, error) {
	mediaType := strings.ToLower(strings.TrimSpace(att.MIME))
	switch mediaType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
	default:
		return nil, nil
	}
	path := att.Path
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("附件路径必须是绝对的: %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	part := map[string]any{
		"type":      "image",
		"mediaType": mediaType,
		"data":      base64.StdEncoding.EncodeToString(data),
	}
	if att.Name != "" {
		part["name"] = att.Name
	}
	return part, nil
}

// dshAttachmentPart 使用 ACP 兼容的内容块：图片继续内嵌给视觉模型，其它文件用所有
// ACP Agent 都必须支持的 resource_link 引用。文件保留在 host 本机，模型能解析就读，
// 不能解析时也不会因为 Fleet 预先猜测类型而拦截上传。
func dshAttachmentPart(att ChatAttachment) (map[string]any, error) {
	if part, err := dshImagePart(att); err != nil || part != nil {
		return part, err
	}
	if att.Path == "" || !filepath.IsAbs(att.Path) {
		return nil, fmt.Errorf("附件路径必须是绝对的: %q", att.Path)
	}
	info, err := os.Stat(att.Path)
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = errors.New("不是普通文件")
		}
		return nil, fmt.Errorf("无法读取附件 %q: %w", att.Path, err)
	}
	name := strings.TrimSpace(att.Name)
	if name == "" {
		name = filepath.Base(att.Path)
	}
	part := map[string]any{
		"type": "resource_link",
		"name": name,
		"uri":  (&url.URL{Scheme: "file", Path: att.Path}).String(),
	}
	if mimeType := strings.TrimSpace(att.MIME); mimeType != "" {
		part["mimeType"] = mimeType
	}
	return part, nil
}

// dshApprovalOutcome 把浏览器的决定翻译成 DSH 的 outcome。
func dshApprovalOutcome(response json.RawMessage) (map[string]any, error) {
	var body struct {
		Decision string          `json:"decision"`
		Answers  json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(response, &body); err != nil {
		return nil, fmt.Errorf("无法解析审批响应: %w", err)
	}
	if len(body.Answers) > 0 {
		return map[string]any{"kind": "result", "value": map[string]any{"answers": body.Answers}}, nil
	}
	switch strings.ToLower(strings.TrimSpace(body.Decision)) {
	case "accept", "allow", "allowed-once", "allow_once":
		return map[string]any{"kind": "result", "value": "allowed-once"}, nil
	case "decline", "reject", "rejected":
		return map[string]any{"kind": "result", "value": "rejected"}, nil
	case "":
		return nil, errors.New("审批响应缺少 decision")
	default:
		// 会话级授权（acceptForSession）在 DSH 没有对应 outcome，明确拒绝而不是
		// 悄悄降级成"允许一次"——那会让用户以为授权范围比实际更大。
		return nil, fmt.Errorf("DSH 不支持该审批决定: %q", body.Decision)
	}
}

// frameSessionID 尽力从 waterfall 载荷里取会话 id（取不到时归到空会话）。
func frameSessionID(payload json.RawMessage) string {
	var data map[string]any
	if json.Unmarshal(payload, &data) != nil {
		return ""
	}
	if id, ok := data["sessionId"].(string); ok {
		return id
	}
	return ""
}
