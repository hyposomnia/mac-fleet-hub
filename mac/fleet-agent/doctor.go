// fleet-agent doctor inspects shared readiness and can start a stopped keeper.
// Shared Desktop intent is preserved; recovery never kills an active writer.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// shared app-server 的 launchd 标签（见 mac/com.macfleet.codex-shared-app-server.plist）。
	codexAppServerLabel = "com.macfleet.codex-app-server"

	doctorAppPathEnv          = "FLEET_CODEX_DESKTOP_APP_PATH"
	doctorCodexVersionTimeout = 5 * time.Second
)

// 可注入入口：默认复用 selfcmd.go / codex_bin.go / desktop_env.go 的真实实现，
// 测试（doctor_test.go）替换成假实现，避免真实 launchctl、网络与进程调用。
var (
	doctorSvcLoaded   = svcLoaded
	doctorSvcPID      = svcPID
	doctorProbeHealth = probeHealth
	doctorPlistEnv    = plistEnv
	doctorListEnv     = launchctlGetenv
	doctorResolveBin  = resolveCodexBin
	doctorBinVersion  = codexBinVersion
	doctorProbeReadyz = probeReadyz
	doctorRunCmd      = runCmd
	doctorPlistPath   = svcPlistPath

	// --fix 复测次数与间隔（测试里调小以免真等待）。
	doctorFixAttempts = 6
	doctorFixInterval = time.Second
)

// doctorSummary 是报告里给调用方（退出码、--fix 决策）看的结论。
type doctorSummary struct {
	failed     bool   // 发现明确故障 → 退出码 1
	sharedMode bool   // 当前是否 shared 模式
	ready      bool   // shared app-server readyz 是否 ready（非 shared 模式视为无需修复）
	desktopURL string // 期望注入 GUI 域的 Desktop WS 端点
}

// runDoctor：`fleet-agent doctor [--fix]` 的入口。
func runDoctor(args []string) int {
	fix := false
	for _, arg := range args {
		switch arg {
		case "--fix":
			fix = true
		case "":
		default:
			fmt.Fprintf(os.Stderr, "doctor: 未知参数 %q（仅支持 --fix）\n", arg)
			return 2
		}
	}
	return doctorRun(os.Stdout, fix)
}

// doctorRun 输出体检报告并按需自愈；返回进程退出码（明确故障 1，其余 0）。
func doctorRun(out io.Writer, fix bool) int {
	report, summary := buildDoctorReport()
	fmt.Fprint(out, report)

	if fix {
		switch {
		case !summary.sharedMode:
			fmt.Fprintln(out, "--fix：当前不是 shared 模式，不做任何 GUI 域改动。")
		case summary.ready:
			fmt.Fprintln(out, "--fix：shared app-server readyz 正常，不做任何 GUI 域改动。")
		default:
			return doctorFix(out, summary.desktopURL)
		}
	}
	if summary.failed {
		return 1
	}
	return 0
}

