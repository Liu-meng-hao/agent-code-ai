package main

import (
	"fmt"
	"os"
)

// 本流程设计为"不阻塞 CI"：任何失败只记录日志，进程始终以 0 退出。
func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "[ai-review] 审查流程失败（不阻塞 CI）: %v\n", err)
	}
}

func run() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	systemPrompt, err := os.ReadFile("prompt/review.txt")
	if err != nil {
		return fmt.Errorf("读取 prompt/review.txt 失败: %w", err)
	}

	files, err := ChangedFiles(cfg.BaseSHA, cfg.HeadSHA)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Println("[ai-review] 没有需要审查的变更文件，跳过")
		return nil
	}
	fmt.Printf("[ai-review] 待审查文件 %d 个: %v\n", len(files), files)

	diff, err := BuildDiff(cfg.BaseSHA, cfg.HeadSHA, files)
	if err != nil {
		return err
	}

	raw, err := ReviewWithLLM(cfg, string(systemPrompt), buildUserPrompt(diff))
	if err != nil {
		return fmt.Errorf("调用 LLM 失败: %w", err)
	}

	// 解析模型输出为结构化评论；失败则整体降级为原文提交
	comments, parseErr := ParseComments(raw)
	if parseErr != nil {
		fmt.Printf("[ai-review] 结构化解析失败，降级为原文提交: %v\n", parseErr)
		return submitWithFallback(cfg, "## 🤖 AI 代码审查（原始输出）\n\n"+raw, nil)
	}

	// 按 diff 行号把评论分为可挂载到代码行的与无法定位的
	valid := ParseDiffLines(diff)
	inline, orphan := SplitComments(comments, valid)

	payloadComments := make([]reviewComment, 0, len(inline))
	for _, c := range inline {
		payloadComments = append(payloadComments, reviewComment{
			Path: c.File,
			Line: c.Line,
			Side: "RIGHT",
			Body: FormatInlineBody(c),
		})
	}

	body := FormatReviewBody(len(comments), len(inline), orphan)
	fmt.Printf("[ai-review] 意见 %d 条：行内 %d 条，整体正文 %d 条\n", len(comments), len(inline), len(orphan))
	return submitWithFallback(cfg, body, payloadComments)
}

// submitWithFallback 先按「整体正文 + 行内评论」提交；失败则降级为仅整体正文（含全部意见）。
// 无论成败都不返回错误（CI 不阻塞），最坏情况只打印审查正文。
func submitWithFallback(cfg *Config, body string, comments []reviewComment) error {
	if err := SubmitReview(cfg, body, comments); err == nil {
		fmt.Println("[ai-review] 审查评论已提交")
		return nil
	} else {
		fmt.Printf("[ai-review] 提交行内评论失败，降级为整体评论: %v\n", err)
	}

	if err := SubmitReview(cfg, body, nil); err != nil {
		fmt.Printf("[ai-review] 提交评论失败: %v\n", err)
		printBody(body)
		return nil // 不阻塞：提交失败不影响 CI
	}
	fmt.Println("[ai-review] 审查评论已提交（降级整体模式）")
	return nil
}

// printBody 在无法提交时把审查正文回显到日志，便于排查。
func printBody(body string) {
	fmt.Println("[ai-review] ---- 本次生成的审查内容 ----")
	fmt.Println(body)
	fmt.Println("[ai-review] ---- 审查内容结束 ----")
}

func buildUserPrompt(diff string) string {
	return "以下是本次 Pull Request 的代码变更（unified diff，含 5 行上下文）：\n\n" +
		"```diff\n" + diff + "\n```\n\n" +
		"请严格按约定 JSON 格式输出审查意见。"
}