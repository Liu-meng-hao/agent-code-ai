package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Comment 是模型中约定输出的一条审查意见。
type Comment struct {
	Severity   string `json:"severity"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Category   string `json:"category"`
	Issue      string `json:"issue"`
	Suggestion string `json:"suggestion"`
}

// ReviewResult 是模型约定的顶层 JSON 结构。
type ReviewResult struct {
	Comments []Comment `json:"comments"`
}

var severitySet = map[string]bool{"critical": true, "high": true, "medium": true, "low": true}

// stripCodeFence 去掉模型可能包裹的 ```json ... ``` 代码块标记。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.Index(s, "\n"); i != -1 {
		s = s[i+1:] // 丢弃 ```json 那一行
	}
	if i := strings.LastIndex(s, "```"); i != -1 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// ParseComments 解析模型输出为结构化评论。解析失败时调用方应降级为原文提交。
func ParseComments(raw string) ([]Comment, error) {
	var res ReviewResult
	if err := json.Unmarshal([]byte(stripCodeFence(raw)), &res); err != nil {
		return nil, err
	}
	return res.Comments, nil
}

// FormatAsMarkdown 把结构化评论渲染成一条整体 review 的 Markdown 正文。
func FormatAsMarkdown(comments []Comment) string {
	if len(comments) == 0 {
		return "## 🤖 AI 代码审查\n\n未发现高置信度问题。"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## 🤖 AI 代码审查\n\n共发现 **%d** 条意见：\n\n", len(comments))
	for _, c := range comments {
		sev := c.Severity
		if !severitySet[sev] {
			sev = "unknown"
		}
		fmt.Fprintf(&sb, "### [%s] %s:%d\n\n", strings.ToUpper(sev), c.File, c.Line)
		if c.Category != "" {
			fmt.Fprintf(&sb, "- 分类：`%s`\n", c.Category)
		}
		if c.Issue != "" {
			fmt.Fprintf(&sb, "- 问题：%s\n", c.Issue)
		}
		if c.Suggestion != "" {
			fmt.Fprintf(&sb, "- 建议：%s\n", c.Suggestion)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}