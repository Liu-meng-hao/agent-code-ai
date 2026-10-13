package main

import "testing"

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens(""); got < 1 {
		t.Errorf("空串估算应至少为 1，实际 %d", got)
	}
	// 400 个 ASCII 字符约 100 tokens，允许一定误差
	ascii := ""
	for i := 0; i < 400; i++ {
		ascii += "a"
	}
	if got := estimateTokens(ascii); got < 90 || got > 110 {
		t.Errorf("400 ASCII 字符估算应接近 100，实际 %d", got)
	}
}

func TestIsLowValueFile(t *testing.T) {
	lowValue := []string{
		"go.sum", "package-lock.json", "web/app.min.js",
		"api/foo.pb.go", "node_modules/x/index.js",
	}
	for _, f := range lowValue {
		if !isLowValueFile(f) {
			t.Errorf("%s 应被判定为低价值文件", f)
		}
	}
	keep := []string{"review/main.go", "README.md", "internal/service.go"}
	for _, f := range keep {
		if isLowValueFile(f) {
			t.Errorf("%s 不应被判定为低价值文件", f)
		}
	}
}
