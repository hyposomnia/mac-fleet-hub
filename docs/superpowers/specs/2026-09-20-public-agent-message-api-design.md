# Fleet 公网 AI 会话消息 API v1

## 1. 目标

在 Fleet 网关提供一组由访问密钥保护的公网异步 API，使外部系统可以向指定内网 Mac 上的
Codex 或 DeepSeek 项目会话发送一条用户消息，并通过查询接口或回调地址获取最终 AI 回复。

v1 只暴露两条业务接口：

1. `POST /api/v1/messages`：提交消息，立即返回 `message_id`。
2. `GET /api/v1/messages/{message_id}`：查询排队、执行、失败或完成状态；完成时返回 AI 消息。

网页「会话设置」新增「API 访问」页签，用来生成、轮换和撤销访问密钥。访问密钥不复用
Authelia 登录态；调用公网 API 时不需要浏览器 Cookie。

## 2. 核心决定

- 公网协议使用 `codex`、`deepseek` 两个稳定名称；`deepseek` 在内部映射到现有 `dsh` backend。
- `device` 默认使用网页设备显示名称（如 `Mac mini M4`、`mac-dev`），兼容稳定设备 ID（如 `m1`）。
- `project` 默认使用项目名称，兼容目标 Mac 上项目目录的绝对路径。项目名称重名时返回全部同名项目路径，
  要求调用方改用路径。
- `session` 默认使用会话名称，兼容会话 ID。会话名称重名时返回全部同名会话 ID，要求调用方改用 ID。
- `session` 省略或为 `null` 时，在 `project` 下新建会话；传入时恢复该会话，并校验它属于同一
  `ai_client` 和 `project`。
- 设备名称/ID、项目名称/路径、会话名称/ID 的匹配全部大小写不敏感，名称只做完整匹配，不做模糊匹配。
- 提交接口是异步接口。请求通过校验并持久化后返回 HTTP `202`，不等待设备上线或 AI 完成。
- 同一设备、AI 客户端和会话中的消息严格按接收顺序执行，不并发写同一会话。
- v1 回调只推送终态 `completed` / `failed`，不推送 token 流或中间工具事件。
- v1 不提供审批/提问回答接口。执行中出现必须由人回答的审批或问题时，任务终止为 `failed`，
  错误码为 `interaction_required`，避免任务无限挂起或静默自动授权。
- 队列、请求正文、结果和回调投递记录必须持久化；网关或目标 agent 重启后可以继续执行或恢复查询。

## 3. 认证

所有 `/api/v1/*` 请求使用 Bearer Token：

```http
Authorization: Bearer mfh_live_<secret>
Content-Type: application/json
```

约束：

- 密钥不得放在 URL 查询参数或 JSON body 中。
- 密钥格式为 `mfh_live_` 加 256 bit 随机值的 base64url 编码。
- 网关仅保存密钥的 SHA-256 摘要、前缀和审计元数据，不保存可再次展示的明文。
- 比较摘要时使用常量时间比较。
- 系统 v1 同时只有一个有效密钥；轮换后旧密钥立即不能发起新请求。
- 任务属于这套 Fleet 部署，而不绑定某一代密钥；轮换后的新密钥仍可查询保留期内的旧任务。
- 密钥只在创建/轮换成功的响应和网页中显示一次，刷新后不能找回，只能再次轮换。
- 该密钥具有所有 Fleet 设备的消息执行权限，设置页必须明确展示这一权限范围。

认证失败统一返回 HTTP `401`：

```json
{
  "error": {
    "code": "invalid_access_key",
    "message": "访问密钥无效或已撤销",
    "request_id": "req_01K5..."
  }
}
```

## 4. 提交消息

### `POST /api/v1/messages`

提交一条用户消息。服务端完成同步参数校验和持久化后立即返回，不等待目标 Mac 或 AI。

### 请求头

```http
Authorization: Bearer mfh_live_<secret>
Content-Type: application/json
Idempotency-Key: request-20260920-0001
```

这里不是两个访问密钥：

- `Authorization` 中的值是设置页生成的**访问密钥**，用于证明调用权限，所有请求可复用同一个值。
- `Idempotency-Key` 是调用方为**本次业务请求**生成的可选幂等请求号，不是秘密，也不授予任何权限；
  每条新消息应使用不同的值，只有网络重试同一条消息时才复用。