// buildDoctorReport 收集体检结果并渲染成文本报告。
func buildDoctorReport() (string, doctorSummary) {
	cfgNow := loadConfig()
	summary := doctorSummary{}
	degraded := false
	var fixes []string

	var b strings.Builder
	b.WriteString("== fleet-agent doctor ==\n")

	// ---------------- 服务 ----------------
	loaded := doctorSvcLoaded()
	listen := strings.TrimSpace(doctorPlistEnv("FLEET_LISTEN"))
	if listen == "" {
		listen = cfgNow.Listen
	}
	pid := ""
	health := "-"
	if loaded {
		pid = strings.TrimSpace(doctorSvcPID())
		health = doctorProbeHealth(listen)
	}
	b.WriteString("服务\n")
	loadedText := "未加载"
	if loaded {
		loadedText = "已加载"
	}
	if pid == "" || pid == "-" {
		pid = "-"
	}
	fmt.Fprintf(&b, "  launchd    %s\n", loadedText)
	fmt.Fprintf(&b, "  plist      %s\n", doctorPlistPath())
	fmt.Fprintf(&b, "  PID        %s\n", pid)
	fmt.Fprintf(&b, "  监听       %s\n", listen)
	fmt.Fprintf(&b, "  健康       %s\n", health)
	fmt.Fprintf(&b, "  版本       %s\n", version)

	switch {
	case !loaded:
		summary.failed = true
		fixes = append(fixes, "launchd 未加载 fleet-agent：运行 `fleet-agent start`，或重跑 `bash mac/setup-mac.sh` 重装 LaunchAgent。")
	case health != "ok":
		degraded = true
		fixes = append(fixes, fmt.Sprintf("fleet-agent /api/health 返回 %q（监听 %s）：查看 %s，必要时 `fleet-agent restart`。", health, listen, fleetLogPath("agent.err")))
	}

	// ---------------- Codex ----------------
	modeRaw := strings.TrimSpace(doctorPlistEnv("FLEET_CODEX_APPSERVER_MODE"))
	if modeRaw == "" {
		modeRaw = cfgNow.CodexMode
	}
	mode := normalizeCodexAppServerMode(modeRaw)
	summary.sharedMode = mode == codexAppServerModeShared

	configuredBin := strings.TrimSpace(doctorPlistEnv("FLEET_CODEX_BIN"))
	binFromPlist := configuredBin != ""
	if configuredBin == "" {
		configuredBin = cfgNow.CodexBin
	}
	codexHome := strings.TrimSpace(doctorPlistEnv("FLEET_CODEX_HOME"))
	if codexHome == "" {
		codexHome = cfgNow.CodexHome
	}
	appPath := strings.TrimSpace(os.Getenv(doctorAppPathEnv))
	if appPath == "" {
		appPath = defaultCodexDesktopApp
	}

	resolved, source, resolveErr := doctorResolveBin(configuredBin, appPath, codexHome)

	b.WriteString("Codex\n")
	fmt.Fprintf(&b, "  模式       %s\n", mode)
	binSource := "（来自运行环境）"
	if binFromPlist {
		binSource = "（来自 plist）"
	}
	fmt.Fprintf(&b, "  配置 bin   %s%s\n", configuredBin, binSource)
	switch {
	case resolveErr != nil:
		summary.failed = true
		b.WriteString("  可用 bin   解析失败\n")
		for _, line := range strings.Split(resolveErr.Error(), "\n") {
			fmt.Fprintf(&b, "             %s\n", line)
		}
		fixes = append(fixes, "Codex 可执行文件无法解析：重跑 `bash mac/setup-mac.sh`，或把 FLEET_CODEX_BIN 指向当前可用的 codex。")
	default:
		fmt.Fprintf(&b, "  可用 bin   %s（来源 %s）\n", resolved, source)
		if binVersion := doctorBinVersion(resolved, doctorCodexVersionTimeout); binVersion != "" {
			fmt.Fprintf(&b, "  版本       %s\n", binVersion)
		} else {
			degraded = true
			fmt.Fprintf(&b, "  版本       （%s --version 无输出）\n", resolved)
			fixes = append(fixes, fmt.Sprintf("`%s --version` 无输出：确认该二进制可执行且未损坏。", resolved))
		}
	}

	// ---------------- shared app-server ----------------
	desktopURL := strings.TrimSpace(doctorPlistEnv("FLEET_CODEX_DESKTOP_WS_URL"))
	if desktopURL == "" {
		desktopURL = strings.TrimSpace(cfgNow.CodexDesktopURL)
	}
	if desktopURL == "" {
		desktopURL = codexSharedWebSocketEndpoint
	}
	summary.desktopURL = desktopURL

	b.WriteString("shared app-server\n")
	fmt.Fprintf(&b, "  Desktop WS %s\n", desktopURL)
	if !summary.sharedMode {
		summary.ready = true
		fmt.Fprintf(&b, "  readyz     跳过（当前模式 %s 不使用 shared loopback app-server）\n", mode)
	} else {
		summary.ready = doctorProbeReadyz(desktopURL, desktopEnvReadyzTimeout)
		readyText := "未 ready"
		if summary.ready {
			readyText = "ready"
		}
		fmt.Fprintf(&b, "  readyz     %s（%s）\n", readyText, readyzURL(desktopURL))
		if !summary.ready {
			summary.failed = true
			fixes = append(fixes, fmt.Sprintf("shared app-server 未 ready：运行 `fleet-agent doctor --fix`；仍失败看 %s 与 `launchctl print gui/<uid>/%s`。", codexAppServerLogPath(), codexAppServerLabel))
		}
	}

	sockRaw := strings.TrimSpace(doctorPlistEnv("FLEET_CODEX_APPSERVER_SOCK"))
	if sockRaw == "" {
		sockRaw = strings.TrimSpace(cfgNow.CodexSock)
	}
	switch {
	case sockRaw == "":
		degraded = true
		b.WriteString("  Unix proxy （未配置）\n")
		fixes = append(fixes, "未配置 FLEET_CODEX_APPSERVER_SOCK：shared 模式应指向 keeper 提供的 0600 Unix proxy。")
	case strings.HasPrefix(sockRaw, "ws://") || strings.HasPrefix(sockRaw, "wss://"):
		fmt.Fprintf(&b, "  Unix proxy 不适用（FLEET_CODEX_APPSERVER_SOCK 是 loopback WS 端点 %s）\n", sockRaw)
	default:
		sockPath := strings.TrimPrefix(sockRaw, "unix://")
		info, err := os.Stat(sockPath)
		switch {
		case err != nil:
			degraded = true
			fmt.Fprintf(&b, "  Unix proxy %s（不可用: %v）\n", sockPath, err)
			fixes = append(fixes, "shared app-server 的 Unix proxy 不存在：确认 com.macfleet.codex-app-server 已加载，`launchctl print gui/<uid>/"+codexAppServerLabel+"`。")
		case info.Mode().Perm() != 0o600:
			degraded = true
			fmt.Fprintf(&b, "  Unix proxy %s（权限 %04o，应为 0600）\n", sockPath, info.Mode().Perm())
			fixes = append(fixes, fmt.Sprintf("Unix proxy %s 权限为 %04o：应为 0600，重跑 `bash mac/setup-mac.sh` 修复。", sockPath, info.Mode().Perm()))
		default:
			fmt.Fprintf(&b, "  Unix proxy %s（0600，正常）\n", sockPath)
		}
	}

	// ---------------- GUI 域环境变量 ----------------
	b.WriteString("GUI 域环境变量\n")
	actual, getenvErr := doctorListEnv(codexDesktopWebSocketEnv)
	actual = strings.TrimSpace(actual)
	actualText := actual
	if actualText == "" {
		actualText = "（空）"
	}
	fmt.Fprintf(&b, "  %s  %s\n", codexDesktopWebSocketEnv, actualText)
	switch {
	case getenvErr != nil:
		degraded = true
		fmt.Fprintf(&b, "  判定       读取失败：%v\n", getenvErr)
	case !summary.sharedMode:
		if actual == "" {
			fmt.Fprintf(&b, "  判定       已清空（%s 模式期望）\n", mode)
		} else {
			degraded = true
			fmt.Fprintf(&b, "  判定       非预期注入（%s 模式应清空；风险：Desktop 可能被指向死端口）\n", mode)
			fixes = append(fixes, "非 shared 模式仍注入 CODEX_APP_SERVER_WS_URL：运行 `launchctl unsetenv CODEX_APP_SERVER_WS_URL`。")
		}
	case actual == "":
		degraded = true
		fmt.Fprintf(&b, "  判定       为空（shared 模式下 Desktop 不会自动接入；期望 %s）\n", desktopURL)
		fixes = append(fixes, fmt.Sprintf("GUI 域未注入 Desktop WS：确认 shared app-server ready 后运行 `bash mac/codex-desktop-env.sh shared %s` 并用 `/usr/bin/open --env %s=%s -a %s` 重开 ChatGPT.app。", desktopURL, codexDesktopWebSocketEnv, desktopURL, appPath))
	case actual == desktopURL:
		b.WriteString("  判定       与配置一致\n")
	default:
		degraded = true
		fmt.Fprintf(&b, "  判定       与配置不一致（配置 %s，实际 %s；风险：Desktop 可能被指向死端口）\n", desktopURL, actual)
		fixes = append(fixes, fmt.Sprintf("GUI 域 WS 端点与配置不一致：确认 shared 恢复后运行 `bash mac/codex-desktop-env.sh shared %s` 并重开 ChatGPT.app。", desktopURL))
	}

	conclusion := "HEALTHY"
	switch {
	case summary.failed:
		conclusion = "FAILED"
	case degraded:
		conclusion = "DEGRADED"
	}
	fmt.Fprintf(&b, "结论：%s\n", conclusion)
	b.WriteString("修复建议\n")
	if len(fixes) == 0 {
		b.WriteString("  - 暂无（未发现需要修复的问题）\n")
	}
	for _, fix := range fixes {
		fmt.Fprintf(&b, "  - %s\n", fix)
	}

	return b.String(), summary
}

