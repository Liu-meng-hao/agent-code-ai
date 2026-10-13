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

	c := &reviewCache{Files: map[string][]string{}}
	c.markReviewed(map[string][]string{
		"a.go": {"h1"},
		"b.go": {"h2", "h3"},
	})
	if err := c.save(); err != nil {
		t.Fatalf("保存缓存失败: %v", err)
	}

	loaded := loadCache()
	if got := loaded.Files["a.go"]; len(got) != 1 || got[0] != "h1" {
		t.Errorf("a.go 缓存往返结果不符: %v", got)
	}
	if got := loaded.Files["b.go"]; len(got) != 2 || got[0] != "h2" || got[1] != "h3" {
		t.Errorf("b.go 缓存往返结果不符: %v", got)
	}
}

func TestMarkReviewedOverwrites(t *testing.T) {
	c := &reviewCache{Files: map[string][]string{}}
	c.markReviewed(map[string][]string{"a.go": {"h1", "h2"}})
	// 同一文件再次审查：应整体覆盖为当前 hunk 指纹集合，而不是累加
	c.markReviewed(map[string][]string{"a.go": {"h2", "h3"}})
	if got := c.Files["a.go"]; len(got) != 2 || got[0] != "h2" || got[1] != "h3" {
		t.Errorf("markReviewed 应覆盖而非累加，实际: %v", got)
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

// TestLoadCacheLegacyFormat 旧版缓存是「路径 -> blob hash 字符串」，与当前格式不兼容，
// 应安全降级为空缓存而非报错，本次全量审查后顺带升级为新格式。
func TestLoadCacheLegacyFormat(t *testing.T) {
	t.Chdir(t.TempDir())
	legacy := `{"files":{"a.go":"deadbeef","b.go":"cafebabe"}}`
	if err := os.WriteFile(cacheFile, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	c := loadCache()
	if len(c.Files) != 0 {
		t.Errorf("旧格式缓存应降级为空缓存，实际: %v", c.Files)
	}
}

func TestHunkHash(t *testing.T) {
	if hunkHash("same") != hunkHash("same") {
		t.Error("相同内容应得到相同指纹")
	}
	if hunkHash("a") == hunkHash("b") {
		t.Error("不同内容应得到不同指纹")
	}
	h1 := "@@ -1,1 +1,1 @@\n+a\n"
	h2 := "@@ -9,1 +9,1 @@\n+a\n"
	if hunkHash(h1) == hunkHash(h2) {
		t.Error("内容相同但位置不同应得到不同指纹")
	}
}