`Idempotency-Key` 可选但强烈建议传入，因为提交成功后的网络断开可能让调用方收不到 `message_id`；
没有幂等请求号就无法安全重试，重试可能让 AI 执行两遍：

- 长度 1～128 字符，只允许可打印 ASCII。
- 同一调用方在 24 小时内以相同 key 和相同请求体重试，返回原 `message_id`，不会重复给 AI 发消息。
- 相同 key 配不同请求体返回 HTTP `409 idempotency_conflict`。

### 请求体

```json
{
  "device": "Mac mini M4",
  "ai_client": "codex",
  "project": "example",
  "session": "修复登录测试",
  "message": "检查当前项目的测试失败原因，并给出修复建议。",
  "callback_url": "https://example.com/hooks/fleet-ai"
}
```

| 字段 | 类型 | 必填 | 规则 |
|---|---|---:|---|
| `device` | string | 是 | 设备显示名称，兼容稳定设备 ID；大小写不敏感；1～128 字符 |
| `ai_client` | enum | 是 | `codex` 或 `deepseek` |
| `project` | string | 是 | 项目名称，兼容项目绝对路径；大小写不敏感；最长 4096 字符 |
| `session` | string/null | 否 | 会话名称，兼容会话 ID；大小写不敏感；省略/`null` 表示新建会话 |
| `message` | string | 是 | 用户消息；去除首尾空白后不得为空；UTF-8，最大 200 KiB |
| `callback_url` | string/null | 否 | 接收终态通知的公网 HTTPS URL；最长 2048 字符 |

### 目标解析规则

提交接口在返回 `202` 前完成设备、项目和已有会话的定位，但不等待 AI 执行：

1. 所有输入先去除首尾空白，再做 Unicode 大小写不敏感的完整匹配。
2. `device` 先按显示名称匹配；没有同名设备时再按稳定设备 ID 匹配。显示名称重名时返回
   `409 ambiguous_device`，并在 `error.details.candidates` 返回所有候选设备 ID。
3. `project` 是绝对路径时按路径匹配，否则按项目名称匹配。项目名称重名时返回
   `409 ambiguous_project`，并返回所有同名项目的完整路径。
4. `session` 符合现有会话 ID 时优先按 ID 匹配，否则按会话名称匹配。会话名称重名时返回
   `409 ambiguous_session`，并返回所有同名会话的 ID。
5. 路径和 ID 匹配成功后，服务端始终使用目标设备返回的规范大小写和规范值执行，不使用调用方输入的大小写。
6. `session` 传入时必须属于已经解析出的 `project`；不属于时返回 `409 session_project_mismatch`。

项目重名示例：

```json
{
  "error": {
    "code": "ambiguous_project",
    "message": "项目名称 example 不唯一，请改用项目路径",
    "request_id": "req_01K5...",
    "details": {
      "candidates": [
        { "name": "example", "path": "/Users/alice/work/example" },
        { "name": "Example", "path": "/Users/alice/archive/Example" }
      ]
    }
  }
}
```

会话重名示例：

```json
{
  "error": {
    "code": "ambiguous_session",
    "message": "会话名称 修复登录测试 不唯一，请改用会话 ID",
    "request_id": "req_01K5...",
    "details": {
      "candidates": [
        { "name": "修复登录测试", "id": "019f1b4d-9b3a-7d40-a66e-123456789abc" },
        { "name": "修复登录测试", "id": "019f1c88-02d5-77a1-9125-abcdef012345" }
      ]
    }
  }
}
```

v1 不支持附件、图片、skills、模型切换、推理档位或调用方指定权限模式；这些字段出现时按未知字段返回
`400 invalid_request`，防止调用方误以为参数已经生效。

### 成功响应

HTTP `202 Accepted`

```json
{
  "message_id": "msg_01K5C8Y0J7M4K9S5R2AQPN6XTT"
}
```

服务端响应头同时返回：

```http
Location: /api/v1/messages/msg_01K5C8Y0J7M4K9S5R2AQPN6XTT
```

如果命中 `Idempotency-Key`，仍返回同一个 HTTP `202` 和同一个 `message_id`。

### 同步校验错误

