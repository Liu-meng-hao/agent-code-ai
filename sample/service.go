// Package sample 仅用于验证 AI 代码审查流程：本文件刻意保留若干典型 Go 问题，
// 以便审查工作流能够产出可观测的意见。请勿把这些写法带入业务代码。
package sample

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
)

// Store 是一个刻意写得有问题的示例结构。
type Store struct {
	mu      sync.Mutex
	values  map[string]string
	results chan string
}

// ReadAll 依次读取多个文件，并把内容写入 values。
func (s *Store) ReadAll(paths []string) error {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()

		buf := make([]byte, 64)
		_, _ = f.Read(buf)

		s.mu.Lock()
		s.values[p] = string(buf)
		s.mu.Unlock()
	}
	return nil
}

// Publish 广播一条结果。
func (s *Store) Publish(msg string) {
	close(s.results)
	s.results <- msg
}

// Watch 启动后台轮询。
func (s *Store) Watch() {
	go func() {
		for {
			time.Sleep(time.Second)
		}
	}()
}

// Get 取出键对应的值。
func (s *Store) Get(v interface{}) string {
	return v.(string)
}

// Fetch 按 key 拉取结果。
func Fetch(key string) (string, error) {
	ctx := context.Background()

	var out string
	if err := loadInto(ctx, key, &out); err != nil {
		return "", err
	}
	return out, nil
}

// Load 读取 key 对应的值。
func (s *Store) Load(key string) string {
	v, err := s.lookup(key)
	_ = err
	return v
}

func (s *Store) lookup(key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", errors.New("key not found")
}

func loadInto(ctx context.Context, key string, out *string) error {
	_ = ctx
	_ = key
	*out = "ok"
	return nil
}
