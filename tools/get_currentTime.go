package tools

import (
	"context"
	"time"

	"github.com/openai/openai-go/v3"
)

func NewGetCurrentTime() *ToolDef {
	return &ToolDef{
		Name:        "get_current_time",
		Description: "获取本地当前的日期和时间",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"format": map[string]string{
					"type":        "string",
					"description": "时间格式，可选值：datetime（默认，2006-01-02 15:04:05）、date（2006-01-02）、time（15:04:05）",
				},
			},
			"required": []string{},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			now := time.Now()

			format, _ := args["format"].(string)
			var layout string
			switch format {
			case "date":
				layout = "2006-01-02"
			case "time":
				layout = "15:04:05"
			default:
				layout = "2006-01-02 15:04:05"
			}

			return now.Format(layout), nil
		},
	}
}
