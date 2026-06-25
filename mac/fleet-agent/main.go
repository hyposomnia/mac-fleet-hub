// fleet-agent —— 每台 Mac 的会话管理服务（纯标准库，零运行时依赖）。
//
// 职责：
//   GET  /api/sessions?scope=active|all   列出 Claude 会话（按 cwd 由前端分组）
//   GET  /api/projects                    历史会话的 cwd 去重 → 项目目录列表
//   POST /api/open    {sessionId,bypass}  在 tmux 起/复用窗口跑 claude --resume，返回终端入口
//   POST /api/new     {cwd,bypass}         在指定目录起新 claude
//   POST /api/close   {sessionId|sid}      终止该会话对应的 fleet tmux（进程结束，会话记录保留）
//   GET  /api/watch?sid=fleet-xxx         检测是否有 Desktop 外部写入（DAG 分叉判定）
//   POST /api/reload  {sid}               kill+resume 该窗口（拉取 Desktop 最新）
//
//   open/new 的 bypass=true → claude 带 --dangerously-skip-permissions（跳过工具权限确认）。
//
// 仅绑 mesh IP，不公网；访问控制交给 Headscale ACL（见 plan §5.1）。
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ---------------- 配置 ----------------
type Config struct {
	Listen     string // 绑定地址，如 100.x.x.x:7682
	ClaudeHome string // ~/.claude
	ClaudeBin  string // claude 可执行文件
	MacIndex   string // 1/2/3 → 终端入口 /m{idx}/term
	IdleSec    int64  // 空闲回收秒数（默认 1800）
	AutoCmdR   bool   // 会话结束自动给 Desktop 发 Cmd+R
	DesktopApp   string // osascript 目标应用名（默认 Claude）
	ProxyFile    string // 代理配置持久化文件（~/.macfleet-proxy.json）
	DesktopStore string // Claude Desktop 会话库目录（一次数据源）
}

// 代理配置：Web 端可设，按会话注入到 claude 的环境（HTTP(S)_PROXY）。
type ProxyCfg struct {
	Enabled bool   `json:"enabled"`
	HTTP    string `json:"http"`
	HTTPS   string `json:"https"`
}

var (
	proxyCfg ProxyCfg
	proxyMu  sync.Mutex
)

func loadProxy() {
	b, err := os.ReadFile(cfg.ProxyFile)
	if err != nil {
		return
	}
	proxyMu.Lock()
	json.Unmarshal(b, &proxyCfg)
	proxyMu.Unlock()
}

func saveProxy() {
	proxyMu.Lock()
	b, _ := json.MarshalIndent(proxyCfg, "", "  ")
	proxyMu.Unlock()
	os.WriteFile(cfg.ProxyFile, b, 0600)
}

// 启动 claude 前的环境前缀：env HTTP_PROXY=... HTTPS_PROXY=...（含小写别名）。
func proxyEnvPrefix() string {
	proxyMu.Lock()
	p := proxyCfg
	proxyMu.Unlock()
	if !p.Enabled || (p.HTTP == "" && p.HTTPS == "") {
		return ""
	}
	h, s := p.HTTP, p.HTTPS
	if h == "" {
		h = s
	}
	if s == "" {
		s = h
	}
	return fmt.Sprintf("env HTTP_PROXY=%s HTTPS_PROXY=%s http_proxy=%s https_proxy=%s ",
		shellQuote(h), shellQuote(s), shellQuote(h), shellQuote(s))
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func loadConfig() Config {
	home, _ := os.UserHomeDir()
	idle, _ := strconv.ParseInt(envOr("FLEET_IDLE_SEC", "1800"), 10, 64)
	return Config{
		Listen:     envOr("FLEET_LISTEN", "127.0.0.1:7682"),
		ClaudeHome: envOr("FLEET_CLAUDE_HOME", filepath.Join(home, ".claude")),
		ClaudeBin:  envOr("FLEET_CLAUDE_BIN", "claude"),
		MacIndex:   envOr("FLEET_MAC_INDEX", "1"),
		IdleSec:    idle,
		AutoCmdR:   envOr("FLEET_AUTO_CMDR", "1") == "1",
		DesktopApp: envOr("FLEET_DESKTOP_APP", "Claude"),
		ProxyFile:    envOr("FLEET_PROXY_FILE", filepath.Join(home, ".macfleet-proxy.json")),
		DesktopStore: envOr("FLEET_DESKTOP_STORE", filepath.Join(home, "Library", "Application Support", "Claude", "claude-code-sessions")),
	}
}

var cfg Config

// ---------------- 数据类型 ----------------
type Session struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title"`
	GitBranch string `json:"gitBranch"`
	Mtime     int64  `json:"mtime"` // 毫秒
	Live      bool   `json:"live"`    // Desktop 未归档（活跃）
	Pty       bool   `json:"pty"`     // 控制台已为该会话起过 fleet tmux（有可终止/可回到的进程）
	Waiting   bool   `json:"waiting"` // 卡在「等你回答/授权」：jsonl 最后一条 assistant 且 stop_reason==tool_use
}

