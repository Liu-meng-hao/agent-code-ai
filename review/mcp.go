package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
)

// MCP 工具箱：把 MCP server 暴露的工具注册给 LLM，由模型在多轮 tool calling
// 循环中自主决定调用哪些工具、用什么参数调用（见 llm.go 的 LLMToolLoop）。
//
// 配置文件（默认 .mcp.json，可用 MCP_CONFIG_PATH 覆盖），兼容 Claude Desktop 的
// mcpServers 格式，生态里现成的 MCP server 可直接复用：
//
//	{
//	  "mcpServers": {
//	    "github": {
//	      "command": "npx",
//	      "args": ["-y", "@modelcontextprotocol/server-github"],
//	      "env": { "GITHUB_TOKEN": "${GITHUB_TOKEN}" }
//	    }
//	  }
//	}
//
// command/args/env 中的字符串支持 ${VAR} 插值，VAR 取内置变量（OWNER/REPO/
// PR_NUMBER/BASE_SHA/HEAD_SHA）或进程环境变量。
//
// 安全与降级约束：
//   - 工具暴露遵循「白名单 > server annotations > 工具名分词兜底」，默认屏蔽写操作类
//   - 单次工具返回文本截断到 maxMCPResultTokens；转发前用 inputSchema 做必填/类型/枚举校验
//   - 配置文件不存在、server 启动失败、工具调用失败都只记日志并降级，不影响既有审查流程
//     （与「不阻塞 CI」一致）
//
// 注意：多数 MCP server 以 npx/uvx 分发，CI 中启用前需确保相应运行时可用。
const (
	// maxMCPResultTokens 单次工具调用返回文本的 token 上限，超出截断。
	maxMCPResultTokens = 8000
	// mcpCallTimeout 单次 MCP 请求（握手/列工具/调用）的超时时间。
	mcpCallTimeout = 30 * time.Second
	// mcpProtocolVersion 握手时声明的协议版本（2024-11-05 为各 server 广泛支持的版本）。
	mcpProtocolVersion = "2024-11-05"
	// maxToolListPages tools/list 分页拉取的最大页数，防止异常 server 造成死循环。
	maxToolListPages = 100
)

