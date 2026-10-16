//go:build dshlive

package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in: creates and deletes only its own clearly labelled test session.
func TestLiveDSHQuestionRoundTrip(t *testing.T) {
	if os.Getenv("FLEET_DSH_QUESTION_LIVE") != "1" {
		t.Skip("set FLEET_DSH_QUESTION_LIVE=1 to create a question UAT session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	b := newDSHChatBackend(defaultDSHHome(), defaultDSHLog(), os.Getenv("FLEET_DSH_ENDPOINT"))
	defer b.Close()
	client, err := b.ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	started, err := b.Start(ctx, "dsh", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = b.Interrupt(cleanup, "dsh", started.SessionID)
		_, _ = client.call(cleanup, "session/delete", map[string]any{"request": map[string]any{"sessionId": started.SessionID}})
	}()
	_, _ = client.call(ctx, "session/rename", map[string]any{"request": map[string]any{"sessionId": started.SessionID, "title": "Fleet 提问回传自动验收"}})
	events, err := b.Events(ctx, "dsh", started.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Input(ctx, "dsh", started.SessionID, "这是 Fleet 提问回传自动验收。仅调用一次 ask_user_question，包含三个问题：第一题 id=choice，单选，选项“单选回传成功”和“失败”；第二题 id=multi，多选，选项“多选A”和“多选B”；第三题 id=text，无选项自由文本，问“输入测试文本”。请使用 timeout=120000。不要调用任何其他工具，也不要读取文件。收到答案后，在最终回复中原样复述全部答案。", nil, nil, ChatTurnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	answered := false
	var answerText strings.Builder
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("real DSH question round trip timed out; answered=%v output=%s", answered, answerText.String())
		case ev, ok := <-events:
			if !ok {
				t.Fatal("DSH event stream closed")
			}
			if ev.Type == "interaction_request" {
				var request struct {
					RequestID string        `json:"requestId"`
					Questions []dshQuestion `json:"questions"`
				}
				_ = json.Unmarshal(ev.Data, &request)
				if len(request.Questions) != 3 {
					t.Fatalf("expected 3 questions, got %s", ev.Data)
				}
				answers := map[string]any{}
				for _, q := range request.Questions {
					values := []string{"自由文本回传成功"}
					if len(q.Options) > 0 {
						values = []string{q.Options[0].Label}
						if q.MultiSelect && len(q.Options) > 1 {
							values = append(values, q.Options[1].Label)
						}
					}
					answers[q.ID] = map[string]any{"answers": values}
				}
				raw, _ := json.Marshal(map[string]any{"answers": answers})
				if err := b.Respond(ctx, "dsh", started.SessionID, request.RequestID, raw); err != nil {
					t.Fatal(err)
				}
				answered = true
				t.Log("real DSH accepted single/multiple/free-text answers")
			}
			if ev.Type == "assistant_done" {
				var data map[string]any
				_ = json.Unmarshal(ev.Data, &data)
				raw, _ := json.Marshal(data)
				answerText.Write(raw)
			}
			if ev.Type == "turn_done" {
				if !answered {
					t.Fatalf("model ended without an interactive question: %s", ev.Data)
				}
				for _, value := range []string{"单选回传成功", "多选A", "多选B", "自由文本回传成功"} {
					if !strings.Contains(answerText.String(), value) {
						t.Fatalf("model did not receive %q, output=%s", value, answerText.String())
					}
				}
				t.Log("real DSH model echoed all expected answers")
				return
			}
		}
	}
}