// jsonl 行（只取需要字段）
type line struct {
	Type        string          `json:"type"`
	AiTitle     string          `json:"aiTitle"`
	CustomTitle string          `json:"customTitle"`
	SessionID   string          `json:"sessionId"`
	Cwd         string          `json:"cwd"`
	GitBranch   string          `json:"gitBranch"`
	UUID        string          `json:"uuid"`
	ParentUUID  *string         `json:"parentUuid"`
	IsSidechain bool            `json:"isSidechain"`
	Message     json.RawMessage `json:"message"`
}

// 会话文件元数据缓存（按 mtime 失效）
type meta struct {
	mtime    int64
	cwd      string
	branch   string
	title    string
	lastUUID string // 末尾叶子 uuid（用于 watch 初始化 mobileTip）
}

var (
	metaCache = map[string]meta{} // path -> meta
	metaMu    sync.Mutex
)

// ---------------- 会话扫描 ----------------
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// 活跃会话：~/.claude/sessions/*.json 中 pid 存活者 → sessionId 集合
func activeSet() map[string]bool {
	set := map[string]bool{}
	dir := filepath.Join(cfg.ClaudeHome, "sessions")
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s struct {
			Pid       int    `json:"pid"`
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(b, &s) == nil && s.SessionID != "" && pidAlive(s.Pid) {
			set[s.SessionID] = true
		}
	}
	return set
}

// 解析单个 .jsonl，提取元数据（带 mtime 缓存）
func fileMeta(path string, mtimeMs int64) meta {
	metaMu.Lock()
	if m, ok := metaCache[path]; ok && m.mtime == mtimeMs {
		metaMu.Unlock()
		return m
	}
	metaMu.Unlock()

	m := meta{mtime: mtimeMs}
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	var firstUser, customTitle, aiTitle string
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		// 取首条 cwd（会话启动目录=项目根）。会话中途 cd 进子目录的行不应改变分组归属。
		if l.Cwd != "" && m.cwd == "" {
			m.cwd = l.Cwd
		}
		if l.GitBranch != "" {
			m.branch = l.GitBranch
		}
		if l.UUID != "" {
			m.lastUUID = l.UUID
		}
		// 用户 rename：jsonl 里 type=="custom-title" 的 customTitle 字段，每次 rename 追加一行，取最后一条。
		if l.Type == "custom-title" && l.CustomTitle != "" {
			customTitle = l.CustomTitle
		}
		if l.Type == "ai-title" && l.AiTitle != "" {
			aiTitle = l.AiTitle
		}
		if firstUser == "" && l.Type == "user" {
			firstUser = extractText(l.Message)
		}
	}
	// 优先级：用户 rename（customTitle） > 自动 ai-title > 首条 user 消息。
	switch {
	case customTitle != "":
		m.title = customTitle
	case aiTitle != "":
		m.title = aiTitle
	default:
		m.title = firstUser
	}
	if m.title == "" {
		m.title = "(无标题)"
	}
	metaMu.Lock()
	metaCache[path] = m
	metaMu.Unlock()
	return m
}

