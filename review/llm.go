package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
)

// Usage 汇总一次 LLM 调用的 token 用量，用于效果评估与成本监控。
type Usage struct {
	Input  int64
	Output int64
}

// newLLMClient 为单次审查创建的 OpenAI 客户端，统一复用 BaseURL / APIKey / 超时。
// openai-go v3 的 NewClient 返回值类型 openai.Client（非指针）。
func newLLMClient(cfg *Config) openai.Client {
	return openai.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(cfg.BaseURL),
		option.WithRequestTimeout(120*time.Second),
	)
}

// ReviewWithLLM 执行单次 Chat Completions 调用（方案二的降级路径：无 MCP 工具箱时的等价行为）。
// 当 tools 为空时调用此函数，效果与原 Responses API 单次调用一致，只是走 Chat Completions。
func ReviewWithLLM(cfg *Config, systemPrompt, userPrompt string) (string, Usage, error) {
	client := newLLMClient(cfg)
	ctx := context.Background()

	messages := []openai.ChatCompletionMessageParamUnion{
		{OfSystem: &openai.ChatCompletionSystemMessageParam{
			Content: openai.ChatCompletionSystemMessageParamContentUnion{OfString: param.NewOpt(systemPrompt)},
		}},
		{OfUser: &openai.ChatCompletionUserMessageParam{
			Content: openai.ChatCompletionUserMessageParamContentUnion{OfString: param.NewOpt(userPrompt)},
		}},
	}

	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    cfg.Model,
	})
	if err != nil {
		return "", Usage{}, err
	}
	if len(resp.Choices) == 0 {
		return "", Usage{}, fmt.Errorf("模型无输出")
	}
	msg := resp.Choices[0].Message
	usage := Usage{Input: resp.Usage.PromptTokens, Output: resp.Usage.CompletionTokens}
	return msg.Content, usage, nil
}

