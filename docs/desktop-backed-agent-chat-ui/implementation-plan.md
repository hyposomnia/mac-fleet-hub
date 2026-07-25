# Desktop-backed Claude / Codex 自绘会话 UI 实现计划

> 历史 POC 计划。当前 Codex 实现与后续迭代以 `codex-app-server-refactor.md` 为准；本文未勾选项不代表当前实现状态。

> **For `$dev`:** 这是复杂功能。建议按 SDD + TDD 推进，先做 Codex app-server POC，不要同时改 Claude 和 Codex 两条深链路。保留 ttyd fallback。

**Goal:** 让 dashboard 可以不用 ttyd 自绘 Codex 会话交互，优先复用 Desktop/app-server 后端；为 Claude 后续接入 Desktop 会话库 + headless CLI 留抽象。

**Architecture:** `dashboard` 只面对 `fleet-agent` 的统一 chat API；`fleet-agent` 内部新增 Codex app-server driver。Codex driver 连接本机 app-server unix socket / proxy / stdio，映射 Thread/Turn/Item 事件到统一 `ChatEvent`。Claude 暂不实现自绘，只保留现有 ttyd 与会话扫描。

**Spec:** `docs/desktop-backed-agent-chat-ui/design.md`

---

## File Structure

**Codex app-server client**

- Add `mac/fleet-agent/codex_appserver.go`
- Add `mac/fleet-agent/codex_appserver_test.go`

**Chat API / event projection**

- Add `mac/fleet-agent/chat.go`
- Add `mac/fleet-agent/chat_test.go`
- Modify `mac/fleet-agent/main.go`

**Dashboard POC**

- Modify `server/dashboard/index.html`
- Modify `server/dashboard/app.js`
- Modify `server/dashboard/style.css`

**Fallback / docs**

- Keep existing `/api/open`, `/api/new`, ttyd iframe pool untouched.
- Optional changelog after POC works.

---

## Part 1 — Codex app-server Client

### Task A1: JSON-RPC transport abstraction

**Files:**

- Add: `mac/fleet-agent/codex_appserver.go`
- Add: `mac/fleet-agent/codex_appserver_test.go`

- [ ] Define request/response/notification structs:

```go
type rpcRequest struct {
	ID     int64       `json:"id"`
	Method string      `json:"method"`
	Params interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcNotification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}
```

- [ ] Implement a line-oriented JSON-RPC client for stdio-like streams.
- [ ] Unit test:
  - request IDs increment.
  - response is routed to pending caller.
  - notification is delivered to notification channel.
  - malformed JSON does not panic; emits error/log and continues when possible.

**Verification:**

```bash
cd mac/fleet-agent && go test -run 'TestCodexAppServerRPC' ./...
```

### Task A2: app-server handshake

**Files:**

- Modify: `mac/fleet-agent/codex_appserver.go`
- Test: `mac/fleet-agent/codex_appserver_test.go`

- [ ] Implement `Initialize(ctx)`:
  - send `initialize` with `clientInfo.name=mac_fleet_hub`.
  - wait response.
  - send notification `{ "method": "initialized" }`.
- [ ] Unit test with fake stream:
  - fake server receives `initialize`.
  - client sends `initialized` after response.
  - repeated initialization is not allowed by our client.

**Verification:**

```bash
cd mac/fleet-agent && go test -run 'TestCodexAppServerInitialize' ./...
```

### Task A3: process / socket connection strategy

**Files:**

- Modify: `mac/fleet-agent/codex_appserver.go`

Implement connection priority:

- [ ] If `FLEET_CODEX_APPSERVER_SOCK` is set, connect that unix socket.
- [ ] Else default to `$CODEX_HOME/app-server-control/app-server-control.sock`.
- [ ] If socket exists, connect via unix socket websocket/proxy path, or use `codex app-server proxy --sock <path>` if direct websocket framing is too much for POC.
- [ ] If no socket, run `codex app-server daemon start`, then retry socket/proxy.
- [ ] If still unavailable, start `codex app-server` over stdio as a last app-server path.
- [ ] If all fail, report `appserver_unavailable` so UI can show ttyd fallback.