func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// message.content 可能是 string 或 [{type:text,text:...}]
	var asObj struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &asObj) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(asObj.Content, &s) == nil {
		return trim(s)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(asObj.Content, &parts) == nil {
		for _, p := range parts {
			if p.Type == "text" && p.Text != "" {
				return trim(p.Text)
			}
		}
	}
	return ""
}
func trim(s string) string {
	s = cleanTitle(s)
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	// 截断按 rune，避免切坏 UTF-8（中文标题尤甚）
	r := []rune(s)
	if len(r) > 60 {
		s = string(r[:60])
	}
	return s
}

// cleanTitle：slash 命令首条消息形如
// "<command-message>x</command-message> <command-name>/y</command-name> ..."，
// 优先取 /y 作标题；否则剥掉所有 <...> 标签。
func cleanTitle(s string) string {
	if i := strings.Index(s, "<command-name>"); i >= 0 {
		rest := s[i+len("<command-name>"):]
		if j := strings.Index(rest, "</command-name>"); j >= 0 {
			if name := strings.TrimSpace(rest[:j]); name != "" {
				return name
			}
		}
	}
	if strings.Contains(s, "<") && strings.Contains(s, ">") {
		var b strings.Builder
		depth := 0
		for _, c := range s {
			switch c {
			case '<':
				depth++
			case '>':
				if depth > 0 {
					depth--
				}
			default:
				if depth == 0 {
					b.WriteRune(c)
				}
			}
		}
		if t := strings.TrimSpace(b.String()); t != "" {
			return t
		}
	}
	return s
}

