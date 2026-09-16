# DeepSeek Harness（DSH）shared 接入 · 实现计划

依据：`docs/superpowers/specs/2026-09-16-dsh-shared-attach-design.md`（下称「规格」）。
分支：`feat/dsh-shared-attach`，独立 worktree `../mac-fleet-hub-dsh`。

执行约定：

- 每个 Phase 自带测试闭环，可独立验证；每个 Phase 结束提交一次。
- TDD 铁律：先写会失败的测试 → 亲眼看它红 → 最小实现 → 亲眼看它绿 → 重构。
- 每个 Phase 结束跑 `bash scripts/verify.sh`，失败不进入下一 Phase。
- 涉及真实 Desktop 的验证（Phase D）单独隔离，不放进 `go test ./...` 默认路径。

---

## Phase A：assistant 能力化重构（纯重构，行为零变化）

**Files**：`mac/fleet-agent/chat.go`、`mac/fleet-agent/chat_queue.go`、新增 `mac/fleet-agent/chat_capabilities.go`

**Interfaces**：

```go
type assistantCapabilities struct {
    SelfDraw     bool
    Queue        bool
    Interactions bool
}

func chatCapabilities(assistant string) assistantCapabilities
```

**Steps**

- [ ] 1. 新增 `chat_capabilities.go`，实现 `chatCapabilities`：`codex` → 全 true；其余（含 `claude`）→ 全 false。
- [ ] 2. 新增 `chat_capabilities_test.go`，先断言 `chatCapabilities("claude").SelfDraw == false`（锁住 Claude 仍走终端）。
      Run: `go test ./... -run TestChatCapabilities` → **红**（函数不存在）。
- [ ] 3. 实现函数 → **绿**。
- [ ] 4. 把 `chat.go` 与 `chat_queue.go` 里 32 处 `assistant != "codex"` 守卫替换为
      `if !chatCapabilities(assistant).SelfDraw { writeErr(..., "unsupported_assistant", ...) }`
      （`chat_queue.go` 的队列操作守卫用 `.Queue`）。
- [ ] 5. Run: `go test ./...` → 全绿（**这就是"行为零变化"的证据**，现有 5 个测试文件覆盖了这些分支）。
- [ ] 6. 确认 `grep -rn 'assistant != "codex"' mac/fleet-agent/*.go | grep -v _test` 无输出。
- [ ] 7. Commit: `refactor(agent): 把 assistant 硬编码守卫换成能力判定`

---

## Phase B：端点发现与凭据（`dsh_discovery.go`）

**Files**：新增 `mac/fleet-agent/dsh_discovery.go`、`dsh_discovery_test.go`

**Interfaces**：

```go
type dshEndpoint struct {
    Port  int
    Token string
}

// parseDSHWebLine 从 harness.log 的一行里提取 (port, token)。
func parseDSHWebLine(line string) (dshEndpoint, bool)
// dshEndpointFromLog 从日志文件尾部向前找最后一条 dsh web: 行。
func dshEndpointFromLog(path string) (dshEndpoint, error)
// projectKey 精确复制 DSH 的 cwd → 会话目录名编码。
func projectKey(cwd string) (string, error)
// signDSHCookie 用 .credentials.yaml 的 secret 自签 cookie。
func signDSHCookie(secret, authority string, now time.Time) (string, error)
// dshCookieSecret 读取 $DSH_HOME/.credentials.yaml 的 client-connection/browser-session secret。
func dshCookieSecret(home string) (string, error)
```

**Steps**

- [ ] 1. `parseDSHWebLine` 测试：正常行、无 token、URL 畸形、无关行 → 红 → 实现 → 绿。
- [ ] 2. `projectKey` 表驱动测试，**必须包含侦察实测的两个真实样例**：
      `/Users/hjc/Git_Repositories/mac-fleet-hub` → `--Users-hjc-Git_Repositories-mac-fleet-hub--`；
      `/Users/hjc/Library/CloudStorage/OneDrive-个人/Mixed/codes/research`
      → `--Users-hjc-Library-CloudStorage-OneDrive-~4E2A~4EBA-Mixed-codes-research--`。
      另加：反斜杠、冒号、连续分隔符折叠、空串报错、超长截断到 251。
- [ ] 3. `signDSHCookie` 固定向量测试：给定 secret/authority/时间戳，断言
      `v1.<b64url(body)>.<b64url(HMAC-SHA256(secret, body))>`，并断言
      `expiresAt-issuedAt > 30d` 时报错。
