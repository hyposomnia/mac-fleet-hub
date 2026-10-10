package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// doctorFakes 描述一次 doctor 体检的假环境。
type doctorFakes struct {
	loaded     bool
	pid        string
	listen     string
	health     string
	plist      map[string]string
	guiEnv     string
	guiErr     error
	ready      bool
	statePath  string
	binVersion string
	resolve    func(configured, appPath, codexHome string) (string, string, error)
	runErr     map[string]error // 命令 → 错误（模拟 launchctl 失败）

	cmds   []string
	probes int
}

// useDoctorFakes 安装假实现并在测试结束时还原，保证不碰真实 launchctl / 网络。
func useDoctorFakes(t *testing.T, f *doctorFakes) *doctorFakes {
	t.Helper()
	if f.plist == nil {
		f.plist = map[string]string{}
	}
	if f.statePath == "" {
		f.statePath = filepath.Join(t.TempDir(), "state", "app-server.json")
	}
	if f.resolve == nil {
		f.resolve = func(string, string, string) (string, string, error) {
			return "/fake/codex", codexBinSourceConfigured, nil
		}
	}

	oldLoaded, oldPID, oldHealth := doctorSvcLoaded, doctorSvcPID, doctorProbeHealth
	oldPlistEnv, oldListEnv := doctorPlistEnv, doctorListEnv
	oldResolve, oldBinVersion := doctorResolveBin, doctorBinVersion
	oldProbe, oldRunCmd, oldStatePath := doctorProbeReadyz, doctorRunCmd, doctorStatePath
	oldAttempts, oldInterval := doctorFixAttempts, doctorFixInterval
	t.Cleanup(func() {
		doctorSvcLoaded, doctorSvcPID, doctorProbeHealth = oldLoaded, oldPID, oldHealth
		doctorPlistEnv, doctorListEnv = oldPlistEnv, oldListEnv
		doctorResolveBin, doctorBinVersion = oldResolve, oldBinVersion
		doctorProbeReadyz, doctorRunCmd, doctorStatePath = oldProbe, oldRunCmd, oldStatePath
		doctorFixAttempts, doctorFixInterval = oldAttempts, oldInterval
	})

	doctorSvcLoaded = func() bool { return f.loaded }
	doctorSvcPID = func() string { return f.pid }
	doctorProbeHealth = func(string) string { return f.health }
	doctorPlistEnv = func(key string) string { return f.plist[key] }
	doctorListEnv = func(key string) (string, error) {
		if key != codexDesktopWebSocketEnv {
			return "", fmt.Errorf("测试只期望查询 %s，实际 %s", codexDesktopWebSocketEnv, key)
		}
		return f.guiEnv, f.guiErr
	}
	doctorResolveBin = func(configured, appPath, codexHome string) (string, string, error) {
		return f.resolve(configured, appPath, codexHome)
	}
	doctorBinVersion = func(string, time.Duration) string { return f.binVersion }
	doctorProbeReadyz = func(string, time.Duration) bool {
		f.probes++
		return f.ready
	}
	doctorRunCmd = func(name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		f.cmds = append(f.cmds, line)
		if f.runErr != nil {
			if err, ok := f.runErr[line]; ok {
				return "launchctl: 模拟失败", err
			}
		}
		return "", nil
	}
	doctorStatePath = func() string { return f.statePath }
	doctorFixAttempts, doctorFixInterval = 1, 0 // 测试里不做真实等待
	return f
}

func (f *doctorFakes) hasCmd(substr string) bool {
	for _, cmd := range f.cmds {
		if strings.Contains(cmd, substr) {
			return true
		}
	}
	return false
}

// reportLine 返回报告中以 label 开头的第一行（去缩进）；找不到返回空串。
func reportLine(report, label string) string {
	for _, line := range strings.Split(report, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, label) {
			return trimmed
		}
	}
	return ""
}

// reportValue 返回 label 行去掉 label 后的取值。
func reportValue(report, label string) string {
	line := reportLine(report, label)
	if line == "" {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(line, label))
}

