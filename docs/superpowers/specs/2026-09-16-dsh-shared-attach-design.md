# DeepSeek Harness（DSH）shared 接入设计

## 背景

Fleet 的自绘聊天目前只支持 Codex：`chat.go` / `chat_queue.go` 用 `ChatEvent` 作为面向浏览器的稳定投影，
`handlerChat*` 里 32 处 `assistant == "codex"` 守卫把 Claude 挡在门外（Claude 走 `scanSessions()` + tmux/ttyd 终端）。
现在要把 **DeepSeek Harness** 作为第三个 assistant 接进来，复用同一套自绘链路。

2026-09-16 完成了三轮只读侦察（DSH Desktop 0.8.2 / harness 0.1.2-rc.1），结论记录在
`.agents/dev/memories/dsh-control-plane.md`。三个决定性事实决定了本设计：

1. DSH 有**和它自己浏览器 GUI 同一套**的控制面（75 个生成式类型化 RPC 端点，3 个 stream 端点），
   不是 JSON-RPC、也没有 SSE RPC；一元走 `POST /api/<ns>/<method>`，流式走 WS `/api/remote.mux`。
2. DSH 的「每会话一个活动写入方」是**写给部署方的约定，不是代码强制**：会话日志 `append(path,"a")`
   从不校验文件长度，`storages/*` 头注释直接声明 last-write-wins 且无跨进程锁。
   **同一 host 进程内多客户端是安全的**（每 session 一个 Agent + 有序 Inbox），**两个 host 进程写同一会话会破坏
   seq/帧**。
3. `--host 0.0.0.0` 被 DSH 显式拒绝（理由是会把 RCE 暴露到网络），所以 DSH 只能监听 loopback。

## 目标

1. Dashboard 出现第三个 assistant「DeepSeek」，能力对齐现有 Codex 自绘聊天：会话列表、历史分页、
   发送/排队/steer、流式渲染、审批与提问往返、打断、模型与权限预设、控制态快照。
2. fleet-agent 以**客户端身份**接入 Desktop 正在运行的 DSH host（shared 模式），由 agent 独占持有
   DSH 连接并把结果投影为现有 `ChatEvent`，浏览器不感知 DSH 协议。
3. **不引入第二个 DSH 写入方**：Fleet 绝不自己启动 `dsh` 进程，绝不写 DSH 的 `sessions/` 或 `storages/`。
4. Desktop 未运行或连接中断时**优雅降级**：会话列表退化为只读磁盘扫描，写操作明确报错，不静默失败。
5. 复用现有 `chatBackend` 接口与 `ChatEvent` 契约，**dashboard 的 `chat_model.js` reducer 不做语义改动**。

## 非目标

- **不做 isolated 模式**：不启动 Fleet 专属 DSH host / ACP / SDK 进程（用户已明确选择只做 shared）。
  理由：跨进程无锁，第二个写入方是真实的数据损坏风险；isolated 需要独立的 `DSH_HOME`，
  而独立 home 就看不到 Desktop 的会话历史。
- **不接 ACP / SDK 协议**：`dsh --profile acp` 虽有正式协议契约，但它是第二个写入方且 profile 需自建。
- **不代理 DSH 原生 GUI**：不把 `dsh web` 的网页界面经 nginx 暴露到 `/mN/dsh/`（资产路径 `<base href="/">`
  与 Authelia 前缀冲突，且需要独立域名）。Fleet 只提供自绘界面。
- **不接管模型凭据**：shared 模式下模型调用由 DSH host 自己完成，fleet-agent 不接触
  `DEEPSEEK_API_KEY`，也不需要它。
- **不碰 DSH 的配置面**：不改 `settings.yaml`、`.credentials.yaml`、`profiles/`、`cordis.patch.yml`。
- 不新增第三方依赖（`github.com/gorilla/websocket v1.5.3` 已在 `mac/fleet-agent/go.mod` 中并被
  `codex_appserver.go` 使用）。

## 运行前提

- 目标 Mac 上安装了 **DSH Desktop**（`/Applications/DSH Desktop.app`），且用户**正在运行它**。
  shared 模式下没有 Desktop 就没有可挂的 host，这是本设计的硬前提。