提交阶段检查认证、JSON、字段范围、callback URL，以及设备、项目和已有会话的定位。目标设备不可达时
返回 `503 device_unavailable`，不创建 `message_id`；定位超时上限为 5 秒。成功返回 `202` 只表示目标已经
唯一确定且请求已经持久化，不表示 AI 已经开始执行。请求被接受后设备掉线、AI backend 故障等问题，才会
通过查询和回调以 `failed` 终态呈现。

## 5. 查询执行情况

### `GET /api/v1/messages/{message_id}`

```http
GET /api/v1/messages/msg_01K5C8Y0J7M4K9S5R2AQPN6XTT HTTP/1.1
Authorization: Bearer mfh_live_<secret>
```

HTTP `200 OK`：

```json
{
  "message_id": "msg_01K5C8Y0J7M4K9S5R2AQPN6XTT",
  "status": "completed",
  "device": {
    "id": "m1",
    "name": "Mac mini M4"
  },
  "ai_client": "codex",
  "project": "/Users/alice/Git_Repositories/example",
  "project_name": "example",
  "session_id": "019f1b4d-9b3a-7d40-a66e-123456789abc",
  "session_name": "修复登录测试",
  "ai_message": {
    "role": "assistant",
    "content": "失败来自……建议修改……",
    "format": "markdown"
  },
  "created_at": "2026-09-20T10:21:15.231Z",
  "started_at": "2026-09-20T10:21:16.044Z",
  "completed_at": "2026-09-20T10:22:03.902Z",
  "callback": {
    "status": "delivered",
    "attempts": 1,
    "last_attempt_at": "2026-09-20T10:22:04.113Z"
  }
}
```

查询与回调不再回显可能大小写不准确的定位输入，而是返回解析后的规范值：`device.name`、项目规范路径
`project` 与名称 `project_name`、会话稳定标识 `session_id` 与名称 `session_name`。后续调用继续会话时，
优先使用这里返回的 `session_id`；项目重名环境下优先使用这里返回的规范路径。

### 状态枚举

| `status` | 含义 | 终态 |
|---|---|---:|
| `queued` | 请求已持久化，正在等待设备上线、前序消息完成、外部 writer 释放或 worker 调度 | 否 |
| `running` | 目标 AI 已接受本条消息，对应 turn 正在执行 | 否 |
| `failed` | 无法继续执行；返回结构化 `error` | 是 |
| `completed` | 对应 turn 已正常结束；返回 `ai_message` | 是 |

状态只允许按以下方向变化：

```text
queued -> running -> completed
   |         |
   +-------> failed
```

`queued` 不代表设备当前在线，只代表消息已经可靠接收。任务在队列中超过 24 小时仍未开始时转为
`failed / queue_timeout`；单次运行超过 2 小时转为 `failed / execution_timeout`。两个时长是服务端默认值，
后续可以做成管理员配置，但不放进单次请求参数。

### 各状态的响应差异

`queued` 示例：

```json
{
  "message_id": "msg_01K5C8Y0J7M4K9S5R2AQPN6XTT",
  "status": "queued",
  "device": { "id": "m1", "name": "Mac mini M4" },
  "ai_client": "deepseek",
  "project": "/Users/alice/Git_Repositories/example",
  "project_name": "example",
  "session_id": null,
  "created_at": "2026-09-20T10:21:15.231Z"
}
```

新建会话的任务在 `queued` 阶段 `session_id` 为 `null`；创建成功后，`running`、`completed` 或
`failed` 响应会返回实际 `session_id`。调用方应保存它，后续消息才能继续同一会话。

`running` 示例：

```json
{
  "message_id": "msg_01K5C8Y0J7M4K9S5R2AQPN6XTT",
  "status": "running",
  "device": { "id": "m1", "name": "Mac mini M4" },
  "ai_client": "codex",
  "project": "/Users/alice/Git_Repositories/example",
  "project_name": "example",
  "session_id": "019f1b4d-9b3a-7d40-a66e-123456789abc",
  "session_name": "修复登录测试",
  "created_at": "2026-09-20T10:21:15.231Z",
  "started_at": "2026-09-20T10:21:16.044Z"
}
```

`failed` 示例：

