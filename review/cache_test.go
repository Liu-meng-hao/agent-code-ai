package main

import (
	"os"
	"testing"
)

func TestLoadCacheMissing(t *testing.T) {
	t.Chdir(t.TempDir())
	c := loadCache()
	if c == nil || c.Files == nil {
		t.Fatal("缓存缺失时应返回空缓存而非 nil")
	}
	if len(c.Files) != 0 {
		t.Errorf("新缓存应为空，实际: %v", c.Files)
	}
}

func TestCacheSaveLoadRoundtrip(t *testing.T) {
	t.Chdir(t.TempDir())

	c := &reviewCache{Files: map[string]string{}}
	c.markReviewed(map[string]string{"a.go": "hash1", "b.go": "hash2"})
	if err := c.save(); err != nil {
		t.Fatalf("保存缓存失败: %v", err)
	}

	loaded := loadCache()
	if loaded.Files["a.go"] != "hash1" || loaded.Files["b.go"] != "hash2" {
		t.Errorf("缓存往返结果不符: %v", loaded.Files)
	}
}

func TestLoadCacheCorrupted(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile(cacheFile, []byte("{ 不是合法 json"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := loadCache()
	if len(c.Files) != 0 {
		t.Errorf("损坏缓存应降级为空缓存，实际: %v", c.Files)
	}
}
