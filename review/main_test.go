package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteStepSummary(t *testing.T) {
	// 未在 Actions 中运行（无 GITHUB_STEP_SUMMARY）时应静默返回，不写入任何文件
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	writeStepSummary(ReviewStats{Total: 1})

	path := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", path)
	writeStepSummary(ReviewStats{
		Batches:      3,
		Total:        4,
		Inline:       2,
		Orphan:       2,
		CrossFile:    1,
		Severity:     map[string]int{"high": 1, "low": 3},
		InputTokens:  100,
		OutputTokens: 20,
		PrevComments: 5,
		Adopted:      2,
	})
	// 追加写入：第二次调用不应覆盖已有内容
	writeStepSummary(ReviewStats{Total: 0})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 Step Summary 失败: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"AI 审查统计",
		"调用 LLM：3 次",
		"输入 100，输出 20，合计 120",
		"意见：4 条（行内 2，正文 2，跨文件 1）",
		"严重级分布：high 1，low 3",
		"历史意见采纳：2/5 条获得 👍",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Step Summary 缺少 %q，实际:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "AI 审查统计"); n != 2 {
		t.Errorf("应追加写入（两次调用得到两段），实际段数 %d:\n%s", n, got)
	}
}