- [ ] 4. `dshEndpointFromLog`：多行取最后一条、文件不存在、无匹配 → 明确错误。
- [ ] 5. Commit: `feat(agent): DSH 端点发现与 cookie 自签`

---

## Phase C：线协议客户端（`dsh_client.go`）

**Files**：新增 `mac/fleet-agent/dsh_client.go`、`dsh_client_test.go`

**Interfaces**：

```go
type dshClient struct{ /* baseURL, cookie, http.Client, ws conn, mu */ }

func newDSHClient(baseURL string, cookie string) *dshClient
// call 一元 RPC：POST /api/<endpoint>，返回 result.value 或翻译后的稳定错误。
func (c *dshClient) call(ctx context.Context, endpoint string, args map[string]any) (json.RawMessage, error)
// openStream 在 remote.mux 上开一条逻辑流。
func (c *dshClient) openStream(ctx context.Context, endpoint string, args map[string]any) (*dshStream, error)

var (
    errDSHHostUnavailable = errors.New("dsh_host_unavailable")
    errDSHAuthFailed      = errors.New("dsh_auth_failed")
    errDSHProtocolChanged = errors.New("dsh_protocol_changed")
    errDSHSessionNotFound = errors.New("dsh_session_not_found")
)
```

**Steps**

- [ ] 1. 一元 envelope 编解码测试（`httptest.Server` 当假 host）：断言请求体是
      `{"type":"client-request","rpcId":…,"method":"<endpoint>","payload":{"args":{…}}}`，
      且**带 `Host: 127.0.0.1:<port>`、不带 `Origin`**（规格「请求约束」）。→ 红 → 实现 → 绿。
- [ ] 2. 错误翻译测试：`{"ok":false,"error":{"code":"session/not-found"}}` → `errDSHSessionNotFound`；
      `gateway/signature-invalid` → `errDSHProtocolChanged`；HTTP 401 → `errDSHAuthFailed`；
      连接失败 → `errDSHHostUnavailable`。
- [ ] 3. `remote.mux` 多路复用测试（`httptest` + gorilla upgrader）：两条并发 streamId 不串帧；
      `item`/`end`/`error` 正确分发；`end` 后 stream 关闭。→ 红 → 实现 → 绿。
- [ ] 4. 心跳/断线：服务端主动关闭 → stream 收到错误、连接标记失效、触发一次重连回调。
- [ ] 5. Commit: `feat(agent): DSH 线协议客户端（一元 RPC + remote.mux 多路复用）`

---

## Phase D：对真实 Desktop 跑 G1–G3 验证门

不进 `go test ./...`，写成带 build tag `//go:build dshlive` 的集成测试，手动触发。

- [ ] G1：`FLEET_DSH_LIVE=1 go test -tags dshlive ./... -run TestLiveSessionList`
      → `session/list`（参数名 `_request`）返回 `ok:true` + 真实会话列表。
- [ ] G2：`TestLiveApprovalWaterfall` → 打开 `$events` 拿到 `clientId`；由人在 Desktop 触发一次审批，
      断言收到 `approval/request`。
- [ ] G3：`TestLiveChunkExpansion` → 抓一条含 `text-chunks` 的历史，按 `dt` 展开后与 Desktop 显示逐字一致。
- [ ] **任一门与预期不符 → 停下改规格，不带着错误假设写 Phase E。**

---

## Phase E：chatBackend 实现（`dsh_chat.go`）

**Files**：新增 `mac/fleet-agent/dsh_chat.go`、`dsh_eventmap.go`、`dsh_eventmap_test.go`、`dsh_chat_test.go`、
`mac/fleet-agent/testdata/dsh/*.json`（真实帧 fixture）

**Interfaces**：实现现有 `chatBackend` 全部方法（`Start`/`Resume`/`History`/`Skills`/`Input`/`Steer`/
`Events`/`Respond`/`Interrupt`/`Release`/`Settings`/`Control`）。

**Steps**

- [ ] 1. 先落 fixture：把 Phase D 抓到的真实帧存进 `testdata/dsh/`。
- [ ] 2. `dsh_eventmap_test.go`：按规格「事件映射表」逐行表驱动，输入 fixture，断言完整 `ChatEvent`。
      → 红 → 实现映射 → 绿。
