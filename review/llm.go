package main

import (
	"context"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

// ReviewWithLLM 组装消息并调用 DeepSeek Responses API，返回模型原始文本。
func ReviewWithLLM(cfg *Config, systemPrompt, userPrompt string) (string, error) {
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
		return "", err
	}
	return resp.OutputText(), nil
}