// scanJSONL：直接扫 ~/.claude/projects 的 .jsonl —— 仅在 Desktop store 不可用时作回退源。
// 此处 Live 用「进程存活」近似，不如 Desktop 的 isArchived 准确。
func scanJSONL() []Session {
	active := activeSet()
	root := filepath.Join(cfg.ClaudeHome, "projects")
	var out []Session
	projs, _ := os.ReadDir(root)
	for _, p := range projs {
		if !p.IsDir() {
			continue
		}
		pdir := filepath.Join(root, p.Name())
		files, _ := os.ReadDir(pdir)
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(pdir, f.Name())
			info, err := f.Info()
			if err != nil {
				continue
			}
			mtimeMs := info.ModTime().UnixMilli()
			m := fileMeta(path, mtimeMs)
			sid := strings.TrimSuffix(f.Name(), ".jsonl")
			out = append(out, Session{
				SessionID: sid, Cwd: m.cwd, Title: m.title, GitBranch: m.branch,
				Mtime: mtimeMs, Live: active[sid],
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime > out[j].Mtime })
	return out
}

func jsonlPath(sid string) string {
	// 反查某 sessionId 的 .jsonl 路径
	root := filepath.Join(cfg.ClaudeHome, "projects")
	projs, _ := os.ReadDir(root)
	for _, p := range projs {
		path := filepath.Join(root, p.Name(), sid+".jsonl")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// jsonlPaths：一次扫 ~/.claude/projects 建 sessionId → .jsonl 路径 映射，
// 供 handleSessions 批量标记，避免每会话各扫一遍目录。
func jsonlPaths() map[string]string {
	m := map[string]string{}
	root := filepath.Join(cfg.ClaudeHome, "projects")
	projs, _ := os.ReadDir(root)
	for _, p := range projs {
		if !p.IsDir() {
			continue
		}
		pdir := filepath.Join(root, p.Name())
		files, _ := os.ReadDir(pdir)
		for _, f := range files {
			if n := f.Name(); strings.HasSuffix(n, ".jsonl") {
				m[strings.TrimSuffix(n, ".jsonl")] = filepath.Join(pdir, n)
			}
		}
	}
	return m
}

// 「等待用户」缓存：jsonl 路径 → (mtime, waiting)，按 mtime 失效，免每次轮询重读尾部。
var (
	waitCache = map[string]struct {
		mtime   int64
		waiting bool
	}{}
	waitMu sync.Mutex
)

// sessionWaiting：会话是否卡在「等你回答 / 授权」。判据（见实证）——jsonl 最后一条可解析记录
// 是 assistant 且 message.stop_reason=="tool_use"：AskUserQuestion 待答、或工具待授权都属此态；
// 轮次正常结束是 end_turn（不亮），工具已执行则末条是 tool_result/attachment（不亮）。按 mtime 缓存。
func sessionWaiting(path string) bool {
	if path == "" {
		return false
	}
	mt := statMtime(path)
	waitMu.Lock()
	if c, ok := waitCache[path]; ok && c.mtime == mt {
		waitMu.Unlock()
		return c.waiting
	}
	waitMu.Unlock()

	w := tailWaiting(readTail(path, 256*1024))
	waitMu.Lock()
	waitCache[path] = struct {
		mtime   int64
		waiting bool
	}{mt, w}
	waitMu.Unlock()
	return w
}

// readTail：读文件末尾最多 n 字节（jsonl 末几条足够判断等待态）。
func readTail(path string, n int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	if start := info.Size() - n; start > 0 {
		if _, err := f.Seek(start, 0); err != nil {
			return nil
		}
	}
	b, _ := io.ReadAll(f)
	return b
}

// tailWaiting：纯逻辑（便于单测）——取尾部内容里最后一条可解析 json 记录，判断是否等待态。
// 从末尾往前找第一条能解析的行（尾部首行可能被 readTail 截断，跳过）。
func tailWaiting(tail []byte) bool {
	lines := bytes.Split(tail, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		ln := bytes.TrimSpace(lines[i])
		if len(ln) == 0 {
			continue
		}
		var o struct {
			Type    string `json:"type"`
			Message struct {
				Role       string `json:"role"`
				StopReason string `json:"stop_reason"`
			} `json:"message"`
		}
		if json.Unmarshal(ln, &o) != nil {
			continue
		}
		return (o.Type == "assistant" || o.Message.Role == "assistant") && o.Message.StopReason == "tool_use"
	}
	return false
}

// ---------------- Claude Desktop 会话库（一次数据源） ----------------
// Desktop 在 ~/Library/Application Support/Claude/claude-code-sessions/*/*/local_*.json
// 维护每个会话的真实元数据：cliSessionId（= CLI .jsonl 的 id，resume 用）、title（titleSource
// user=用户 rename / auto=自动标题）、cwd / originCwd（worktree 时分别为工作树 / 主仓）、
// isArchived（= 是否「活跃」的权威依据：未归档即活跃）、lastActivityAt。
type dsess struct {
	cli      string
	title    string
	cwd      string // 真实工作目录（worktree 时为工作树），resume 用
	group    string // 分组目录（originCwd 优先，回退 cwd）
	branch   string
	archived bool
	activity int64 // ms
}

func scanDesktop() []dsess {
	files, _ := filepath.Glob(filepath.Join(cfg.DesktopStore, "*", "*", "local_*.json"))
	var out []dsess
	seen := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var d struct {
			CliSessionID   string `json:"cliSessionId"`
			Title          string `json:"title"`
			Cwd            string `json:"cwd"`
			OriginCwd      string `json:"originCwd"`
			Branch         string `json:"branch"`
			IsArchived     bool   `json:"isArchived"`
			LastActivityAt int64  `json:"lastActivityAt"`
			LastFocusedAt  int64  `json:"lastFocusedAt"`
		}
		if json.Unmarshal(b, &d) != nil || d.CliSessionID == "" || seen[d.CliSessionID] {
			continue
		}
		seen[d.CliSessionID] = true
		group := d.OriginCwd
		if group == "" {
			group = d.Cwd
		}
		act := d.LastActivityAt
		if act < d.LastFocusedAt {
			act = d.LastFocusedAt
		}
		out = append(out, dsess{
			cli: d.CliSessionID, title: strings.TrimSpace(d.Title), cwd: d.Cwd,
			group: group, branch: d.Branch, archived: d.IsArchived, activity: act,
		})
	}
	return out
}

// desktopSessions：把 Desktop 库映射为会话列表（Live = 未归档）。
func desktopSessions() []Session {
	ds := scanDesktop()
	out := make([]Session, 0, len(ds))
	for _, d := range ds {
		title := d.title
		if title == "" { // Desktop 偶尔无 title → 回退 jsonl 解析
			if p := jsonlPath(d.cli); p != "" {
				title = fileMeta(p, statMtime(p)).title
			}
		}
		if title == "" {
			title = "(无标题)"
		}
		out = append(out, Session{
			SessionID: d.cli, Cwd: d.group, Title: title, GitBranch: d.branch,
			Mtime: d.activity, Live: !d.archived,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime > out[j].Mtime })
	return out
}

// scanSessions：优先用 Desktop 库（active=未归档、标题/分组/resume id 皆权威）；
// 库不可用（未装 Desktop 等）时回退到 .jsonl 扫描。
func scanSessions() []Session {
	if ds := desktopSessions(); len(ds) > 0 {
		return ds
	}
	return scanJSONL()
}

// cwdOf：resume 的工作目录 —— 优先 Desktop 的真实 cwd（worktree 友好），回退 jsonl 推断。
func cwdOf(sid string) string {
	for _, d := range scanDesktop() {
		if d.cli == sid {
			return d.cwd
		}
	}
	for _, s := range scanJSONL() {
		if s.SessionID == sid {
			return s.Cwd
		}
	}
	return ""
}

// ---------------- tmux ----------------
func tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	return string(out), err
}
func tmuxHas(name string) bool {
	_, err := tmux("has-session", "-t", name)
	return err == nil
}

// fleetTmuxSet：一次列出所有 fleet-* tmux 会话名 → 集合，供 handleSessions 标记每个会话是否「开了 pty」。
// 比逐会话 tmux has-session 便宜（一次调用），避免会话多时 N 次 exec。
func fleetTmuxSet() map[string]bool {
	set := map[string]bool{}
	out, err := tmux("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return set
	}
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(ln, "fleet-") {
			set[ln] = true
		}
	}
	return set
}
func shortSid(sessionID string) string {
	id := strings.ReplaceAll(sessionID, "-", "")
	if len(id) > 10 {
		id = id[:10]
	}
	return "fleet-" + id
}

// 起/复用一个 tmux 会话跑命令
func ensureTmux(name, cwd, cmd string) error {
	if tmuxHas(name) {
		return nil
	}
	full := fmt.Sprintf("cd %s; exec %s", shellQuote(cwd), cmd)
	_, err := tmux("new-session", "-d", "-s", name, "-x", "220", "-y", "50", "sh", "-c", full)
	return err
}
func shellQuote(s string) string {
	if s == "" {
		return "$HOME"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
func termURL(name string) string {
	return fmt.Sprintf("/m%s/term/?arg=%s", cfg.MacIndex, name)
}

// ---------------- watch 注册表（Desktop→ttyd 检测） ----------------
type watcher struct {
	sessionID string
	path      string
	offset    int64
	tip       string // mobileTip：当前手机分支叶子 uuid
	external  bool
	bypass    bool // F1：本会话以 --dangerously-skip-permissions 启动，reload 时须沿用
}

var (
	watchers = map[string]*watcher{} // sid -> watcher
	watchMu  sync.Mutex
)

func registerWatch(sid, sessionID string, bypass bool) {
	path := jsonlPath(sessionID)
	w := &watcher{sessionID: sessionID, path: path, bypass: bypass}
	if path != "" {
		if info, err := os.Stat(path); err == nil {
			w.offset = info.Size()
		}
		w.tip = fileMeta(path, statMtime(path)).lastUUID
	}
	watchMu.Lock()
	watchers[sid] = w
	watchMu.Unlock()
}
func statMtime(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime().UnixMilli()
	}
	return 0
}

// 读取新追加行，按 DAG 分叉判定是否有 Desktop 外部写入
func evalWatch(sid string) bool {
	watchMu.Lock()
	w := watchers[sid]
	watchMu.Unlock()
	if w == nil || w.path == "" {
		return false
	}
	if w.external {
		return true
	}
	f, err := os.Open(w.path)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err := f.Seek(w.offset, 0); err != nil {
		return false
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	var consumed int64
	for sc.Scan() {
		b := sc.Bytes()
		consumed += int64(len(b)) + 1
		var l line
		if json.Unmarshal(b, &l) != nil || l.UUID == "" {
			continue
		}
		if l.IsSidechain { // 子 agent 行归入手机自身活动
			w.tip = l.UUID
			continue
		}
		parent := ""
		if l.ParentUUID != nil {
			parent = *l.ParentUUID
		}
		if w.tip == "" || parent == w.tip {
			w.tip = l.UUID // 手机自写，推进分支
		} else {
			w.external = true // 接到别的父/冒出第二叶子 → Desktop 外部写
		}
	}
	w.offset += consumed
	return w.external
}

// ---------------- HTTP handlers ----------------
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// writeErr：结构化错误体 {error,message}，前端按 message 展示可读文案。
func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": kind, "message": msg})
}

// ptyUsage：探测系统当前已分配的 pty 从设备数(/dev/ttys*)与上限(kern.tty.ptmx_max)。
// 设为变量以便测试注入。任一探测失败时对应项返回 0（→ ptyExhausted 视为「未知不判耗尽」）。
var ptyUsage = func() (used, max int) {
	if entries, err := os.ReadDir("/dev"); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "ttys") {
				used++
			}
		}
	}
	if out, err := exec.Command("sysctl", "-n", "kern.tty.ptmx_max").Output(); err == nil {
		max, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
	return
}

