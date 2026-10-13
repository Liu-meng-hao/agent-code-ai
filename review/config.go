package main

import (
	"fmt"
	"os"
	"strings"
)

// Config 保存审查流程所需的全部外部配置，全部来自环境变量。
type Config struct {
	APIKey   string // DeepSeek API Key
	BaseURL  string // LLM 服务地址
	Model    string // 模型名
	Owner    string // 仓库 owner
	Repo     string // 仓库名
	PRNumber string // PR 编号
	BaseSHA  string // PR 基准 commit
	HeadSHA  string // PR 最新 commit
}

// LoadConfig 从环境变量读取配置，缺失必需项时返回错误。
func LoadConfig() (*Config, error) {
	cfg := &Config{
		APIKey:   os.Getenv("DEEPSEEK_API_KEY"),
		BaseURL:  envOr("LLM_BASE_URL", "https://api.deepseek.com"),
		Model:    envOr("LLM_MODEL", "deepseek-flash"),
		PRNumber: os.Getenv("PR_NUMBER"),
		BaseSHA:  os.Getenv("BASE_SHA"),
		HeadSHA:  os.Getenv("HEAD_SHA"),
	}

	// GITHUB_REPOSITORY 形如 "owner/repo"，由 GitHub Actions 自动注入
	if parts := strings.SplitN(os.Getenv("GITHUB_REPOSITORY"), "/", 2); len(parts) == 2 {
		cfg.Owner, cfg.Repo = parts[0], parts[1]
	}

	var missing []string
	if cfg.APIKey == "" {
		missing = append(missing, "DEEPSEEK_API_KEY")
	}
	if cfg.Owner == "" || cfg.Repo == "" {
		missing = append(missing, "GITHUB_REPOSITORY")
	}
	if cfg.PRNumber == "" {
		missing = append(missing, "PR_NUMBER")
	}
	if cfg.BaseSHA == "" {
		missing = append(missing, "BASE_SHA")
	}
	if cfg.HeadSHA == "" {
		missing = append(missing, "HEAD_SHA")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("缺少必需的环境变量: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