- [ ] 3. 打包行展开测试：`text-chunks`/`reasoning-chunks`/`tool-call-chunks` 三种，`dt` 与成员数一致。
- [ ] 4. 未映射类型 → 返回空且不 panic（显式列出，不静默吞未来类型）。
- [ ] 5. `{type:'cancel', eventId}` → `interaction_resolved`。
- [ ] 6. `dsh_chat_test.go`（假 host，参照 `chat_handlers_test.go`）：start → input → follow → 审批往返 →
      interrupt → release 全序列。
- [ ] 7. `requestId` 对账：收到 `agent/inbox/spliced` 后本地队列项精确移除。
- [ ] 8. `read_only` 模式下写操作被拒。
- [ ] 9. Commit: `feat(agent): DSH 自绘聊天后端`

---

## Phase F：磁盘降级扫描（`dsh_scan.go`）

- [ ] 1. 测试：构造临时 `$DSH_HOME/sessions/--x--/<id>/session.jsonl.zstd`（用 zstd 压缩单帧 header），
      断言只解第一帧即得 id/cwd/createdAt 且子代理会话被排除。→ 红 → 实现 → 绿。
- [ ] 2. 文件非 zstd / 空文件 / 目录不存在 → 跳过并返回空列表，不报错。
- [ ] 3. Commit: `feat(agent): DSH host 不可用时的磁盘会话扫描`

---

## Phase G：接线、配置、安装

- [ ] 1. `main.go`：`normAssistant` 加 `"dsh"`；`handleSessions` 加 dsh 分支（host 优先，失败降级）；
      `scanSessionsFor`/`cwdOfFor` 分发；`/api/info` 加 `dsh` 能力块（规格「配置」节的确切 JSON）。
- [ ] 2. `cfg` 增加 `DSHEnabled`/`DSHHome`/`DSHLog`/`DSHEndpoint`，读环境变量。
- [ ] 3. `com.macfleet.fleet-agent.plist` 加三个 key + 占位符注释更新。
- [ ] 4. `setup-mac.sh` 渲染占位符 + 探测 `/Applications/DSH Desktop.app`。
- [ ] 5. `tests/setup-mac-shared_test.sh` 补一条断言：plist 渲染后含 `FLEET_DSH_HOME`。
- [ ] 6. Commit: `feat(agent): 接入 DSH 配置与安装流程`

---

## Phase H：Dashboard

- [ ] 1. `index.html` 两处 seg 各加 `<button data-assistant="dsh">DeepSeek</button>`。
- [ ] 2. `app.js`：`state.assistant` 三值校验；`assistantLabel` 加分支；`canSelfDrawChat` 读能力；
      41 处硬编码 `assistant: 'codex'` 改读当前 assistant。
- [ ] 3. 审批卡片按 `allowedDecisions` 渲染按钮（DSH 两个，Codex 维持现状）。
- [ ] 4. 降级横幅。
- [ ] 5. `chat_model.test.mjs` **零改动**并全绿（契约未破坏的证据）。
- [ ] 6. Commit: `feat(dashboard): 增加 DeepSeek assistant`

---

## Phase I：收口

- [ ] 1. `bash scripts/verify.sh` 全绿（贴真实输出）。
- [ ] 2. 重建 `mac/fleet-agent/dist/` 双架构产物：
      `GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/fleet-agent-darwin-arm64 .`
      （amd64 同理）。**重建 ≠ 已部署**。
- [ ] 3. CHANGELOG 条目。
- [ ] 4. Commit: `build(agent): 重建 DSH 接入产物` + `docs: CHANGELOG`

---

## Phase J–K：PR、部署、验证

- [ ] 1. `git push -u origin feat/dsh-shared-attach` + 提 PR（base `master`）。
- [ ] 2. 合并后部署：dashboard 走静态发布（网关 `git pull` + `cp` 到 `/var/www/fleet/`）；
      agent 走现有正式通道 —— **必须由持有 Developer ID 的签名构建机执行
      `bash scripts/release-fleet-agent.sh`**，不得在其他 Mac 本地编译后覆盖生产分发源。
- [ ] 3. 线上验证：`curl -I` 入口、`/mN/api/info` 出现 `dsh` 能力块、
      真实浏览器里出现「DeepSeek」tab 并列出 Desktop 会话（截图/输出为证）。
- [ ] 4. `FLEET_DSH_ENABLED` 先在**一台** Mac 打开，验证通过再逐台放量（规格的灰度设计）。

---

## 明确不做（防止范围蔓延）

- 不做 isolated 模式、不接 ACP/SDK、不代理 DSH 原生 GUI、不接管模型凭据、不改 nginx、不改 `chat_model.js`。