```json
{
  "message_id": "msg_01K5C8Y0J7M4K9S5R2AQPN6XTT",
  "status": "failed",
  "device": { "id": "m1", "name": "Mac mini M4" },
  "ai_client": "deepseek",
  "project": "/Users/alice/Git_Repositories/example",
  "project_name": "example",
  "session_id": null,
  "error": {
    "code": "ai_client_unavailable",
    "message": "目标设备上的 DeepSeek Desktop 当前不可用",
    "retryable": true
  },
  "created_at": "2026-09-20T10:21:15.231Z",
  "failed_at": "2026-09-20T10:21:17.488Z"
}
```

## 6. 回调

存在 `callback_url` 时，任务进入 `completed` 或 `failed` 后由网关发送一次逻辑事件。网络失败会重试，
但不会改变消息本身的终态。

### 完成回调

```http
POST /hooks/fleet-ai HTTP/1.1
Content-Type: application/json
User-Agent: mac-fleet-hub-webhook/1.0
X-Fleet-Event-Id: evt_01K5...
X-Fleet-Timestamp: 1789870924
X-Fleet-Signature: v1=6f0c...
```

```json
{
  "event": "message.completed",
  "message_id": "msg_01K5C8Y0J7M4K9S5R2AQPN6XTT",
  "status": "completed",
  "device": { "id": "m1", "name": "Mac mini M4" },
  "ai_client": "codex",
  "project": "/Users/alice/Git_Repositories/example",
  "project_name": "example",
  "session_id": "019f1b4d-9b3a-7d40-a66e-123456789abc",
  "session_name": "修复登录测试",
  "ai_message": {
    "role": "assistant",
    "content": "失败来自……建议修改……",
    "format": "markdown"
  },
  "completed_at": "2026-09-20T10:22:03.902Z"
}
```

这满足最小消费条件：回调方只需要读取 `message_id` 和 `ai_message`。

### 失败回调

```json
{
  "event": "message.failed",
  "message_id": "msg_01K5C8Y0J7M4K9S5R2AQPN6XTT",
  "status": "failed",
  "device": { "id": "m1", "name": "Mac mini M4" },
  "ai_client": "deepseek",
  "project": "/Users/alice/Git_Repositories/example",
  "project_name": "example",
  "session_id": null,
  "error": {
    "code": "ai_client_unavailable",
    "message": "目标设备上的 DeepSeek Desktop 当前不可用",
    "retryable": true
  },
  "failed_at": "2026-09-20T10:21:17.488Z"
}
```

### 签名验证

签名密钥为 `SHA256(access_key)`，签名内容为：

```text
HMAC-SHA256(signing_key, X-Fleet-Timestamp + "." + raw_request_body)
```

结果以小写十六进制放入 `X-Fleet-Signature: v1=<hex>`。回调方应：

1. 拒绝与本机时间相差超过 5 分钟的时间戳。
2. 使用收到该任务时的访问密钥计算摘要和 HMAC。
3. 常量时间比较签名。
4. 按 `X-Fleet-Event-Id` 去重；回调保证至少一次投递，不保证只投递一次。

密钥轮换不影响已经接收的任务：网关随任务保留其不可逆的签名密钥摘要，只用于该任务的回调签名；
调用方也应保留旧密钥，直到旧任务全部进入终态且回调完成。

### 投递规则

- 回调方在 10 秒内返回任意 `2xx` 即视为成功。
- 连接错误、超时或非 `2xx` 按 `0s、10s、1m、5m、30m、2h` 最多尝试 6 次。
- 每次重试使用同一个 `X-Fleet-Event-Id`，时间戳和签名按本次请求重新生成。
- 不跟随 HTTP 重定向。
- 最终投递失败时，消息仍保持 `completed` / `failed`；查询响应中的 `callback.status` 为
  `delivery_failed`。

### Callback URL 安全约束

为避免把网关变成 SSRF 跳板：

- 只允许 `https://`，禁止 URL 用户名/密码和片段。
- DNS 解析结果不得是 loopback、link-local、私网、CGNAT、mesh 或其他保留地址。
- 每次投递和重试都重新解析并重新校验全部 A/AAAA 地址，防止 DNS rebinding。
- 不跟随重定向。
- 域名、目标端口和最终连接地址写入审计日志，但不记录请求消息或 AI 正文。

