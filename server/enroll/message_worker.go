package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type agentQueueItem struct {
	ID              string `json:"id"`
	ClientMessageID string `json:"clientMessageId"`
	Status          string `json:"status"`
	StateVersion    int64  `json:"stateVersion"`
	TurnID          string `json:"turnId"`
	Error           string `json:"error"`
}

type gatewayChatEvent struct {
	Type   string          `json:"type"`
	TurnID string          `json:"turnId"`
	ItemID string          `json:"itemId"`
	Data   json.RawMessage `json:"data"`
}

type messageEventTracker struct {
	messageID   string
	targetTurn  string
	ackSeen     bool
	started     bool
	done        bool
	doneStatus  string
	interaction bool
	errorText   string
	texts       map[string]string
	textOrder   []string
}

func newMessageEventTracker(messageID, turnID string) *messageEventTracker {
	return &messageEventTracker{messageID: messageID, targetTurn: turnID, texts: map[string]string{}}
}

func (t *messageEventTracker) process(event gatewayChatEvent) {
	var data map[string]interface{}
	_ = json.Unmarshal(event.Data, &data)
	turnID := strings.TrimSpace(event.TurnID)
	if turnID == "" {
		turnID = firstMapString(data, "turnId", "turn_id")
	}
	switch event.Type {
	case "user_done":
		clientID := firstMapString(data, "clientId", "client_id", "clientUserMessageId", "client_user_message_id")
		if clientID == t.messageID {
			t.ackSeen = true
			if turnID != "" && t.targetTurn == "" {
				t.targetTurn = turnID
			}
		}
	case "turn_started":
		if t.targetTurn == "" && t.ackSeen {
			t.targetTurn = turnID
		}
		if turnID != "" && turnID == t.targetTurn {
			t.started = true
		}
	case "assistant_done":
		if t.targetTurn == "" || turnID != t.targetTurn {
			return
		}
		text := firstMapString(data, "text")
		if text == "" {
			if item, ok := data["item"].(map[string]interface{}); ok {
				text = firstMapString(item, "text")
			}
		}
		key := event.ItemID
		if key == "" {
			key = hashString(turnID + "\x00" + text)
		}
		if _, exists := t.texts[key]; !exists {
			t.textOrder = append(t.textOrder, key)
		}
		t.texts[key] = text
	case "interaction_request", "approval_request":
		if t.targetTurn == "" || turnID == "" || turnID == t.targetTurn {
			t.interaction = true
		}
	case "error":
		if t.targetTurn == "" || turnID == "" || turnID == t.targetTurn {
			t.errorText = firstMapString(data, "message", "detail")
		}
	case "turn_done":
		if t.targetTurn == "" || turnID != t.targetTurn {
			return
		}
		t.done = true
		t.doneStatus = strings.ToLower(firstMapString(data, "status"))
		if t.doneStatus == "" {
			if turn, ok := data["turn"].(map[string]interface{}); ok {
				t.doneStatus = strings.ToLower(firstMapString(turn, "status"))
			}
		}
	}
}

func (t *messageEventTracker) processHistory(events []gatewayChatEvent) {
	if t.targetTurn == "" {
		ackIndex := -1
		for i, event := range events {
			if event.Type != "user_done" {
				continue
			}
			var data map[string]interface{}
			_ = json.Unmarshal(event.Data, &data)
			if firstMapString(data, "clientId", "client_id", "clientUserMessageId", "client_user_message_id") == t.messageID {
				ackIndex = i
				t.ackSeen = true
				break
			}
		}
		if ackIndex >= 0 {
			bestDistance := len(events) + 1
			for i, event := range events {
				if event.Type != "turn_started" || event.TurnID == "" {
					continue
				}
				distance := i - ackIndex
				if distance < 0 {
					distance = -distance
				}
				if distance < bestDistance {
					t.targetTurn, bestDistance = event.TurnID, distance
				}
			}
		}
	}
	for _, event := range events {
		t.process(event)
	}
}