// MCPConfig 对应 .mcp.json 的顶层结构。
type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"mcpServers"`
}

// MCPServerConfig 描述一个 MCP server 的启动方式（stdio 子进程）。
type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// expandVarString 展开字符串中的 ${VAR}：优先内置变量，其次进程环境变量，未定义展开为空串。
func expandVarString(s string, vars map[string]string) string {
	return os.Expand(s, func(k string) string {
		if v, ok := vars[k]; ok {
			return v
		}
		return os.Getenv(k)
	})
}

// LoadMCPConfig 读取并解析 MCP 配置文件，对 server 启动参数中的 ${VAR} 做插值。
// 文件不存在返回 (nil, nil)，调用方据此判定 MCP 未启用。
func LoadMCPConfig(path string, vars map[string]string) (*MCPConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cfg MCPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	for name, srv := range cfg.Servers {
		srv.Command = expandVarString(srv.Command, vars)
		for i, a := range srv.Args {
			srv.Args[i] = expandVarString(a, vars)
		}
		for k, v := range srv.Env {
			srv.Env[k] = expandVarString(v, vars)
		}
		cfg.Servers[name] = srv
	}
	return &cfg, nil
}

// ---- stdio JSON-RPC 客户端（MCP 传输层为换行分隔的 JSON-RPC 2.0，无 LSP 式头）----

// rpcError 是 JSON-RPC 2.0 的错误对象。
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcMessage 是 JSON-RPC 2.0 消息的最小表示：只关心 id/method/result/error。
// 请求与响应共用此结构；ID 用 json.RawMessage 以兼容数字与字符串两种 id 形式。
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// cappedBuffer 捕获子进程 stderr，最多保留 cap 字节，防止异常 server 刷屏撑爆内存。
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	cap int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if remain := b.cap - b.buf.Len(); remain > 0 {
		if len(p) > remain {
			b.buf.Write(p[:remain])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

// String 返回已捕获的 stderr（并发安全，供错误信息使用）。
func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// mcpClient 持有一个 MCP server 子进程。请求可以并发发起：nextID/pending 由 mu 保护，
// stdin 写入由 wmu 串行化，readLoop 按响应 id 投递到对应请求的 channel。
type mcpClient struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *cappedBuffer
	done   chan struct{} // readLoop 退出（进程断开）时关闭

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan rpcMessage
	wmu     sync.Mutex
}

// startMCPServer 启动 server 子进程并完成 initialize 握手。
func startMCPServer(name string, cfg MCPServerConfig) (*mcpClient, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	// 继承进程环境（PATH 等），再叠加配置中的 env 覆盖项
	env := os.Environ()
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &cappedBuffer{cap: 64 * 1024}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动失败: %w", err)
	}

	c := &mcpClient{
		name:    name,
		cmd:     cmd,
		stdin:   stdin,
		stderr:  stderr,
		done:    make(chan struct{}),
		pending: map[string]chan rpcMessage{},
	}
	go c.readLoop(stdout)

	// MCP 握手：initialize → notifications/initialized
	ctx, cancel := context.WithTimeout(context.Background(), mcpCallTimeout)
	defer cancel()
	_, err = c.call(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "ai-review", "version": "1.0.0"},
	})
	if err != nil {
		c.close()
		return nil, fmt.Errorf("initialize 握手失败: %w", err)
	}
	// 通知无响应，发送失败仅说明管道已断，直接报错
	if err := c.notify("notifications/initialized"); err != nil {
		c.close()
		return nil, fmt.Errorf("发送 initialized 通知失败: %w", err)
	}
	return c, nil
}

// readLoop 按行读取 server stdout：
//   - 带 id 且无 method 的消息是响应，投递给对应 in-flight 请求
//   - 带 id 且带 method 的消息是服务端反向请求，本客户端不支持，按协议回 -32601
//   - 无 id 的消息是通知，记录关心的类型后忽略
func (c *mcpClient) readLoop(stdout io.Reader) {
	defer close(c.done)
	r := bufio.NewReader(stdout)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg rpcMessage
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		if len(msg.ID) == 0 {
			if msg.Method != "" {
				// 工具列表等变更通知：本轮沿用启动时快照，下次审查生效
				fmt.Printf("[ai-review] MCP server %q 通知 %s（已忽略）\n", c.name, msg.Method)
			}
			continue
		}
		if msg.Method != "" {
			// 服务端反向请求（如 roots/sampling）：回 method not found，避免对端悬挂
			_ = c.send(rpcMessage{ID: msg.ID, Error: &rpcError{
				Code:    -32601,
				Message: "method not found: " + msg.Method,
			}})
			continue
		}
		key := string(msg.ID)
		c.mu.Lock()
		ch := c.pending[key]
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- msg:
			default: // 同一 id 的重复响应：丢弃，避免阻塞读循环
			}
		}
	}
}

// rpcID 把自增序号编码为数字形式的 JSON-RPC id。
func (c *mcpClient) rpcID() json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return json.RawMessage(strconv.FormatInt(c.nextID, 10))
}

// send 把一条消息写入 server stdin（换行结尾）。写入串行化，支持并发调用。
func (c *mcpClient) send(msg rpcMessage) error {
	msg.JSONRPC = "2.0"
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

// call 发送请求并等待匹配 id 的响应；ctx 控制超时。
func (c *mcpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.rpcID()
	key := string(id)
	ch := make(chan rpcMessage, 1)

	c.mu.Lock()
	c.pending[key] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}()

	if err := c.send(rpcMessage{ID: id, Method: method, Params: params}); err != nil {
		return nil, fmt.Errorf("发送 %s 请求失败: %w", method, err)
	}
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("%s 请求超时: %w", method, ctx.Err())
	case <-c.done:
		return nil, fmt.Errorf("%s 请求时 server 已断开: %s", method, strings.TrimSpace(c.stderr.String()))
	case msg := <-ch:
		if msg.Error != nil {
			return nil, fmt.Errorf("%s 返回错误 %d: %s", method, msg.Error.Code, msg.Error.Message)
		}
		return msg.Result, nil
	}
}

// notify 发送 JSON-RPC 通知（无 id，无响应）。
func (c *mcpClient) notify(method string) error {
	return c.send(rpcMessage{Method: method})
}

// callTool 调用 MCP 工具并提取文本结果：拼接 result.content[] 中所有 type=="text" 的
// text 字段；result.isError 为 true 时返回错误。
func (c *mcpClient) callTool(ctx context.Context, tool string, args map[string]any) (string, error) {
	result, err := c.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return "", err
	}
	var parsed struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return "", fmt.Errorf("解析 tools/call 结果失败: %w", err)
	}
	var sb strings.Builder
	for _, item := range parsed.Content {
		if item.Type == "text" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(item.Text)
		}
	}
	text := sb.String()
	if parsed.IsError {
		return "", fmt.Errorf("工具返回错误: %s", text)
	}
	return text, nil
}

// close 关闭 stdin 让 server 自然退出，最多等 2 秒后强杀，避免遗留子进程。
func (c *mcpClient) close() {
	_ = c.stdin.Close()
	doneCh := make(chan error, 1)
	go func() { doneCh <- c.cmd.Wait() }()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		<-doneCh
	}
}

// ---- 工具发现与注册 ----

// mcpToolAnnotations 是 MCP 工具的可选安全注解。
// 字段用指针以区分「未声明」和「显式 false」。
type mcpToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint"`
	DestructiveHint *bool `json:"destructiveHint"`
}

