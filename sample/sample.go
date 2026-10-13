package sample

import (
	"context"
	"fmt"
	"os"
)

// 本文件仅用于触发 AI 代码审查流水线的端到端验证（Phase 3E 效果评估），
// 内含若干典型待审查问题，请勿在生产代码中参照。

func processFiles(paths []string) {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		// 问题：循环中使用 defer，句柄会累积到函数返回才释放
		defer f.Close()
		fmt.Println(f.Name())
	}
}

func broadcast(ch chan int, n int) {
	close(ch)
	// 问题：向已关闭的 channel 发送数据会 panic
	ch <- n
}

func typeAssert(v interface{}) int {
	// 问题：类型断言未检查 ok，可能 panic
	n := v.(int)
	return n
}

func query() {
	// 问题：硬编码 context.Background()，未向下传递调用方 context
	ctx := context.Background()
	_ = ctx
}

func readAll(path string) []byte {
	data, _ := os.ReadFile(path)
	// 问题：忽略了 os.ReadFile 返回的 error
	return data
}