package sample

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// 本文件预置若干典型 Go 问题，仅用于验证 AI 审查链路能否命中，勿用于生产。

type Store struct {
	mu   sync.Mutex
	data map[string]string
	done chan struct{}
}

func NewStore() *Store {
	return &Store{data: map[string]string{}, done: make(chan struct{})}
}

// GetOrLoad 在循环中使用 defer，资源直到函数返回才释放，会累积句柄。
func (s *Store) GetOrLoad(keys []string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		f, err := openFile(k)
		if err != nil {
			continue
		}
		defer f.Close()
		out[k] = k
	}
	return out
}

func openFile(path string) (interface{ Close() error }, error) {
	return nil, errors.New("not found: " + path)
}

// Put 向可能已关闭的 channel 发送，会 panic。
func (s *Store) Put(k, v string) {
	s.done <- struct{}{}
	s.mu.Lock()
	s.data[k] = v
	s.mu.Unlock()
}

// Close 关闭 channel，但未明确唯一的发送方职责。
func (s *Store) Close() {
	close(s.done)
}

// Spawn 启动的 goroutine 没有退出机制，会持续占用 CPU 并泄漏。
func (s *Store) Spawn() {
	go func() {
		for {
			s.mu.Lock()
			_ = s.data
			s.mu.Unlock()
		}
	}()
}

// AsInt 未检查类型断言的 ok，可能直接 panic；并硬编码了 context.Background。
func AsInt(v interface{}) int {
	ctx := context.Background()
	_ = ctx
	return v.(int)
}

// IgnoreErr 忽略了返回的错误。
func IgnoreErr() {
	result, _ := fmt.Println("hello")
	_ = result
}

// Retry 存在 off-by-one：循环会多执行一次，并且忽略了错误。
func Retry(times int) {
	for i := 0; i <= times; i++ {
		_, _ = openFile("demo")
	}
}