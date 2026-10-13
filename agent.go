package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"tui/core"
	"tui/tools"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

const MaxLoop = 10

// getLlmTools 获取工具
func getLlmTools() map[string]*tools.ToolDef {
	llmTools := make(map[string]*tools.ToolDef)
	//weatherTool := tools.NewGetRealWeather()
	wardrobeTool := tools.NewGetWardrobe()
	currentTimeTool := tools.NewGetCurrentTime()
	//llmTools[weatherTool.Name] = weatherTool
	shell := tools.NewExecShell()
	llmTools[wardrobeTool.Name] = wardrobeTool
	llmTools[currentTimeTool.Name] = currentTimeTool
	llmTools[shell.Name] = shell
	return llmTools
}

// buildOpenaiTools 生成openai格式工具
func buildOpenaiTools(tools map[string]*tools.ToolDef) []responses.ToolUnionParam {
	openaiTools := make([]responses.ToolUnionParam, 0)
	for _, tool := range tools {
		openaiTools = append(openaiTools, tool.ToParam())
	}
	return openaiTools
}

func main() {
	session := core.NewSession()

	session.AddMessage(
		responses.ResponseInputItemParamOfMessage(
			"你是人类专业的助手;回答文件简介干练,只回答和用户的问题相关的问题。根据工具返回内容事实回答;think也要中文输出，假如用户的问题没有可用工具，你可以用shell 完成 Python 编写脚本、执行完成用的的任务",
			responses.EasyInputMessageRoleSystem),
		responses.ResponseInputItemParamOfMessage(
			"你具备执行shell 脚本完成网络请求，获取目标数据的能力",
			responses.EasyInputMessageRoleSystem),
		responses.ResponseInputItemParamOfMessage("后天我要去深圳穿什么衣服合适", responses.EasyInputMessageRoleUser),
	)

	llmTools := getLlmTools()
	openaiTools := buildOpenaiTools(llmTools)

	ctx := context.Background()

	client := openai.NewClient(
		option.WithAPIKey(os.Getenv("DEEPSEEK_API_KEY")), // 从环境变量读取，切勿硬编码在源码中
		option.WithBaseURL("https://api.deepseek.com"),
		option.WithRequestTimeout(90*time.Second),
		//option.WithJSONSet("thinking.type", "disabled"),
	)

	for realLoop := 0; realLoop <= MaxLoop; realLoop++ {
		fmt.Println("调用第几轮", realLoop)
		resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
			Input: responses.ResponseNewParamsInputUnion{
				OfInputItemList: session.GetMessages(),
			},
			Tools: openaiTools,
			Model: "deepseek-flash",
			//Temperature: openai.Float(0.1),
		})

		if err != nil {
			var apiErr *openai.Error
			if errors.As(err, &apiErr) {
				fmt.Println("HTTP 状态码:", apiErr.StatusCode)
				fmt.Println("错误类型:", apiErr.Type)
				fmt.Println("原始响应体:", apiErr.RawJSON())
			} else {
				fmt.Println("非 API 错误:", err)
			}
			panic(err)
		}

		hasToolCall := false
		for _, item := range resp.Output {
			switch item.Type {
			case "reasoning":
				fmt.Println("----think:----")
				for _, summary := range item.Summary {
					fmt.Println(summary.Text)
				}
				fmt.Println("----think-end----")
			case "function_call":
				fmt.Println("调用了工具", item.AsFunctionCall().Name)
				hasToolCall = true
				//火山引擎 处理
				session.AddMessage(responses.ResponseInputItemUnionParam{
					OfFunctionCall: &responses.ResponseFunctionToolCallParam{
						Arguments: item.AsFunctionCall().Arguments,
						CallID:    item.AsFunctionCall().CallID,
						Name:      item.AsFunctionCall().Name,
					},
				})

				if v, ok := llmTools[item.AsFunctionCall().Name]; ok {
					var toolRe string
					var err error
					if toolRe, err = v.Call(ctx, item.AsFunctionCall().Arguments); err != nil {
						toolRe = fmt.Sprintf("%s%v", item.AsFunctionCall().Name, err)
						fmt.Println("----->error:", toolRe)
					}
					fmt.Println("----->", item.AsFunctionCall().Name, toolRe)

					session.AddMessage(responses.ResponseInputItemUnionParam{
						OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
							Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
								OfString: openai.String(toolRe),
							},
							CallID: openai.String(item.AsFunctionCall().CallID),
						},
					})
				}
			}
		}

		if !hasToolCall {
			fmt.Println(resp.OutputText())
			return
		}
	}

}