- Desktop 的 harness host 监听 loopback（默认 `127.0.0.1:43129`，dev 版 43130；被占用时回退到临时端口）。
- 默认 `DSH_HOME` = `~/Library/Application Support/dsh-desktop/harness`，
  默认日志 = `~/Library/Logs/DSH Desktop/harness.log`。

## 架构总览

```
浏览器 ──/mN/api/chat/*──> nginx ──> fleet-agent（唯一控制面，与 Codex 共用 chatBackend 接口）
                                        │
                                        ├─ HTTP POST 127.0.0.1:<port>/api/<ns>/<method>   一元 RPC
                                        ├─ WS   127.0.0.1:<port>/api/remote.mux           流（session/follow、
                                        │                                                 session/control、$events）
                                        └─ 只读扫描 $DSH_HOME/sessions/（host 不可用时的列表降级）
                                                     │
                                          DSH Desktop 的 harness host（唯一写入方）
```

关键点：**fleet-agent 是 DSH 的唯一客户端**（Fleet 侧），浏览器只跟 fleet-agent 说话。
DSH 的 WS 连接是 agent↔host 的 loopback 连接，**不暴露给浏览器**，因此 nginx 无需任何改动。

## 组件与文件

| 文件 | 职责 | 变更 |
|---|---|---|
| `mac/fleet-agent/dsh_client.go` | DSH 线协议客户端：一元 envelope、`remote.mux` 多路复用、错误码翻译、重连 | 新建 |
| `mac/fleet-agent/dsh_discovery.go` | 端点与凭据发现：解析 `harness.log` 取 (port, token)、token 换 cookie、`.credentials.yaml` 自签 cookie 兜底 | 新建 |
| `mac/fleet-agent/dsh_chat.go` | `chatBackend` 实现：列表/历史/发送/steer/审批/打断/控制态；`$events` waterfall 订阅；事件 → `ChatEvent` 映射 | 新建 |
| `mac/fleet-agent/dsh_scan.go` | host 不可用时的会话列表降级：扫 `sessions/--<projectKey(cwd)>--/<id>/session.jsonl.zstd` 只解第一帧 | 新建 |
| `mac/fleet-agent/main.go` | `normAssistant` 增加 `"dsh"`；`handleSessions` 增加 dsh 分支（走 `dsh` 列表，host 不可用时降级扫描）；`scanSessionsFor` 分发；`/api/info` 增加 dsh 能力字段；`cfg` 增加 DSH 配置 | 修改 |
| `mac/fleet-agent/chat.go` | 32 处 `assistant != "codex"` 守卫改为按后端能力判定（见「能力化的 assistant 守卫」） | 修改 |
| `mac/fleet-agent/chat_queue.go` | 同样是能力判定；队列键已按 `(assistant, sessionID)` 分区，无需改结构 | 修改 |
| `mac/com.macfleet.fleet-agent.plist` | 注入 `FLEET_DSH_ENABLED` / `FLEET_DSH_HOME` / `FLEET_DSH_LOG` | 修改 |
| `mac/setup-mac.sh` | 渲染上述 plist 占位符；探测 DSH Desktop 是否存在 | 修改 |
| `server/dashboard/index.html` | 第三个 tab「DeepSeek」（两处 seg：桌面 + mobile） | 修改 |
| `server/dashboard/app.js` | `state.assistant` 接受 `'dsh'`；`assistantLabel`；`canSelfDrawChat()` 能力化；41 处 codex 分支中的 provider 判定改为读 `/api/info` 能力 | 修改 |
| `server/dashboard/chat_model.js` | **不改**（契约不变） | — |
| `server/nginx/*` | **不改** | — |
| `scripts/verify.sh` | 已覆盖 `go test ./...`，无需改 | — |

## 发现与认证

### 端点发现（优先级）

1. **解析 `harness.log`**：从文件尾部向前找最后一条匹配 `\bdsh web:\s*(\S+)` 的行
   （与 Desktop 自己用的正则一致），解析出 `http://127.0.0.1:<port>/?token=<token>`，
   同时得到 **port 与 token**。
2. **兜底端口**：读 `~/Library/Application Support/dsh-desktop/http-cache-origin`（内容即
   `http://127.0.0.1:<port>`）。
