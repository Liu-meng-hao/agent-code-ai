package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
)

// reviewComment 是挂在 diff 具体代码行上的行内评论。
type reviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"` // RIGHT=变更后（新文件）侧
	Body string `json:"body"`
}

// reviewPayload 是创建 PR review 的请求体。
type reviewPayload struct {
	Body     string          `json:"body"`
	Event    string          `json:"event"`
	Comments []reviewComment `json:"comments,omitempty"`
}

// SubmitReview 通过 gh api 提交一条 review：整体正文 + 可选的行内评论。
// 使用 event=COMMENT，只提建议、不阻塞合并。
func SubmitReview(cfg *Config, body string, comments []reviewComment) error {
	payload, err := json.Marshal(reviewPayload{Body: body, Event: "COMMENT", Comments: comments})
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%s/reviews", cfg.Owner, cfg.Repo, cfg.PRNumber)
	cmd := exec.Command("gh", "api", "-X", "POST", endpoint, "--input", "-")
	cmd.Stdin = bytes.NewReader(payload)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh api 提交 review 失败: %v: %s", err, stderr.String())
	}
	return nil
}

// maxCommentsPerReview 单条 review 最多携带的行内评论数。意见过多时单次提交易被拒绝，
// 因此按此上限分批提交（难点 4.4）。
const maxCommentsPerReview = 50

// submitBatched 把行内评论按 maxCommentsPerReview 分批，每批一条 review；
// 整体正文只挂在第一批，后续批次不再重复正文。任一批失败即返回错误，由上层降级。
func submitBatched(cfg *Config, body string, comments []reviewComment) error {
	if len(comments) == 0 {
		return SubmitReview(cfg, body, nil)
	}
	for i := 0; i < len(comments); i += maxCommentsPerReview {
		end := i + maxCommentsPerReview
		if end > len(comments) {
			end = len(comments)
		}
		batchBody := body
		if i > 0 {
			batchBody = "（AI 代码审查，续）" // 后续批次不重复整体正文
		}
		if err := SubmitReview(cfg, batchBody, comments[i:end]); err != nil {
			return err
		}
	}
	return nil
}

// submitIndividually 逐条提交行内评论：单条失败只记录日志、不影响其余（难点 3.3 降级）。
// 返回成功与失败的条数。
func submitIndividually(cfg *Config, comments []reviewComment) (ok, failed int) {
	for _, c := range comments {
		if err := SubmitReview(cfg, "", []reviewComment{c}); err != nil {
			fmt.Printf("[ai-review] 单条评论提交失败（跳过）: %s:%d: %v\n", c.Path, c.Line, err)
			failed++
			continue
		}
		ok++
	}
	return ok, failed
}