// ptyExhausted：max>0 且已用达到上限才算耗尽。探测失败(max==0)不误判，
// 避免把普通 tmux 错误都归因成 pty 耗尽。
func ptyExhausted(used, max int) bool { return max > 0 && used >= max }

// httpTmuxErr：tmux 起会话失败的统一响应。pty 耗尽是可预期的运维状态（常由长跑 GUI 应用
// 泄露 pty 触发，非本服务 bug），单独判出来给 503 + 可读文案，与普通 500 区分，便于前端精确提示。
func httpTmuxErr(w http.ResponseWriter, err error) {
	if used, max := ptyUsage(); ptyExhausted(used, max) {
		writeErr(w, http.StatusServiceUnavailable, "pty_exhausted",
			fmt.Sprintf("系统终端(pty)已达上限 %d/%d，无法新建会话。请关闭闲置的网页终端，或重启占用 pty 的应用后重试。", used, max))
		return
	}
	writeErr(w, http.StatusInternalServerError, "tmux_failed", "启动终端会话失败："+err.Error())
}

func handleSessions(w http.ResponseWriter, r *http.Request) {
	all := scanSessions()
	// 标记每个会话：是否已有 fleet tmux 进程（pty，前端显示「终止」「进入连接」）、
	// 是否卡在等你回答/授权（waiting，前端显示棕色点）。jsonl 路径一次性建映射避免逐会话扫目录。
	ptySet := fleetTmuxSet()
	paths := jsonlPaths()
	for i := range all {
		all[i].Pty = ptySet[shortSid(all[i].SessionID)]
		all[i].Waiting = sessionWaiting(paths[all[i].SessionID])
	}
	scope := r.URL.Query().Get("scope")
	list := all
	if scope == "active" {
		list = list[:0]
		for _, s := range all {
			if s.Live {
				list = append(list, s)
			}
		}
	}
	writeJSON(w, map[string]interface{}{"sessions": list, "total": len(all)})
}

