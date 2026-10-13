package demo

import (
	"context"
	"fmt"
	"os"
	"time"
)

// 忽略 error
func ReadConfig(path string) []byte {
	data, _ := os.ReadFile(path)
	return data
}

// 循环中 defer，文件句柄累积到函数返回才释放
func ProcessFiles(files []string) {
	for _, name := range files {
		f, err := os.Open(name)
		if err != nil {
			continue
		}
		defer f.Close()
		fmt.Println(name)
	}
}

// 向已关闭的 channel 发送数据会 panic
func FanIn(items []int) {
	ch := make(chan int, len(items))
	for _, v := range items {
		ch <- v
	}
	close(ch)

	for _, v := range items {
		ch <- v * 2
	}
}

// goroutine 泄漏：worker 无从退出
func StartWorkers() {
	ch := make(chan int)
	for i := 0; i < 5; i++ {
		go func(id int) {
			for range ch {
				_ = id
			}
		}(i)
	}
}

// 硬编码 context.Background()，无法响应上游取消
func Query(ctx context.Context, id string) (string, error) {
	return fetch(context.Background(), id)
}

func fetch(ctx context.Context, id string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return "ok:" + id, nil
	}
}

// 类型断言未检查 ok，可能 panic
func NameOf(v interface{}) string {
	return v.(string)
}