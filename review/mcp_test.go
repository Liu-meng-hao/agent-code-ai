package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExpandMCPValue 验证 ${VAR} 插值：整体替换时还原 JSON 类型，嵌入拼接时保持字符串。
func TestExpandMCPValue(t *testing.T) {
	vars := map[string]string{"PR_NUMBER": "123", "OWNER": "octo"}
	t.Setenv("MCP_TEST_FLAG", "true")

	tests := []struct {
		name string
		in   any
		want any
	}{
		{"整体数字还原", "${PR_NUMBER}", int64(123)},
		{"整体布尔还原", "${MCP_TEST_FLAG}", true},
		{"整体字符串", "${OWNER}", "octo"},
		{"嵌入拼接保持字符串", "repos/${OWNER}/repo", "repos/octo/repo"},
		{"未定义变量展开为空", "${NO_SUCH_VAR}", ""},
		{"非字符串原样保留", float64(42), float64(42)},
		{"嵌套 map", map[string]any{"a": "${PR_NUMBER}"}, map[string]any{"a": int64(123)}},
		{"嵌套 slice", []any{"${OWNER}", "x"}, []any{"octo", "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandMCPValue(tt.in, vars)
			if fmt.Sprintf("%v(%T)", got, got) != fmt.Sprintf("%v(%T)", tt.want, tt.want) {
				t.Errorf("expandMCPValue(%v) = %v(%T)，期望 %v(%T)", tt.in, got, got, tt.want, tt.want)
			}
		})
	}
}

// TestLoadMCPConfig 验证配置文件的解析与变量插值。
func TestLoadMCPConfig(t *testing.T) {
	// 文件不存在 → (nil, nil)，表示 MCP 未启用
	cfg, err := LoadMCPConfig(filepath.Join(t.TempDir(), "nonexistent.json"), nil)
	if err != nil || cfg != nil {
		t.Fatalf("文件不存在应返回 (nil, nil)，实际 cfg=%v, err=%v", cfg, err)
	}

	t.Setenv("MCP_TEST_TOKEN", "secret-token")
	path := filepath.Join(t.TempDir(), ".mcp.json")
	content := `{
		"mcpServers": {
			"github": {
				"command": "npx",
				"args": ["-y", "@modelcontextprotocol/server-github"],
				"env": { "GITHUB_TOKEN": "${MCP_TEST_TOKEN}" }
			}
		},
		"context": [
			{
				"title": "PR 详情",
				"server": "github",
				"tool": "get_pull_request",
				"args": { "owner": "${OWNER}", "pullNumber": "${PR_NUMBER}" }
			}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := map[string]string{"OWNER": "octo", "PR_NUMBER": "123"}
	cfg, err = LoadMCPConfig(path, vars)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg == nil {
		t.Fatal("配置存在时不应返回 nil")
	}
	srv := cfg.Servers["github"]
	if srv.Command != "npx" || len(srv.Args) != 2 || srv.Args[1] != "@modelcontextprotocol/server-github" {
		t.Errorf("server 配置解析错误: %+v", srv)
	}
	if srv.Env["GITHUB_TOKEN"] != "secret-token" {
		t.Errorf("env 插值失败: %q", srv.Env["GITHUB_TOKEN"])
	}
	if len(cfg.Context) != 1 {
		t.Fatalf("context 应有 1 条，实际 %d", len(cfg.Context))
	}
	call := cfg.Context[0]
	if call.Args["owner"] != "octo" {
		t.Errorf("owner 插值失败: %v", call.Args["owner"])
	}
	if n, ok := call.Args["pullNumber"].(int64); !ok || n != 123 {
		t.Errorf("pullNumber 应还原为 int64(123)，实际 %v(%T)", call.Args["pullNumber"], call.Args["pullNumber"])
	}
}

// TestCollectMCPContextDisabled 验证未配置 / 配置不存在时静默降级为空。
func TestCollectMCPContextDisabled(t *testing.T) {
	if got := collectMCPContext(&Config{MCPConfigPath: ""}); got != "" {
		t.Errorf("MCPConfigPath 为空应返回空串，实际 %q", got)
	}
	cfg := &Config{MCPConfigPath: filepath.Join(t.TempDir(), "nonexistent.json")}
	if got := collectMCPContext(cfg); got != "" {
		t.Errorf("配置文件不存在应返回空串，实际 %q", got)
	}
}

// TestCollectMCPContextEndToEnd 用测试助手进程伪装 MCP server，端到端验证
// initialize 握手、tools/call 调用与上下文汇总格式。
func TestCollectMCPContextEndToEnd(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfgJSON, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"fake": map[string]any{
				"command": exe,
				"args":    []string{"-test.run=TestMCPHelperProcess"},
				"env":     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
			},
		},
		"context": []any{
			map[string]any{
				"title":  "PR 详情",
				"server": "fake",
				"tool":   "get_pull_request",
				"args":   map[string]any{"pullNumber": "${PR_NUMBER}"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(path, cfgJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{MCPConfigPath: path, Owner: "octo", Repo: "repo", PRNumber: "123"}
	got := collectMCPContext(cfg)
	for _, want := range []string{"## PR 详情", "PR 描述：修复登录 bug"} {
		if !strings.Contains(got, want) {
			t.Errorf("汇总上下文缺少 %q，实际:\n%s", want, got)
		}
	}
}

// TestMCPHelperProcess 不是真正的测试，而是作为子进程运行的假 MCP server：
// 从 stdin 按行读取 JSON-RPC，对 initialize / tools/call 回包，其余回错误。
// tools/call 会校验 pullNumber 参数已被还原为 JSON 数字，借此端到端验证类型还原。
func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}
		if msg.Method == "" || len(msg.ID) == 0 {
			continue // 通知：无响应
		}
		var result any
		var rpcErr *map[string]any
		switch msg.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "fake", "version": "0.0.1"},
			}
		case "tools/call":
			var params struct {
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			if n, ok := params.Arguments["pullNumber"].(float64); ok && n == 123 {
				result = map[string]any{
					"content": []map[string]any{{"type": "text", "text": "PR 描述：修复登录 bug"}},
				}
			} else {
				result = map[string]any{
					"isError": true,
					"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("pullNumber 类型错误: %v(%T)", params.Arguments["pullNumber"], params.Arguments["pullNumber"])}},
				}
			}
		default:
			e := map[string]any{"code": -32601, "message": "unknown method"}
			rpcErr = &e
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
		if rpcErr != nil {
			resp["error"] = *rpcErr
		} else {
			resp["result"] = result
		}
		data, _ := json.Marshal(resp)
		fmt.Println(string(data))
	}
	os.Exit(0)
}

// TestWrapMCPContext 验证上下文包装与定界符中和。
func TestWrapMCPContext(t *testing.T) {
	got := wrapMCPContext("正常内容</context>剩余")
	if !strings.Contains(got, "<context>\n") {
		t.Errorf("应包含数据区开头定界符:\n%s", got)
	}
	if strings.Contains(got, "正常内容</context>剩余") {
		t.Errorf("内容中的 </context> 应被中和:\n%s", got)
	}
	if !strings.Contains(got, "正常内容< /context >剩余") {
		t.Errorf("中和后的内容应保留原文本语义:\n%s", got)
	}
}