func handleProjects(w http.ResponseWriter, r *http.Request) {
	seen := map[string]*struct {
		Cwd   string `json:"cwd"`
		Count int    `json:"count"`
		Mtime int64  `json:"mtime"`
	}{}
	for _, s := range scanSessions() {
		if s.Cwd == "" {
			continue
		}
		p := seen[s.Cwd]
		if p == nil {
			p = &struct {
				Cwd   string `json:"cwd"`
				Count int    `json:"count"`
				Mtime int64  `json:"mtime"`
			}{Cwd: s.Cwd}
			seen[s.Cwd] = p
		}
		p.Count++
		if s.Mtime > p.Mtime {
			p.Mtime = s.Mtime
		}
	}
	var ps []interface{}
	for _, v := range seen {
		ps = append(ps, v)
	}
	sort.Slice(ps, func(i, j int) bool {
		return ps[i].(*struct {
			Cwd   string `json:"cwd"`
			Count int    `json:"count"`
			Mtime int64  `json:"mtime"`
		}).Mtime > ps[j].(*struct {
			Cwd   string `json:"cwd"`
			Count int    `json:"count"`
			Mtime int64  `json:"mtime"`
		}).Mtime
	})
	writeJSON(w, map[string]interface{}{"projects": ps})
}

// claudeResumeCmd / claudeNewCmd：构造 tmux 内启动 claude 的 shell 命令。
// bypass=true 追加 --dangerously-skip-permissions（F1「Bypass连接」，跳过工具权限确认，
// 属高风险，前端用 danger 样式 + 警示徽标区分）。代理前缀按会话注入（见 proxyEnvPrefix）。
func claudeResumeCmd(sessionID string, bypass bool) string {
	cmd := proxyEnvPrefix() + fmt.Sprintf("%s --resume %s", cfg.ClaudeBin, shellQuote(sessionID))
	if bypass {
		cmd += " --dangerously-skip-permissions"
	}
	return cmd
}