// writeTestSock 造一个假 Unix proxy 文件并设定权限。
func writeTestSock(t *testing.T, perm os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex-app-server.sock")
	if err := os.WriteFile(path, []byte("sock"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
	return path
}

func healthyDoctorFakes(t *testing.T) *doctorFakes {
	t.Helper()
	return useDoctorFakes(t, &doctorFakes{
		loaded: true, pid: "4242", listen: "127.0.0.1:7682", health: "ok",
		plist: map[string]string{
			"FLEET_CODEX_APPSERVER_MODE": codexAppServerModeShared,
			"FLEET_CODEX_BIN":            "/Applications/ChatGPT.app/Contents/Resources/codex",
			"FLEET_CODEX_HOME":           "/tmp/codex-home",
			"FLEET_CODEX_DESKTOP_WS_URL": codexSharedWebSocketEndpoint,
			"FLEET_CODEX_APPSERVER_SOCK": writeTestSock(t, 0o600),
		},
		guiEnv:     codexSharedWebSocketEndpoint,
		ready:      true,
		binVersion: "0.159.0",
		resolve: func(string, string, string) (string, string, error) {
			return "/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex", codexBinSourceManifest, nil
		},
	})
}

func TestDoctorHealthyReport(t *testing.T) {
	f := healthyDoctorFakes(t)
	var buf bytes.Buffer
	if code := doctorRun(&buf, false); code != 0 {
		t.Fatalf("健康应返回 0，实际 %d\n%s", code, buf.String())
	}
	report := buf.String()

	if got := reportValue(report, "launchd"); got != "已加载" {
		t.Fatalf("launchd=%q\n%s", got, report)
	}
	if got := reportValue(report, "PID"); got != "4242" {
		t.Fatalf("PID=%q\n%s", got, report)
	}
	if got := reportValue(report, "模式"); got != codexAppServerModeShared {
		t.Fatalf("模式=%q\n%s", got, report)
	}
	if got := reportValue(report, "配置 bin"); got != "/Applications/ChatGPT.app/Contents/Resources/codex（来自 plist）" {
		t.Fatalf("配置 bin=%q\n%s", got, report)
	}
	if got := reportValue(report, "生效 bin"); got != "/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex（来源 chatgpt-layout-manifest）" {
		t.Fatalf("生效 bin=%q\n%s", got, report)
	}
	if !strings.Contains(report, "已替换 plist 里写死的旧路径") {
		t.Fatalf("未提示自愈替换:\n%s", report)
	}
	if !strings.Contains(report, "0.159.0") {
		t.Fatalf("缺少 codex --version:\n%s", report)
	}
	if got := reportValue(report, "readyz"); !strings.Contains(got, "ready") || strings.Contains(got, "未 ready") {
		t.Fatalf("readyz=%q\n%s", got, report)
	}
	if got := reportValue(report, "Unix proxy"); !strings.Contains(got, "0600") {
		t.Fatalf("Unix proxy=%q\n%s", got, report)
	}
	if got := reportValue(report, "判定"); got != "与配置一致" {
		t.Fatalf("判定=%q\n%s", got, report)
	}
	if !strings.Contains(report, "结论：HEALTHY") {
		t.Fatalf("缺少 HEALTHY 结论:\n%s", report)
	}
	if !strings.Contains(report, "暂无（未发现需要修复的问题）") {
		t.Fatalf("健康时不应给修复建议:\n%s", report)
	}
	if len(f.cmds) != 0 {
		t.Fatalf("非 --fix 不应执行任何命令: %v", f.cmds)
	}
}

func TestDoctorFailedWhenAppServerNotReadyAndEnvHijacked(t *testing.T) {
	f := healthyDoctorFakes(t)
	f.ready = false
	f.guiEnv = "ws://127.0.0.1:1/rpc" // 指向死端口

	var buf bytes.Buffer
	code := doctorRun(&buf, false)
	if code != 1 {
		t.Fatalf("明确故障应返回 1，实际 %d\n%s", code, buf.String())
	}
	report := buf.String()
	if got := reportValue(report, "readyz"); !strings.Contains(got, "未 ready") {
		t.Fatalf("readyz=%q\n%s", got, report)
	}
	if got := reportValue(report, "判定"); !strings.Contains(got, "与配置不一致") || !strings.Contains(got, "风险：Desktop 可能被指向死端口") {
		t.Fatalf("判定=%q\n%s", got, report)
	}
	if !strings.Contains(report, "结论：FAILED") || !strings.Contains(report, "fleet-agent doctor --fix") {
		t.Fatalf("缺少 FAILED 结论或修复建议:\n%s", report)
	}
}

func TestDoctorFixPreservesSharedIntentAndDoesNotKillRunningKeeper(t *testing.T) {
	f := healthyDoctorFakes(t)
	f.ready = false

	var buf bytes.Buffer
	code := doctorRun(&buf, true)
	if code != 1 {
		t.Fatalf("--fix 后仍不 ready 应返回 1，实际 %d\n%s", code, buf.String())
	}
	if f.hasCmd("launchctl unsetenv ") || f.hasCmd("launchctl kickstart -k") {
		t.Fatalf("恢复不能分流 Desktop 或强杀 writer: %v", f.cmds)
	}
	if !f.hasCmd("launchctl kickstart gui/") || !f.hasCmd("/"+codexAppServerLabel) {
		t.Fatalf("未发出 kickstart: %v", f.cmds)
	}
	if f.probes < 2 {
		t.Fatalf("--fix 后必须重新探测 readyz，probes=%d", f.probes)
	}
	out := buf.String()
	for _, want := range []string{
		"$ launchctl kickstart gui/",
		"结论：FAILED（shared app-server 仍未 ready）",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("--fix 输出缺少 %q:\n%s", want, out)
		}
	}
}

// --fix 恢复成功时必须返回 0，并提示如何让 Desktop 重新接入。
func TestDoctorFixReportsRecovery(t *testing.T) {
	f := healthyDoctorFakes(t)
	f.ready = false
	// 第一次探测（报告）失败，之后的复测成功。
	f.probes = -1 // 让下方闭包按第几次调用翻转
	doctorProbeReadyz = func(string, time.Duration) bool {
		f.probes++
		return f.probes > 0
	}

	var buf bytes.Buffer
	if code := doctorRun(&buf, true); code != 0 {
		t.Fatalf("恢复后应返回 0，实际 %d\n%s", code, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "结论：HEALTHY（shared app-server 已恢复）") {
		t.Fatalf("缺少恢复结论:\n%s", out)
	}
	if !strings.Contains(out, "codex-desktop-env.sh shared") {
		t.Fatalf("缺少 Desktop 重新接入提示:\n%s", out)
	}
}

func TestDoctorFixStartFailurePreservesSharedIntent(t *testing.T) {
	f := healthyDoctorFakes(t)
	f.ready = false
	f.runErr = map[string]error{
		"launchctl kickstart " + svcDomain() + "/" + codexAppServerLabel: fmt.Errorf("模拟失败"),
	}
	var buf bytes.Buffer
	if code := doctorRun(&buf, true); code != 1 {
		t.Fatalf("失败须返回 1: %d", code)
	}
	if !strings.Contains(buf.String(), "模拟失败") {
		t.Fatalf("未输出启动失败原因: %s", buf.String())
	}
	if f.hasCmd("unsetenv") || f.hasCmd("kickstart -k") {
		t.Fatalf("故障不能取消共享或强杀: %v", f.cmds)
	}
}

func TestDoctorFixDoesNothingWhenReady(t *testing.T) {
	f := healthyDoctorFakes(t)

	var buf bytes.Buffer
	if code := doctorRun(&buf, true); code != 0 {
		t.Fatalf("ready 时 --fix 应返回 0，实际 %d\n%s", code, buf.String())
	}
	if len(f.cmds) != 0 {
		t.Fatalf("ready 时不允许修改任何环境: %v", f.cmds)
	}
	if !strings.Contains(buf.String(), "不做任何 GUI 域改动") {
		t.Fatalf("缺少 ready 短路说明:\n%s", buf.String())
	}
}

func TestDoctorDegradedOnSockPermission(t *testing.T) {
	f := healthyDoctorFakes(t)
	f.plist["FLEET_CODEX_APPSERVER_SOCK"] = writeTestSock(t, 0o644)

	var buf bytes.Buffer
	if code := doctorRun(&buf, false); code != 0 {
		t.Fatalf("仅 sock 权限异常应返回 0（DEGRADED），实际 %d\n%s", code, buf.String())
	}
	report := buf.String()
	if !strings.Contains(report, "结论：DEGRADED") {
		t.Fatalf("结论应为 DEGRADED:\n%s", report)
	}
	if got := reportValue(report, "Unix proxy"); !strings.Contains(got, "应为 0600") {
		t.Fatalf("Unix proxy=%q\n%s", got, report)
	}
}

func TestDoctorShowsAppServerStateFile(t *testing.T) {
	f := healthyDoctorFakes(t)
	statePath := filepath.Join(t.TempDir(), "state", "app-server.json")
	if err := writeAppServerState(statePath, appServerStateFailed, "readyz 连续 3 次探测失败", true); err != nil {
		t.Fatal(err)
	}
	f.statePath = statePath

	var buf bytes.Buffer
	if code := doctorRun(&buf, false); code != 0 {
		t.Fatalf("熔断历史应 DEGRADED 而非 FAILED，实际 %d\n%s", code, buf.String())
	}
	report := buf.String()
	if !strings.Contains(report, "结论：DEGRADED") {
		t.Fatalf("结论应为 DEGRADED:\n%s", report)
	}
	if got := reportValue(report, "state"); got != appServerStateFailed {
		t.Fatalf("state=%q\n%s", got, report)
	}
	if got := reportValue(report, "lastError"); got != "readyz 连续 3 次探测失败" {
		t.Fatalf("lastError=%q\n%s", got, report)
	}
	if got := reportValue(report, "摘除记录"); !strings.HasPrefix(got, "是") {
		t.Fatalf("摘除记录=%q\n%s", got, report)
	}
}

// 同一个状态文件由 shell 侧监督包装与看门狗共同维护：keeper 的 ok/failed 词汇
// 必须被正确识别，诊断字段要展示出来。
func TestDoctorAcceptsKeeperStateVocabulary(t *testing.T) {
	writeKeeperState := func(t *testing.T, body string) string {
		t.Helper()
		statePath := filepath.Join(t.TempDir(), "state", "app-server.json")
		if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statePath, []byte(body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return statePath
	}

	f := healthyDoctorFakes(t)
	f.statePath = writeKeeperState(t, `{"state":"ok","lastError":"","updatedAt":"2026-09-30T12:00:00Z","clearedDesktopEnv":false,"codexBin":"/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex","listen":"ws://127.0.0.1:47682/rpc"}`)
	var buf bytes.Buffer
	if code := doctorRun(&buf, false); code != 0 {
		t.Fatalf("keeper ok 状态应返回 0，实际 %d\n%s", code, buf.String())
	}
	report := buf.String()
	if !strings.Contains(report, "结论：HEALTHY") {
		t.Fatalf("keeper ok 状态不应降级:\n%s", report)
	}
	if got := reportValue(report, "codexBin"); got != "/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex" {
		t.Fatalf("codexBin=%q\n%s", got, report)
	}

	f.statePath = writeKeeperState(t, `{"state":"failed","lastError":"keeper 连续启动失败","updatedAt":"2026-09-30T12:00:00Z","clearedDesktopEnv":1}`)
	buf.Reset()
	if code := doctorRun(&buf, false); code != 0 {
		t.Fatalf("熔断历史应 DEGRADED（0），实际 %d\n%s", code, buf.String())
	}
	report = buf.String()
	if !strings.Contains(report, "结论：DEGRADED") || !strings.Contains(report, "keeper 连续启动失败") {
		t.Fatalf("keeper failed 状态未被点名:\n%s", report)
	}
	if got := reportValue(report, "摘除记录"); !strings.HasPrefix(got, "是") {
		t.Fatalf("clearedDesktopEnv=1 应显示摘除记录: %q\n%s", got, report)
	}
}

func TestDoctorFailsOnUnresolvableCodexAndNoService(t *testing.T) {
	f := healthyDoctorFakes(t)
	f.loaded = false
	f.health = "-"
	f.resolve = func(string, string, string) (string, string, error) {
		return "", "", fmt.Errorf("无法解析 Codex 可执行文件：已尝试 1 个候选路径\n  - /old/codex\n提示：ChatGPT.app 布局可能已变化，请重跑安装 `bash mac/setup-mac.sh`")
	}

	var buf bytes.Buffer
	if code := doctorRun(&buf, false); code != 1 {
		t.Fatalf("服务未加载 + codex 解析失败应返回 1，实际 %d\n%s", code, buf.String())
	}
	report := buf.String()
	if got := reportValue(report, "launchd"); got != "未加载" {
		t.Fatalf("launchd=%q\n%s", got, report)
	}
	if got := reportValue(report, "生效 bin"); got != "解析失败" {
		t.Fatalf("生效 bin=%q\n%s", got, report)
	}
	if !strings.Contains(report, "结论：FAILED") || !strings.Contains(report, "setup-mac.sh") || !strings.Contains(report, "fleet-agent start") {
		t.Fatalf("缺少 FAILED 结论或修复建议:\n%s", report)
	}
}

// 非 shared 模式：不做 readyz 判定，GUI 域应清空；残留注入要报风险。
func TestDoctorIsolatedModeExpectsClearedGUIEnv(t *testing.T) {
	f := healthyDoctorFakes(t)
	f.plist["FLEET_CODEX_APPSERVER_MODE"] = codexAppServerModeIsolated
	f.ready = false
	f.guiEnv = ""

	var buf bytes.Buffer
	if code := doctorRun(&buf, false); code != 0 {
		t.Fatalf("isolated 模式不应因 readyz 返回 1，实际 %d\n%s", code, buf.String())
	}
	report := buf.String()
	if got := reportValue(report, "readyz"); !strings.Contains(got, "跳过") {
		t.Fatalf("readyz=%q\n%s", got, report)
	}
	if got := reportValue(report, "判定"); !strings.Contains(got, "已清空") {
		t.Fatalf("判定=%q\n%s", got, report)
	}

	f.guiEnv = codexSharedWebSocketEndpoint
	buf.Reset()
	if code := doctorRun(&buf, false); code != 0 {
		t.Fatalf("isolated + 残留注入应 DEGRADED（0），实际 %d\n%s", code, buf.String())
	}
	if got := reportValue(buf.String(), "判定"); !strings.Contains(got, "非预期注入") {
		t.Fatalf("判定=%q\n%s", got, buf.String())
	}
}

func TestDoctorUnknownFlagReturnsUsageError(t *testing.T) {
	if code := runDoctor([]string{"--bogus"}); code != 2 {
		t.Fatalf("未知参数应返回 2，实际 %d", code)
	}
}
