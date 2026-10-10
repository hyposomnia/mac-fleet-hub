// Resolve the current Codex executable for CLI sessions and diagnostics.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// execCommand 是外部命令调用的唯一入口（包级可替换），测试注入假实现时不用真起进程。
var execCommand = exec.Command

// 候选来源标识（resolveCodexBin 的第二个返回值）。
const (
	codexBinSourceConfigured = "configured"
	codexBinSourceManifest   = "chatgpt-layout-manifest"
	codexBinSourceCodexCLI   = "chatgpt-codex-cli"
	codexBinSourceLegacy     = "chatgpt-legacy"
	codexBinSourceManaged    = "managed-standalone"
	codexBinSourcePath       = "path"
)

// codexAppManifest 是新版 ChatGPT.app 的 Contents/Resources/codex-cli/codex-package.json。
// layoutVersion=1 / entrypoint="bin/codex" / version="0.159.0"。
type codexAppManifest struct {
	LayoutVersion int    `json:"layoutVersion"`
	Entrypoint    string `json:"entrypoint"`
	Version       string `json:"version"`
}

// codexCandidate 是一个待验证的 codex 可执行文件候选。
type codexCandidate struct {
	path   string
	source string
}

// resolveCodexBin 返回 (可执行文件绝对路径, 来源标识, error)。
//
// 每个候选都必须同时满足：存在、可执行、filepath.EvalSymlinks 能解析到真实文件
// （软链断链一律拒绝）。全部失败时返回的 error 会列出所有尝试过的候选路径与修复提示。
func resolveCodexBin(configured, appPath, codexHome string) (string, string, error) {
	candidates, notes := codexBinCandidates(configured, appPath, codexHome)
	tried := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		tried = append(tried, candidate.path)
		if !codexBinUsable(candidate.path) {
			continue
		}
		abs, err := filepath.Abs(candidate.path)
		if err != nil {
			abs = candidate.path
		}
		return abs, candidate.source, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "无法解析 Codex 可执行文件：已尝试 %d 个候选路径，均不可用（需存在、可执行、软链可解析）：",
		len(tried)+len(notes))
	for _, path := range tried {
		fmt.Fprintf(&b, "\n  - %s", path)
	}
	for _, note := range notes {
		fmt.Fprintf(&b, "\n  - %s", note)
	}
	b.WriteString("\n提示：ChatGPT.app 布局可能已变化，请重跑安装 `bash mac/setup-mac.sh`" +
		"（或把 FLEET_CODEX_BIN 指向当前可用的 codex 可执行文件）。")
	return "", "", errors.New(b.String())
}

// codexBinCandidates 按优先级列出候选；notes 记录「检查过但没能成为候选」的路径
// （manifest 缺失/entrypoint 非法、PATH 未命中），供错误信息与排障使用。
func codexBinCandidates(configured, appPath, codexHome string) ([]codexCandidate, []string) {
	var candidates []codexCandidate
	var notes []string

	if c := strings.TrimSpace(configured); c != "" {
		candidates = append(candidates, codexCandidate{path: c, source: codexBinSourceConfigured})
	}
	if app := strings.TrimSpace(appPath); app != "" {
		cliDir := filepath.Join(app, "Contents", "Resources", "codex-cli")
		manifestPath := filepath.Join(cliDir, "codex-package.json")
		if entrypoint, ok := codexManifestEntrypoint(manifestPath); ok {
			candidates = append(candidates, codexCandidate{
				path:   filepath.Join(cliDir, filepath.FromSlash(entrypoint)),
				source: codexBinSourceManifest,
			})
		} else {
			notes = append(notes, manifestPath+"（layout manifest 缺失或 entrypoint 非法，已跳过）")
		}
		candidates = append(candidates, codexCandidate{
			path:   filepath.Join(cliDir, "bin", "codex"),
			source: codexBinSourceCodexCLI,
		})
		candidates = append(candidates, codexCandidate{
			path:   filepath.Join(app, "Contents", "Resources", "codex"),
			source: codexBinSourceLegacy,
		})
	}
	if home := strings.TrimSpace(codexHome); home != "" {
		candidates = append(candidates, codexCandidate{
			path:   filepath.Join(home, "packages", "standalone", "current", "codex"),
			source: codexBinSourceManaged,
		})
	}
	if path, err := exec.LookPath("codex"); err == nil {
		candidates = append(candidates, codexCandidate{path: path, source: codexBinSourcePath})
	} else {
		notes = append(notes, "PATH 中未找到 codex（exec.LookPath 未命中）")
	}
	return candidates, notes
}

// codexManifestEntrypoint 读取 manifest 里的相对入口；文件缺失、JSON 坏、入口为空、
// 绝对路径或含 ".." 一律视为非法（跳过该候选，不报错中断）。
func codexManifestEntrypoint(manifestPath string) (string, bool) {
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", false
	}
	var manifest codexAppManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return "", false
	}
	entrypoint := strings.TrimSpace(manifest.Entrypoint)
	if entrypoint == "" || filepath.IsAbs(entrypoint) || strings.Contains(entrypoint, "..") {
		return "", false
	}
	return entrypoint, true
}

// codexBinUsable：存在、非目录、可执行、且软链能解析到真实文件。断链软链返回 false。
func codexBinUsable(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path) // Stat 跟随软链：断链会直接报错
	if err != nil || info.IsDir() {
		return false
	}
	if info.Mode().Perm()&0o111 == 0 {
		return false
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	realInfo, err := os.Stat(real)
	if err != nil || realInfo.IsDir() {
		return false
	}
	return true
}

// codexBinVersion 跑 `path --version`，取第一行最后一个字段（如 "codex-cli 0.159.0" → "0.159.0"）；
// 起进程失败、超时、无输出都返回空串。
func codexBinVersion(path string, timeout time.Duration) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cmd := execCommand(path, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	// 超时被 Kill 后，子进程留下的后代可能仍持有输出管道；WaitDelay 保证 Wait 不会
	// 被这种管道一直拖住（否则超时形同虚设）。
	cmd.WaitDelay = 250 * time.Millisecond
	if err := cmd.Start(); err != nil {
		return ""
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return ""
	}
	fields := strings.Fields(firstLine(out.String()))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}