func claudeNewCmd(bypass bool) string {
	cmd := proxyEnvPrefix() + cfg.ClaudeBin
	if bypass {
		cmd += " --dangerously-skip-permissions"
	}
	return cmd
}

func handleOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
		Bypass    bool   `json:"bypass"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.SessionID == "" {
		http.Error(w, "bad request", 400)
		return
	}
	cwd := cwdOf(req.SessionID)
	name := shortSid(req.SessionID)
	if err := ensureTmux(name, cwd, claudeResumeCmd(req.SessionID, req.Bypass)); err != nil {
		httpTmuxErr(w, err)
		return
	}
	registerWatch(name, req.SessionID, req.Bypass)
	writeJSON(w, map[string]interface{}{"url": termURL(name), "sid": name, "bypass": req.Bypass})
}

func handleNew(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cwd    string `json:"cwd"`
		Bypass bool   `json:"bypass"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	name := fmt.Sprintf("fleet-new%d", time.Now().Unix())
	if err := ensureTmux(name, req.Cwd, claudeNewCmd(req.Bypass)); err != nil {
		httpTmuxErr(w, err)
		return
	}
	writeJSON(w, map[string]interface{}{"url": termURL(name), "sid": name, "bypass": req.Bypass})
}

// handleClose：F2 终止会话进程 —— kill 该会话对应的 fleet tmux（claude/tmux 进程随之结束）。
// 入参 {sessionId}（列表里的 claude 会话，tmux 名 = shortSid）或 {sid}（直接的 tmux 名，如新会话）。
// 这是「终止进程」而非「删除会话」：会话的 .jsonl / Desktop 记录不动，条目仍会出现在列表。
// 返回 killed 表示是否真的杀掉了一个由控制台启动的进程（无对应 tmux 时为 false，便于前端如实提示）。
func handleClose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
		Sid       string `json:"sid"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	name := req.Sid
	if name == "" && req.SessionID != "" {
		name = shortSid(req.SessionID)
	}
	if name == "" {
		http.Error(w, "bad request", 400)
		return
	}
	killed := tmuxHas(name)
	if killed {
		tmux("kill-session", "-t", name)
	}
	watchMu.Lock()
	delete(watchers, name)
	watchMu.Unlock()
	writeJSON(w, map[string]bool{"ok": true, "killed": killed})
}

// GET 返回当前代理；POST {enabled,http,https} 更新并持久化（按会话注入，对新开/重开会话生效）。
func handleProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var p ProxyCfg
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		proxyMu.Lock()
		proxyCfg = p
		proxyMu.Unlock()
		saveProxy()
	}
	proxyMu.Lock()
	p := proxyCfg
	proxyMu.Unlock()
	writeJSON(w, p)
}

// 主机信息（弹窗用）：mesh IP、mac 序号、当前代理状态。
func handleInfo(w http.ResponseWriter, r *http.Request) {
	host := cfg.Listen
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	proxyMu.Lock()
	p := proxyCfg
	proxyMu.Unlock()
	writeJSON(w, map[string]interface{}{
		"macIndex": cfg.MacIndex, "meshIP": host, "proxy": p,
	})
}

func handleWatch(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("sid")
	writeJSON(w, map[string]bool{"external": evalWatch(sid)})
}

func handleReload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sid string `json:"sid"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Sid == "" {
		http.Error(w, "bad request", 400)
		return
	}
	watchMu.Lock()
	wt := watchers[req.Sid]
	watchMu.Unlock()
	tmux("kill-session", "-t", req.Sid)
	if wt != nil && wt.sessionID != "" {
		ensureTmux(req.Sid, cwdOf(wt.sessionID), claudeResumeCmd(wt.sessionID, wt.bypass))
		registerWatch(req.Sid, wt.sessionID, wt.bypass) // 重置 offset/tip/external，沿用 bypass
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---------------- 空闲回收 + 结束钩子 ----------------
func reaper() {
	for {
		time.Sleep(60 * time.Second)
		out, err := tmux("list-sessions", "-F", "#{session_name} #{session_attached} #{session_activity}")
		if err != nil {
			continue
		}
		now := time.Now().Unix()
		for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
			f := strings.Fields(ln)
			if len(f) < 3 || !strings.HasPrefix(f[0], "fleet-") {
				continue
			}
			attached, _ := strconv.Atoi(f[1])
			act, _ := strconv.ParseInt(f[2], 10, 64)
			if attached == 0 && now-act > cfg.IdleSec {
				onSessionEnd(f[0])
				tmux("kill-session", "-t", f[0])
			}
		}
	}
}

