package main

import (
	"bytes"
	"fmt"
	"net"
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
	guiCLI     string
	guiForce   string
	guiErr     error
	ready      bool
	binVersion string
	resolve    func(configured, appPath, codexHome string) (string, string, error)

	probes int
}

// useDoctorFakes 安装假实现并在测试结束时还原，保证不碰真实 launchctl / 网络。
func useDoctorFakes(t *testing.T, f *doctorFakes) *doctorFakes {
	t.Helper()
	if f.plist == nil {
		f.plist = map[string]string{}
	}
	if f.resolve == nil {
		f.resolve = func(string, string, string) (string, string, error) {
			return "/fake/codex", codexBinSourceConfigured, nil
		}
	}

	oldLoaded, oldPID, oldHealth := doctorSvcLoaded, doctorSvcPID, doctorProbeHealth
	oldPlistEnv, oldListEnv := doctorPlistEnv, doctorListEnv
	oldResolve, oldBinVersion := doctorResolveBin, doctorBinVersion
	oldProbe := doctorProbeReadyz
	t.Cleanup(func() {
		doctorSvcLoaded, doctorSvcPID, doctorProbeHealth = oldLoaded, oldPID, oldHealth
		doctorPlistEnv, doctorListEnv = oldPlistEnv, oldListEnv
		doctorResolveBin, doctorBinVersion = oldResolve, oldBinVersion
		doctorProbeReadyz = oldProbe
	})

	doctorSvcLoaded = func() bool { return f.loaded }
	doctorSvcPID = func() string { return f.pid }
	doctorProbeHealth = func(string) string { return f.health }
	doctorPlistEnv = func(key string) string { return f.plist[key] }
	doctorListEnv = func(key string) (string, error) {
		switch key {
		case codexDesktopCLIPathEnv:
			return f.guiCLI, f.guiErr
		case codexDesktopForceCLIEnv:
			return f.guiForce, f.guiErr
		default:
			return "", fmt.Errorf("unexpected GUI key %s", key)
		}
	}

	doctorResolveBin = func(configured, appPath, codexHome string) (string, string, error) {
		return f.resolve(configured, appPath, codexHome)
	}
	doctorBinVersion = func(string, time.Duration) string { return f.binVersion }
	doctorProbeReadyz = func(string, time.Duration) bool {
		f.probes++
		return f.ready
	}

	return f
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
	dir, err := os.MkdirTemp("/tmp", "fleet-doctor.")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "proxy.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close(); os.RemoveAll(dir) })
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
		guiCLI:     filepath.Join(os.Getenv("HOME"), ".local/lib/macfleet/codex-desktop-launch.sh"),
		guiForce:   "1",
		ready:      true,
		binVersion: "0.159.0",
		resolve: func(string, string, string) (string, string, error) {
			return "/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex", codexBinSourceManifest, nil
		},
	})
}