## 7. 错误格式与错误码

同步 HTTP 错误统一使用：

```json
{
  "error": {
    "code": "invalid_request",
    "message": "message 不能为空",
    "request_id": "req_01K5..."
  }
}
```

| HTTP | `code` | 场景 |
|---:|---|---|
| 400 | `invalid_request` | JSON、字段、长度或 URL 不合法 |
| 401 | `invalid_access_key` | 密钥缺失、无效或已撤销 |
| 404 | `message_not_found` | `message_id` 不存在或结果已过保留期 |
| 404 | `device_not_found` | 找不到设备显示名称或兼容 ID |
| 404 | `project_not_found` | 找不到项目名称或兼容路径 |
| 404 | `session_not_found` | 找不到会话名称或兼容 ID |
| 409 | `ambiguous_device` | 多台设备使用同一显示名；`details.candidates` 返回设备 ID |
| 409 | `ambiguous_project` | 项目名称重名；`details.candidates` 返回全部项目路径 |
| 409 | `ambiguous_session` | 会话名称重名；`details.candidates` 返回全部会话 ID |
| 409 | `session_project_mismatch` | 会话不属于指定项目 |
| 409 | `idempotency_conflict` | 相同幂等 key 对应不同请求体 |
| 415 | `unsupported_media_type` | 非 `application/json` |
| 429 | `rate_limited` | 超过速率或待执行任务上限 |
| 503 | `device_unavailable` | 目标设备不可达，无法完成项目/会话定位 |
| 500 | `internal_error` | 网关内部错误；不得泄露路径、密钥或栈 |

异步任务常见 `error.code`：

| `code` | `retryable` | 含义 |
|---|---:|---|
| `device_offline` | true | 设备在队列期限内始终不可达 |
| `project_not_found` | false | 请求接受后项目被移动或删除 |
| `session_not_found` | false | 请求接受后指定会话被删除 |
| `session_project_mismatch` | false | 请求接受后会话的项目归属发生变化 |
| `ai_client_unavailable` | true | Codex app-server 或 DeepSeek Desktop/backend 不可用 |
| `external_writer_timeout` | true | 会话持续被 Desktop/其他客户端占用，等待超时 |
| `interaction_required` | false | AI 请求人工审批或补充回答，v1 无法处理 |
| `queue_timeout` | true | 超过 24 小时仍未开始 |
| `execution_timeout` | true | 单次执行超过 2 小时 |
| `execution_failed` | 视底层错误 | AI/backend 返回明确失败 |
| `delivery_uncertain` | true | 非幂等底层 RPC 结果无法确认；系统不会自动重放 |

## 8. 网页密钥设置

在现有「会话设置」弹窗增加第三个页签「API 访问」。页面包括：

- 当前状态：未启用 / 已启用。
- 当前 key 前缀（例如 `mfh_live_Z4J8…`），不显示完整密钥。
- 创建时间、最近使用时间。
- 「生成访问密钥」或「轮换密钥」。
- 「撤销密钥」。
- 权限警告：持有者可向所有已纳管设备的 Codex/DeepSeek 会话发送消息。
- 一次性密钥展示框和复制按钮；用户关闭后不可恢复。

这些管理接口继续由 Authelia 登录态保护，不接受访问密钥本身：

### `GET /api/settings/access-key`

```json
{
  "enabled": true,
  "prefix": "mfh_live_Z4J8",
  "created_at": "2026-09-20T09:00:00Z",
  "last_used_at": "2026-09-20T10:21:15Z"
}
```

### `POST /api/settings/access-key/rotate`

生成首个密钥或轮换现有密钥：

```json
{
  "key": "mfh_live_<only-shown-once>",
  "prefix": "mfh_live_Z4J8",
  "created_at": "2026-09-20T09:00:00Z"
}
```

### `DELETE /api/settings/access-key`

撤销当前密钥，返回 HTTP `204`。撤销只阻止新的公网 API 调用；已经可靠接收的任务继续执行和回调，
避免出现“调用方不知道任务是否实际执行”的不确定状态。

访问密钥及其摘要不得混入现有 `GET /api/settings` 的 dashboard 偏好 JSON，防止普通设置保存时意外覆盖。