// mcpToolDef 对应 MCP tools/list 返回的单个工具定义。
type mcpToolDef struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	InputSchema map[string]any      `json:"inputSchema"`
	Annotations *mcpToolAnnotations `json:"annotations"`
}

// listTools 调 MCP tools/list 拉取当前 server 的全部工具定义，按 nextCursor 分页。
func (c *mcpClient) listTools(ctx context.Context) ([]mcpToolDef, error) {
	var all []mcpToolDef
	cursor := ""
	for page := 0; page < maxToolListPages; page++ {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		result, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var parsed struct {
			Tools      []mcpToolDef `json:"tools"`
			NextCursor string       `json:"nextCursor"`
		}
		if err := json.Unmarshal(result, &parsed); err != nil {
			return nil, fmt.Errorf("解析 tools/list 结果失败: %w", err)
		}
		all = append(all, parsed.Tools...)
		if parsed.NextCursor == "" {
			return all, nil
		}
		cursor = parsed.NextCursor
	}
	return nil, fmt.Errorf("tools/list 分页超过 %d 页，判定为异常", maxToolListPages)
}

// mcpGlobalRegistry 持有方案二所需的全局状态：所有活跃 MCP server 客户端 + 工具路由。
// 一次审查生命周期内只初始化一次（initMCPServers），主循环结束后 closeMCPServers 统一清理。
type mcpGlobalRegistry struct {
	clients     []*mcpClient                          // 所有活跃 server，用于统一 close
	toolRoute   map[string]*mcpClient                 // "server:tool" → client（反向路由）
	toolSchemas map[string]map[string]any             // "server:tool" → inputSchema（参数校验）
	openaiTools []openai.ChatCompletionToolUnionParam // 过滤后转成的 OpenAI 工具列表
	enabled     bool
}

