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

// markdownEscaper 转义模型输出中的 HTML 危险字符。
// 模型输出属于不可信内容，转义后可防止其注入 HTML/脚本或破坏评论的 Markdown 结构；
// 渲染时用户仍然看到原始字符（如 <script> 会显示为字面量）。
var markdownEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// escapeMarkdown 对不可信的模型输出做 HTML 转义。
func escapeMarkdown(s string) string {
	return markdownEscaper.Replace(s)
}

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
		fmt.Fprintf(sb, "### [%s] %s:%d\n\n", strings.ToUpper(normalizedSeverity(c.Severity)), escapeMarkdown(c.File), c.Line)
		if c.Category != "" {
			fmt.Fprintf(sb, "- 分类：`%s`\n", escapeMarkdown(c.Category))
		}
		if c.Issue != "" {
			fmt.Fprintf(sb, "- 问题：%s\n", escapeMarkdown(c.Issue))
		}
		if c.Suggestion != "" {
			fmt.Fprintf(sb, "- 建议：%s\n", escapeMarkdown(c.Suggestion))
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

// severityOrder 控制统计信息里严重级的展示顺序。
var severityOrder = []string{"critical", "high", "medium", "low", "unknown"}

// ReviewStats 汇总一次审查的成本与产出，用于效果评估。
type ReviewStats struct {
	Batches      int            // 调用 LLM 的批次数
	Total        int            // 意见总数
	Inline       int            // 可挂载到代码行的意见数
	Orphan       int            // 无法定位到代码行的意见数
	Severity     map[string]int // 各严重级的意见数
	InputTokens  int64          // 输入 token 合计
	OutputTokens int64          // 输出 token 合计
	PrevComments int            // 历史上已提交的 AI 行内意见数（用于采纳率）
	Adopted      int            // 其中获得 👍 reaction 的条数
}

// FormatStatsFooter 渲染审查统计信息，附在 review 正文末尾，便于观察每次审查的成本与产出。
// 内容由本流程生成而非模型输出，无需转义；折叠展示以免干扰审查意见本身。
func FormatStatsFooter(s ReviewStats) string {
	var sb strings.Builder
	sb.WriteString("\n\n---\n\n<details><summary>📊 本次审查统计</summary>\n\n")
	fmt.Fprintf(&sb, "- 调用 LLM：%d 次\n", s.Batches)
	if s.InputTokens > 0 || s.OutputTokens > 0 {
		fmt.Fprintf(&sb, "- token 用量：输入 %d，输出 %d，合计 %d\n",
			s.InputTokens, s.OutputTokens, s.InputTokens+s.OutputTokens)
	}
	fmt.Fprintf(&sb, "- 意见：%d 条（行内 %d，正文 %d）\n", s.Total, s.Inline, s.Orphan)

	var parts []string
	for _, sev := range severityOrder {
		if n := s.Severity[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", sev, n))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(&sb, "- 严重级分布：%s\n", strings.Join(parts, "，"))
	}
	if s.PrevComments > 0 {
		fmt.Fprintf(&sb, "- 历史意见采纳：%d/%d 条获得 👍\n", s.Adopted, s.PrevComments)
	}
	sb.WriteString("\n</details>")
	return sb.String()
}

// FormatInlineBody 渲染单条行内评论的正文（挂载到代码行上）。
func FormatInlineBody(c Comment) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "**[%s]**", strings.ToUpper(normalizedSeverity(c.Severity)))
	if c.Category != "" {
		fmt.Fprintf(&sb, " `%s`", escapeMarkdown(c.Category))
	}
	sb.WriteString("\n\n")
	if c.Issue != "" {
		sb.WriteString(escapeMarkdown(c.Issue) + "\n")
	}
	if c.Suggestion != "" {
		fmt.Fprintf(&sb, "\n建议：%s", escapeMarkdown(c.Suggestion))
	}
	return strings.TrimSpace(sb.String())
}
