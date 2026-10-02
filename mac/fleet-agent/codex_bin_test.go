package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeCodexVersionScript = "#!/bin/sh\necho \"codex-cli 0.159.0\"\n"

func writeFakeBinary(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// newFakeApp 造一个空的假 ChatGPT.app，返回 appPath。
func newFakeApp(t *testing.T) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "ChatGPT.app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	return app
}

// addFakeCodexCLI 造新版布局的 Contents/Resources/codex-cli/bin/codex。
func addFakeCodexCLI(t *testing.T, app string) string {
	t.Helper()
	return writeFakeBinary(t, filepath.Join(app, "Contents", "Resources", "codex-cli", "bin", "codex"), fakeCodexVersionScript)
}

// addFakeLegacyCodex 造旧版布局的 Contents/Resources/codex。
func addFakeLegacyCodex(t *testing.T, app string) string {
	t.Helper()
	return writeFakeBinary(t, filepath.Join(app, "Contents", "Resources", "codex"), fakeCodexVersionScript)
}

// addFakeManifest 写新版布局的 codex-cli/codex-package.json。
func addFakeManifest(t *testing.T, app, entrypoint string) string {
	t.Helper()
	path := filepath.Join(app, "Contents", "Resources", "codex-cli", "codex-package.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("{\"layoutVersion\":1,\"entrypoint\":%q,\"version\":\"0.159.0\"}", entrypoint)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// emptyPATH 让 exec.LookPath("codex") 确定性地失败，避免测试机自带的 codex 干扰断言。
func emptyPATH(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty-bin"))
}

func TestResolveCodexBinNewLayoutWithManifest(t *testing.T) {
	app := newFakeApp(t)
	want := addFakeCodexCLI(t, app)
	addFakeManifest(t, app, "bin/codex")

	got, source, err := resolveCodexBin("", app, "")
	if err != nil {
		t.Fatalf("resolveCodexBin: %v", err)
	}
	if source != codexBinSourceManifest {
		t.Fatalf("source=%q want %q", source, codexBinSourceManifest)
	}
	if got != want {
		t.Fatalf("path=%q want %q", got, want)
	}
}

func TestResolveCodexBinNewLayoutWithoutManifest(t *testing.T) {
	app := newFakeApp(t)
	want := addFakeCodexCLI(t, app)

	got, source, err := resolveCodexBin("", app, "")
	if err != nil {
		t.Fatalf("resolveCodexBin: %v", err)
	}
	if source != codexBinSourceCodexCLI {
		t.Fatalf("source=%q want %q", source, codexBinSourceCodexCLI)
	}
	if got != want {
		t.Fatalf("path=%q want %q", got, want)
	}
}

func TestResolveCodexBinLegacyLayout(t *testing.T) {
	app := newFakeApp(t)
	want := addFakeLegacyCodex(t, app)

	got, source, err := resolveCodexBin("", app, "")
	if err != nil {
		t.Fatalf("resolveCodexBin: %v", err)
	}
	if source != codexBinSourceLegacy {
		t.Fatalf("source=%q want %q", source, codexBinSourceLegacy)
	}
	if got != want {
		t.Fatalf("path=%q want %q", got, want)
	}
}

// 事故主场景：plist 里写死的旧路径已不存在 → 必须自愈到新版布局。
func TestResolveCodexBinSelfHealsFromStaleConfigured(t *testing.T) {
	app := newFakeApp(t)
	want := addFakeCodexCLI(t, app)
	addFakeManifest(t, app, "bin/codex")
	stale := filepath.Join(t.TempDir(), "Contents", "Resources", "codex")

	got, source, err := resolveCodexBin(stale, app, "")
	if err != nil {
		t.Fatalf("resolveCodexBin: %v", err)
	}
	if got != want || source != codexBinSourceManifest {
		t.Fatalf("got (%q, %q) want (%q, %q)", got, source, want, codexBinSourceManifest)
	}
}

func TestResolveCodexBinConfiguredWins(t *testing.T) {
	configured := writeFakeBinary(t, filepath.Join(t.TempDir(), "bin", "codex"), fakeCodexVersionScript)
	app := newFakeApp(t)
	addFakeCodexCLI(t, app)

	got, source, err := resolveCodexBin(configured, app, "")
	if err != nil {
		t.Fatalf("resolveCodexBin: %v", err)
	}
	if got != configured || source != codexBinSourceConfigured {
		t.Fatalf("got (%q, %q) want (%q, %q)", got, source, configured, codexBinSourceConfigured)
	}
}

// 断链软链必须被拒绝，而不是当成可用候选；本例中应继续回落到旧布局。
func TestResolveCodexBinRejectsBrokenSymlink(t *testing.T) {
	app := newFakeApp(t)
	cliDir := filepath.Join(app, "Contents", "Resources", "codex-cli")
	if err := os.MkdirAll(filepath.Join(cliDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(cliDir, "bin", "codex")
	if err := os.Symlink(filepath.Join(cliDir, "bin", "codex-missing"), broken); err != nil {
		t.Fatal(err)
	}
	addFakeManifest(t, app, "bin/codex")
	want := addFakeLegacyCodex(t, app)

	got, source, err := resolveCodexBin("", app, "")
	if err != nil {
		t.Fatalf("resolveCodexBin: %v", err)
	}
	if got != want || source != codexBinSourceLegacy {
		t.Fatalf("got (%q, %q) want (%q, %q)", got, source, want, codexBinSourceLegacy)
	}
}

// manifest 的 entrypoint 含 ".." 必须整条跳过（否则可被指到 bundle 外）。
func TestResolveCodexBinRejectsEntrypointTraversal(t *testing.T) {
	app := newFakeApp(t)
	// manifest 若被采纳，会解析到 Contents/Resources/bin/codex —— 这里故意放一个真可执行文件。
	escape := writeFakeBinary(t, filepath.Join(app, "Contents", "Resources", "bin", "codex"), fakeCodexVersionScript)
	addFakeManifest(t, app, "../bin/codex")
	want := addFakeCodexCLI(t, app)

	got, source, err := resolveCodexBin("", app, "")
	if err != nil {
		t.Fatalf("resolveCodexBin: %v", err)
	}
	if got == escape {
		t.Fatalf("entrypoint 含 .. 仍被采纳: %q", escape)
	}
	if got != want || source != codexBinSourceCodexCLI {
		t.Fatalf("got (%q, %q) want (%q, %q)", got, source, want, codexBinSourceCodexCLI)
	}
}

func TestResolveCodexBinManagedStandalone(t *testing.T) {
	codexHome := t.TempDir()
	want := writeFakeBinary(t, filepath.Join(codexHome, "packages", "standalone", "current", "codex"), fakeCodexVersionScript)

	got, source, err := resolveCodexBin("", newFakeApp(t), codexHome)
	if err != nil {
		t.Fatalf("resolveCodexBin: %v", err)
	}
	if got != want || source != codexBinSourceManaged {
		t.Fatalf("got (%q, %q) want (%q, %q)", got, source, want, codexBinSourceManaged)
	}
}

func TestResolveCodexBinAllMissingErrorListsCandidates(t *testing.T) {
	emptyPATH(t)
	app := newFakeApp(t)
	codexHome := t.TempDir()
	configured := filepath.Join(t.TempDir(), "old", "codex")

	_, _, err := resolveCodexBin(configured, app, codexHome)
	if err == nil {
		t.Fatal("全部候选不可用时必须报错")
	}
	msg := err.Error()
	for _, want := range []string{
		configured,
		filepath.Join(app, "Contents", "Resources", "codex-cli", "codex-package.json"),
		filepath.Join(app, "Contents", "Resources", "codex-cli", "bin", "codex"),
		filepath.Join(app, "Contents", "Resources", "codex"),
		filepath.Join(codexHome, "packages", "standalone", "current", "codex"),
		"setup-mac.sh",
		"LookPath",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息缺少候选 %q:\n%s", want, msg)
		}
	}
}

func TestResolveCodexBinSkipsNonExecutableCandidate(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(configured, []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := newFakeApp(t)
	want := addFakeCodexCLI(t, app)

	got, source, err := resolveCodexBin(configured, app, "")
	if err != nil {
		t.Fatalf("resolveCodexBin: %v", err)
	}
	if got != want || source != codexBinSourceCodexCLI {
		t.Fatalf("got (%q, %q) want (%q, %q)", got, source, want, codexBinSourceCodexCLI)
	}
}

func TestCodexBinVersionReadsFirstLineLastField(t *testing.T) {
	path := writeFakeBinary(t, filepath.Join(t.TempDir(), "codex"), fakeCodexVersionScript)
	if got := codexBinVersion(path, 5*time.Second); got != "0.159.0" {
		t.Fatalf("codexBinVersion=%q want 0.159.0", got)
	}
}

func TestCodexBinVersionFailureReturnsEmpty(t *testing.T) {
	if got := codexBinVersion(filepath.Join(t.TempDir(), "missing"), 5*time.Second); got != "" {
		t.Fatalf("不存在的路径应返回空串，得到 %q", got)
	}
	if got := codexBinVersion("", 5*time.Second); got != "" {
		t.Fatalf("空路径应返回空串，得到 %q", got)
	}
}

func TestCodexBinVersionHonorsTimeout(t *testing.T) {
	path := writeFakeBinary(t, filepath.Join(t.TempDir(), "codex-hang"), "#!/bin/sh\nsleep 30\n")
	start := time.Now()
	if got := codexBinVersion(path, 200*time.Millisecond); got != "" {
		t.Fatalf("超时应返回空串，得到 %q", got)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("超时未生效，耗时 %s", elapsed)
	}
}

// 外部命令必须走包级可替换变量，否则测试无法脱离真实 codex 二进制。
func TestCodexBinVersionUsesInjectableCommand(t *testing.T) {
	old := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/bin/sh", "-c", "echo codex-cli 9.9.9")
	}
	t.Cleanup(func() { execCommand = old })

	if got := codexBinVersion("/nonexistent/codex", 5*time.Second); got != "9.9.9" {
		t.Fatalf("未使用注入的 execCommand：%q", got)
	}
}

// manifest 解析必须走 encoding/json（字段名 layoutVersion/entrypoint/version）。
func TestCodexManifestEntrypointParsesJSON(t *testing.T) {
	app := newFakeApp(t)
	path := addFakeManifest(t, app, "bin/codex")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest codexAppManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatalf("manifest 不是合法 JSON: %v", err)
	}
	entry, ok := codexManifestEntrypoint(path)
	if !ok || entry != "bin/codex" {
		t.Fatalf("codexManifestEntrypoint=(%q,%v) want (bin/codex,true)", entry, ok)
	}
}
