// fleet-enroll —— 网关侧自助入网服务（纯标准库，零依赖）。
//
// 作用：装机的新 Mac 无法登录 Authelia，故用一个独立的 TOTP（入网专用密钥，与登录 2FA 分开）
// 来确认「是机主本人」。验证码正确 → 生成一次性短效 Headscale preauthkey 返回，客户端据此入网。
//
//   POST /join {code}         校验 TOTP → 服务端自动分配下一个空闲编号 → 返回 {loginServer, authKey, index}
//   GET  /healthz             存活探针
//
// 仅绑 127.0.0.1，由 nginx 暴露在 /enroll/（公开、不过 Authelia）。
// 防爆破：连续失败锁定 + 单次只发一次性 key。bootstrap.sh / uninstall.sh / bundle 由 nginx 静态托管。
package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------- 配置 ----------------
var (
	listen      = envOr("ENROLL_LISTEN", "127.0.0.1:7090")
	secretFile  = envOr("ENROLL_SECRET_FILE", "/etc/fleet-enroll/totp.secret")
	loginServer = envOr("ENROLL_LOGIN_SERVER", "https://fleet.example.com:8443") // 占位默认；部署时由 setup-server.sh 按 .env 注入真实值（HS_BASE）
	hsUser      = envOr("ENROLL_HS_USER", "1")        // headscale 用户 id
	hsBin       = envOr("ENROLL_HEADSCALE", "headscale")
	keyTTL      = envOr("ENROLL_KEY_TTL", "10m")      // preauthkey 有效期
	maxFails    = envInt("ENROLL_MAX_FAILS", 5)       // 锁定阈值
	lockMin     = envInt("ENROLL_LOCK_MIN", 15)       // 锁定分钟
	// Mac 的 web 显示名（仅供 PWA 展示，不改真实主机名）。存网关、所有浏览器共享。
	namesFile = envOr("ENROLL_NAMES_FILE", "/var/lib/fleet-enroll/names.json")
)

func envOr(k, d string) string { if v := os.Getenv(k); v != "" { return v }; return d }
func envInt(k string, d int) int { if v := os.Getenv(k); v != "" { if n, e := strconv.Atoi(v); e == nil { return n } }; return d }

// ---------------- TOTP（RFC 6238, SHA1, 6 位, 30s） ----------------
func loadSecret() (string, error) {
	b, err := os.ReadFile(secretFile)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(strings.TrimSpace(strings.ReplaceAll(string(b), " ", ""))), nil
}

func hotp(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	h := hmac.New(sha1.New, key)
	h.Write(buf[:])
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := (uint32(sum[off]&0x7f) << 24) | (uint32(sum[off+1]) << 16) | (uint32(sum[off+2]) << 8) | uint32(sum[off+3])
	return fmt.Sprintf("%06d", v%1000000)
}

// 校验 6 位验证码，允许 ±1 个时间窗（±30s 容差）。常量时间比较防计时侧信道。
func verifyTOTP(secret, code string, nowUnix int64) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return false
	}
	step := uint64(nowUnix / 30)
	for d := -1; d <= 1; d++ {
		want := hotp(key, step+uint64(int64(d)))
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// otpauth:// URI（供 setup 显示二维码/手动添加）
func otpauthURI(secret string) string {
	return "otpauth://totp/mac-fleet-hub:enroll?secret=" + secret + "&issuer=mac-fleet-hub&period=30&digits=6"
}

// ---------------- 防爆破（按来源 IP 计数 + 锁定） ----------------
// 关键：锁定必须 per-IP，否则匿名者刷错码只会触发全局锁定、把机主自己也挡在外面
// （lockout-as-DoS 反模式）。失败计数按 IP 隔离 + 滑动衰减 + 过期清理（防内存膨胀）。
type ipState struct {
	fails    int
	lockedTo time.Time
	last     time.Time
}

var (
	ipMu      sync.Mutex
	ipStates  = map[string]*ipState{}
	maxTracked = 50000 // 硬上限，极端泛洪下不无界增长
)

// clientIP：服务仅绑 127.0.0.1，nginx 以「覆盖（非追加）」方式设 X-Forwarded-For=$remote_addr，
// 故此处该头可信。取第一个值；缺失时回退 RemoteAddr。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		if ip := strings.TrimSpace(xff); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func lockedIP(ip string, now time.Time) bool {
	ipMu.Lock()
	defer ipMu.Unlock()
	st := ipStates[ip]
	return st != nil && now.Before(st.lockedTo)
}

