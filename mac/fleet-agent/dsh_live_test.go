//go:build dshlive

// 对真实 DSH Desktop 的验证门（规格 Phase D 的 G1–G3）。
//
// 用 build tag 隔离，不进 `go test ./...` 默认路径：这些测试依赖本机正在运行的
// DSH Desktop、它的 loopback 端点与凭据，在 CI 或没装 Desktop 的机器上必然失败。
//
// 跑法：
//
//	FLEET_DSH_LIVE=1 go test -tags dshlive -v -run TestLive ./...
//
// 全部只读：只调 session/list 与打开流，不发送 prompt、不改任何会话。

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func liveDSHClient(t *testing.T) *dshClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, endpoint, mode, err := connectDSH(ctx, defaultDSHHome(), defaultDSHLog(), os.Getenv("FLEET_DSH_ENDPOINT"))
	if err != nil {
		t.Fatalf("连接 DSH host 失败（DSH Desktop 是否在运行？）: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	t.Logf("端点=127.0.0.1:%d 凭据路径=%s", endpoint.Port, mode)
	return client
}

func liveCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 30*time.Second)
}

func truncateJSON(raw []byte, n int) string {
	if len(raw) <= n {
		return string(raw)
	}
	return string(raw[:n]) + "…"
}

// G1：端点发现 + 凭据 + session/list（参数名必须是 _request）。
func TestLiveSessionList(t *testing.T) {
	client := liveDSHClient(t)
	ctx, cancel := liveCtx(t)
	defer cancel()

	value, err := client.call(ctx, "session/list", map[string]any{"_request": map[string]any{}})
	if err != nil {
		t.Fatalf("session/list 失败: %v", err)
	}
	t.Logf("session/list 原始返回: %s", truncateJSON(value, 700))

	var envelope struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(value, &envelope); err != nil {
		t.Fatalf("session/list 返回不是 {items:[…]}: %v", err)
	}
	if len(envelope.Items) == 0 {
		t.Fatalf("session/list 返回空列表，期望至少一个会话")
	}
	t.Logf("G1 通过：session/list 返回 %d 个会话", len(envelope.Items))
	for i, item := range envelope.Items {
		if i >= 5 {
			break
		}
		id, _ := item["sessionId"].(string)
		if id == "" {
			id, _ = item["id"].(string)
		}
		cwd, _ := item["cwd"].(string)
		title, _ := item["title"].(string)
		t.Logf("  · %s  cwd=%s  title=%q", id, cwd, title)
	}
}

// G2（机械部分）：$events 流能建立并给出 clientId，反向通道端点确实被 host 认领。
//
// 完整往返（真的触发一次审批并从非浏览器客户端作答）需要人在 Desktop 里造出一次
// 审批；这里先把"能不能接上"这件事证明掉，剩下的靠 TestLiveApprovalRoundTrip。
func TestLiveEventsReadyAndReverseChannel(t *testing.T) {
	client := liveDSHClient(t)
	ctx, cancel := liveCtx(t)
	defer cancel()

	stream, err := client.openStream(ctx, "$events", nil)
	if err != nil {
		t.Fatalf("打开 $events 失败: %v", err)
	}
	defer stream.Close()

	item, err := stream.Recv(ctx)
	if err != nil {
		t.Fatalf("$events 首帧失败: %v", err)
	}
	t.Logf("$events 首帧: %s", truncateJSON(item, 400))

	var ready struct {
		Type     string `json:"type"`
		ClientID string `json:"clientId"`
	}
	if err := json.Unmarshal(item, &ready); err != nil {
		t.Fatalf("首帧解析失败: %v", err)
	}
	if ready.Type != "ready" {
		t.Fatalf("首帧 type = %q, want ready", ready.Type)
	}
	if ready.ClientID == "" {
		t.Fatal("ready 帧必须带 clientId：回答案时要用它")
	}
	t.Logf("G2(机械) 通过：拿到 clientId=%s", ready.ClientID)

	// 用假 clientId 回答案：host 应当回"没有活动事件流"，说明端点已被认领并在校验，
	// 而不是 404（端点不存在）。
	_, err = client.call(ctx, "$events/result", map[string]any{
		"clientId": "00000000-0000-0000-0000-000000000000",
		"eventId":  "no-such-event",
		"outcome":  map[string]any{"kind": "next"},
	})
	if err == nil {
		t.Fatal("用假 clientId 回答案不该成功")
	}
	if errors.Is(err, errDSHProtocolChanged) {
		t.Fatalf("$events/result 未被认领（协议变了？）: %v", err)
	}
	if !strings.Contains(err.Error(), "active event stream") {
		t.Fatalf("期望 host 报「没有活动事件流」，实际: %v", err)
	}
	t.Logf("G2(机械) 通过：$events/result 已被认领并校验（%v）", err)
}

