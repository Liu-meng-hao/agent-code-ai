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

	body := raw
	if comments, parseErr := ParseComments(raw); parseErr != nil {
		fmt.Printf("[ai-review] 结构化解析失败，降级为原文提交: %v\n", parseErr)
		body = "## 🤖 AI 代码审查（原始输出）\n\n" + raw
	} else {
		body = FormatAsMarkdown(comments)
	}

	if err := SubmitReview(cfg, body); err != nil {
		fmt.Printf("[ai-review] 提交评论失败: %v\n", err)
		fmt.Println("[ai-review] ---- 本次生成的审查内容 ----")
		fmt.Println(body)
		fmt.Println("[ai-review] ---- 审查内容结束 ----")
		return nil // 不阻塞：提交失败不影响 CI
	}
	fmt.Println("[ai-review] 审查评论已提交")
	return nil
}

func buildUserPrompt(diff string) string {
	return "以下是本次 Pull Request 的代码变更（unified diff，含 5 行上下文）：\n\n" +
		"```diff\n" + diff + "\n```\n\n" +
		"请严格按约定 JSON 格式输出审查意见。"
}