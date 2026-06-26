# 终端自动关闭时长可配 + 刷新后终端恢复 — 设计

日期：2026-06-26 · 状态：已与用户对齐架构，待写实现计划

## 背景与问题

- dashboard 的「终端池」(`state.pool`) 是**纯前端内存数组** + 动态 iframe，无任何持久化。刷新 / 关标签页 → 池清空、iframe 销毁、WS 断、tmux detach。
- 各 Mac 上跑 claude 的 **tmux 会话（常驻 pty）不随刷新消失**——只有显式 `/api/close`（⏹）或 agent 的 `reaper()` 才 kill。
- fleet-agent `reaper()`（`mac/fleet-agent/main.go:1027`）每 60s 扫 `fleet-*` tmux：`session_attached==0 && now-activity > IdleSec` → `kill-session`。`IdleSec` 默认 1800（30min），来自启动 env `FLEET_IDLE_SEC`，**运行中不可变**。
- 用户两个诉求：
  1. **刷新后终端保留**——刷新后自动恢复之前打开的终端，不必手动重连。
  2. **整体 pool 同步、保证不爆**——孤儿 tmux 不能因「开了又刷新遗忘」无限累积，撑爆系统 pty。

前一版（commit fdf28e3）加的前端 `poolReapIdle` 是**无效实现**：只 detach 当前页 iframe、不 kill 后台、刷新即失效。真正回收一直是 agent reaper。本设计纠正之。

## 目标 / 非目标

**目标**
- G1：网页上「自动关闭时长」真正驱动各 Mac fleet-agent 的 `IdleSec`，端到端、全局最终一致（任何 Mac 任何时刻上线/重启，最终都用网页设的值）。
- G2：刷新 / 崩溃后，前端自动恢复刷新前打开的终端会话（重新 attach），受池上限约束。

**非目标**
- 不把前端 pool 状态搬到服务端（多设备/多浏览器语义复杂，YAGNI）。
- 不改 reaper 的回收判据（`attached==0 && idle>IdleSec` 不变），只让 `IdleSec` 可运行时配置。
- 不做 push 模型 / 不给 agent 加鉴权 token（端点公开只读，见安全节）。
- 不持久化恢复跨「关标签页」（用 sessionStorage，关闭即清）。

## 设计 A：自动关闭时长端到端可配（G1）

**数据流**
1. 网页改「自动关闭 = N 分钟」→ 保存 → `POST /api/settings`（Authelia）→ fleet-enroll 写 `dashboard-settings.json` 的 `autoCloseMinutes`（normalize 钳 [1,1440]）。*【已存在，复用 commit fdf28e3 字段】*
2. 各 Mac fleet-agent：**启动时 + 每 5min** → `GET https://${DOMAIN}/enroll/agent-config`（公开）→ 拿 `{idleSec: autoCloseMinutes*60}` → 原子更新内存 `IdleSec`。
3. `reaper()`（每 60s，逻辑不变）读当前 `IdleSec` 执行回收。

**各层改动（单一职责）**
- **fleet-enroll**（`server/enroll/main.go`）：加 `GET /agent-config` handler → 返回 `{"idleSec": loadSettings().AutoCloseMinutes*60}`。只读投影，无副作用。注册到 mux。
- **nginx**（`server/nginx/fleet.conf`）：加 `location = /enroll/agent-config { limit_req zone=fleet_enroll burst=10 nodelay; proxy_pass http://127.0.0.1:7090/agent-config; }`——**无 `auth_request`**（公开），与现有 `/enroll/join` 同段同风格。
- **fleet-agent**（`mac/fleet-agent/main.go`）：
  - `IdleSec` 从 `cfg int64` 改为运行时可变：用 `atomic.Int64`（或带 mutex 的 getter/setter）。`FLEET_IDLE_SEC` env 仍作**初始值 + 永久回退默认**。
  - 新增 `configSync()` goroutine：启动 pull 一次 + `time.Ticker(5min)` 周期 pull。网关地址由现成的 `FLEET_UPDATE_BASE`（`https://${DOMAIN}/enroll/dist`，install.sh 写入）推导：strip `/dist` → `<base>/agent-config`。
  - `reaper()` 改读 atomic `IdleSec`。
  - **改 main.go → 重建 dist 双架构（`go build -trimpath -ldflags="-s -w"`）+ 部署三台 Mac（install 新二进制 + `fleet-agent restart`）。**