func (t *messageEventTracker) finalText() string {
	parts := make([]string, 0, len(t.textOrder))
	for _, key := range t.textOrder {
		if text := strings.TrimSpace(t.texts[key]); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func firstMapString(data map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := data[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (a *messageAPI) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		a.schedule()
		select {
		case <-ctx.Done():
			return
		case <-a.wake:
		case <-ticker.C:
		}
	}
}

func (a *messageAPI) schedule() {
	a.mu.Lock()
	now := time.Now()
	dirty := false
	for id, job := range a.jobs {
		if job.Status == messageQueued && !a.activeExec[id] && now.Sub(job.CreatedAt) >= 24*time.Hour {
			job.Status = messageFailed
			job.Error = &messageError{Code: "queue_timeout", Message: "超过 24 小时仍未开始执行", Retryable: true}
			job.FailedAt = now.UTC()
			dirty = true
		}
		terminalAt := job.CompletedAt
		if terminalAt.IsZero() {
			terminalAt = job.FailedAt
		}
		if !terminalAt.IsZero() && now.Sub(terminalAt) >= 7*24*time.Hour && !a.activeCallback[id] {
			delete(a.jobs, id)
			dirty = true
		}
	}
	if dirty {
		if err := a.saveJobsLocked(); err != nil {
			logWorkerError("清理消息记录", err)
		}
	}
	jobs := make([]*messageJob, 0, len(a.jobs))
	for _, job := range a.jobs {
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.Before(jobs[j].CreatedAt) })
	activeSessions := map[string]bool{}
	for id := range a.activeExec {
		if job := a.jobs[id]; job != nil && job.SessionID != "" {
			activeSessions[job.DeviceID+"\x00"+job.Assistant+"\x00"+job.SessionID] = true
		}
	}
	available := a.maxConcurrent - len(a.activeExec)
	var executeIDs []string
	if available > 0 {
		for _, job := range jobs {
			if available == 0 {
				break
			}
			if job.Status != messageQueued || a.activeExec[job.ID] {
				continue
			}
			key := ""
			if job.SessionID != "" {
				key = job.DeviceID + "\x00" + job.Assistant + "\x00" + job.SessionID
				if activeSessions[key] {
					continue
				}
			}
			a.activeExec[job.ID] = true
			if key != "" {
				activeSessions[key] = true
			}
			executeIDs = append(executeIDs, job.ID)
			available--
		}
	}
	var callbackIDs []string
	for _, job := range jobs {
		if job.CallbackURL == "" || (job.Status != messageCompleted && job.Status != messageFailed) || a.activeCallback[job.ID] {
			continue
		}
		if job.Callback.Status == "delivered" || job.Callback.Status == "delivery_failed" {
			continue
		}
		if !job.Callback.NextAttemptAt.IsZero() && job.Callback.NextAttemptAt.After(now) {
			continue
		}
		a.activeCallback[job.ID] = true
		callbackIDs = append(callbackIDs, job.ID)
	}
	a.mu.Unlock()
	for _, id := range executeIDs {
		go a.executeJob(id)
	}
	for _, id := range callbackIDs {
		go a.deliverCallback(id)
	}
}

func (a *messageAPI) executeJob(id string) {
	defer func() {
		a.mu.Lock()
		delete(a.activeExec, id)
		a.mu.Unlock()
		a.signal()
	}()
	a.mu.Lock()
	job := cloneMessageJob(a.jobs[id])
	a.mu.Unlock()
	if job == nil || job.Status != messageQueued {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	if job.SessionID == "" {
		var started struct {
			SessionID string `json:"sessionId"`
		}
		err := a.agentJSON(ctx, job.DeviceIP, http.MethodPost, "chat/start", map[string]string{
			"assistant": job.Assistant, "cwd": job.ProjectPath, "mode": "default",
		}, &started)
		if err != nil || started.SessionID == "" {
			a.failJob(id, "ai_client_unavailable", fallbackError(err, "AI 客户端未能创建会话"), true)
			return
		}
		job.SessionID = started.SessionID
		job.SessionName = "新" + displayAIClient(job.AIClient) + "会话"
		a.updateJob(id, func(current *messageJob) {
			current.SessionID, current.SessionName = job.SessionID, job.SessionName
		})
	} else {
		var resumed map[string]interface{}
		if err := a.agentJSON(ctx, job.DeviceIP, http.MethodPost, "chat/resume", map[string]string{
			"assistant": job.Assistant, "sessionId": job.SessionID, "mode": "default",
		}, &resumed); err != nil {
			a.failJob(id, "ai_client_unavailable", err.Error(), true)
			return
		}
	}
	var queued agentQueueItem
	if err := a.agentJSON(ctx, job.DeviceIP, http.MethodPost, "chat/queue", map[string]interface{}{
		"assistant": job.Assistant, "sessionId": job.SessionID, "clientMessageId": job.ID,
		"cwd": job.ProjectPath, "text": job.Message, "deliveryMode": "auto",
	}, &queued); err != nil {
		a.failJob(id, "execution_failed", err.Error(), true)
		return
	}
	a.updateJob(id, func(current *messageJob) { current.AgentQueueID = queued.ID })
	tracker := newMessageEventTracker(job.ID, queued.TurnID)
	eventCtx, stopEvents := context.WithCancel(ctx)
	events := a.streamEvents(eventCtx, job)
	defer stopEvents()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	historyTick := 0
	for {
		select {
		case <-ctx.Done():
			a.failJob(id, "execution_timeout", "单次执行超过 2 小时", true)
			return
		case event, ok := <-events:
			if ok {
				tracker.process(event)
			} else {
				events = nil
			}
		case <-ticker.C:
			historyTick++
			item, err := a.pollAgentQueue(ctx, job, queued.ID)
			if err != nil {
				continue
			}
			queued = item
			switch item.Status {
			case "writer_confirmation_required":
				_ = a.chooseQueueWait(ctx, job, item)
			case "failed", "uncertain", "cancelled":
				code := "execution_failed"
				if item.Status == "uncertain" {
					code = "delivery_uncertain"
				}
				a.failJob(id, code, fallbackString(item.Error, "目标设备消息队列执行失败"), item.Status != "cancelled")
				return
			case "waiting_access":
				a.failJob(id, "fleet_read_only", "该会话当前为 Fleet 只读状态", false)
				return
			case "sent":
				if tracker.targetTurn == "" && item.TurnID != "" {
					tracker.targetTurn = item.TurnID
				}
			}
			if historyTick%3 == 0 || item.Status == "sent" {
				if history, historyErr := a.fetchHistory(ctx, job); historyErr == nil {
					tracker.processHistory(history)
				}
			}
		}
		if tracker.started && job.Status != messageRunning {
			job.Status = messageRunning
			a.updateJob(id, func(current *messageJob) {
				if current.Status == messageQueued {
					current.Status, current.TurnID, current.StartedAt = messageRunning, tracker.targetTurn, time.Now().UTC()
				}
			})
		}
		if tracker.interaction {
			_ = a.interruptJob(ctx, job)
			a.failJob(id, "interaction_required", "AI 请求人工审批或补充回答，公网 API v1 无法处理", false)
			return
		}
		if tracker.errorText != "" {
			a.failJob(id, "execution_failed", tracker.errorText, true)
			return
		}
		if tracker.done {
			if isFailedTurnStatus(tracker.doneStatus) {
				a.failJob(id, "execution_failed", fallbackString(tracker.doneStatus, "AI 执行失败"), true)
				return
			}
			a.completeJob(id, tracker.targetTurn, tracker.finalText())
			return
		}
	}
}

func (a *messageAPI) updateJob(id string, mutate func(*messageJob)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if job := a.jobs[id]; job != nil {
		mutate(job)
		if err := a.saveJobsLocked(); err != nil {
			logWorkerError("保存消息状态", err)
		}
	}
}

func (a *messageAPI) failJob(id, code, message string, retryable bool) {
	a.updateJob(id, func(job *messageJob) {
		job.Status, job.Error, job.FailedAt = messageFailed, &messageError{Code: code, Message: message, Retryable: retryable}, time.Now().UTC()
		if job.CallbackURL != "" && job.Callback.Status == "" {
			job.Callback.Status = "pending"
		}
	})
	a.signal()
}

func (a *messageAPI) completeJob(id, turnID, text string) {
	a.updateJob(id, func(job *messageJob) {
		job.Status, job.TurnID, job.AIMessage, job.CompletedAt, job.Error = messageCompleted, turnID, text, time.Now().UTC(), nil
		if job.StartedAt.IsZero() {
			job.StartedAt = job.CompletedAt
		}
		if job.CallbackURL != "" && job.Callback.Status == "" {
			job.Callback.Status = "pending"
		}
	})
	a.signal()
}

func (a *messageAPI) pollAgentQueue(ctx context.Context, job *messageJob, queueID string) (agentQueueItem, error) {
	query := url.Values{"assistant": {job.Assistant}, "sessionId": {job.SessionID}}
	var control struct {
		Items []agentQueueItem `json:"items"`
	}
	if err := a.agentJSON(ctx, job.DeviceIP, http.MethodGet, "chat/queue?"+query.Encode(), nil, &control); err != nil {
		return agentQueueItem{}, err
	}
	for _, item := range control.Items {
		if item.ID == queueID || item.ClientMessageID == job.ID {
			return item, nil
		}
	}
	// fleet-agent 的控制快照会隐藏 sent/cancelled 终态。公网 worker 从不主动取消，
	// 因此一个曾成功入队、随后从可见列表消失的项按 sent 处理，再由 history/events
	// 用 clientMessageId + turnId 做最终对账；不能因 UI 投影隐藏 sent 就停止跟踪。
	return agentQueueItem{ID: queueID, ClientMessageID: job.ID, Status: "sent"}, nil
}

func (a *messageAPI) chooseQueueWait(ctx context.Context, job *messageJob, item agentQueueItem) error {
	var ignored map[string]interface{}
	return a.agentJSON(ctx, job.DeviceIP, http.MethodPost, "chat/queue/decision", map[string]interface{}{
		"id": item.ID, "action": "wait", "stateVersion": item.StateVersion,
	}, &ignored)
}

func (a *messageAPI) interruptJob(ctx context.Context, job *messageJob) error {
	var ignored map[string]interface{}
	return a.agentJSON(ctx, job.DeviceIP, http.MethodPost, "chat/interrupt", map[string]string{
		"assistant": job.Assistant, "sessionId": job.SessionID,
	}, &ignored)
}

func (a *messageAPI) fetchHistory(ctx context.Context, job *messageJob) ([]gatewayChatEvent, error) {
	query := url.Values{"assistant": {job.Assistant}, "sessionId": {job.SessionID}}
	var page struct {
		Events []gatewayChatEvent `json:"events"`
	}
	if err := a.agentJSON(ctx, job.DeviceIP, http.MethodGet, "chat/history?"+query.Encode(), nil, &page); err != nil {
		return nil, err
	}
	return page.Events, nil
}

func (a *messageAPI) streamEvents(ctx context.Context, job *messageJob) <-chan gatewayChatEvent {
	out := make(chan gatewayChatEvent, 64)
	go func() {
		defer close(out)
		query := url.Values{"assistant": {job.Assistant}, "sessionId": {job.SessionID}}
		endpoint := fmt.Sprintf("http://%s:%d/api/chat/events?%s", job.DeviceIP, a.agentPort, query.Encode())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return
		}
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64<<10), 2<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event gatewayChatEvent
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
				continue
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func isFailedTurnStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error", "errored", "cancelled", "canceled", "aborted", "rejected":
		return true
	default:
		return false
	}
}

