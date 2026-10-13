package main

import (
	"strings"
	"testing"
)

func TestStripCodeFence(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"{\"comments\":[]}", "{\"comments\":[]}"},
		{"```json\n{\"comments\":[]}\n```", "{\"comments\":[]}"},
		{"```\n{\"comments\":[]}\n```", "{\"comments\":[]}"},
		{"  ```json\n{\"comments\":[]}\n```  ", "{\"comments\":[]}"},
	}
	for _, c := range cases {
		if got := stripCodeFence(c.in); got != c.want {
			t.Errorf("stripCodeFence(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestParseComments(t *testing.T) {
	raw := "```json\n{\"comments\":[{\"severity\":\"high\",\"file\":\"a.go\",\"line\":3,\"category\":\"logic\",\"issue\":\"x\",\"suggestion\":\"y\"}]}\n```"
	comments, err := ParseComments(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(comments) != 1 || comments[0].File != "a.go" || comments[0].Line != 3 {
		t.Fatalf("解析结果不符: %+v", comments)
	}

	if _, err := ParseComments("这不是 JSON"); err == nil {
		t.Error("非法输入应当返回解析错误")
	}
}

func TestEscapeMarkdown(t *testing.T) {
	got := escapeMarkdown("<script>alert(1)</script> & more")
	want := "&lt;script&gt;alert(1)&lt;/script&gt; &amp; more"
	if got != want {
		t.Errorf("escapeMarkdown = %q, 期望 %q", got, want)
	}
}

func TestFormatInlineBodyEscapes(t *testing.T) {
	body := FormatInlineBody(Comment{
		Severity:   "critical",
		Category:   "security",
		Issue:      "<img src=x onerror=alert(1)>",
		Suggestion: "修复 <html> 注入",
	})
	if strings.Contains(body, "<img") || strings.Contains(body, "<html>") {
		t.Errorf("行内评论正文未转义 HTML: %s", body)
	}
}

func TestNormalizedSeverity(t *testing.T) {
	if got := normalizedSeverity("HIGH"); got != "unknown" {
		t.Errorf("大小写不同的值应归为 unknown，实际 %q", got)
	}
	if got := normalizedSeverity("high"); got != "high" {
		t.Errorf("合法值应原样返回，实际 %q", got)
	}
}

func TestValidateComments(t *testing.T) {
	changed := []string{"a.go", "b/c.go"}
	comments := []Comment{
		{Severity: "high", File: "a.go", Line: 3, Issue: "ok"},
		{Severity: "critical", File: " b/c.go ", Line: 10, Issue: "路径两端空白应被裁剪"},
		{Severity: "HIGH", File: "a.go", Line: 3, Issue: "severity 非法"}, // 大小写不同即非法
		{Severity: "high", File: "z.go", Line: 3, Issue: "file 不在变更列表"}, // 未变更文件
		{Severity: "high", File: "", Line: 3, Issue: "file 为空"},         // 空 file
		{Severity: "high", File: "a.go", Line: 0, Issue: "line 非正"},     // line<=0
		{Severity: "low", File: "a.go", Line: -2, Issue: "line 为负"},     // line<0
	}

	valid, dropped := ValidateComments(comments, changed)
	if dropped != 5 {
		t.Errorf("应丢弃 5 条，实际 %d", dropped)
	}
	if len(valid) != 2 {
		t.Fatalf("应保留 2 条，实际 %d: %+v", len(valid), valid)
	}
	if valid[0].File != "a.go" || valid[1].File != "b/c.go" {
		t.Errorf("保留的意见不符: %+v", valid)
	}
}

func TestFormatStatsFooter(t *testing.T) {
	footer := FormatStatsFooter(ReviewStats{
		Batches:      2,
		Total:        3,
		Inline:       2,
		Orphan:       1,
		CrossFile:    1,
		Severity:     map[string]int{"critical": 1, "high": 2},
		InputTokens:  1200,
		OutputTokens: 300,
		PrevComments: 4,
		Adopted:      3,
	})
	for _, want := range []string{
		"调用 LLM：2 次",
		"输入 1200，输出 300，合计 1500",
		"意见：3 条（行内 2，正文 1）",
		"跨文件影响意见：1 条（已并入正文）",
		"critical 1，high 2",
		"历史意见采纳：3/4 条获得 👍",
	} {
		if !strings.Contains(footer, want) {
			t.Errorf("统计信息缺少 %q，实际: %s", want, footer)
		}
	}

	// 无 token 数据时应省略用量行，避免展示误导性的 0
	none := FormatStatsFooter(ReviewStats{Total: 0})
	if strings.Contains(none, "token 用量") {
		t.Errorf("无用量数据时不应输出 token 行: %s", none)
	}
	// 无跨文件意见时不应输出跨文件行
	if strings.Contains(none, "跨文件影响意见") {
		t.Errorf("无跨文件意见时不应输出跨文件行: %s", none)
	}
	// 无历史评论时不应输出采纳行
	if strings.Contains(none, "历史意见采纳") {
		t.Errorf("无历史评论时不应输出采纳行: %s", none)
	}
}

func TestFormatAsMarkdownCrossFileMarker(t *testing.T) {
	out := FormatAsMarkdown([]Comment{
		{Severity: "high", File: "b/b.go", Line: 5, CrossFile: true, Issue: "x"},
	})
	if !strings.Contains(out, "跨文件影响分析") {
		t.Errorf("跨文件意见应标注来源: %s", out)
	}
}
