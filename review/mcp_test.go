package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestLoadMCPConfig 验证 server 配置的解析与 ${VAR} 插值。
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
				"args": ["-y", "@modelcontextprotocol/server-github", "${OWNER}/repo"],
				"env": { "GITHUB_TOKEN": "${MCP_TEST_TOKEN}" }
			}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err = LoadMCPConfig(path, map[string]string{"OWNER": "octo"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg == nil {
		t.Fatal("配置存在时不应返回 nil")
	}
	srv := cfg.Servers["github"]
	if srv.Command != "npx" || len(srv.Args) != 3 || srv.Args[2] != "octo/repo" {
		t.Errorf("server 配置解析/插值错误: %+v", srv)
	}
	if srv.Env["GITHUB_TOKEN"] != "secret-token" {
		t.Errorf("env 插值失败: %q", srv.Env["GITHUB_TOKEN"])
	}
}

// TestShouldExposeTool 验证白名单 > annotations > 工具名分词兜底的暴露规则。
func TestShouldExposeTool(t *testing.T) {
	readOnly, destructive := true, true
	tests := []struct {
		name    string
		tool    mcpToolDef
		allowed []string
		want    bool
	}{
		{"只读工具默认放行", mcpToolDef{Name: "read_file"}, nil, true},
		{"写操作工具默认屏蔽", mcpToolDef{Name: "delete_file"}, nil, false},
		{"分词避免子串误杀", mcpToolDef{Name: "get_input"}, nil, true},
		{"驼峰分词识别写操作", mcpToolDef{Name: "deleteFile"}, nil, false},
		{"注解 readOnly 放行", mcpToolDef{Name: "write_report", Annotations: &mcpToolAnnotations{ReadOnlyHint: &readOnly}}, nil, true},
		{"注解 destructive 拒绝", mcpToolDef{Name: "peek", Annotations: &mcpToolAnnotations{DestructiveHint: &destructive}}, nil, false},
		{"白名单命中优先", mcpToolDef{Name: "delete_file"}, []string{"delete_file"}, true},
		{"白名单非空未命中则过滤", mcpToolDef{Name: "read_file"}, []string{"write_file"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldExposeTool(tt.tool, tt.allowed); got != tt.want {
				t.Errorf("shouldExposeTool(%s) = %v，期望 %v", tt.tool.Name, got, tt.want)
			}
		})
	}
}

// TestValidateToolArgs 验证转发前的 required / 类型 / enum 校验。
func TestValidateToolArgs(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"path"},
		"properties": map[string]any{
			"path":  map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer"},
			"mode":  map[string]any{"type": "string", "enum": []any{"read", "rw"}},
		},
		"additionalProperties": false,
	}
	tests := []struct {
		name    string
		args    map[string]any
		wantErr string
	}{
		{"合法参数", map[string]any{"path": "a.go", "limit": float64(10), "mode": "read"}, ""},
		{"缺少必填", map[string]any{}, "缺少必填字段"},
		{"类型错误", map[string]any{"path": float64(1)}, "类型应为 string"},
		{"整数校验", map[string]any{"path": "a.go", "limit": 1.5}, "类型应为 integer"},
		{"枚举校验", map[string]any{"path": "a.go", "mode": "write"}, "枚举范围"},
		{"多余字段", map[string]any{"path": "a.go", "extra": true}, "不允许的字段"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateToolArgs(schema, tt.args)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过，实际报错: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("期望错误包含 %q，实际: %v", tt.wantErr, err)
			}
		})
	}
}

// startFakeRegistry 启动一个伪装 MCP server 的测试进程，返回已注册工具的 registry。
func startFakeRegistry(t *testing.T) *mcpGlobalRegistry {
	t.Helper()
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
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(path, cfgJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	return initMCPServers(&Config{MCPConfigPath: path})
}

// TestMCPToolsEndToEnd 用测试助手进程伪装 MCP server，端到端验证：
// initialize 握手、tools/list cursor 分页、schema 校验、tools/call 调用。
func TestMCPToolsEndToEnd(t *testing.T) {
	reg := startFakeRegistry(t)
	defer closeMCPServers(reg)

	if !reg.enabled {
		t.Fatal("应成功启用 MCP 工具箱")
	}
	if len(reg.openaiTools) != 2 {
		t.Fatalf("分页应注册 2 个工具，实际 %d", len(reg.openaiTools))
	}

	ctx := context.Background()
	if got := reg.invokeTool(ctx, "fake:read_file", `{"path":"a.go"}`); got != "file content: a.go" {
		t.Errorf("工具调用结果错误: %q", got)
	}
	if got := reg.invokeTool(ctx, "fake:read_file", `{}`); !strings.Contains(got, "缺少必填字段") {
		t.Errorf("缺少必填参数时应返回校验错误，实际: %q", got)
	}
	if got := reg.invokeTool(ctx, "fake:read_file", `{"path":123}`); !strings.Contains(got, "类型应为 string") {
		t.Errorf("参数类型错误时应返回校验错误，实际: %q", got)
	}
	if got := reg.invokeTool(ctx, "fake:unknown", `{}`); !strings.Contains(got, "未注册的工具") {
		t.Errorf("未注册工具应返回错误提示，实际: %q", got)
	}
}

// TestMCPInvokeToolConcurrent 验证多个 tool_call 并发执行时响应按 id 正确路由，不会串包。
func TestMCPInvokeToolConcurrent(t *testing.T) {
	reg := startFakeRegistry(t)
	defer closeMCPServers(reg)

	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := fmt.Sprintf("f%d.go", i)
			got := reg.invokeTool(ctx, "fake:read_file", fmt.Sprintf(`{"path":%q}`, path))
			if want := "file content: " + path; got != want {
				t.Errorf("并发调用结果错配: got %q, want %q", got, want)
			}
		}(i)
	}
	wg.Wait()
}

// TestMCPHelperProcess 不是真正的测试，而是作为子进程运行的假 MCP server：
// 从 stdin 按行读取 JSON-RPC，对 initialize / tools/list（两页） / tools/call 回包。
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
		case "tools/list":
			var p struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if p.Cursor == "" {
				result = map[string]any{
					"tools": []any{map[string]any{
						"name":        "read_file",
						"description": "读取文件",
						"inputSchema": map[string]any{
							"type":     "object",
							"required": []any{"path"},
							"properties": map[string]any{
								"path": map[string]any{"type": "string"},
							},
						},
					}},
					"nextCursor": "page2",
				}
			} else {
				result = map[string]any{
					"tools": []any{map[string]any{
						"name":        "list_issues",
						"description": "列出 issue",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
						"annotations": map[string]any{"readOnlyHint": true},
					}},
				}
			}
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if p.Name == "read_file" {
				result = map[string]any{
					"content": []map[string]any{{"type": "text", "text": "file content: " + fmt.Sprint(p.Arguments["path"])}},
				}
			} else {
				result = map[string]any{
					"content": []map[string]any{{"type": "text", "text": "issues"}},
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
