package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
)

// cacheFile 记录「文件路径 -> 已审查过的 hunk 内容指纹列表」，用于跳过内容未变化的 hunk。
// 该文件由 workflow 的 actions/cache 按 PR 号隔离保存与恢复，不应提交到仓库。
const cacheFile = ".ai-review-cache.json"

type reviewCache struct {
	Files map[string][]string `json:"files"`
}

// loadCache 读取本地缓存文件；不存在、损坏或为旧版本格式时返回空缓存（不影响主流程）。
func loadCache() *reviewCache {
	c := &reviewCache{Files: map[string][]string{}}
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return c
	}
	// 旧版缓存是「路径 -> blob hash 字符串」，与当前「路径 -> hunk 指纹列表」不兼容；
	// 解析失败即降级为空缓存，本次全量审查并顺带把缓存升级为新格式。
	if err := json.Unmarshal(data, c); err != nil || c.Files == nil {
		c.Files = map[string][]string{}
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

// markReviewed 用本次审查后各文件「当前的 hunk 指纹集合」覆盖旧记录。
func (c *reviewCache) markReviewed(snapshot map[string][]string) {
	for f, hashes := range snapshot {
		c.Files[f] = hashes
	}
}

// hunkHash 计算单个 hunk 的内容指纹（sha256 前 16 位十六进制）。
// 输入为 hunk 原文（含 @@ 头行号），因此内容或位置任一变化都会得到不同的指纹。
func hunkHash(hunk string) string {
	sum := sha256.Sum256([]byte(hunk))
	return hex.EncodeToString(sum[:])[:16]
}