3. **最终兜底**：按 `43129`（prod）/ `43130`（dev）探测 `GET /`，401 视为"host 在跑但需要凭据"。

发现结果缓存，并在任何 `401`/连接失败时失效重探。

### 认证（两条路径，按顺序尝试）

**路径 1（首选）：token 换 cookie。** `GET http://127.0.0.1:<port>/?token=<token>`（禁止自动跟随重定向），
读 `303` 响应的 `Set-Cookie`。Cookie 名形如 `dsh-auth-<base64url(sha256(authority))>`，
属性 `HttpOnly; SameSite=Strict; Path=/`，默认 30 天。

**路径 2（兜底）：自签 cookie。** 读 `$DSH_HOME/.credentials.yaml` 的
`records['client-connection/browser-session'].payload.secret`（32 随机字节的 base64url），
构造 `v1.<base64url(json body)>.<base64url(HMAC-SHA256(secret, body))>`，
body = `{"version":1,"authority":"127.0.0.1:<port>","issuedAt":<ms>,"expiresAt":<ms>}`，
`expiresAt - issuedAt` **不得超过 30 天**（host 端会校验该上界）。

**请求约束（必须严格遵守，否则 403）**：

- `Host` 必须是 `127.0.0.1:<port>`（信任栅栏先于认证：Host 非 loopback 或 `trustedHosts` 即 403）。
- **不要发送 `Origin` 头**（Go 的 `net/http` 与 gorilla/websocket 默认都不发；若发送，必须等于 Host）。
- **不要发送 `Sec-Fetch-Site`**（值为 `cross-site` 一律 403）。
- 不支持 `Authorization` header，不要尝试。

**安全声明（必须写进代码注释与文档）**：这两条路径都依赖本机文件可读，DSH 官方并未提供第三方集成契约，
它们是**非公开接口**。DSH Desktop 升级可能使其失效；届时 `/api/info` 会报告 `dsh.hostRunning=false`
或 `dsh.authFailed=true`，dashboard 显示明确降级提示而不是空白。这与 Codex shared 模式的
"loopback 不是安全边界"免责声明同性质，**多用户共享 Mac 不得启用 DSH**。

## 数据流

### 1. 会话列表

- **主路径**：一元 `POST /api/session/list`，`payload.args` 为 **`{"_request":{}}`**
  （注意参数名是 `_request`，不是 `request`；传 `{}` 会得到 `gateway/arguments-invalid`）。
  返回 `SessionListValue{items: SessionSummary[]}`；实测每项含
  `sessionId/updatedAt/running/blank/cwd/projections{title,tokenUsage,contextPressure,…}`。
- **过滤**：排除子代理会话（裸 uuid 形态的 id、header `origin == "subagent"` 或 `delegationDepth > 0`），
  与 Codex 侧排除 subagent 的规则对齐。
- **降级路径**：host 不可用时，扫 `$DSH_HOME/sessions/--*/` 下每个 `session.jsonl.zstd`，
  **只解压第一帧**取 header（`{type:"session",version,id,createdAt,cwd,delegationDepth,agentPreset}`），
  得到 id/cwd/时间，无标题无用量。响应里带 `degraded: true`，dashboard 显示"Desktop 未运行，仅显示磁盘会话"。
  该路径**不需要** `projectKey`：cwd 是从 header 读出来的，只正向遍历目录即可。
  （`projectKey(cwd)` 的精确规则已记录在 dev 记忆 `dsh-control-plane.md`，等真出现 cwd→目录反查的调用方再实现。）

### 2. 历史与实时流（`session/follow`）

**参数名与嵌套形状一律以生成产物为准，不要凭直觉写**。实测（写成 `sessionId` 会得到
`gateway/arguments-invalid: missing "request"; unexpected "sessionId"`）：

```
session/follow  args = { "request": { "address": { "kind": "session", "sessionId": "<id>" },
                                      "maxMessages": <可选> } }
session/control args = {}                       // 无参数，全局控制流
session/page    args = { "request": { "address": 同上, "throughSeq": <n>,
                                      "beforeSeq": <可选>, "maxMessages": <可选> } }
```

打开后的帧：

