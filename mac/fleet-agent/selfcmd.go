// selfcmd.go —— fleet-agent 的自管理子命令：start/stop/restart/status/update。
//
// 无参数时 main() 走 runServer()（launchd 即如此调用，plist 无需改）；带参时由
// runSelfCommand 分发。update 自下载替换二进制并重启服务，免去逐机重装。
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// version 由构建期 -ldflags "-X main.version=..." 注入；本地构建为 dev。
var version = "dev"

const svcLabel = "com.macfleet.fleet-agent"

// 默认更新源：仓库内预编译产物（dist/）。公开后匿名可取；私有期用 FLEET_UPDATE_URL
// 指向网关 mesh 地址、或配合 FLEET_UPDATE_TOKEN 走 GitHub 私有 raw。
const defaultUpdateBase = "https://raw.githubusercontent.com/YOUR_GH_USER/mac-fleet-hub/master/mac/fleet-agent/dist"

const usage = `fleet-agent —— 每台 Mac 的会话管理服务

用法:
  fleet-agent           启动服务（前台；通常由 launchd 调用，无需手动跑）
  fleet-agent start     加载并启动 launchd 服务
  fleet-agent stop      停止并卸载 launchd 服务
  fleet-agent restart   重启服务（kickstart -k）
  fleet-agent status    查看运行状态（PID / 监听 / 健康 / 版本）
  fleet-agent update    下载最新二进制、原子替换并重启服务
  fleet-agent version   打印版本
  fleet-agent help      显示本帮助

环境变量:
  FLEET_UPDATE_URL    覆盖默认下载地址（默认 GitHub raw 的 dist/<arch>）
  FLEET_UPDATE_TOKEN  私有仓库下载用的 GitHub token（加 Authorization 头）
`

// ---------------- 命令分发 ----------------

type svcAction int

const (
	actUnknown svcAction = iota
	actStart
	actStop
	actRestart
	actStatus
	actUpdate
	actVersion
	actHelp
)

// parseCommand：命令名 → 动作。纯函数，便于单测；执行(含 launchctl/网络)留在 runSelfCommand。
func parseCommand(name string) svcAction {
	switch name {
	case "start":
		return actStart
	case "stop":
		return actStop
	case "restart":
		return actRestart
	case "status":
		return actStatus
	case "update":
		return actUpdate
	case "version", "-v", "--version":
		return actVersion
	case "help", "-h", "--help":
		return actHelp
	default:
		return actUnknown
	}
}

// runSelfCommand：执行子命令，返回进程退出码。
func runSelfCommand(args []string) int {
	switch parseCommand(args[0]) {
	case actStart:
		return done(svcStart())
	case actStop:
		return done(svcStop())
	case actRestart:
		return done(svcRestart())
	case actStatus:
		fmt.Println(svcStatus())
		return 0
	case actUpdate:
		return done(cmdUpdate())
	case actVersion:
		fmt.Println(version)
		return 0
	case actHelp:
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n%s", args[0], usage)
		return 2
	}
}

func done(err error) int {
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}
	return 0
}

// ---------------- launchd 服务 ----------------

func svcDomain() string  { return fmt.Sprintf("gui/%d", os.Getuid()) }
func svcTarget() string  { return svcDomain() + "/" + svcLabel }
func svcPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", svcLabel+".plist")
}

func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// svcLoaded：服务是否已在 launchd 注册（不论是否有 PID）。
func svcLoaded() bool {
	_, err := runCmd("launchctl", "print", svcTarget())
	return err == nil
}

func svcStart() error {
	plist := svcPlistPath()
	if _, err := os.Stat(plist); err != nil {
		return fmt.Errorf("找不到服务定义 %s（请先用 setup-mac.sh 安装）", plist)
	}
	if svcLoaded() {
		fmt.Println("服务已在运行")
		return nil
	}
	if out, err := runCmd("launchctl", "bootstrap", svcDomain(), plist); err != nil {
		if svcLoaded() { // 竞态/已加载 → 当成功
			return nil
		}
		return fmt.Errorf("启动失败: %s", firstLine(out))
	}
	fmt.Println("已启动")
	return nil
}

func svcStop() error {
	// KeepAlive=true：必须 bootout 卸载才真停，否则 kill 后自动重启。
	out, err := runCmd("launchctl", "bootout", svcTarget())
	if err != nil {
		low := strings.ToLower(out)
		if strings.Contains(low, "no such process") || strings.Contains(low, "could not find") {
			fmt.Println("服务本就未运行")
			return nil
		}
		return fmt.Errorf("停止失败: %s", firstLine(out))
	}
	fmt.Println("已停止")
	return nil
}