// 会话结束钩子：满足护栏则给 Desktop 发一次 Cmd+R
func onSessionEnd(sid string) {
	watchMu.Lock()
	wt := watchers[sid]
	delete(watchers, sid)
	watchMu.Unlock()
	if !cfg.AutoCmdR || wt == nil {
		return
	}
	// 护栏：Desktop 在运行 且 当前打开的正是这个 sessionId 才发
	if !desktopRunning() || !desktopOnSession(wt.sessionID) {
		return
	}
	script := fmt.Sprintf(
		`tell application %q to activate
tell application "System Events" to keystroke "r" using command down`, cfg.DesktopApp)
	exec.Command("osascript", "-e", script).Run()
}
func desktopRunning() bool {
	out, _ := exec.Command("pgrep", "-x", cfg.DesktopApp).Output()
	return strings.TrimSpace(string(out)) != ""
}
func desktopOnSession(sessionID string) bool {
	// 护栏：检查 ~/.claude/sessions/*.json 是否有 entrypoint=claude-desktop 且打开此 sessionID
	dir := filepath.Join(cfg.ClaudeHome, "sessions")
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		var s struct {
			SessionID  string `json:"sessionId"`
			Entrypoint string `json:"entrypoint"`
			Pid        int    `json:"pid"`
		}
		if json.Unmarshal(b, &s) == nil && s.SessionID == sessionID &&
			strings.Contains(s.Entrypoint, "desktop") && pidAlive(s.Pid) {
			return true
		}
	}
	return false
}

// ---------------- main ----------------
func main() {
	// 带参 → 自管理子命令（start/stop/restart/status/update/version/help）；
	// 无参 → 启动服务（launchd 即如此调用）。
	if len(os.Args) > 1 {
		os.Exit(runSelfCommand(os.Args[1:]))
	}
	runServer()
}

func runServer() {
	cfg = loadConfig()
	loadProxy()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", handleSessions)
	mux.HandleFunc("/api/projects", handleProjects)
	mux.HandleFunc("/api/open", handleOpen)
	mux.HandleFunc("/api/new", handleNew)
	mux.HandleFunc("/api/close", handleClose)
	mux.HandleFunc("/api/watch", handleWatch)
	mux.HandleFunc("/api/reload", handleReload)
	mux.HandleFunc("/api/proxy", handleProxy)
	mux.HandleFunc("/api/info", handleInfo)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	go reaper()
	log.Printf("fleet-agent listening on %s (mac index %s, idle %ds)", cfg.Listen, cfg.MacIndex, cfg.IdleSec)
	log.Fatal(http.ListenAndServe(cfg.Listen, mux))
}