- 首帧 `{type:'snapshot', header, cursor, records[], hasMore, projections}` → 先灌历史再进实时。
- 后续帧是 `SessionEventEntry`；`records` 里可能是 `{type:'event', event}` 或 `{type:'chunks', event}`。
- **`{type:'chunks'}` 必须展开**，且线上形态与存储层文档不同（实测）：

  ```json
  {"type":"chunks","event":{"type":"chunkrow/reasoning-chunks","seq":81369,"time":1789549471330,
   "data":{"turn":1,"step":229,"index":0,"dt":[…],"texts":[…]}}}
  ```

  内层 type 带 `chunkrow/` 前缀（`chunkrow/text-chunks` / `chunkrow/reasoning-chunks` /
  `chunkrow/tool-call-chunks`）；首成员身份是 **`seq` + `time`**（不是 `seq0`/`time0`）；
  成员数 = `texts`（或 `args`）长度；第 i 个成员 `seq = seq + i`、`time = time + Σdt[0..i-1]`；
  **`dt` 长度 = 成员数 − 1**（codec 的 `validateRunData` 强制该等式，写成 N 会判畸形或静默错位）。
  只有连续 ≥3 条同 block 的 `assistant/chunk` 才打包，短 run 仍是明文 `assistant/chunk`，
  因此快照里两种形态并存，读取端必须都能吃。
- 向后分页走 `session/page`，映射为现有 `ChatHistoryPage{cursor}`。

### 3. 发送 / 排队 / steer

一元 `session/prompt`（注意 `request` 包装层）：

```
args = { "request": { requestId: <唯一串>, sessionId, mode: 'queue' | 'steer',
                      content: [{type:'text',text}|{type:'image',mediaType,data,name?}],
                      clientTimeZone? } }
→ {accepted: true}
```

- `requestId` 是**关联标识**：host 会把它回显为持久用户事件的 `data.source.rpcId`
  （落进 `agent/inbox/spliced`）。这正是 Codex 侧 `clientUserMessageId` 的等价物——
  用它做 `user_done` 对账与本地乐观队列项的精确移除。
- Dashboard 的 `deliveryMode` 语义映射：`auto` → 当前有活跃 turn 时 `queue`、否则直接 prompt；
  `next` → `steer`。规则与 Codex 侧保持一致，由 agent 判定，浏览器不判断。
- `session/cancel` 的 args 是 `{request:{sessionId}}` → `{accepted:true}`；host 内部用 `keepInbox: true`
  （只中止轮次、保留排队项），与 Fleet 现有语义一致。

### 4. 审批与提问往返

- agent 在连接建立后**始终**打开一条 `$events` 流（`{type:'open',streamId,endpoint:'$events',payload:{args:{}}}`），
  首帧 `{type:'ready', clientId, host}` 提供 `clientId`，**必须持久保存**（回答案时要用）。
- 收到的 waterfall 事件：`approval/request`、`user-questions/request`
  → 投影为 `interaction_request`。
- 浏览器回答（现有 `POST /api/chat/respond {assistant,sessionId,requestId,response}`）→ agent 翻译并
  `POST /api/$events/result`，`payload.args = {clientId, eventId: requestId, outcome}`：

| 浏览器 `response` | DSH `outcome` |
|---|---|
| `{decision:'accept'}` | `{kind:'result', value:'allowed-once'}` |
| `{decision:'decline'}` | `{kind:'result', value:'rejected'}` |
| `{answers:{...}}` | `{kind:'result', value:{answers:[...]}}`（形状转换见下） |
| 用户主动放弃/请求已失效 | `{kind:'rejected', error}` 或 `{kind:'next'}` |

- **DSH 的审批 outcome 只有 `allowed-once` / `rejected` / `cancelled` / `unavailable`**，没有会话级授权。
  因此 DSH 会话**只渲染「允许一次 / 拒绝」两个按钮**，「本会话允许」按钮按 capability 隐藏
  （v1 不做 settings/mutate 提升预设，列为二期）。
- 提问的 `answers` 形状不同：Fleet 浏览器给的是以 question id 为键的**对象**，DSH 要的是**数组**。
  agent 必须用请求帧里携带的问题描述符逐项转换；转换必须 fixture 测试覆盖。
  问题描述符与答案数组的权威 schema 在
  `@deepseek-ai/dsh-api-session-controller/lib/typert.host.js` 的生成类型里（不是猜测来源）。
