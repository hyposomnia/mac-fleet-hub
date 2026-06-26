# 终端自动关闭时长可配 + 刷新后终端恢复 实现计划

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让网页「自动关闭时长」端到端驱动各 Mac fleet-agent 的空闲回收（全局最终一致），并让 dashboard 刷新后自动恢复已打开的终端。

**Architecture:** G1 = dashboard→网关 `dashboard-settings.json`（已存）→ fleet-agent 启动+定期 GET 网关公开只读 `/enroll/agent-config` → 运行时改 `idleSec`（atomic）→ 既有 `reaper()` 读它回收 pty。G2 = 纯前端 `sessionStorage` 池快照，`init()` 时自动重连恢复。撤销前一版无效的前端 `poolReapIdle`。

**Tech Stack:** Go（fleet-agent / fleet-enroll，标准库 net/http、sync/atomic、encoding/json）、nginx、原生 JS（无框架 dashboard）、launchd plist + shell。

**Spec:** `docs/superpowers/specs/2026-06-26-terminal-autoclose-and-refresh-restore-design.md`

---

## File Structure

**Part 1 — G1 后端可配（Design A）**
- Modify `server/enroll/main.go` — 加 `GET /agent-config` 只读端点（投影 `AutoCloseMinutes*60`）。
- Modify `server/enroll/settings_test.go`（或新增 `agentconfig_test.go`，同 package main）— 端点单测。
- Modify `server/nginx/fleet.conf` — 加公开 `location = /enroll/agent-config`。
- Modify `mac/fleet-agent/main.go` — `idleSec` 改运行时可变（atomic）；新增 `configSync()`；`reaper()` 读 atomic。
- Modify `mac/fleet-agent/configsync_test.go`（新增，package main）— URL 推导 / 解析 / 回退单测。
- Modify `mac/com.macfleet.fleet-agent.plist` — 加 `FLEET_CONFIG_URL` 占位 `__FLEET_CONFIG_URL__`。
- Modify `mac/setup-mac.sh` — render 注入 `FLEET_CONFIG_URL`（由 `FLEET_UPDATE_BASE` 推导）。

**Part 2 — G2 刷新恢复（Design B）**
- Modify `server/dashboard/app.js` — 删 `poolReapIdle` + 其 `setInterval`；`poolAdd`/`poolDrop` 内写 `sessionStorage` 快照；`init()` 末尾恢复；hint 文案。
- Modify `server/dashboard/index.html` — 改「自动关闭」hint 文案。

**Part 3 — 收尾**
- 重建 `server/enroll/dist/fleet-enroll-linux-amd64` + `mac/fleet-agent/dist`（双架构）。
- `CHANGELOG.md` 追加。
- 部署（见末尾）。

---

## Part 1 — G1：自动关闭时长端到端可配

### Task A1: fleet-enroll `/agent-config` 只读端点

**Files:**
- Modify: `server/enroll/main.go`（`handleSettings` 附近加 handler；`mux.HandleFunc` 处注册）
- Test: `server/enroll/agentconfig_test.go`（新建，`package main`）