// doctorFix starts a stopped keeper without interrupting a concurrently running listener.
func doctorFix(out io.Writer, desktopURL string) int {
	target := svcDomain() + "/" + codexAppServerLabel
	probeTarget := readyzURL(desktopURL)

	fmt.Fprintln(out, "== doctor --fix：shared app-server 未 ready，开始自愈 ==")

	fmt.Fprintf(out, "$ launchctl kickstart %s\n", target)
	if detail, err := doctorRunCmd("launchctl", "kickstart", target); err != nil {
		fmt.Fprintf(out, "  → 失败: %v %s\n", err, firstLine(detail))
	} else {
		fmt.Fprintln(out, "  → ok，已请求启动 shared app-server")
	}

	fmt.Fprintf(out, "重新探测 %s …\n", probeTarget)
	ready := false
	for attempt := 1; attempt <= doctorFixAttempts; attempt++ {
		if doctorProbeReadyz(desktopURL, desktopEnvReadyzTimeout) {
			ready = true
			break
		}
		if attempt < doctorFixAttempts && doctorFixInterval > 0 {
			time.Sleep(doctorFixInterval)
		}
	}

	if !ready {
		fmt.Fprintln(out, "结论：FAILED（shared app-server 仍未 ready）")
		fmt.Fprintf(out, "  下一步：查看 %s 与 `launchctl print %s`。\n", codexAppServerLogPath(), target)
		return 1
	}

	fmt.Fprintln(out, "结论：HEALTHY（shared app-server 已恢复）")
	fmt.Fprintf(out, "提示：如需 Desktop 立即重新接入，运行 `bash mac/codex-desktop-env.sh shared %s`，再用 `/usr/bin/open --env %s=%s -a %s` 重开 app。\n",
		desktopURL, codexDesktopWebSocketEnv, desktopURL, defaultCodexDesktopApp)
	return 0
}

// fleetLogPath 返回本机 Fleet 日志文件路径：默认 ~/Library/Logs/macfleet，
// 与 setup-mac.sh 渲染进 plist 的 FLEET_LOG_DIR 一致（显式设置时跟随）。
func fleetLogPath(name string) string {
	if dir := strings.TrimSpace(os.Getenv("FLEET_LOG_DIR")); dir != "" {
		return filepath.Join(dir, name)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, "Library", "Logs", "macfleet", name)
}

// Shared launchd stderr path from mac/com.macfleet.codex-shared-app-server.plist.
func codexAppServerLogPath() string { return "/tmp/macfleet-codex-app-server.err" }
