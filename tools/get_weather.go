package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/openai/openai-go/v3"
)

func NewGetWeather() *ToolDef {
	return &ToolDef{
		Name:        "get_weather",
		Description: "查询指定城市的天气，可指定日期，不指定则默认今天",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"location": map[string]string{
					"type":        "string",
					"description": "城市名称，例如 Beijing",
				},
				"date": map[string]string{
					"type":        "string",
					"description": "日期，格式为 YYYY-MM-DD。可选，不传则默认今天",
				},
			},
			"required": []string{"location"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			location := args["location"].(string)

			date, _ := args["date"].(string)
			if date == "" {
				date = time.Now().Format("2006-01-02")
			}

			return fmt.Sprintf("%s 在 %s 的天气：晴，25°C", location, date), nil
		},
	}
}
