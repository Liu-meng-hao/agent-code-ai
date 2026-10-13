package demo

import (
	"context"
	"os"
	"strings"
)

// 忽略 error：读取失败时静默返回空切片
func LoadNames(path string) []string {
	data, _ := os.ReadFile(path)
	return strings.Split(string(data), "\n")
}

// 循环中 defer，文件句柄累积到函数返回才释放
func CloseAll(paths []string) {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		defer f.Close()
	}
}

// 硬编码 context.Background()，无法响应上游取消
func FetchAll(ids []string) []string {
	var out []string
	for _, id := range ids {
		v, _ := fetchOne(context.Background(), id)
		out = append(out, v)
	}
	return out
}

func fetchOne(ctx context.Context, id string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
		return "ok:" + id, nil
	}
}