package main

import (
	"context"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

// Usage 汇总一次 LLM 调用的 token 用量，用于效果评估与成本监控。
type Usage struct {
	Input  int64
	Output int64
}

// ReviewWithLLM 组装消息并调用 DeepSeek Responses API，返回模型原始文本与本次调用的 token 用量。
func ReviewWithLLM(cfg *Config, systemPrompt, userPrompt string) (string, Usage, error) {
	client := openai.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(cfg.BaseURL),
		option.WithRequestTimeout(120*time.Second),
	)

	ctx := context.Background()
	resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam{
				responses.ResponseInputItemParamOfMessage(systemPrompt, responses.EasyInputMessageRoleSystem),
				responses.ResponseInputItemParamOfMessage(userPrompt, responses.EasyInputMessageRoleUser),
			},
		},
		Model: cfg.Model,
	})
	if err != nil {
		return "", Usage{}, err
	}
	usage := Usage{Input: resp.Usage.InputTokens, Output: resp.Usage.OutputTokens}
	return resp.OutputText(), usage, nil
}