## 9. 执行与现有 Fleet 会话的关系

网关 API worker 不直接实现 Codex/DeepSeek 协议，而是调用目标 Mac 现有 fleet-agent 能力：

```text
公网调用方
  -> Fleet 网关 /api/v1/messages（鉴权、持久队列、状态、callback）
    -> /mN/api/chat/start|resume（新建/恢复会话）
    -> /mN/api/chat/queue（持久提交消息）
    -> /mN/api/chat/events + control/history（确认对应 turn 的终态与最终回复）
      -> Codex app-server 或 DeepSeek Desktop host
```

必须遵守现有 writer 规则：

- 发现 Desktop/其他外部 writer 正在执行时只等待，不强制接管、不打断用户的活动 turn。
- 公网 API 自己启动的消息由现有目标机持久队列串行处理。
- 以请求自己的 `clientMessageId = message_id` 做端到端对账；非幂等写结果未知时不得盲目重放。
- 只有与该 `message_id` 对应的 turn 正常结束并得到最终 `assistant_done`，才能标记 `completed`。
- 工具输出、推理片段和中间 delta 不写入 `ai_message`；v1 只保存最终 assistant Markdown 正文。

## 10. 持久化、保留与隐私

- 网关必须在返回 `202` 之前完成任务落盘。
- v1 使用网关本地原子 JSON 状态文件保存 job、幂等记录和 callback delivery；所有读写由单进程互斥锁串行化，
  写入采用同目录临时文件 `fsync` 后原子 rename，返回 `202` 前必须落盘成功。数据量增长后可迁移到 SQLite，
  但不得改变公网协议。
- 数据文件和备份权限为 `0600`，运行目录为 `0700`。
- 终态任务默认保留 7 天，之后删除请求正文、AI 回复和 callback 记录；查询返回
  `404 message_not_found`。
- 待执行/执行中的任务不因保留清理而删除。
- 普通日志只记录 `request_id`、`message_id`、设备 ID、状态、错误码和耗时，不记录 access key、用户消息、
  AI 回复或 callback 签名。
- 这是相对当前架构的重要隐私变化：为了支持网关侧查询和回调，用户消息与最终 AI 回复会暂存在网关，
  不再只存在目标 Mac。设置页与部署文档必须明确说明。

## 11. 限流与容量默认值

- 单访问密钥：提交接口 60 次/分钟；查询接口 300 次/分钟。
- 全局未进入终态的任务最多 1000 条；超过后提交返回 `429 queue_full`。
- 单个 `message_id` 建议轮询间隔不低于 2 秒。
- 查询响应可返回 `Retry-After: 2` 提示下一次轮询时间。

## 12. curl 示例

### 新建会话并发送消息

```bash
curl -sS https://fleet.example.com/api/v1/messages \
  -H 'Authorization: Bearer mfh_live_REDACTED' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: request-demo-001' \
  --data '{
    "device":"Mac mini M4",
    "ai_client":"codex",
    "project":"example",
    "message":"总结当前项目的架构。",
    "callback_url":"https://example.com/hooks/fleet-ai"
  }'
```

### 继续已有会话

```bash
curl -sS https://fleet.example.com/api/v1/messages \
  -H 'Authorization: Bearer mfh_live_REDACTED' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: request-demo-002' \
  --data '{
    "device":"mac-dev",
    "ai_client":"deepseek",
    "project":"example",
    "session":"架构分析",
    "message":"继续：把刚才的方案拆成三步。"
  }'
```

### 查询

```bash
curl -sS \
  -H 'Authorization: Bearer mfh_live_REDACTED' \
  https://fleet.example.com/api/v1/messages/msg_01K5C8Y0J7M4K9S5R2AQPN6XTT
```

## 13. v1 非目标

- 流式回调、SSE 或 WebSocket。
- 上传图片、文件或 skill。
- 从公网 API 回答审批、用户问题或强制接管 Desktop writer。
- 取消、重试或删除任务的 API。
- 多个访问密钥、按设备/项目细分权限、IP allowlist。
- 设备、项目、会话的公网枚举接口。
- 在单次请求中切换模型、推理强度、service tier 或权限模式。

这些能力以后可以增量加入，不改变本设计中 `message_id`、四状态模型和两个核心端点。