func TestDoctorHealthyReport(t *testing.T) {
	healthyDoctorFakes(t)
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
	if got := reportValue(report, "可用 bin"); got != "/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex（来源 chatgpt-layout-manifest）" {
		t.Fatalf("可用 bin=%q\n%s", got, report)
	}
	if strings.Contains(report, "已替换") {
		t.Fatalf("只读体检不能声称修改了配置:\n%s", report)
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
}

func TestDoctorFailedWhenAppServerNotReadyAndEnvHijacked(t *testing.T) {
	f := healthyDoctorFakes(t)
	f.ready = false
	f.guiCLI = "ws://127.0.0.1:1/rpc" // 指向死端口

	var buf bytes.Buffer
	code := doctorRun(&buf, false)
	if code != 1 {
		t.Fatalf("明确故障应返回 1，实际 %d\n%s", code, buf.String())
	}
	report := buf.String()
	if got := reportValue(report, "readyz"); !strings.Contains(got, "未 ready") {
		t.Fatalf("readyz=%q\n%s", got, report)
	}
	if got := reportValue(report, "判定"); !strings.Contains(got, "与配置不一致") || !strings.Contains(got, "Desktop CLI hook") {
		t.Fatalf("判定=%q\n%s", got, report)
	}
	if !strings.Contains(report, "结论：FAILED") || !strings.Contains(report, "fleet-agent doctor --fix") {
		t.Fatalf("缺少 FAILED 结论或修复建议:\n%s", report)
	}
}

func TestDoctorFixDoesNothingWhenReady(t *testing.T) {
	healthyDoctorFakes(t)

	var buf bytes.Buffer
	if code := doctorRun(&buf, true); code != 0 {
		t.Fatalf("ready 时 --fix 应返回 0，实际 %d\n%s", code, buf.String())
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

func TestDoctorUsesLiveReadinessDespiteOldStateFile(t *testing.T) {
	f := healthyDoctorFakes(t)
	stateDir := t.TempDir()
	t.Setenv("FLEET_STATE_DIR", stateDir)
	statePath := filepath.Join(stateDir, "app-server.json")
	if err := os.WriteFile(statePath, []byte(`{"state":"failed","lastError":"old keeper failed","clearedDesktopEnv":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := doctorRun(&buf, true); code != 0 || !strings.Contains(buf.String(), "结论：HEALTHY") {
		t.Fatalf("live ready listener should be healthy: %d\n%s", code, buf.String())
	}
	f.ready = false
	buf.Reset()
	if code := doctorRun(&buf, false); code != 1 || !strings.Contains(buf.String(), "结论：FAILED") {
		t.Fatalf("live unavailable listener should fail: %d\n%s", code, buf.String())
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
	if got := reportValue(report, "可用 bin"); got != "解析失败" {
		t.Fatalf("可用 bin=%q\n%s", got, report)
	}
	if !strings.Contains(report, "结论：FAILED") || !strings.Contains(report, "setup-mac.sh") || !strings.Contains(report, "fleet-agent start") {
		t.Fatalf("缺少 FAILED 结论或修复建议:\n%s", report)
	}
}

// 非 shared 模式不探测 listener；安装器按所有权恢复，用户自定义值保留。
func TestDoctorIsolatedModePreservesCustomGUIEnv(t *testing.T) {
	f := healthyDoctorFakes(t)
	f.plist["FLEET_CODEX_APPSERVER_MODE"] = codexAppServerModeIsolated
	f.ready = false
	f.guiCLI = ""

	var buf bytes.Buffer
	if code := doctorRun(&buf, false); code != 0 {
		t.Fatalf("isolated 模式不应因 readyz 返回 1，实际 %d\n%s", code, buf.String())
	}
	report := buf.String()
	if got := reportValue(report, "readyz"); !strings.Contains(got, "跳过") {
		t.Fatalf("readyz=%q\n%s", got, report)
	}
	if got := reportValue(report, "判定"); !strings.Contains(got, "保留用户自定义值") {
		t.Fatalf("判定=%q\n%s", got, report)
	}

	f.guiCLI = codexSharedWebSocketEndpoint
	buf.Reset()
	if code := doctorRun(&buf, false); code != 0 {
		t.Fatalf("isolated + 残留注入应 DEGRADED（0），实际 %d\n%s", code, buf.String())
	}
	if got := reportValue(buf.String(), "判定"); !strings.Contains(got, "保留用户自定义值") {
		t.Fatalf("判定=%q\n%s", got, buf.String())
	}
}

func TestDoctorUnknownFlagReturnsUsageError(t *testing.T) {
	if code := runDoctor([]string{"--bogus"}); code != 2 {
		t.Fatalf("未知参数应返回 2，实际 %d", code)
	}
}

func TestDoctorFixDoesNotCreateRuntimeOutsideDesktop(t *testing.T) {
	f := healthyDoctorFakes(t)
	f.ready = false
	var out bytes.Buffer
	if code := doctorRun(&out, true); code != 1 {
		t.Fatalf("unavailable runtime must remain explicit: %d", code)
	}
	if strings.Contains(out.String(), "launchctl kickstart") || strings.Contains(out.String(), "$ launchctl") {
		t.Fatal(out.String())
	}
	if !strings.Contains(out.String(), "Desktop") {
		t.Fatal(out.String())
	}
}
