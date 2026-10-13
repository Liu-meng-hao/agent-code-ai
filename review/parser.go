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

// normalizedSeverity 返回合法的小写严重级，非法值归为 unknown。
func normalizedSeverity(s string) string {
	if !severitySet[s] {
		return "unknown"
	}
	return s
}

// writeComments 把若干条评论按统一的 Markdown 标题格式写入 sb。
func writeComments(sb *strings.Builder, comments []Comment) {
	for _, c := range comments {
		fmt.Fprintf(sb, "### [%s] %s:%d\n\n", strings.ToUpper(normalizedSeverity(c.Severity)), c.File, c.Line)
		if c.Category != "" {
			fmt.Fprintf(sb, "- 分类：`%s`\n", c.Category)
		}
		if c.Issue != "" {
			fmt.Fprintf(sb, "- 问题：%s\n", c.Issue)
		}
		if c.Suggestion != "" {
			fmt.Fprintf(sb, "- 建议：%s\n", c.Suggestion)
		}
		sb.WriteString("\n")
	}
}

// FormatAsMarkdown 把全部结构化评论渲染成一条整体 review 的 Markdown 正文（降级模式使用）。
func FormatAsMarkdown(comments []Comment) string {
	var sb strings.Builder
	sb.WriteString("## 🤖 AI 代码审查\n\n")
	if len(comments) == 0 {
		sb.WriteString("未发现高置信度问题。")
		return sb.String()
	}
	fmt.Fprintf(&sb, "共发现 **%d** 条意见：\n\n", len(comments))
	writeComments(&sb, comments)
	return sb.String()
}

// FormatReviewBody 生成带行内评论时 review 的整体正文：
// 概述命中数量，并列出那些无法定位到具体代码行的意见，避免遗漏。
func FormatReviewBody(total, inlineCount int, orphan []Comment) string {
	var sb strings.Builder
	sb.WriteString("## 🤖 AI 代码审查\n\n")
	if total == 0 {
		sb.WriteString("未发现高置信度问题。")
		return sb.String()
	}
	fmt.Fprintf(&sb, "共发现 **%d** 条意见。", total)
	if inlineCount > 0 {
		fmt.Fprintf(&sb, "其中 **%d** 条已就近标注在相关代码行。", inlineCount)
	}
	if len(orphan) > 0 {
		fmt.Fprintf(&sb, "\n\n以下 **%d** 条无法定位到具体代码行：\n\n", len(orphan))
		writeComments(&sb, orphan)
	}
	return sb.String()
}

// FormatInlineBody 渲染单条行内评论的正文（挂载到代码行上）。
func FormatInlineBody(c Comment) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "**[%s]**", strings.ToUpper(normalizedSeverity(c.Severity)))
	if c.Category != "" {
		fmt.Fprintf(&sb, " `%s`", c.Category)
	}
	sb.WriteString("\n\n")
	if c.Issue != "" {
		sb.WriteString(c.Issue + "\n")
	}
	if c.Suggestion != "" {
		fmt.Fprintf(&sb, "\n建议：%s", c.Suggestion)
	}
	return strings.TrimSpace(sb.String())
}