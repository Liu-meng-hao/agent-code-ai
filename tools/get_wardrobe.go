package tools

import (
	"context"
	"encoding/json"

	"github.com/openai/openai-go/v3"
)

func NewGetWardrobe() *ToolDef {
	return &ToolDef{
		Name:        "get_wardrobe",
		Description: "获取用户衣橱中所有的衣服列表",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"category": map[string]string{
					"type":        "string",
					"description": "可选，按类别筛选，如 top、bottom、outerwear、shoes、accessory",
				},
			},
			"required": []string{},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			// 这里可以替换成从数据库或文件读取
			wardrobe := []map[string]any{
				{"id": "1", "name": "白色 T 恤", "category": "top", "color": "白", "season": "夏"},
				{"id": "2", "name": "牛仔裤", "category": "bottom", "color": "蓝", "season": "四季"},
				{"id": "3", "name": "羽绒服", "category": "outerwear", "color": "黑", "season": "冬"},
				{"id": "4", "name": "运动鞋", "category": "shoes", "color": "灰", "season": "四季"},
				{"id": "5", "name": "棒球帽", "category": "accessory", "color": "红", "season": "夏"},
			}

			// 如果传了 category，就做一次筛选
			if c, ok := args["category"].(string); ok && c != "" {
				var filtered []map[string]any
				for _, item := range wardrobe {
					if item["category"] == c {
						filtered = append(filtered, item)
					}
				}
				wardrobe = filtered
			}

			b, err := json.Marshal(wardrobe)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	}
}
