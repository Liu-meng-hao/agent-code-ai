package demo

import (
	"context"
	"fmt"
	"os"
	"time"
)

// 本文件用于验证 AI 代码审查 Agent，故意写了多种 Go 常见问题。

// ReadConfig 读取配置文件内容。
func ReadConfig(path string) string {
	data, _ := os.ReadFile(path) // 忽略了 error
	return string(data)
}

// ProcessAll 逐个处理文件。
func ProcessAll(items []string) {
	for _, it := range items {
		f, err := os.Open(it)
		if err != nil {
			continue
		}
		defer f.Close() // 在循环中使用 defer，句柄会累积到函数结束才释放
		fmt.Println(f.Name())
	}
}

// CloseThenSend 演示 channel 的关闭与发送。
func CloseThenSend() {
	ch := make(chan int)
	close(ch)
	ch <- 1 // 向已关闭的 channel 发送数据，会 panic
}

// LeakGoroutine 启动一个后台协程。
func LeakGoroutine() {
	ch := make(chan int)
	go func() {
		<-ch // 没有发送方，goroutine 永久阻塞泄漏
	}()
}

// WithBackground 演示 context 的使用。
func WithBackground() {
	ctx := context.Background() // 硬编码 context，无法被上层取消
	_ = ctx
	time.Sleep(time.Second)
}

// UnsafeAssert 把入参断言为字符串。
func UnsafeAssert(v interface{}) string {
	return v.(string) // 未使用 comma-ok 形式，类型不符时 panic
}