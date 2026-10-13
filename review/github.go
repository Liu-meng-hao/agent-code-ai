package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
)

// reviewPayload 是创建 PR review 的请求体。
type reviewPayload struct {
	Body  string `json:"body"`
	Event string `json:"event"`
}

// SubmitReview 通过 gh api 提交一条整体 review 评论。
// 使用 event=COMMENT，只提建议、不阻塞合并。
func SubmitReview(cfg *Config, body string) error {
	payload, err := json.Marshal(reviewPayload{Body: body, Event: "COMMENT"})
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