**回退 / 兼容语义（健壮性）**
- 网关不可达 / pull 失败 / JSON 坏 → **保留当前内存值**（从未成功过则用 env 默认 1800）。reaper 照常跑，故障不停摆。
- 旧网关无 `/agent-config` → GET 404 → 当失败 → 用默认。向后兼容。
- idleSec 越界由 fleet-enroll 端 normalize 已钳 [1,1440] 分钟；agent 侧再做一次 sanity（>0）兜底。

**安全**
- `/agent-config` 公开只读，仅暴露一个超时秒数，无敏感性。**写**仍走 `/api/settings`（Authelia）。
- fleet-enroll 维持「只绑 127.0.0.1 + nginx 可信 XFF」模型——**不**改绑 mesh IP。

## 设计 B：刷新后终端自动恢复（G2）

**机制**：纯前端 `sessionStorage` 快照。
- **写快照**：池变化时（`poolAdd` / `poolDrop` / 权限模式变更）把池序列化为 `[{macId, sessionId, permMode}]` 存 `sessionStorage['fleet-pool']`。只存重建所需的最小标识——`sid`/`url` 是 attach 时新生成的，不存。
- **恢复**：`init()` 末尾读快照，对每条 `connect(macId, sessionId, ..., permMode)` 重新 `api open` + `poolAdd`。受 `poolMax` 上限约束（超出走 `poolEvict`）。恢复时不抢焦点——保持快照里的 `current` 或落到最后一个。
- 无快照 / 解析失败 → 空态，照旧。

**与 reaper 配合**：恢复后这些会话又有 attach client → reaper 不会 kill。无快照时孤儿无 attach → 到 idle 阈值被 reaper 收。两机制正交，互补覆盖「不爆」。

**边界**
- 恢复 N 个会触发 N 次 `api open`（N 个 attach），即刷新前的真实状态，受 `poolMax` 约束，合理。
- 快照里的会话若已被 agent 回收（idle kill）→ `api open` 重新 `ensureTmux` 起新 attach（claude `--resume`），仍可恢复，不报错。

## 测试（TDD）

- **fleet-enroll**（`settings_test.go` 同包）：`/agent-config` handler 返回 `autoCloseMinutes*60`；缺省回退 30→1800；越界经 normalize 钳定。
- **fleet-agent**：`IdleSec` atomic get/set；`configSync` 的 URL 推导（`FLEET_UPDATE_BASE` strip `/dist`）；`{idleSec}` 解析 + 越界 sanity；pull 失败保留旧值。
- **dashboard**：快照序列化/反序列化最小标识；恢复受 `poolMax` 约束（手测 / 真实 Chrome，见 dev 记忆 debug-with-claude-in-chrome）。
- **端到端**（部署后）：网页改值 → `curl https://${DOMAIN}/enroll/agent-config` 看变 → agent 日志/行为；刷新页面 → 终端自动恢复。

## 部署

- dashboard 静态：网关 git pull + `cp -r server/dashboard/. /var/www/fleet/`。
- fleet-enroll：重建 `dist/fleet-enroll-linux-amd64` + 网关 install + `systemctl restart fleet-enroll`。
- nginx：改 `fleet.conf` → backup → `nginx -t` → reload（**高风险实操，按 dev 约定切 /deploy 或显式确认**）。
- fleet-agent：重建 dist 双架构 + 三台 Mac install + restart。
- CHANGELOG 追加一条；撤销 commit fdf28e3 的前端 `poolReapIdle` 假实现。