func fallbackError(err error, fallback string) string {
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		return err.Error()
	}
	return fallback
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func displayAIClient(client string) string {
	if client == "deepseek" {
		return "DeepSeek"
	}
	return "Codex"
}

func logWorkerError(action string, err error) {
	if err != nil {
		fmt.Printf("fleet message worker: %s失败: %v\n", action, err)
	}
}

var callbackRetryDelays = []time.Duration{0, 10 * time.Second, time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour}

func (a *messageAPI) deliverCallback(id string) {
	defer func() {
		a.mu.Lock()
		delete(a.activeCallback, id)
		a.mu.Unlock()
		a.signal()
	}()
	a.mu.Lock()
	job := cloneMessageJob(a.jobs[id])
	a.mu.Unlock()
	if job == nil || job.CallbackURL == "" {
		return
	}
	payload := publicMessage(job, false)
	event := "message.failed"
	if job.Status == messageCompleted {
		event = "message.completed"
	}
	payload["event"] = event
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	client, problem := safeCallbackClient(ctx, job.CallbackURL)
	if problem != nil {
		cancel()
		a.recordCallbackFailure(id, problem.Message)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, job.CallbackURL, bytes.NewReader(body))
	if err != nil {
		cancel()
		a.recordCallbackFailure(id, err.Error())
		return
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signingKey, _ := hex.DecodeString(job.CallbackSigningKey)
	mac := hmac.New(sha256.New, signingKey)
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	eventID := "evt_" + strings.TrimPrefix(job.ID, "msg_")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mac-fleet-hub-webhook/1.0")
	req.Header.Set("X-Fleet-Event-Id", eventID)
	req.Header.Set("X-Fleet-Timestamp", timestamp)
	req.Header.Set("X-Fleet-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := client.Do(req)
	cancel()
	if err == nil && resp != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
	}
	if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		a.updateJob(id, func(current *messageJob) {
			current.Callback.Status = "delivered"
			current.Callback.Attempts++
			current.Callback.LastAttemptAt = time.Now().UTC()
			current.Callback.NextAttemptAt = time.Time{}
			current.Callback.LastError = ""
		})
		return
	}
	message := "callback 请求失败"
	if err != nil {
		message = err.Error()
	} else if resp != nil {
		message = fmt.Sprintf("callback HTTP %d", resp.StatusCode)
	}
	a.recordCallbackFailure(id, message)
}

func (a *messageAPI) recordCallbackFailure(id, message string) {
	a.updateJob(id, func(job *messageJob) {
		job.Callback.Attempts++
		job.Callback.LastAttemptAt = time.Now().UTC()
		job.Callback.LastError = message
		if job.Callback.Attempts >= len(callbackRetryDelays) {
			job.Callback.Status = "delivery_failed"
			job.Callback.NextAttemptAt = time.Time{}
			return
		}
		job.Callback.Status = "retrying"
		job.Callback.NextAttemptAt = time.Now().Add(callbackRetryDelays[job.Callback.Attempts]).UTC()
	})
}

func safeCallbackClient(ctx context.Context, raw string) (*http.Client, *apiProblem) {
	if problem := validatePublicCallbackURL(ctx, raw); problem != nil {
		return nil, problem
	}
	parsed, _ := url.Parse(raw)
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, &apiProblem{Status: 400, Code: "invalid_callback_url", Message: "callback_url 域名无法解析"}
	}
	selected := addresses[0].IP.String()
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	dialTarget := net.JoinHostPort(selected, port)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: parsed.Hostname()},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, dialTarget)
		},
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}
