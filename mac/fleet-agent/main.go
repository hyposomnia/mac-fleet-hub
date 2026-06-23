// fleet-agent —— 每台 Mac 的会话管理服务（纯标准库，零运行时依赖）。
//
// 职责：
//   GET  /api/sessions?scope=active|all   列出 Claude 会话（按 cwd 由前端分组）
//   GET  /api/projects                    历史会话的 cwd 去重 → 项目目录列表
//   POST /api/open    {sessionId}         在 tmux 起/复用窗口跑 claude --resume，返回终端入口
//   POST /api/new     {cwd}               在指定目录起新 claude
//   GET  /api/watch?sid=fleet-xxx         检测是否有 Desktop 外部写入（DAG 分叉判定）
//   POST /api/reload  {sid}               kill+resume 该窗口（拉取 Desktop 最新）
//
// 仅绑 mesh IP，不公网；访问控制交给 Headscale ACL（见 plan §5.1）。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
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
	MacIndex   string // 1/2/3 → 终端入口 /fleet/m{idx}/term
	IdleSec    int64  // 空闲回收秒数（默认 1800）
	AutoCmdR   bool   // 会话结束自动给 Desktop 发 Cmd+R
	DesktopApp string // osascript 目标应用名（默认 Claude）
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
	Live      bool   `json:"live"`
}

// jsonl 行（只取需要字段）
type line struct {
	Type        string          `json:"type"`
	AiTitle     string          `json:"aiTitle"`
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
	var firstUser string
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if l.Cwd != "" {
			m.cwd = l.Cwd
		}
		if l.GitBranch != "" {
			m.branch = l.GitBranch
		}
		if l.UUID != "" {
			m.lastUUID = l.UUID
		}
		if l.Type == "ai-title" && l.AiTitle != "" {
			m.title = l.AiTitle
		}
		if firstUser == "" && l.Type == "user" {
			firstUser = extractText(l.Message)
		}
	}
	if m.title == "" {
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
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// 扫描所有会话
func scanSessions() []Session {
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
func cwdOf(sid string) string {
	for _, s := range scanSessions() {
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
	return fmt.Sprintf("/fleet/m%s/term/?arg=%s", cfg.MacIndex, name)
}

// ---------------- watch 注册表（Desktop→ttyd 检测） ----------------
type watcher struct {
	sessionID string
	path      string
	offset    int64
	tip       string // mobileTip：当前手机分支叶子 uuid
	external  bool
}

var (
	watchers = map[string]*watcher{} // sid -> watcher
	watchMu  sync.Mutex
)

func registerWatch(sid, sessionID string) {
	path := jsonlPath(sessionID)
	w := &watcher{sessionID: sessionID, path: path}
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

func handleSessions(w http.ResponseWriter, r *http.Request) {
	all := scanSessions()
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

func handleOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.SessionID == "" {
		http.Error(w, "bad request", 400)
		return
	}
	cwd := cwdOf(req.SessionID)
	name := shortSid(req.SessionID)
	cmd := fmt.Sprintf("%s --resume %s", cfg.ClaudeBin, shellQuote(req.SessionID))
	if err := ensureTmux(name, cwd, cmd); err != nil {
		http.Error(w, "tmux: "+err.Error(), 500)
		return
	}
	registerWatch(name, req.SessionID)
	writeJSON(w, map[string]string{"url": termURL(name), "sid": name})
}

func handleNew(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cwd string `json:"cwd"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	name := fmt.Sprintf("fleet-new%d", time.Now().Unix())
	if err := ensureTmux(name, req.Cwd, cfg.ClaudeBin); err != nil {
		http.Error(w, "tmux: "+err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"url": termURL(name), "sid": name})
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
		cmd := fmt.Sprintf("%s --resume %s", cfg.ClaudeBin, shellQuote(wt.sessionID))
		ensureTmux(req.Sid, cwdOf(wt.sessionID), cmd)
		registerWatch(req.Sid, wt.sessionID) // 重置 offset/tip/external
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
	cfg = loadConfig()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", handleSessions)
	mux.HandleFunc("/api/projects", handleProjects)
	mux.HandleFunc("/api/open", handleOpen)
	mux.HandleFunc("/api/new", handleNew)
	mux.HandleFunc("/api/watch", handleWatch)
	mux.HandleFunc("/api/reload", handleReload)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	go reaper()
	log.Printf("fleet-agent listening on %s (mac index %s, idle %ds)", cfg.Listen, cfg.MacIndex, cfg.IdleSec)
	log.Fatal(http.ListenAndServe(cfg.Listen, mux))
}
