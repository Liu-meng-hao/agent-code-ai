package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// lowValueSuffixes 改动量大但审查价值低的文件后缀，直接跳过以节省 token。
var lowValueSuffixes = []string{
	".lock", ".sum", ".min.js", ".min.css", ".pb.go", ".generated.go",
}

// lowValueNames 常见的依赖锁文件。
var lowValueNames = map[string]bool{
	"package-lock.json": true,
	"yarn.lock":         true,
	"pnpm-lock.yaml":    true,
	"go.sum":            true,
	"Cargo.lock":        true,
	"poetry.lock":       true,
}

// isLowValueFile 判断文件是否属于"低价值"，例如锁文件、生成代码、第三方目录。
func isLowValueFile(path string) bool {
	base := filepath.Base(path)
	if lowValueNames[base] {
		return true
	}
	for _, s := range lowValueSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	lower := strings.ToLower(path)
	for _, dir := range []string{"/vendor/", "/node_modules/", "/dist/", "/build/"} {
		if strings.Contains(lower, dir) {
			return true
		}
	}
	return false
}

// runGit 执行 git 命令并返回 stdout。
func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s 失败: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// ChangedFiles 返回本次 PR 变更的文件列表（已过滤低价值文件）。
func ChangedFiles(base, head string) ([]string, error) {
	out, err := runGit("diff", "--name-only", "--diff-filter=ACMR", base, head)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isLowValueFile(line) {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

// BuildDiff 为每个文件生成带 5 行上下文的 diff 并拼接为单段文本。
func BuildDiff(base, head string, files []string) (string, error) {
	var sb strings.Builder
	for _, f := range files {
		diff, err := runGit("diff", "-U5", base, head, "--", f)
		if err != nil {
			continue // 单文件失败不影响整体
		}
		if strings.TrimSpace(diff) == "" {
			continue
		}
		sb.WriteString(diff)
		if !strings.HasSuffix(diff, "\n") {
			sb.WriteString("\n")
		}
	}
	return sb.String(), nil
}