// 硬编码兜底规则：工具名分词后命中这些词默认屏蔽（CI 安全约束）。
var mcpToolWriteWords = []string{
	"delete", "remove", "write", "update", "create", "edit", "put", "post",
	"execute", "run", "shell", "bash", "command",
}

// toolNameWords 把工具名按非字母数字、驼峰/数字边界拆成小写单词。
// 用词匹配替代子串匹配，避免 "get_input" 被 "put" 误杀。
func toolNameWords(name string) []string {
	var words []string
	for _, part := range strings.FieldsFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) {
		start := 0
		for i := 1; i < len(part); i++ {
			prev, cur := part[i-1], part[i]
			lowerToUpper := prev >= 'a' && prev <= 'z' && cur >= 'A' && cur <= 'Z'
			letterToDigit := (prev >= 'a' && prev <= 'z' || prev >= 'A' && prev <= 'Z') && cur >= '0' && cur <= '9'
			if lowerToUpper || letterToDigit {
				words = append(words, strings.ToLower(part[start:i]))
				start = i
			}
		}
		words = append(words, strings.ToLower(part[start:]))
	}
	return words
}

// shouldExposeTool 判断工具是否可暴露给 LLM，优先级：
//  1. 显式白名单（allowed 非空）命中 → 暴露；白名单非空但未命中 → 过滤
//  2. annotations.destructiveHint == true → 过滤
//  3. annotations.readOnlyHint == true → 暴露
//  4. 无注解时按工具名分词兜底，命中写操作词 → 过滤
func shouldExposeTool(tool mcpToolDef, allowed []string) bool {
	for _, a := range allowed {
		if a == tool.Name {
			return true
		}
	}
	if len(allowed) > 0 {
		return false
	}
	if tool.Annotations != nil {
		if tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint {
			return false
		}
		if tool.Annotations.ReadOnlyHint != nil && *tool.Annotations.ReadOnlyHint {
			return true
		}
	}
	for _, w := range toolNameWords(tool.Name) {
		for _, blocked := range mcpToolWriteWords {
			if w == blocked {
				return false
			}
		}
	}
	return true
}

