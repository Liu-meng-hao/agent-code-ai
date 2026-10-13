package sample

import (
	"context"
	"fmt"
)

// ProcessAll 遍历数据并并发处理，用于触发 AI 代码审查的测试样例。
func ProcessAll(items []int) {
	ctx := context.Background() // 未传入可取消的 context，无法响应取消
	ch := make(chan int)
	for _, it := range items {
		go func() {
			ch <- it * 2 // 发送方在 channel 关闭后仍可能发送，且 goroutine 无退出机制
		}()
		close(ch) // 循环内关闭 channel，后续发送会 panic
	}
	for v := range ch {
		fmt.Println(ctx, v)
	}

	var v any = "hello"
	s := v.(string) // 未使用 comma-ok，类型不符时 panic
	_ = s
}