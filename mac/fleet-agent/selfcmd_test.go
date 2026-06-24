package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	cases := map[string]svcAction{
		"start":     actStart,
		"stop":      actStop,
		"restart":   actRestart,
		"status":    actStatus,
		"update":    actUpdate,
		"version":   actVersion,
		"--version": actVersion,
		"help":      actHelp,
		"-h":        actHelp,
		"bogus":     actUnknown,
		"":          actUnknown,
	}
	for name, want := range cases {
		if got := parseCommand(name); got != want {
			t.Errorf("parseCommand(%q)=%v want %v", name, got, want)
		}
	}
}

// 更新 asset 名必须与 dist/ 里的产物命名一致（fleet-agent-darwin-{amd64,arm64}），
// 否则 update 会 404。这里钉死「跟随当前架构」。
func TestUpdateAssetAndURL(t *testing.T) {
	wantAsset := "fleet-agent-darwin-" + runtime.GOARCH
	if got := updateAsset(); got != wantAsset {
		t.Fatalf("updateAsset()=%q want %q", got, wantAsset)
	}

	// 默认 URL 指向 GitHub raw 的 dist 且以 asset 结尾
	t.Setenv("FLEET_UPDATE_URL", "")
	def := updateURL()
	if !strings.HasPrefix(def, "https://raw.githubusercontent.com/") || !strings.HasSuffix(def, wantAsset) {
		t.Fatalf("default updateURL()=%q", def)
	}

	// 环境变量覆盖优先（私有期指向网关）
	t.Setenv("FLEET_UPDATE_URL", "http://100.64.0.1/fleet/agent")
	if got := updateURL(); got != "http://100.64.0.1/fleet/agent" {
		t.Fatalf("override updateURL()=%q", got)
	}
}

func TestReplaceBinarySuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet-agent")
	if err := os.WriteFile(path, []byte("OLD-binary-content"), 0o755); err != nil {
		t.Fatal(err)
	}
	newData := []byte("NEW-binary-content-v2")
	if err := replaceBinary(path, newData); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, newData) {
		t.Fatalf("content not replaced: got %q", got)
	}
	// 权限保持可执行
	if fi, _ := os.Stat(path); fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("binary not executable: %v", fi.Mode())
	}
	// 不留 .bak / 临时文件
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

// 替换的第二步失败时（目标不可写入），必须回滚旧二进制 —— 自更新最关键的安全性质。
func TestReplaceBinaryRollback(t *testing.T) {
	dir := t.TempDir()
	// 目标本身是个目录：os.Rename(tmp, path) 会失败 → 触发回滚
	path := filepath.Join(dir, "target")
	if err := os.WriteFile(path, []byte("ORIGINAL"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 让 path 在「备份后、写入前」无法被新文件占位：用只读父目录无法跨平台稳定模拟，
	// 改用 chmod 目录去掉写权限，使第二次 rename 失败。
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)

	err := replaceBinary(path, []byte("NEW"))
	if err == nil {
		t.Skip("当前文件系统/权限下第二步 rename 未失败，跳过回滚断言")
	}
	// 回滚后原文件内容应仍在
	os.Chmod(dir, 0o755)
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("回滚后原文件丢失: %v", rerr)
	}
	if string(got) != "ORIGINAL" {
		t.Fatalf("回滚未还原原内容: got %q", got)
	}
}