func svcRestart() error {
	if !svcLoaded() {
		return svcStart() // 未加载时 kickstart 无目标，等价于启动
	}
	if out, err := runCmd("launchctl", "kickstart", "-k", svcTarget()); err != nil {
		return fmt.Errorf("重启失败: %s", firstLine(out))
	}
	fmt.Println("已重启")
	return nil
}

func svcStatus() string {
	loaded := svcLoaded()
	listen := plistEnv("FLEET_LISTEN")
	if listen == "" {
		listen = loadConfig().Listen
	}
	run := "未加载"
	if loaded {
		if pid := svcPID(); pid != "" && pid != "-" {
			run = "运行中 (PID " + pid + ")"
		} else {
			run = "已加载，未运行"
		}
	}
	health := "-"
	if loaded {
		health = probeHealth(listen)
	}
	return fmt.Sprintf("服务   %s\n状态   %s\n监听   %s\n健康   %s\n版本   %s",
		svcLabel, run, listen, health, version)
}

// svcPID：从 launchctl list 解析当前 PID（"PID" = 1234;）；未运行返回空。
func svcPID() string {
	out, err := runCmd("launchctl", "list", svcLabel)
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "\"PID\"") {
			v := strings.SplitN(ln, "=", 2)
			if len(v) == 2 {
				return strings.TrimSuffix(strings.TrimSpace(v[1]), ";")
			}
		}
	}
	return ""
}

// plistEnv：从已安装 plist 里抠出某个 EnvironmentVariables 值（如 FLEET_LISTEN）。
// 简单文本扫描，避免引入 plist 解析依赖。
func plistEnv(key string) string {
	b, err := os.ReadFile(svcPlistPath())
	if err != nil {
		return ""
	}
	s := string(b)
	i := strings.Index(s, "<key>"+key+"</key>")
	if i < 0 {
		return ""
	}
	rest := s[i:]
	a := strings.Index(rest, "<string>")
	z := strings.Index(rest, "</string>")
	if a < 0 || z < 0 || a > z {
		return ""
	}
	return rest[a+len("<string>") : z]
}

func probeHealth(listen string) string {
	if listen == "" {
		return "未知"
	}
	resp, err := (&http.Client{Timeout: time.Second}).Get("http://" + listen + "/api/health")
	if err != nil {
		return "无响应"
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(b)) == "ok" {
		return "ok"
	}
	return "异常(" + resp.Status + ")"
}

// ---------------- update（自更新） ----------------

func updateAsset() string { return "fleet-agent-darwin-" + runtime.GOARCH }

func updateURL() string {
	if u := os.Getenv("FLEET_UPDATE_URL"); u != "" {
		return u
	}
	return defaultUpdateBase + "/" + updateAsset()
}

func cmdUpdate() error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位自身路径失败: %v", err)
	}
	if real, err := filepath.EvalSymlinks(self); err == nil {
		self = real
	}

	url := updateURL()
	fmt.Printf("下载 %s …\n", url)
	data, err := downloadBinary(url)
	if err != nil {
		return err
	}
	if len(data) < 1<<20 { // <1MB 几乎肯定不是合法二进制（多半是 404/HTML 错误页）
		return fmt.Errorf("下载内容异常（仅 %d 字节），已中止；请检查 %s", len(data), url)
	}

	if cur, err := os.ReadFile(self); err == nil && bytes.Equal(sha(cur), sha(data)) {
		fmt.Println("已是最新版本，无需更新")
		return nil
	}

	if err := replaceBinary(self, data); err != nil {
		return fmt.Errorf("替换二进制失败（已回滚）: %v", err)
	}
	fmt.Println("二进制已更新，重启服务 …")
	if err := svcRestart(); err != nil {
		return fmt.Errorf("二进制已更新但服务重启失败，请手动 `fleet-agent restart`：%v", err)
	}
	fmt.Println("✅ 更新完成")
	return nil
}

func downloadBinary(url string) ([]byte, error) {
	req, _ := http.NewRequest("GET", url, nil)
	if tok := os.Getenv("FLEET_UPDATE_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "token "+tok)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载失败 HTTP %d（%s）", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// replaceBinary：原子替换 path 处的可执行文件，失败回滚旧文件。
// 临时文件建在同目录以保证 rename 是原子的（同一文件系统）。
func replaceBinary(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".fleet-agent-new-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 成功后已被 rename 走，这里是失败兜底
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	bak := path + ".bak"
	if err := os.Rename(path, bak); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Rename(bak, path) // 回滚
		return err
	}
	os.Remove(bak)
	return nil
}

func sha(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