POC recommendation: prefer `codex app-server proxy` because it hides websocket-over-unix framing and gives stdio bytes to our JSON-RPC client.

**Manual probe:**

```bash
codex app-server daemon version
codex app-server proxy --help
```

Do not use `codex app-server --listen ws://...` for production/browser direct connection.

---

## Part 2 — Chat Domain Model

### Task B1: Define unified chat types

**Files:**

- Add: `mac/fleet-agent/chat.go`
- Add: `mac/fleet-agent/chat_test.go`

Define stable frontend-facing types:

```go
type ChatEvent struct {
	Type      string          `json:"type"`
	Assistant string         `json:"assistant"`
	SessionID string          `json:"sessionId"`
	TurnID    string          `json:"turnId,omitempty"`
	ItemID    string          `json:"itemId,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}
```

Use helper constructors instead of ad hoc maps:

- `thread_status`
- `assistant_delta`
- `assistant_done`
- `tool_start`
- `tool_delta`
- `tool_done`
- `diff_update`
- `approval_request`
- `approval_resolved`
- `turn_done`
- `error`

- [ ] Unit test JSON shape for 3-5 representative events.
- [ ] Keep event names stable and documented in comments.

### Task B2: Map Codex notifications to ChatEvent

**Files:**

- Modify: `mac/fleet-agent/chat.go`
- Modify: `mac/fleet-agent/codex_appserver.go`
- Test: `mac/fleet-agent/chat_test.go`

Map minimum events:

- `thread/status/changed` -> `thread_status`
- `item/agentMessage/delta` -> `assistant_delta`
- `item/completed` with agent message -> `assistant_done` if final text is available
- `item/commandExecution/outputDelta` -> `tool_delta`
- `item/fileChange/patchUpdated` -> `diff_update`
- `item/commandExecution/requestApproval` -> `approval_request`
- `item/fileChange/requestApproval` -> `approval_request`
- `turn/completed` -> `turn_done`

- [ ] Unit test with raw JSON fixtures copied from generated schema shape.
- [ ] Unknown notifications should be ignored or logged, not fatal.

**Important:** Before implementing exact fields, run:

```bash
tmp=$(mktemp -d)
codex app-server generate-json-schema --out "$tmp"
```

Use the generated schema from the installed Codex version as the source of truth.

---

## Part 3 — HTTP API in fleet-agent

### Task C1: Add chat handlers without breaking ttyd

**Files:**

- Modify: `mac/fleet-agent/main.go`
- Modify/Add tests as practical.

Add routes:

```text
GET  /api/chat/thread?assistant=codex&sessionId=...
POST /api/chat/resume
POST /api/chat/input
POST /api/chat/steer
POST /api/chat/approve
POST /api/chat/interrupt
GET  /api/chat/events?assistant=codex&sessionId=...
```

POC minimum:

- [ ] `POST /api/chat/resume`
  - body `{assistant:"codex",sessionId,mode}`
  - calls `thread/resume`
  - returns `{sessionId, threadId, status}`
- [ ] `POST /api/chat/input`
  - body `{assistant:"codex",sessionId,text,clientMessageId?}`
  - calls `turn/start` or `turn/steer`
  - returns `{turnId}`
- [ ] `POST /api/chat/steer`
  - body `{assistant:"codex",sessionId,text,images?}`
  - resolves the active turn and calls `turn/steer` with `expectedTurnId`
  - returns `409 no_active_turn` when no active turn is known
- [ ] `GET /api/chat/events`
  - SSE stream.
  - sends `ChatEvent` JSON in `data: ...`.
  - closes cleanly when client disconnects.

Keep `/api/open` and `/api/new` unchanged.

### Task C2: Approval response handling

**Files:**

- Modify: `mac/fleet-agent/codex_appserver.go`
- Modify: `mac/fleet-agent/main.go`

- [ ] Store pending approval requests by `approvalId` or JSON-RPC request ID.
- [ ] `POST /api/chat/approve` maps:
  - approve -> app-server response decision approved
  - deny -> denied
  - abort -> abort
- [ ] Emit `approval_resolved` after response succeeds.

POC can support only command approval first; file-change approval can return 501 until mapped.

### Task C3: Error and fallback semantics

**Files:**

- Modify: `mac/fleet-agent/main.go`

- [ ] If app-server unavailable, return structured error:

```json
{"error":"appserver_unavailable","message":"Codex app-server 不可用，可用终端打开。"}
```

- [ ] Dashboard should show a “用终端打开” action that calls existing `/api/open`.
- [ ] Do not auto-start high-risk bypass mode silently.

---

## Part 4 — Dashboard POC

### Task D1: Add chat pane skeleton

**Files:**

- Modify: `server/dashboard/index.html`
- Modify: `server/dashboard/style.css`
- Modify: `server/dashboard/app.js`

- [ ] Add a `chat-pane` container alongside existing terminal frames.
- [ ] Keep existing ttyd iframe DOM intact.
- [ ] For Codex rows, add a new primary action “打开新版会话” or route normal click to chat POC behind a feature flag.

Recommended feature gate:

```js
const CHAT_POC = true; // or localStorage flag during development
```

### Task D2: Render event stream

**Files:**

- Modify: `server/dashboard/app.js`

- [ ] On opening Codex chat:
  - call `/api/chat/resume`.
  - open EventSource `/api/chat/events?...`.
  - render incoming events.
- [ ] Implement basic renderers:
  - user message bubble
  - assistant streaming bubble
  - command/tool card
  - diff card placeholder
  - approval card with approve/deny buttons
  - error with terminal fallback button
- [ ] Composer:
  - Enter sends.
  - Shift+Enter newline.
  - disable while sending if no active stream state is ready.

### Task D3: Keep terminal fallback

**Files:**

- Modify: `server/dashboard/app.js`

- [ ] Existing `connect(sessionId,title,cwd,mode)` must remain callable.
- [ ] Chat error state includes terminal fallback.
- [ ] Header should clearly indicate whether current view is “结构化会话” or “终端” during POC.

---

## Part 5 — Verification

### Unit tests

Run:

```bash
cd mac/fleet-agent && go test ./...
```

Expected:

- Existing session scanning tests still pass.
- New app-server RPC tests pass.
- New event mapping tests pass.

### Local app-server probe

Run:

```bash
codex app-server daemon version
```

Expected:

- JSON includes `status:"running"` or startable daemon.
- socket path exists or `daemon start` creates it.

### Manual POC flow

1. Open dashboard.
2. Select a Mac.
3. Switch assistant to Codex.
4. Open a Codex Desktop session in structured mode.
5. Send: `只回复 OK，不要改文件。`
6. Verify:
   - assistant text streams in chat pane.
   - no ttyd iframe is needed for this path.
   - session remains visible in Codex Desktop/CLI history.
7. Trigger a command approval with a harmless request, for example asking Codex to run `pwd`.
8. Verify:
   - approval card appears.
   - deny works.
   - approve works.
9. Stop app-server or point socket env to bad path.
10. Verify:
   - UI shows structured error.
   - terminal fallback opens existing ttyd path.

### Browser checks

- Desktop width: no overlap between session list, chat pane, composer, approval card.
- Mobile width: composer remains usable; approval card is visible without terminal keybar.
- Dark/light themes preserve contrast.

---

## Implementation Notes

- Do not edit JSONL session files as a write path.
- Do not scrape TUI output.
- Do not expose app-server websocket through nginx.
- Do not remove ttyd services, plist entries, setup logic, or existing `/api/open`/`/api/new`.
- Keep Codex POC independent from Claude implementation. Claude comes after Codex path is proven.
- If exact Codex app-server field names differ, regenerate schema and update only the driver/mappers.

---

## Future Phase — Claude

After Codex POC:

- Add `claude_headless.go`.
- Keep current Desktop store scanner for list metadata.
- Resume/send via:

```bash
claude -p --input-format stream-json --output-format stream-json --resume <sessionId>
```

- Map Claude stream-json to the same `ChatEvent`.
- Confirm whether this path preserves Desktop-visible session history.
- If CLI headless cannot support continuous interactive UX cleanly, evaluate `ClaudeSDKClient`; treat API-key auth as an explicit product choice, not default.
