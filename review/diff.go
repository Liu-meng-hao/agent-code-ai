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
	// 迁移 SQL（.up.sql / .down.sql）通常由框架生成、审查价值低；普通 .sql 仍会审查
	".up.sql", ".down.sql",
}

// lowValueDirs 跳过整个目录：第三方依赖、构建产物，以及数据库迁移脚本目录。
var lowValueDirs = []string{"vendor", "node_modules", "dist", "build", "migrations", "migration"}

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
	for _, dir := range lowValueDirs {
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

// buildBatches 读取每个文件的 diff，按 hunk 粒度做增量过滤后分组成批次：
// 只保留「新增或内容变化」的 hunk（其余 hunk 上次已审查且未变化，不再重复送审），
// 再按 maxBatchTokens 把待审 diff 分组成可直接送入单次 LLM 调用的文本。
//
// 返回的 snapshot 是每个变更文件「当前全部 hunk 的内容指纹」，供调用方在提交成功后写回缓存；
// skipped 是变化部分仍无法拆分、只能整体跳过的文件。
func buildBatches(base, head string, files []string, c *reviewCache) (batches, skipped []string, snapshot map[string][]string) {
	snapshot = map[string][]string{}
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

		known := make(map[string]bool, len(c.Files[f]))
		for _, h := range c.Files[f] {
			known[h] = true
		}

		header, hunks := splitDiffHunks(diff)
		if len(hunks) == 0 {
			// 没有可识别的 hunk（如二进制 / 仅权限变更）：整段作为一个单元缓存，内容不变时不再审查
			h := hunkHash(diff)
			snapshot[f] = []string{h}
			if !known[h] {
				appendPiece(diff)
			}
			continue
		}

		// 按 hunk 内容指纹做增量：上次已审查且内容未变的 hunk 直接跳过（难点 5 第二层优化）
		var changed strings.Builder
		hashes := make([]string, 0, len(hunks))
		changedN := 0
		for _, hk := range hunks {
			h := hunkHash(hk)
			hashes = append(hashes, h)
			if known[h] {
				continue
			}
			changed.WriteString(hk)
			changedN++
		}
		snapshot[f] = hashes
		if changedN == 0 {
			continue // 该文件所有 hunk 均已审查且未变化
		}
		if changedN < len(hunks) {
			fmt.Printf("[ai-review] 文件 %s：%d/%d 个 hunk 新增或变化，其余 hunk 跳过\n", f, changedN, len(hunks))
		}

		// 变化 hunk 重新拼回文件头，仍是合法的 unified diff，可正常定位行号
		filtered := header + changed.String()
		tokens := estimateTokens(filtered)
		if tokens <= maxFileDiffTokens {
			appendPiece(filtered)
			continue
		}

		// 变化部分仍超过单文件上限：按 hunk 边界拆分，每片带文件头逐片送审（难点 1.3）
		pieces := splitFileDiff(filtered)
		if len(pieces) == 0 {
			fmt.Printf("[ai-review] 文件 %s 变化部分过大（约 %d tokens）且无法按 hunk 拆分，跳过并标记\n", f, tokens)
			skipped = append(skipped, f)
			continue
		}
		fmt.Printf("[ai-review] 文件 %s 变化部分过大（约 %d tokens），拆分为 %d 片分别审查\n", f, tokens, len(pieces))
		for _, p := range pieces {
			appendPiece(p)
		}
	}
	flush()
	return batches, skipped, snapshot
}

// splitDiffHunks 把单个文件的 diff 拆成文件头（diff --git / index / --- / +++ 等）
// 与 hunk 列表（每个 hunk 含其 @@ 头行）。二者拼接后仍是合法 diff。
func splitDiffHunks(diff string) (header string, hunks []string) {
	var head, hunk strings.Builder
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
			head.WriteString(ln)
		}
	}
	if hunk.Len() > 0 {
		hunks = append(hunks, hunk.String())
	}
	return head.String(), hunks
}

// splitFileDiff 把单个文件的 diff 按 hunk 边界拆成若干片段，每个片段都带上文件头
// （diff --git / index / --- / +++ 等），使其可独立送审并仍能定位到具体文件与行号。
// 返回空切片表示该 diff 中没有可识别的 hunk。
func splitFileDiff(diff string) []string {
	head, hunks := splitDiffHunks(diff)
	if len(hunks) == 0 {
		return nil
	}

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
