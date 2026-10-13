package main

import (
	"encoding/json"
	"os"
	"strings"
)

// cacheFile 记录「文件路径 -> 最近一次已审查的 blob hash」，用于跳过内容未变化的文件。
// 该文件由 workflow 的 actions/cache 按 PR 号隔离保存与恢复，不应提交到仓库。
const cacheFile = ".ai-review-cache.json"

type reviewCache struct {
	Files map[string]string `json:"files"`
}

// loadCache 读取本地缓存文件；不存在或损坏时返回空缓存（不影响主流程）。
func loadCache() *reviewCache {
	c := &reviewCache{Files: map[string]string{}}
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return c
	}
	if err := json.Unmarshal(data, c); err != nil || c.Files == nil {
		c.Files = map[string]string{}
	}
	return c
}

// save 把缓存写回磁盘；失败只影响增量优化，不影响审查本身。
func (c *reviewCache) save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cacheFile, data, 0o644)
}

// markReviewed 记录本次已审查文件的 blob hash。
func (c *reviewCache) markReviewed(hashes map[string]string) {
	for f, h := range hashes {
		c.Files[f] = h
	}
}

// SelectUnchanged 从变更文件里挑出「尚未审查或内容已变化」的文件，并返回这些文件当前的 blob hash。
// blob hash 通过 git rev-parse <head>:<path> 得到，只要文件内容不变，hash 就不变。
func SelectUnchanged(files []string, head string, c *reviewCache) (selected []string, hashes map[string]string) {
	hashes = map[string]string{}
	for _, f := range files {
		out, err := runGit("rev-parse", head+":"+f)
		if err != nil {
			// 拿不到 blob（如文件已删除）时保守处理：仍然审查，但不记录 hash
			selected = append(selected, f)
			continue
		}
		h := strings.TrimSpace(out)
		if c.Files[f] == h {
			continue // 已审查且内容未变化，跳过
		}
		hashes[f] = h
		selected = append(selected, f)
	}
	return selected, hashes
}