// initMCPServers 读取 MCP 配置、启动所有 server、拉取工具列表、过滤并转 OpenAI 格式。
// 任何环节失败都降级：enabled=false，返回的 registry 里 openaiTools 为空，主循环退化为单次调用。
func initMCPServers(cfg *Config) *mcpGlobalRegistry {
	reg := &mcpGlobalRegistry{
		toolRoute:   map[string]*mcpClient{},
		toolSchemas: map[string]map[string]any{},
	}
	if cfg.MCPConfigPath == "" {
		return reg
	}
	vars := map[string]string{
		"OWNER":     cfg.Owner,
		"REPO":      cfg.Repo,
		"PR_NUMBER": cfg.PRNumber,
		"BASE_SHA":  cfg.BaseSHA,
		"HEAD_SHA":  cfg.HeadSHA,
	}
	mcpCfg, err := LoadMCPConfig(cfg.MCPConfigPath, vars)
	if err != nil {
		fmt.Printf("[ai-review] 加载 MCP 配置失败，跳过 MCP 工具箱: %v\n", err)
		return reg
	}
	if mcpCfg == nil {
		return reg // 配置文件不存在
	}

	for srvName, srvCfg := range mcpCfg.Servers {
		client, err := startMCPServer(srvName, srvCfg)
		if err != nil {
			fmt.Printf("[ai-review] 启动 MCP server %q 失败，跳过: %v\n", srvName, err)
			continue
		}
		reg.clients = append(reg.clients, client)

		// 每个 server 单独计时，避免某个 server 冷启动慢拖垮后续 server
		ctx, cancel := context.WithTimeout(context.Background(), mcpCallTimeout)
		tools, err := client.listTools(ctx)
		cancel()
		if err != nil {
			fmt.Printf("[ai-review] MCP server %q listTools 失败，跳过: %v\n", srvName, err)
			client.close()
			// 从 clients 里移除
			reg.clients = reg.clients[:len(reg.clients)-1]
			continue
		}

		for _, t := range tools {
			if !shouldExposeTool(t, cfg.MCPAllowedTools) {
				fmt.Printf("[ai-review] 过滤 MCP 工具 %s/%s（安全或白名单原因）\n", srvName, t.Name)
				continue
			}
			prefixed := srvName + ":" + t.Name
			reg.toolRoute[prefixed] = client

			// inputSchema 需要补全 JSONSchema 必填字段（type: object，properties 为空）
			schema := t.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			if _, ok := schema["type"]; !ok {
				schema["type"] = "object"
			}
			if _, ok := schema["properties"]; !ok {
				schema["properties"] = map[string]any{}
			}
			// 移除 JSON Schema 的 $schema / id 字段（OpenAI 会拒绝）
			delete(schema, "$schema")
			delete(schema, "id")
			reg.toolSchemas[prefixed] = schema

			// 通过 param.Override 构造 FunctionDefinitionParam，
			// 绕开 Opt[map] 的 comparable 约束；Override 会用底层 JSON 覆盖所有字段
			fnDef := param.Override[shared.FunctionDefinitionParam](map[string]any{
				"name":        prefixed,
				"description": t.Description,
				"parameters":  schema,
			})
			reg.openaiTools = append(reg.openaiTools, openai.ChatCompletionToolUnionParam{
				OfFunction: &openai.ChatCompletionFunctionToolParam{
					Function: fnDef,
				},
			})
		}
		fmt.Printf("[ai-review] MCP server %q: 暴露 %d/%d 个工具\n", srvName, len(reg.openaiTools)-countPrefix(reg.openaiTools, srvName+":"), len(tools))
	}

	if len(reg.openaiTools) == 0 {
		fmt.Println("[ai-review] MCP 工具箱: 无可用工具，LLM 将退化为单次调用")
		return reg
	}
	reg.enabled = true
	fmt.Printf("[ai-review] MCP 工具箱就绪: %d 个 server, %d 个工具\n", len(reg.clients), len(reg.openaiTools))
	return reg
}

// countPrefix 统计 openaiTools 中 function name 以 "srv:" 开头的数量（仅日志用）。
func countPrefix(tools []openai.ChatCompletionToolUnionParam, prefix string) int {
	n := 0
	for _, t := range tools {
		if t.OfFunction != nil && strings.HasPrefix(t.OfFunction.Function.Name, prefix) {
			n++
		}
	}
	return n
}

// invokeTool 按 "server:tool" 名路由到对应 MCP client 执行，返回截断后的文本结果。
// 转发前用注册时保存的 inputSchema 做必填/类型/枚举校验，校验失败以错误文本回灌给 LLM。
func (reg *mcpGlobalRegistry) invokeTool(ctx context.Context, prefixedName, argsJSON string) string {
	client, ok := reg.toolRoute[prefixedName]
	if !ok {
		return fmt.Sprintf("错误：未注册的工具 %q，可选工具: %s", prefixedName, reg.availableToolNames())
	}
	args := map[string]any{}
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("错误：工具 %q 参数不是合法 JSON: %v", prefixedName, err)
		}
	}
	if schema := reg.toolSchemas[prefixedName]; schema != nil {
		if err := validateToolArgs(schema, args); err != nil {
			return fmt.Sprintf("错误：工具 %q 参数不符合 inputSchema: %v", prefixedName, err)
		}
	}

	text, err := client.callTool(ctx, extractToolName(prefixedName), args)
	if err != nil {
		return fmt.Sprintf("工具 %q 执行失败: %v", prefixedName, err)
	}

	// 工具调用结果截断：单个 tool result 限 maxMCPResultTokens，避免 read_file 撑爆消息
	if tokens := estimateTokens(text); tokens > maxMCPResultTokens {
		lines := strings.Split(text, "\n")
		kept := 0
		var sb strings.Builder
		for i, ln := range lines {
			if estimateTokens(sb.String()+ln+"\n") > maxMCPResultTokens {
				break
			}
			sb.WriteString(ln)
			sb.WriteByte('\n')
			kept = i + 1
		}
		text = sb.String() + fmt.Sprintf("\n... (截断，共 %d 行，已保留前 %d 行)", len(lines), kept)
	}
	if text == "" {
		return "(工具返回空结果)"
	}
	return text
}