- [ ] **Step 1: 写失败测试**

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentConfigReturnsIdleSec(t *testing.T) {
	// settingsFile 不存在 → loadSettings 回退默认 AutoCloseMinutes=30 → idleSec=1800
	settingsFile = t.TempDir() + "/none.json"
	r := httptest.NewRequest(http.MethodGet, "/agent-config", nil)
	w := httptest.NewRecorder()
	handleAgentConfig(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d want 200", w.Code)
	}
	var got struct {
		IdleSec int64 `json:"idleSec"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got.IdleSec != 1800 {
		t.Fatalf("idleSec=%d want 1800（默认 30min）", got.IdleSec)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd server/enroll && go test -run TestAgentConfig ./...`
Expected: FAIL — `undefined: handleAgentConfig`

- [ ] **Step 3: 最小实现**

在 `server/enroll/main.go` 的 `handleSettings` 之后加：

```go
// /agent-config：公开只读，给各 Mac fleet-agent 拉取空闲回收时长（秒）。
// 仅投影 AutoCloseMinutes*60，无敏感性；写设置仍走 /settings（Authelia）。
func handleAgentConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "只读")
		return
	}
	writeJSON(w, 200, map[string]int64{"idleSec": int64(loadSettings().AutoCloseMinutes) * 60})
}
```

注册（`mux.HandleFunc("/settings", handleSettings)` 之后）：

```go
	mux.HandleFunc("/agent-config", handleAgentConfig)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd server/enroll && go test ./...`
Expected: PASS（全部）

- [ ] **Step 5: 提交**

```bash
git add server/enroll/main.go server/enroll/agentconfig_test.go
git commit -m "feat(enroll): /agent-config 公开只读端点 投影 idleSec 给 agent 拉取"
```

---

### Task A2: nginx 公开 location

**Files:**
- Modify: `server/nginx/fleet.conf`（`location = /enroll/join` 附近）

> 无单测；nginx 配置部署时 `nginx -t` + curl 验证（见部署节）。本任务只改源文件。

- [ ] **Step 1: 加 location**

在 `location = /enroll/join { ... }` 块之后、`location ^~ /enroll/ { ... }` 之前加（exact-match 优先于 `^~`）：

```nginx
    # agent 拉取空闲回收时长（公开只读，不过 Authelia；仅暴露一个秒数）
    location = /enroll/agent-config {
        limit_req zone=fleet_enroll burst=10 nodelay;
        proxy_pass http://127.0.0.1:7090/agent-config;
    }
```

- [ ] **Step 2: 本地语法自查（gateway 上才能真 -t，这里仅肉眼核对）**

确认：块在 `^~ /enroll/`（alias 静态）之前；无 `auth_request`；`proxy_pass` 指向 7090 的 `/agent-config`。

- [ ] **Step 3: 提交**

```bash
git add server/nginx/fleet.conf
git commit -m "feat(nginx): /enroll/agent-config 公开反代到 fleet-enroll"
```

---

### Task A3: fleet-agent 运行时可变 idleSec + configSync

**Files:**
- Modify: `mac/fleet-agent/main.go`（`var cfg Config` 附近加 atomic；`reaper()` line ~1042；`main()` `go reaper()` line ~1117 附近）
- Test: `mac/fleet-agent/configsync_test.go`（新建，`package main`）

- [ ] **Step 1: 写失败测试**

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfigURLDerivation(t *testing.T) {
	// 完整地址优先
	t.Setenv("FLEET_CONFIG_URL", "https://gw.example.com/enroll/agent-config")
	if got := configURL(); got != "https://gw.example.com/enroll/agent-config" {
		t.Fatalf("configURL()=%q", got)
	}
	// 未配置 → 空（configSync 关闭）
	t.Setenv("FLEET_CONFIG_URL", "")
	if got := configURL(); got != "" {
		t.Fatalf("未配置应空，得到 %q", got)
	}
}

func TestFetchIdleSec(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"idleSec": 600}`))
	}))
	defer srv.Close()
	got, err := fetchIdleSec(srv.URL)
	if err != nil || got != 600 {
		t.Fatalf("fetchIdleSec=%d err=%v want 600", got, err)
	}
}

func TestFetchIdleSecRejectsGarbageAndBounds(t *testing.T) {
	// 越界 / 非法 → 报错，调用方保留旧值
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"idleSec": 0}`))
	}))
	defer srv.Close()
	if _, err := fetchIdleSec(srv.URL); err == nil {
		t.Fatalf("idleSec<=0 应报错")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd mac/fleet-agent && go test -run 'TestConfigURL|TestFetchIdleSec' ./...`
Expected: FAIL — `undefined: configURL / fetchIdleSec`

- [ ] **Step 3: 最小实现**

在 `mac/fleet-agent/main.go` `var cfg Config` 之后加运行时 idleSec + configSync。

```go
// 运行时空闲回收秒数：configSync 周期从网关更新，reaper 读它。
// 初值 = cfg.IdleSec（来自 FLEET_IDLE_SEC，网关不可达时的永久回退默认）。
var idleSec atomic.Int64

// configURL：daemon 拉设置的网关地址。由 plist 注入 FLEET_CONFIG_URL；
// 未配置（旧装机 / 未开启）则返回空 → configSync 不启动，沿用 FLEET_IDLE_SEC 默认。
func configURL() string { return strings.TrimSpace(os.Getenv("FLEET_CONFIG_URL")) }

// fetchIdleSec：GET 网关 /agent-config，解析 {idleSec}。越界/非法报错（调用方保留旧值）。
func fetchIdleSec(url string) (int64, error) {
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var body struct {
		IdleSec int64 `json:"idleSec"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<12)).Decode(&body); err != nil {
		return 0, err
	}
	if body.IdleSec <= 0 {
		return 0, fmt.Errorf("非法 idleSec=%d", body.IdleSec)
	}
	return body.IdleSec, nil
}

// configSync：启动拉一次 + 每 5min 拉，成功则更新 idleSec；失败保留当前值。
func configSync() {
	url := configURL()
	if url == "" {
		log.Printf("FLEET_CONFIG_URL 未配置，空闲回收沿用本地默认 %ds", idleSec.Load())
		return
	}
	pull := func() {
		if v, err := fetchIdleSec(url); err != nil {
			log.Printf("拉取 agent-config 失败（保留 %ds）：%v", idleSec.Load(), err)
		} else if v != idleSec.Load() {
			idleSec.Store(v)
			log.Printf("空闲回收时长更新为 %ds", v)
		}
	}
	pull()
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		pull()
	}
}
```

加 imports：确认 `sync/atomic`、`time`、`io` 已在 import 块（`io`、`time` 大概率已有；`sync/atomic` 需加）。

`main()` 里 `go reaper()` 之前初始化并启动：

```go
	idleSec.Store(cfg.IdleSec)
	go configSync()
	go reaper()
```

`reaper()` 内（line ~1042）把 `cfg.IdleSec` 改为 `idleSec.Load()`：

```go
		if attached == 0 && now-act > idleSec.Load() {
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd mac/fleet-agent && go test ./... && go vet ./...`
Expected: PASS（含既有 cmd_test/selfcmd_test）

- [ ] **Step 5: 提交**

```bash
git add mac/fleet-agent/main.go mac/fleet-agent/configsync_test.go
git commit -m "feat(agent): idleSec 运行时可变 + configSync 周期拉网关；reaper 读 atomic"
```

---

### Task A4: plist + setup-mac.sh 注入 FLEET_CONFIG_URL

**Files:**
- Modify: `mac/com.macfleet.fleet-agent.plist`（`EnvironmentVariables` dict）
- Modify: `mac/setup-mac.sh`（render() 加替换；推导 `FLEET_CONFIG_URL`）

> 无单测（shell/plist 模板）；部署后由 agent 日志 + curl 验证。

- [ ] **Step 1: plist 加 env 占位**

在 `mac/com.macfleet.fleet-agent.plist` 的 `FLEET_IDLE_SEC` 之后加：

```xml
        <key>FLEET_CONFIG_URL</key>
        <string>__FLEET_CONFIG_URL__</string>
```

并更新文件顶部注释，把 `__FLEET_CONFIG_URL__` 列入替换占位清单。

- [ ] **Step 2: setup-mac.sh 推导 + 替换**

在 `render()` 之前推导（`FLEET_UPDATE_BASE` 形如 `https://${DOMAIN}/enroll/dist`，去掉末尾 `dist` 段 → `.../enroll/agent-config`）：

```bash
# fleet-agent daemon 拉空闲回收时长的网关地址（由 FLEET_UPDATE_BASE 推导；未给则留空→agent 用本地默认）
FLEET_CONFIG_URL=""
if [[ -n "${FLEET_UPDATE_BASE:-}" ]]; then
  FLEET_CONFIG_URL="${FLEET_UPDATE_BASE%/}"; FLEET_CONFIG_URL="${FLEET_CONFIG_URL%/dist}/agent-config"
fi
```

在 `render()` 的 sed 链加一行替换：

```bash
      -e "s#__FLEET_CONFIG_URL__#${FLEET_CONFIG_URL}#g" \
```

- [ ] **Step 3: 自查**

`grep __FLEET_CONFIG_URL__ mac/com.macfleet.fleet-agent.plist` 有 1 处；`grep FLEET_CONFIG_URL mac/setup-mac.sh` 有推导 + 替换两处。

- [ ] **Step 4: 提交**

```bash
git add mac/com.macfleet.fleet-agent.plist mac/setup-mac.sh
git commit -m "feat(agent): plist+setup-mac 注入 FLEET_CONFIG_URL（由 FLEET_UPDATE_BASE 推导）"
```

---

## Part 2 — G2：刷新后终端自动恢复

> dashboard 无 JS 测试框架 → Part 2 用真实 Chrome 端到端验证（见 dev 记忆 debug-with-claude-in-chrome），手测步骤在 Task B3 与部署节，**不可跳过**。

### Task B1: 撤销前一版无效的前端 poolReapIdle

**Files:**
- Modify: `server/dashboard/app.js`（`poolReapIdle` 定义；`init()` 里 `setInterval(poolReapIdle, 60000)`）
- Modify: `server/dashboard/index.html`（「自动关闭」hint）

- [ ] **Step 1: 删除 `poolReapIdle` 函数**（`poolEvict` 之后那段「无操作自动关闭」整块连注释删掉）。

- [ ] **Step 2: 删除 init 里的定时器**

把 `setInterval(poolReapIdle, 60000); // 每分钟扫一遍空闲窗口...` 这一行删掉。

- [ ] **Step 3: 改 index.html hint 文案**

把「窗口在『无新输出』超过自动关闭时长后也会被释放...」那段 `<p class="hint">` 改为：

```html
            <p class="hint">「自动关闭」是各 Mac 后台会话的空闲回收时长：超过该时长无人连接、且无新活动的终端会话会被自动释放（结束进程、释放 pty，会话记录保留，再次点开会重新连接）。改动几分钟内同步到所有在线 Mac。</p>
```

- [ ] **Step 4: 语法自查**

Run: `node --check server/dashboard/app.js`
Expected: 无输出（OK）

- [ ] **Step 5: 提交**

```bash
git add server/dashboard/app.js server/dashboard/index.html
git commit -m "refactor(dashboard): 撤销无效的前端 poolReapIdle；自动关闭改由后端回收，文案对齐"
```

---

### Task B2: 池快照写入 sessionStorage

**Files:**
- Modify: `server/dashboard/app.js`（`poolAdd` / `poolDrop`）

- [ ] **Step 1: 加快照序列化 helper**

在终端 iframe 池段（`poolFind` 附近）加：

```js
const POOL_SNAP_KEY = 'fleet-pool';
// 把当前池序列化成最小重建标识存 sessionStorage（刷新/崩溃恢复用，关标签即清）。
// 只存重建所需：macId/sessionId/permMode/title/cwd——sid/url 是 attach 时新生成的，不存。
function savePoolSnapshot() {
  try {
    const snap = {
      macId: state.macId,
      cur: state.current ? state.current.sessionId : null,
      items: state.pool
        .filter((e) => e.sessionId) // 无 sessionId 的新建会话不持久化（无法 resume 定位）
        .map((e) => ({ macId: e.macId, sessionId: e.sessionId, permMode: e.permMode, title: e.title, cwd: e.cwd })),
    };
    sessionStorage.setItem(POOL_SNAP_KEY, JSON.stringify(snap));
  } catch (_) {}
}
```

- [ ] **Step 2: 在 `poolAdd` 末尾、`poolDrop` 末尾各调一次**

`poolAdd` 的 `return entry;` 之前加 `savePoolSnapshot();`；`poolDrop` 函数体末尾加 `savePoolSnapshot();`。

> 挂在 add/drop 而非 connect：权限模式变更在 connect 里是 `poolDrop`+`poolAdd` 一对，挂这里不会双写漏写。

- [ ] **Step 3: 语法自查**

Run: `node --check server/dashboard/app.js`
Expected: OK

- [ ] **Step 4: 提交**

```bash
git add server/dashboard/app.js
git commit -m "feat(dashboard): 池变化时写 sessionStorage 快照（刷新恢复用）"
```

---

### Task B3: init 时恢复终端

**Files:**
- Modify: `server/dashboard/app.js`（`init()`；恢复函数）

- [ ] **Step 1: 加恢复函数**

```js
// 刷新/崩溃后从快照恢复终端：逐条重连（受 poolMax 上限约束，poolAdd 内已 poolEvict）。
// connect 签名是 connect(sessionId, title, cwd, mode)，macId 取自 state.macId——
// 故恢复每条前先把 state.macId 切到该条，全部恢复后复位到快照当前主机，不抢焦点切走。
async function restorePoolSnapshot() {
  let snap;
  try { snap = JSON.parse(sessionStorage.getItem(POOL_SNAP_KEY) || 'null'); } catch (_) { snap = null; }
  if (!snap || !Array.isArray(snap.items) || !snap.items.length) return;
  const savedMac = state.macId;
  for (const it of snap.items) {
    state.macId = it.macId;
    try { await connect(it.sessionId, it.title, it.cwd, it.permMode || 'default'); } catch (_) {}
  }
  // 复位到快照当前主机与当前会话（poolShow 已在 connect 内对最后一条触发，这里确保焦点回到 cur）
  state.macId = snap.macId || savedMac;
  const cur = snap.cur && poolFind(state.macId, snap.cur);
  if (cur) poolShow(cur);
  renderHosts();
}
```

- [ ] **Step 2: 在 `init()` 末尾调用**

`init()` 结尾（事件绑定之后）加 `restorePoolSnapshot();`。

- [ ] **Step 3: 语法自查**

Run: `node --check server/dashboard/app.js`
Expected: OK

- [ ] **Step 4: 提交**

```bash
git add server/dashboard/app.js
git commit -m "feat(dashboard): init 时从 sessionStorage 快照自动恢复终端"
```

- [ ] **Step 5: 真实 Chrome 端到端手测（不可跳过，部署后做）**

部署 dashboard 后，在真实 Chrome（见部署节）：打开 2~3 个会话 → 刷新页面 → 确认终端自动恢复、焦点落在刷新前的当前会话；关标签页重开 → 确认不恢复（sessionStorage 已清）。

---

## Part 3 — 重建产物 + 部署 + CHANGELOG

### Task C1: 重建双产物

- [ ] **Step 1: fleet-enroll（linux/amd64）**

```bash
cd server/enroll && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/fleet-enroll-linux-amd64 . && file dist/fleet-enroll-linux-amd64
```
Expected: `ELF 64-bit ... stripped`

- [ ] **Step 2: fleet-agent 双架构**

```bash
cd mac/fleet-agent && bash build.sh && ls -la dist/
```
Expected: `fleet-agent-darwin-amd64` + `fleet-agent-darwin-arm64` 更新（见 dev 记忆 fleet-agent-build-flags）

- [ ] **Step 3: 提交产物**

```bash
git add server/enroll/dist/fleet-enroll-linux-amd64 mac/fleet-agent/dist
git commit -m "build: 重建 fleet-enroll + fleet-agent dist（idleSec 可配 + configSync）"
```

### Task C2: CHANGELOG

- [ ] 在 `CHANGELOG.md` `## 2026-06-26` 下追加一条，记：自动关闭时长端到端可配（agent 拉 /enroll/agent-config 改 reaper IdleSec）+ 刷新后 sessionStorage 恢复终端 + 撤销无效前端 poolReapIdle。提交。

### Task C3: 部署（实操，高风险项按 dev 约定）

- [ ] **dashboard 静态**（dev 可直接做）：网关 `git pull` + `cp -r server/dashboard/. /var/www/fleet/`。
- [ ] **fleet-enroll**：网关 install 新 `dist/fleet-enroll-linux-amd64` 到 `/usr/local/bin/fleet-enroll` + `systemctl restart fleet-enroll`。
- [ ] **nginx**（**高风险**：改生效配置）：backup → 渲染/加 location → `nginx -t` → reload。**切 /deploy 人格或显式确认后做**。验证：`curl -s https://${DOMAIN}/enroll/agent-config` 返回 `{"idleSec":1800}`；未登录访问该路径**不应** 302（确认公开）。
- [ ] **fleet-agent**（3 台 Mac，**改了 plist 需重渲染，不止换二进制**）：每台 install 新二进制 + 重新 render plist 注入 `FLEET_CONFIG_URL` + `launchctl unload/load` + `fleet-agent restart`。验证：agent 日志出现「空闲回收时长更新为 1800s」或「FLEET_CONFIG_URL 未配置」。
- [ ] **端到端**：网页改自动关闭为某值 → `curl /enroll/agent-config` 看 idleSec 变 → 等 ≤5min 看 agent 日志更新；刷新 dashboard 看终端恢复。

---

## 验证清单（收尾）

- [ ] `cd server/enroll && go test ./...` PASS
- [ ] `cd mac/fleet-agent && go test ./... && go vet ./...` PASS
- [ ] `node --check server/dashboard/app.js` OK
- [ ] 部署后 `curl https://${DOMAIN}/enroll/agent-config` 返回正确 idleSec
- [ ] 真实 Chrome：刷新后终端恢复；关标签不恢复
- [ ] agent 日志确认 configSync 生效（或明确回退本地默认）