- **"先答者胜"**：Desktop GUI 与 Fleet 同时在线时，谁先答谁生效，其余客户端会收到
  `{type:'cancel', eventId}`。agent 必须监听该帧并发出 `interaction_resolved`，
  把 Fleet 界面上的待答卡片清掉——否则用户会对着一个已经失效的审批按钮点。

### 5. 控制态快照（`session/control`）

WS `session/control` 首帧 `{type:'baseline', value:{queues,jobs,…}}`，后续
`{type:'projection', sessionId, key, value, seq}` 与 `{type:'queue', …}` / `{type:'jobs', …}`。
映射为现有 `control_changed` / `queued` 事件，喂给 `buildChatSessionControlSnapshot`。

### 6. 权限与模型预设

- 读：`session/modelCatalog`、`llm/listProviders`。
- 写模型：`session/selectModel`。
- 写权限预设：**DSH 没有专用端点**，走 `settings/mutate`（写入后体现为会话事件
  `sandbox/mode` + `approval/policy`，再经 `session/follow` 回流为 `control_changed`）。

## 事件映射表

DSH 会话事件（`SessionEventMap` 取值域）→ `ChatEvent.Type`。映射表是**契约**，逐行写表驱动测试。

| DSH 事件 / 帧 | 载体 | `ChatEvent.Type` | 说明 |
|---|---|---|---|
| `turn/start` | follow | `turn_started` | turnId 由事件携带的 turn 标识派生 |
| `turn/end` | follow | `turn_done` | `reason` 放进 data；若含 usage 则先发 `turn_usage` |
| `assistant/chunk`（text） | follow | `assistant_delta` | 打包行须先展开（见上） |
| `assistant/chunk`（reasoning） | follow | `reasoning_delta` | |
| `assistant/message` | follow | `assistant_done` | 收口正文 |
| `tool/call` | follow | `tool_update` | status=in_progress，含 name/args |
| `tool/result` | follow | `tool_update` | status=completed/failed |
| `reasoning/*` 汇总 | follow | `reasoning_update` | |
| `todo/write` | follow | `todo_update` | |
| `model/selection` | follow | `control_changed` | |
| `sandbox/mode`、`approval/policy`、`permission/preset` | follow | `control_changed` | |
| `agent-preset/selected` | follow | `control_changed` | |
| `compaction/start\|summary\|end\|prune` | follow | `context_compaction` | |
| `agent/inbox/spliced`（user） | follow | `user_done` | 用 `data.source.rpcId` 对账 `requestId` |
| `approval/asked`、`approval/decided` | follow | `interaction_resolved` | 兜底收口（正常路径走 `$events`） |
| `approval/request` | **$events waterfall** | `interaction_request` | `requestMethod='item/commandExecution/requestApproval'` 或 `item/fileChange/requestApproval` |
| `user-questions/request` | **$events waterfall** | `interaction_request` | `requestMethod='item/tool/requestUserInput'`，填 `questions[]` |
| `{type:'cancel', eventId}` | $events | `interaction_resolved` | 别的客户端先答了，清掉本地待答卡片 |
| `session/control` baseline/queue/jobs/projection | control | `control_changed` / `queued` | |
| `turn/usage`（源自 `turn/end` 或 projection `tokenUsage`） | follow / control | `turn_usage` | 具体字段名以 `typert.host.js` 的 Zod schema 为准 |
| 网关/传输层失败 | — | `error` | 见「错误语义」 |

**不映射**：`request/header`、`request/context`、`session/end-seed`、`step/start`、`step/end`、
`tool/code-dispatch*`、`command/run|done`、`team/*`、`goal/change`、`subagent/descriptor`
（v1 不进 UI，直接丢弃；丢弃必须显式列在 switch 的 default 分支注释里，不能静默吞掉未来新增类型）。

## 能力化的 assistant 守卫

现状是散落的硬编码 `assistant != "codex"`：**handler 层 16 处**（`chat.go` 13、`chat_queue.go` 3）需要能力化；
`codex_chat.go` 里 12 处是 Codex 后端自身的契约检查（保留），`main.go` 里 4 处是路由分发（属 Phase G）。