// validateToolArgs 按 inputSchema 做轻量校验：required、顶层属性类型、enum、additionalProperties。
// 不递归校验嵌套对象，足以在转发前拦住模型最常见的参数错误。
func validateToolArgs(schema map[string]any, args map[string]any) error {
	var problems []string
	if required, ok := schema["required"].([]any); ok {
		for _, r := range required {
			name, ok := r.(string)
			if !ok {
				continue
			}
			if _, exists := args[name]; !exists {
				problems = append(problems, fmt.Sprintf("缺少必填字段 %q", name))
			}
		}
	}

	props, _ := schema["properties"].(map[string]any)
	additionalAllowed := true
	if ap, ok := schema["additionalProperties"].(bool); ok {
		additionalAllowed = ap
	}
	for name, v := range args {
		prop, ok := props[name].(map[string]any)
		if !ok {
			if !additionalAllowed {
				problems = append(problems, fmt.Sprintf("不允许的字段 %q", name))
			}
			continue
		}
		if v == nil {
			continue
		}
		if typ, ok := prop["type"].(string); ok && !jsonTypeMatches(typ, v) {
			problems = append(problems, fmt.Sprintf("字段 %q 类型应为 %s，实际为 %s", name, typ, jsonTypeName(v)))
		}
		if enum, ok := prop["enum"].([]any); ok && len(enum) > 0 && !jsonValueIn(enum, v) {
			problems = append(problems, fmt.Sprintf("字段 %q 取值不在允许的枚举范围内", name))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "；"))
	}
	return nil
}

// jsonTypeMatches 判断 JSON 反序列化后的值是否符合 schema 声明的类型。
func jsonTypeMatches(typ string, v any) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		return jsonTypeName(v) == "number"
	case "integer":
		switch n := v.(type) {
		case float64:
			return n == math.Trunc(n)
		case int, int64, int32:
			return true
		}
		return false
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "null":
		return v == nil
	default:
		return true // 未声明/复合类型不做校验
	}
}

// jsonTypeName 返回值的 JSON 类型名，用于错误提示。
func jsonTypeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, float32, int, int64, int32:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}

// jsonValueIn 判断值是否命中 enum 列表（JSON 反序列化后类型一致，可直接深比较）。
func jsonValueIn(enum []any, v any) bool {
	for _, e := range enum {
		if reflect.DeepEqual(e, v) {
			return true
		}
	}
	return false
}

// extractToolName 从 "server:tool" 里切出 MCP 原始工具名。
func extractToolName(prefixed string) string {
	if i := strings.Index(prefixed, ":"); i >= 0 {
		return prefixed[i+1:]
	}
	return prefixed
}

// availableToolNames 返回所有可调用的 "server:tool" 名（供错误提示）。
func (reg *mcpGlobalRegistry) availableToolNames() string {
	names := make([]string, 0, len(reg.toolRoute))
	for k := range reg.toolRoute {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// closeMCPServers 关闭所有活跃 server 子进程（审查结束后统一调用）。
func closeMCPServers(reg *mcpGlobalRegistry) {
	if reg == nil {
		return
	}
	for _, c := range reg.clients {
		c.close()
	}
	reg.clients = nil
	reg.toolRoute = map[string]*mcpClient{}
	reg.toolSchemas = map[string]map[string]any{}
	reg.openaiTools = nil
	reg.enabled = false
}
