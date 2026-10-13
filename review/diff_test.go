package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens(""); got < 1 {
		t.Errorf("空串估算应至少为 1，实际 %d", got)
	}
	// 400 个 ASCII 字符约 100 tokens，允许一定误差
	ascii := ""
	for i := 0; i < 400; i++ {
		ascii += "a"
	}
	if got := estimateTokens(ascii); got < 90 || got > 110 {
		t.Errorf("400 ASCII 字符估算应接近 100，实际 %d", got)
	}
}

func TestIsLowValueFile(t *testing.T) {
	lowValue := []string{
		"go.sum", "package-lock.json", "web/app.min.js",
		"api/foo.pb.go", "node_modules/x/index.js",
	}
	for _, f := range lowValue {
		if !isLowValueFile(f) {
			t.Errorf("%s 应被判定为低价值文件", f)
		}
	}
	keep := []string{"review/main.go", "README.md", "internal/service.go"}
	for _, f := range keep {
		if isLowValueFile(f) {
			t.Errorf("%s 不应被判定为低价值文件", f)
		}
	}
}

func TestSplitFileDiff(t *testing.T) {
	const header = "diff --git a/big.go b/big.go\n" +
		"index 111..222 100644\n" +
		"--- a/big.go\n+++ b/big.go\n"
	// hunk 生成一个最小 hunk，body 为新增行内容
	hunk := func(start int, body string) string {
		return fmt.Sprintf("@@ -%d,1 +%d,1 @@\n+%s\n", start, start, body)
	}

	// 单 hunk：应切出 1 片，且保留文件头与 hunk 内容
	pieces := splitFileDiff(header + hunk(1, "small"))
	if len(pieces) != 1 {
		t.Fatalf("单 hunk 应切出 1 片，实际 %d", len(pieces))
	}
	if !strings.Contains(pieces[0], "+++ b/big.go") || !strings.Contains(pieces[0], "+small") {
		t.Errorf("片段应保留文件头与 hunk 内容: %q", pieces[0])
	}

	// 无 hunk（如仅改文件权限）：返回 nil，由上层决定整体跳过
	if got := splitFileDiff(header); got != nil {
		t.Errorf("无 hunk 时应返回 nil，实际 %v", got)
	}

	// 两个各自超过单批上限的 hunk：应拆成 2 片，每片都带文件头，hunk 不丢失
	big := strings.Repeat("a", 200000) // 约 50000 tokens
	pieces = splitFileDiff(header + hunk(1, big) + hunk(1000, big))
	if len(pieces) != 2 {
		t.Fatalf("两个超大 hunk 应切出 2 片，实际 %d", len(pieces))
	}
	for i, p := range pieces {
		if !strings.Contains(p, "+++ b/big.go") {
			t.Errorf("第 %d 片缺少文件头", i+1)
		}
	}
	if n := strings.Count(strings.Join(pieces, ""), "@@ -"); n != 2 {
		t.Errorf("切分后 hunk 总数应保持为 2，实际 %d", n)
	}
}