```go
// chat_capabilities.go
type assistantCapabilities struct {
    SelfDraw bool // 支持自绘聊天面：列表、历史、发送、流式、审批往返
    Queue    bool // 支持服务端持久队列、steer 与访问态控制
}

func chatCapabilities(assistant string) assistantCapabilities
```

- `codex` → 两位均 `true`
- `dsh` → 两位均 `true`（接入完成时打开；实现期先保持 `false`，避免暴露半成品 tab）
- `claude` / 未知 → 两位均 `false`（保持现有行为不变，Claude 仍走终端）

**只保留两个维度，刻意不拆第三个。** 原设计里的 `Interactions`（审批/提问往返）被删掉：handler 层所有
端点问的都是同一个问题——"这个 assistant 有没有自绘聊天面"，审批属于这个面的一部分而不是独立能力；
只有服务端队列/访问态是真正可能缺席的一维（一个只读渲染磁盘会话、不参与排队与 writer 租约的
assistant 就是 `SelfDraw` 有而 `Queue` 无）。等真的出现第三种组合时再拆，不预先发明维度。

handler 层 501 分支改为 `if !chatCapabilities(assistant).SelfDraw { 501 }`；
`chat_queue.go` 的三处改为 `if !chatCapabilities(assistant).Queue || sessionID == "" { … }`
（队列键已是 `(assistant, sessionID)`，结构不变）。

**这一步是纯重构，必须单独一次提交并先跑全绿测试**，确保 Claude 行为零变化，再在其上接 DSH。

## 配置

| 变量 | 默认 | 说明 |
|---|---|---|
| `FLEET_DSH_ENABLED` | `0` | `1` 才启用 DSH 能力（按机器灰度）。默认关，避免未验证就出现在所有 Mac 上 |
| `FLEET_DSH_HOME` | `~/Library/Application Support/dsh-desktop/harness` | 定位 `.credentials.yaml` 与 `sessions/` |
| `FLEET_DSH_LOG` | `~/Library/Logs/DSH Desktop/harness.log` | 端点+token 发现来源 |
| `FLEET_DSH_ENDPOINT` | 空 | 显式覆盖 `127.0.0.1:<port>`（调试用） |

**必须写进 `com.macfleet.fleet-agent.plist`**：fleet-agent 是 launchd 守护进程，环境只来自 plist，
不在 shell 里读 `~/.zshrc`。改 plist 后必须重新 unload/load（`launchctl` 不会自动重载）。

`/api/info` 新增：

```json
"dsh": {
  "enabled": true,
  "desktopAppPath": "/Applications/DSH Desktop.app",
  "hostRunning": true,
  "endpoint": "127.0.0.1:43129",
  "authMode": "token | secret | none",
  "degraded": false
}
```

dashboard 用 `enabled && !degraded` 决定是否显示第三个 tab。

## 错误语义

新增稳定错误码，沿用 `writeChatErr` 与 `chatErrorMessage` 的既有模式（拒绝理由走 `code` 判别，不用字符串匹配）：

| 错误 | 触发 | 浏览器表现 |
|---|---|---|
| `dsh_disabled` | `FLEET_DSH_ENABLED != 1` | 隐藏 tab（正常路径不出现） |
| `dsh_host_unavailable` | 端点探测失败 / Desktop 未运行 | 列表走降级扫描；发送/审批按钮禁用并提示"请先启动 DSH Desktop" |
| `dsh_auth_failed` | 两条凭据路径都失败 | 明确提示凭据不可用 + 降级说明，**不重试轰炸** |
| `dsh_protocol_changed` | `gateway/signature-invalid`、404、schema 解析失败 | 提示"DSH 版本可能已升级"，标记 `degraded` |
| `dsh_read_only` | Fleet 侧 `read_only` 访问模式 | 复用现有 `errFleetChatReadOnly` |
| `dsh_session_not_found` | DSH `session/not-found` | 会话失效提示 |
| `dsh_uncertain` | 非幂等写失败且 `requestId` 对账查不到 | 明确提示"发送结果未知"，不自动重发 |