// LLMToolLoop 执行带 tool calling 的多轮审查：
//  - reg 为 MCP 工具箱注册表；enabled=false 或 openaiTools 为空时自动退化为单次调用
//  - maxRounds 控制最大 tool 循环轮数；每轮累计 token 并在超预算时提前终止
//  - 返回最终文本（模型的审查意见 JSON）、总 token 用量、总调用轮数
//
// 循环核心：
//
//	for round := 1; round <= maxRounds; round++:
//	    resp = ChatCompletions(messages + tools)
//	    if resp 有 tool_calls:
//	        逐个调用 MCP 工具 → 把 assistant(tool_calls) + tool(result) 追加到 messages
//	        continue
//	    else:
//	        return resp.Content  // 最终输出
//
// 退化策略（与 main.go 的"不阻塞 CI"一致）：任何一轮 tool 调用出错 → 把错误文本当 tool result 回传给 LLM；
// 模型连续 3 轮调同一个工具 → 第 4 轮强行 block 该工具并回传停止提示；单轮 token 超预算 → break 循环。
func LLMToolLoop(cfg *Config, systemPrompt, userPrompt string, reg *mcpGlobalRegistry, maxRounds int, remainingBudget int64) (finalText string, totalUsage Usage, rounds int, err error) {
	client := newLLMClient(cfg)
	ctx := context.Background()

	// 构造初始消息列表
	messages := []openai.ChatCompletionMessageParamUnion{
		{OfSystem: &openai.ChatCompletionSystemMessageParam{
			Content: openai.ChatCompletionSystemMessageParamContentUnion{OfString: param.NewOpt(systemPrompt)},
		}},
		{OfUser: &openai.ChatCompletionUserMessageParam{
			Content: openai.ChatCompletionUserMessageParamContentUnion{OfString: param.NewOpt(userPrompt)},
		}},
	}

	// 判断是否有工具箱：reg 存在、enabled=true、openaiTools 非空
	var tools []openai.ChatCompletionToolUnionParam
	if reg != nil && reg.enabled && len(reg.openaiTools) > 0 {
		tools = reg.openaiTools
	}

	// 防死转：记录各工具被连续调用的次数；超过 3 次在下次请求里把这个工具从 tools 里临时移除
	toolRepeatCount := map[string]int{}
	lastText := ""

	for r := 1; r <= maxRounds; r++ {
		// 本轮实际可用的工具列表（去掉上一轮被 block 的）
		var roundTools []openai.ChatCompletionToolUnionParam
		if len(tools) > 0 {
			for _, t := range tools {
				if t.OfFunction != nil && toolRepeatCount[t.OfFunction.Function.Name] > 3 {
					continue
				}
				roundTools = append(roundTools, t)
			}
		}

		resp, callErr := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Messages: messages,
			Tools:    roundTools,
			Model:    cfg.Model,
		})
		if callErr != nil {
			return lastText, totalUsage, rounds, fmt.Errorf("第 %d 轮 Chat Completions 失败: %w", r, callErr)
		}
		if len(resp.Choices) == 0 {
			return lastText, totalUsage, rounds, fmt.Errorf("第 %d 轮模型无输出", r)
		}

		thisUsage := Usage{Input: resp.Usage.PromptTokens, Output: resp.Usage.CompletionTokens}
		totalUsage.Input += thisUsage.Input
		totalUsage.Output += thisUsage.Output
		rounds = r

		msg := resp.Choices[0].Message
		lastText = msg.Content

		// 预算熔断：累计用量已超单批预算就 break
		if remainingBudget > 0 && totalUsage.Input+totalUsage.Output > remainingBudget {
			fmt.Printf("[ai-review] 第 %d 轮后已超单批预算（已用 %d，预算 %d），停止 tool 循环\n",
				r, totalUsage.Input+totalUsage.Output, remainingBudget)
			break
		}

		// 无 tool_calls → 循环结束，直接输出
		if len(msg.ToolCalls) == 0 {
			finalText = msg.Content
			return finalText, totalUsage, rounds, nil
		}

		// 有 tool_calls：把 assistant 消息（含 tool_calls）追加进 messages
		messages = append(messages, msg.ToParam())

		// 逐个执行 tool_call
		for _, tc := range msg.ToolCalls {
			funcTool := tc.AsFunction()
			if funcTool.ID == "" {
				// custom tool call（本项目不处理），跳过
				continue
			}
			callID := funcTool.ID
			funcName := funcTool.Function.Name
			funcArgs := funcTool.Function.Arguments

			// 防死转计数
			toolRepeatCount[funcName]++
			isBlocked := toolRepeatCount[funcName] > 3

			var resultText string
			if isBlocked {
				resultText = fmt.Sprintf("错误：工具 %q 已被调用超过 3 次，请停止调用该工具，直接输出审查意见。", funcName)
			} else if reg != nil {
				ctxTimeout, cancel := context.WithTimeout(context.Background(), mcpCallTimeout)
				resultText = reg.invokeTool(ctxTimeout, callID, funcName, funcArgs)
				cancel()

				// 成功调用重置计数；错误则保留计数让其递增（最终达到 block）
				if !strings.HasPrefix(resultText, "错误：") && !strings.HasPrefix(resultText, "工具") {
					toolRepeatCount[funcName] = 0
				}
			} else {
				resultText = fmt.Sprintf("错误：MCP 工具箱未启用，无法调用工具 %q", funcName)
			}

			messages = append(messages, openai.ChatCompletionMessageParamUnion{
				OfTool: &openai.ChatCompletionToolMessageParam{
					Content:    openai.ChatCompletionToolMessageParamContentUnion{OfString: param.NewOpt(resultText)},
					ToolCallID: callID,
				},
			})
		}

		fmt.Printf("[ai-review] 第 %d 轮 tool 调用完成（已用 %d tokens，预算 %d）\n",
			r, totalUsage.Input+totalUsage.Output, remainingBudget)
	}

	// 达到 maxRounds → 强行输出最后一轮的文本
	if lastText != "" {
		fmt.Printf("[ai-review] 达到 tool 循环上限 %d 轮，强行输出最后一轮结果\n", maxRounds)
		return lastText, totalUsage, rounds, nil
	}
	return "", totalUsage, rounds, fmt.Errorf("tool 循环结束但模型未输出任何文本")
}
