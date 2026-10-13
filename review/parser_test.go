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

func TestFormatStatsFooter(t *testing.T) {
	footer := FormatStatsFooter(ReviewStats{
		Batches:      2,
		Total:        3,
		Inline:       2,
		Orphan:       1,
		Severity:     map[string]int{"critical": 1, "high": 2},
		InputTokens:  1200,
		OutputTokens: 300,
	})
	for _, want := range []string{
		"调用 LLM：2 次",
		"输入 1200，输出 300，合计 1500",
		"意见：3 条（行内 2，正文 1）",
		"critical 1，high 2",
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
}