func failIP(ip string, now time.Time) {
	ipMu.Lock()
	defer ipMu.Unlock()
	gcLocked(now)
	st := ipStates[ip]
	if st == nil {
		if len(ipStates) >= maxTracked {
			return // 跟踪表已满（异常泛洪）：放弃记账而非无界增长
		}
		st = &ipState{}
		ipStates[ip] = st
	}
	// 滑动衰减：距上次失败超过 lockMin 视为新一轮，清零计数（避免机主零星误输累积）
	if !st.last.IsZero() && now.Sub(st.last) > time.Duration(lockMin)*time.Minute {
		st.fails = 0
	}
	st.last = now
	st.fails++
	if st.fails >= maxFails {
		st.lockedTo = now.Add(time.Duration(lockMin) * time.Minute)
		st.fails = 0
	}
}

func okIP(ip string) { ipMu.Lock(); delete(ipStates, ip); ipMu.Unlock() }

// gcLocked：调用方已持 ipMu。清理「未锁定且久未活动」的条目。
func gcLocked(now time.Time) {
	for k, st := range ipStates {
		if now.After(st.lockedTo) && now.Sub(st.last) > time.Duration(lockMin)*time.Minute {
			delete(ipStates, k)
		}
	}
}

// ---------------- 入网 ----------------
type joinReq struct {
	Code  string `json:"code"`
	Index string `json:"index"`
}

func createPreauthKey() (string, error) {
	// 固定参数，无任何用户输入进入命令行：单次性（非 --reusable）、短效、打 tag:fleet-mac。
	out, err := exec.Command(hsBin, "preauthkeys", "create",
		"-u", hsUser, "--expiration", keyTTL, "--tags", "tag:fleet-mac").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("headscale: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// 取最后一行非空 token 作为 key（headscale 裸输出即 key）
	var key string
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			key = ln
		}
	}
	if !strings.HasPrefix(key, "hskey-") {
		return "", fmt.Errorf("unexpected headscale output")
	}
	return key, nil
}

// ---------------- 编号自动分配 ----------------
// 入网编号（mac<N>）由网关统一分配，客户端不再自报，避免人工填错/撞号。
// 真相源：headscale 现有节点的 hostname `mac<N>`，取 max+1（单调递增，不填补空缺）。
// 同时纳入 names.json 已占用编号，避免分配到一个已有显示名的号。
var (
	allocMu    sync.Mutex
	allocFloor int                                       // 本进程已发出的最大编号
	macHostRe  = regexp.MustCompile(`\bmac([0-9]+)\b`)   // 从 headscale 输出（表格或 -o json 均可）抓 mac<N>
)

// parseMaxMacIndex：从 headscale 任意格式输出里扫出最大的 mac<N> 编号；无则 0。
func parseMaxMacIndex(out string) int {
	max := 0
	for _, m := range macHostRe.FindAllStringSubmatch(out, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > max {
			max = n
		}
	}
	return max
}

// computeNext：纯函数，综合 headscale 输出、names.json 占用、本进程 floor 得下一编号。
// floor 关键：headscale 要等客户端真正 `tailscale up` 才出现新节点，分配后到入网前有窗口，
// 期间另一台入网若只看 headscale 会拿到同号；floor 记住本进程已发出的最大号，桥接这段延迟。
func computeNext(hsOut string, names map[string]string, floor int) int {
	max := parseMaxMacIndex(hsOut)
	for id := range names {
		if n, err := strconv.Atoi(strings.TrimPrefix(id, "m")); err == nil && n > max {
			max = n
		}
	}
	if floor > max {
		max = floor
	}
	return max + 1
}

// nextIndex：串行化分配下一个编号。整段持 allocMu，杜绝并发撞号。
func nextIndex() (int, error) {
	allocMu.Lock()
	defer allocMu.Unlock()
	out, err := exec.Command(hsBin, "nodes", "list").CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("headscale nodes list: %v: %s", err, strings.TrimSpace(string(out)))
	}
	next := computeNext(string(out), loadNames(), allocFloor)
	allocFloor = next
	return next, nil
}

func handleJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	now := time.Now()
	ip := clientIP(r)
	if lockedIP(ip, now) {
		writeErr(w, 429, "尝试次数过多，已临时锁定，请稍后再试")
		return
	}
	var req joinReq
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req) != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	// 客户端传来的 index 一律忽略——编号由服务端统一分配。
	secret, err := loadSecret()
	if err != nil || secret == "" {
		writeErr(w, 500, "服务端未配置入网密钥")
		return
	}
	if !verifyTOTP(secret, req.Code, now.Unix()) {
		failIP(ip, now)
		writeErr(w, 401, "验证码错误")
		return
	}
	idx, err := nextIndex()
	if err != nil {
		log.Printf("编号分配失败: %v", err)
		writeErr(w, 500, "分配机器编号失败")
		return
	}
	key, err := createPreauthKey()
	if err != nil {
		log.Printf("preauthkey 生成失败: %v", err)
		writeErr(w, 500, "生成入网密钥失败")
		return
	}
	okIP(ip)
	log.Printf("入网放行 index=%d（已发一次性 preauthkey）", idx)
	writeJSON(w, 200, map[string]string{"loginServer": loginServer, "authKey": key, "index": strconv.Itoa(idx)})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
func writeErr(w http.ResponseWriter, code int, msg string) { writeJSON(w, code, map[string]string{"error": msg}) }

// ---------------- Mac 显示名（网关存储，PWA 共享） ----------------
// 名字只为 web 展示，不动真实主机名。存 namesFile 的 {id:name} JSON。
// 暴露在 nginx 的 /api/names（经 Authelia auth_request 保护），故写操作必须已登录。
var (
	namesMu  sync.Mutex
	macIDRe  = regexp.MustCompile(`^m[1-9][0-9]?$`) // m1..m99，与 nginx /m{n} 路由对齐
	maxNames = 99
)

func validMacID(id string) bool { return macIDRe.MatchString(id) }

// sanitizeName：去首尾空白、剥控制字符（防注入/换行）、限 40 个 rune（保留中文）。
func sanitizeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if rs := []rune(s); len(rs) > 40 {
		s = strings.TrimSpace(string(rs[:40]))
	}
	return s
}

// 调用方须持 namesMu。读不到/坏文件都回空 map（显示名是非关键数据，容错优先）。
func readNamesLocked() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(namesFile); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func loadNames() map[string]string {
	namesMu.Lock()
	defer namesMu.Unlock()
	return readNamesLocked()
}

// 原子写：写 .tmp 再 rename，避免并发/崩溃下半截文件。
func saveNamesLocked(m map[string]string) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := namesFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, namesFile)
}

func handleNames(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, loadNames())
	case http.MethodPost:
		var req struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req) != nil {
			writeErr(w, 400, "请求格式错误")
			return
		}
		if !validMacID(req.ID) {
			writeErr(w, 400, "主机 ID 非法")
			return
		}
		name := sanitizeName(req.Name)
		namesMu.Lock()
		defer namesMu.Unlock()
		m := readNamesLocked()
		if name == "" {
			delete(m, req.ID) // 空名 = 清除自定义，回退默认「Mac N」
		} else {
			if _, exists := m[req.ID]; !exists && len(m) >= maxNames {
				writeErr(w, 400, "自定义名称数量已达上限")
				return
			}
			m[req.ID] = name
		}
		if err := saveNamesLocked(m); err != nil {
			log.Printf("保存显示名失败: %v", err)
			writeErr(w, 500, "保存失败")
			return
		}
		writeJSON(w, 200, m)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func main() {
	// 子命令：-show-uri 打印 otpauth URI（供 setup 显示二维码）；secret 不存在则报错提示先生成。
	if len(os.Args) > 1 && os.Args[1] == "-show-uri" {
		s, err := loadSecret()
		if err != nil || s == "" {
			fmt.Fprintln(os.Stderr, "未找到入网密钥，请先在 setup 中生成 "+secretFile)
			os.Exit(1)
		}
		fmt.Println(otpauthURI(s))
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/join", handleJoin)
	mux.HandleFunc("/names", handleNames)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	log.Printf("fleet-enroll 监听 %s（login=%s, hsUser=%s）", listen, loginServer, hsUser)
	srv := &http.Server{Addr: listen, Handler: mux, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