**重连语义**：WS 断开按指数退避重连（参考 `codex_appserver.go` 的恢复模型：幂等读重试、写不重放）。
Desktop 重启会换 port/token，任何 401/连接拒绝触发一次"失效重探"。
`session/follow` 重连后以 `snapshot` 重建，必须按 cursor/seq 去重，不能重复渲染已显示的增量。
**不重放非幂等写**：`session/prompt` 失败且结果未知时，用 `requestId` 查 `agent/inbox/spliced`
对账；查不到则报 `uncertain`，不盲目重发（与现有 Codex 队列的 `uncertain` 处理一致）。

## Dashboard 改动

1. `index.html` 两处 `seg`（桌面 + mobile）各加一个 `<button data-assistant="dsh">DeepSeek</button>`。
2. `app.js`：`state.assistant` 接受 `'dsh'`（持久化读回时校验三值）；`assistantLabel` 增加分支。
3. `canSelfDrawChat()` 从 `state.assistant === 'codex'` 改为读 `/api/info` 的
   `dsh`/`codex` 能力字段（`selfDraw && cap[state.assistant].selfDraw`）。
4. `respondChatRequest` 里的硬编码 `assistant: 'codex'` 改为 `state.assistant`；
   同类硬编码（`chat/queue`、`chat/history`、`chat/events`、upload 等 41 处）逐一改为读当前 assistant。
5. 审批卡片按 `interaction_request.data.allowedDecisions`（新字段）渲染按钮：
   DSH 只给 `allow_once` / `decline`，Codex 维持现状。
6. 降级横幅：`degraded` 时在会话列表顶部显示「DSH Desktop 未运行，当前仅显示磁盘会话（只读）」。

## 不改什么

- `chat_model.js` reducer：契约不变，零改动。
- `chat_queue.go` 的持久模型结构与磁盘格式。
- nginx 任何 location（浏览器始终走 agent 的 HTTP/SSE，WS 只在 agent↔host 之间）。
- DSH 的任何文件（只读 `.credentials.yaml` / `sessions/` / `harness.log`）。
- 现有 Codex / Claude 链路的行为。

## 验证门（必须先跑完，结果决定后续分支）

这三项在写 `dsh_chat.go` 之前先做，因为是只读/可安全执行的验证；任何一项结果与预期不符，
先回来改本规格再写代码。

| # | 命令 / 动作 | 预期 | 不符时怎么办 |
|---|---|---|---|
| G1 | 用真实 Desktop：解析 `harness.log` 取 token → 换 cookie → `POST /api/session/list {args:{_request:{}}}` | `ok:true` + 真实会话列表 | 若参数名或路径不符，按实际改「数据流 1」 |
| G2 | 打开 `$events` 流后，在 Desktop GUI 里手动触发一次审批，观察 Fleet 侧是否收到 `approval/request` | 收到且 `clientId` 可用于回答案 | 若审批不走 `$events`，改为从 `session/follow` 的 `approval/asked` 推导 |
| G3 | `session/follow` 抓一条含 `text-chunks` 打包行的历史，按 `dt` 展开 | 展开后逐字与 Desktop 显示一致 | 若 codec 与推断不符，按 `dsh-session` 的无损 codec 实现改 |

（**不做**破坏性实验：不启动第二个 host 写同一会话。用户已选择只做 shared，
跨进程破坏形态对本设计不再是决策依据，仅保留为记忆中的风险记录。）

## 测试策略（TDD 顺序）

新增测试全部遵守「先红后绿」，一次只推进一个红→绿。

1. **`dsh_discovery_test.go`**
   - `harness.log` 尾部解析：多条 `dsh web:` 行取最后一条；行被截断/无匹配/文件不存在 → 返回明确错误。
   - `projectKey(cwd)` 表驱动：ASCII、中文（`个` → `~4E2A`）、Windows 反斜杠、冒号、空串抛错、
     `undefined` → `_no-cwd`、超长截断到 251。**用侦察里实测过的两个真实样例做断言**。
   - cookie 自签：给定固定 secret / authority / 时间戳，断言输出与手工计算的 HMAC 串一致（固定向量），
     并断言 `expiresAt - issuedAt > 30d` 时被拒绝。
