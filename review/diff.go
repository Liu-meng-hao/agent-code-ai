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
	for _, dir := range []string{"vendor", "node_modules", "dist", "build"} {
		// 匹配仓库根目录（如 node_modules/x）与任意层级（如 a/vendor/x）
		if strings.HasPrefix(lower, dir+"/") || strings.Contains(lower, "/"+dir+"/") {
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

// Token 相关阈值（难点 1.1 / 1.3 / 7.1）。
const (
	// maxFileDiffTokens 单文件 diff 超过此值即视为超大文件，改为按 hunk 拆分后分批审查，
	// 避免单个文件撑爆上下文；仅在连 hunk 都无法切分时才整体跳过。
	maxFileDiffTokens = 20000
	// maxBatchTokens 单次 LLM 调用的 diff 输入上限；超过则拆分为多次调用。
	maxBatchTokens = 80000
)

// estimateTokens 粗略估算文本的 token 数，仅用于阈值判断：
// ASCII 约 4 字符/token，非 ASCII（如中文注释）按 2 token/字符保守估计。
func estimateTokens(s string) int {
	ascii, other := 0, 0
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			other++
		}
	}
	return ascii/4 + other*2 + 1
}

// fileDiff 生成单个文件带 5 行上下文的 diff。
func fileDiff(base, head, file string) (string, error) {
	return runGit("diff", "-U5", base, head, "--", file)
}

// buildBatches 读取每个文件的 diff，按 maxBatchTokens 把 diff 分组为若干批次。
// 单个文件超过 maxFileDiffTokens 时不整体跳过，而是按 hunk 拆分成多个片段分别审查；
// 返回的 batches 是可直接送入单次 LLM 调用的 diff 文本；skipped 是连 hunk 都无法切分、
// 只能整体跳过的文件。
func buildBatches(base, head string, files []string) (batches, skipped []string) {
	var cur strings.Builder
	curTokens := 0
	flush := func() {
		if curTokens > 0 {
			batches = append(batches, cur.String())
			cur.Reset()
			curTokens = 0
		}
	}
	// append 把一个 diff 片段并入当前批次，放不下就先 flush。
	appendPiece := func(piece string) {
		tokens := estimateTokens(piece)
		if curTokens > 0 && curTokens+tokens > maxBatchTokens {
			flush()
		}
		cur.WriteString(piece)
		if !strings.HasSuffix(piece, "\n") {
			cur.WriteByte('\n')
		}
		curTokens += tokens
	}

	for _, f := range files {
		diff, err := fileDiff(base, head, f)
		if err != nil {
			fmt.Printf("[ai-review] 获取 %s 的 diff 失败，跳过: %v\n", f, err)
			continue
		}
		if strings.TrimSpace(diff) == "" {
			continue
		}
		tokens := estimateTokens(diff)
		if tokens <= maxFileDiffTokens {
			appendPiece(diff)
			continue
		}

		// 超大文件：按 hunk 边界切分，每片都带文件头，逐片送审（难点 1.3）
		pieces := splitFileDiff(diff)
		if len(pieces) == 0 {
			fmt.Printf("[ai-review] 文件 %s diff 过大（约 %d tokens）且无法按 hunk 拆分，跳过并标记\n", f, tokens)
			skipped = append(skipped, f)
			continue
		}
		fmt.Printf("[ai-review] 文件 %s diff 过大（约 %d tokens），拆分为 %d 片分别审查\n", f, tokens, len(pieces))
		for _, p := range pieces {
			appendPiece(p)
		}
	}
	flush()
	return batches, skipped
}

// splitFileDiff 把单个文件的 diff 按 hunk 边界拆成若干片段，每个片段都带上文件头
// （diff --git / index / --- / +++ 等），使其可独立送审并仍能定位到具体文件与行号。
// 返回空切片表示该 diff 中没有可识别的 hunk。
func splitFileDiff(diff string) []string {
	var header, hunk strings.Builder
	var hunks []string
	inHunk := false
	for _, ln := range strings.SplitAfter(diff, "\n") {
		if strings.HasPrefix(ln, "@@") {
			if hunk.Len() > 0 {
				hunks = append(hunks, hunk.String())
				hunk.Reset()
			}
			inHunk = true
		}
		if inHunk {
			hunk.WriteString(ln)
		} else {
			header.WriteString(ln)
		}
	}
	if hunk.Len() > 0 {
		hunks = append(hunks, hunk.String())
	}
	if len(hunks) == 0 {
		return nil
	}

	head := header.String()
	headTokens := estimateTokens(head)
	var pieces []string
	var cur strings.Builder
	curTokens := 0
	for _, h := range hunks {
		tokens := headTokens + estimateTokens(h)
		if curTokens > 0 && curTokens+tokens > maxBatchTokens {
			pieces = append(pieces, cur.String())
			cur.Reset()
			curTokens = 0
		}
		cur.WriteString(head)
		cur.WriteString(h)
		curTokens += tokens
	}
	if cur.Len() > 0 {
		pieces = append(pieces, cur.String())
	}
	return pieces
}