// G3：session/follow 的打包行确实需要按 dt 展开，且 seq 能无损重建。
func TestLiveFollowChunkExpansion(t *testing.T) {
	client := liveDSHClient(t)
	ctx, cancel := liveCtx(t)
	defer cancel()

	// 先拿一个会话 id。
	value, err := client.call(ctx, "session/list", map[string]any{"_request": map[string]any{}})
	if err != nil {
		t.Fatalf("session/list 失败: %v", err)
	}
	var listing struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(value, &listing); err != nil || len(listing.Items) == 0 {
		t.Fatalf("拿不到会话列表: %v", err)
	}
	// 必须挑一个用户在 GUI 里看得到的会话：列表里混着子代理会话（裸 uuid），
	// 对它们开 follow 会得到 session/agent-busy（需要持久的父地址）。
	// 这条规则与生产代码的 dshVisibleSessionID 是同一条。
	var sessionID string
	for _, item := range listing.Items {
		id, _ := item["sessionId"].(string)
		if id == "" {
			id, _ = item["id"].(string)
		}
		if dshVisibleSessionID(id) {
			sessionID = id
			break
		}
	}
	if sessionID == "" {
		t.Fatalf("列表里没有用户可见会话（session- 前缀）: %s", truncateJSON(value, 400))
	}

	// 参数名与嵌套形状取自生成产物 typert.host.js 的 descriptor，不是猜的：
	//   session/follow 的 args 只有 request = { address: {kind:'session', sessionId}, maxMessages? }
	//   （写成 sessionId 会得到 gateway/arguments-invalid: missing "request"）
	stream, err := client.openStream(ctx, "session/follow", map[string]any{
		"request": map[string]any{
			"address": map[string]any{"kind": "session", "sessionId": sessionID},
		},
	})
	if err != nil {
		t.Fatalf("打开 session/follow 失败: %v", err)
	}
	defer stream.Close()

	item, err := stream.Recv(ctx)
	if err != nil {
		t.Fatalf("session/follow 首帧失败: %v", err)
	}
	var snapshot struct {
		Type    string            `json:"type"`
		Cursor  int64             `json:"cursor"`
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(item, &snapshot); err != nil {
		t.Fatalf("首帧解析失败: %v", err)
	}
	if snapshot.Type != "snapshot" {
		t.Fatalf("首帧 type = %q, want snapshot", snapshot.Type)
	}
	t.Logf("快照 cursor=%d records=%d", snapshot.Cursor, len(snapshot.Records))

	kinds := map[string]int{}
	chunkRows := 0
	for _, raw := range snapshot.Records {
		var rec struct {
			Type  string `json:"type"`
			Event struct {
				Type string `json:"type"`
			} `json:"event"`
		}
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if rec.Type == "event" {
			kinds[rec.Event.Type]++
			continue
		}
		if rec.Type != "chunks" {
			continue
		}
		chunkRows++
		var row struct {
			Event struct {
				Type   string `json:"type"`
				Seq0   int64  `json:"seq0"`
				Data   struct {
					DT    []int64  `json:"dt"`
					Texts []string `json:"texts"`
					Args  []string `json:"args"`
				} `json:"data"`
			} `json:"event"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatalf("打包行解析失败: %v", err)
		}
		members := len(row.Event.Data.Texts)
		if members == 0 {
			members = len(row.Event.Data.Args)
		}
		if members == 0 {
			t.Fatalf("打包行 %s 既没有 texts 也没有 args: %s", row.Event.Type, truncateJSON(raw, 300))
		}
		// 核心不变式（取自 dsh-session 的 chunk-rows 编解码器）：
		//   dt[i] = time[i+1] - time[i]，因此 N 个成员恰好 N-1 个间隔。
		// 写错成 N 会让每一行都判为畸形，或者更糟——静默错位。
		if len(row.Event.Data.DT) != members-1 {
			t.Fatalf("打包行 %s 的 dt(%d) 应为成员数-1(%d)",
				row.Event.Type, len(row.Event.Data.DT), members-1)
		}
		if chunkRows <= 2 {
			t.Logf("  打包行 外type=%q 内type=%q seq0=%d 成员=%d dt=%v 首段=%q",
				rec.Type, row.Event.Type, row.Event.Seq0, members, row.Event.Data.DT,
				firstText(row.Event.Data.Texts))
			t.Logf("  原始帧: %s", truncateJSON(raw, 300))
		}
	}

	for kind, n := range kinds {
		t.Logf("  事件 %s × %d", kind, n)
	}
	if chunkRows == 0 {
		t.Logf("G3 未覆盖：该会话快照里没有打包行（换一个有历史的会话再看）")
		return
	}
	t.Logf("G3 通过：%d 条打包行的 dt 与成员数一一对应", chunkRows)
}

func firstText(texts []string) string {
	if len(texts) == 0 {
		return ""
	}
	s := texts[0]
	if len(s) > 40 {
		s = s[:40] + "…"
	}
	return s
}