2. **`dsh_client_test.go`**（`httptest.Server` + `httptest` WS，不需要真实 Desktop）
   - 一元 envelope 编解码：`client-request`/`server-response` 两侧；`payload` 缺 `args` → 本地就报错；
     `ok:false` → 翻译成对应稳定错误码。
   - `remote.mux` 多路复用：两个并发 streamId 不串帧；`item`/`end`/`error` 分发；
     心跳超时 → 连接标记失效并触发重连回调。
   - 信任头：断言发出的请求带 `Host: 127.0.0.1:<port>` 且**不带 `Origin`**。
3. **`dsh_eventmap_test.go`**（映射表逐行表驱动）
   - 「事件映射表」每一行一个用例，输入是**真实抓取的帧 fixture**（放 `mac/fleet-agent/testdata/dsh/`），
     输出断言是完整的 `ChatEvent` 结构。
   - 打包行展开：`text-chunks` / `reasoning-chunks` / `tool-call-chunks` 三种，`dt` 间隔与成员数一致。
   - 未映射类型（`request/header` 等）→ 显式返回空且不 panic。
   - `{type:'cancel', eventId}` → `interaction_resolved`。
4. **`dsh_chat_test.go`**（handler 级，模式参照现有 `chat_handlers_test.go`）
   - 假 host（`httptest`）跑通：start → 发送 → follow 流 → 审批往返 → 打断 → release 的完整序列。
   - 降级：host 不可达时 `chat/history` 走磁盘扫描、`chat/input` 返回 `dsh_host_unavailable`。
   - `read_only` 访问模式下写操作被拒。
   - `requestId` 对账：收到 `agent/inbox/spliced` 后本地队列项被精确移除。
5. **能力化重构的回归**（在接 DSH 之前单独做）
   - 现有 `chat_handlers_test.go` / `chat_test.go` 全绿即证明 Claude/Codex 行为未变；
     另加一个用例断言 `chatCapabilities("claude").SelfDraw == false`（锁住"Claude 仍走终端"）。
6. **dashboard**：`chat_model.test.mjs` 零改动并保持全绿（证明契约未被破坏）。

所有层跑 `bash scripts/verify.sh`，贴真实输出。

## 风险与缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| 依赖非公开接口（token/cookie/内部 RPC） | Desktop 升级即失效 | `degraded` 状态 + 明确提示；协议错误统一映射 `dsh_protocol_changed`；`FLEET_DSH_ENABLED` 默认关，可单机回滚 |
| `harness.log` 权限 644、明文含 token | 本机任意进程可冒充 DSH 客户端 | 这是 DSH 侧既有问题，Fleet 不放大：agent 只读不写、不转发给浏览器、日志里不打印 token/secret |
| Desktop 重启换 port/token | 连接失效 | 401/连接拒绝触发失效重探；WS 指数退避重连 |
| 与 Desktop 抢答审批 | Fleet 界面出现过期按钮 | 监听 `{type:'cancel',eventId}` 清卡片 |
| `session/follow` 重连重复渲染 | 消息重复 | 按 cursor/seq 去重（已有 Codex 侧同类经验） |
| 会话日志无跨进程锁 | 若未来加 isolated 会损坏数据 | v1 明确不做 isolated；写进非目标与记忆 |
| 多用户共享 Mac | cookie 伪造 = 越权 | 文档明确"多用户共享 Mac 不得启用"，与 Codex shared 免责声明同性质 |

## 交付物

1. 五个新 Go 文件 + 两处 Go 修改（`main.go`、`chat.go`/`chat_queue.go` 能力化）。
2. plist + `setup-mac.sh` 的配置注入。
3. dashboard 三处改动（`index.html`、`app.js`；`chat_model.js` 不动）。
4. 测试与 fixture：`mac/fleet-agent/testdata/dsh/` 下的真实帧样本。
5. CHANGELOG 条目。
6. 重建 `mac/fleet-agent/dist/` 双架构产物（改 `main.go` 后必须重建，否则二进制与源码不一致）。
7. **发布走现有正式通道**：`scripts/release-fleet-agent.sh`（签名构建机 Developer ID 签名 + 公证），
   不得在其他 Mac 本地编译后直接覆盖生产分发源。
