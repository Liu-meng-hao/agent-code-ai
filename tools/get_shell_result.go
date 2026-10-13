package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/openai/openai-go/v3"
)

// ShellResult 执行结果
type ShellResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Command  string `json:"command"`
	Shell    string `json:"shell"`
}

func NewExecShell() *ToolDef {
	return &ToolDef{
		Name:        "exec_shell",
		Description: "执行 shell 命令，兼容 Windows 和 Linux/macOS。返回命令的退出码、标准输出和标准错误，可以用shell 命令创建和编辑文件和脚本",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]string{
					"type":        "string",
					"description": "要执行的 shell 命令，例如 `ls -la` 或 `dir`。",
				},
				"timeout_seconds": map[string]string{
					"type":        "integer",
					"description": "可选，命令执行的超时时间（秒），默认 30 秒。",
				},
				"workdir": map[string]string{
					"type":        "string",
					"description": "可选，命令执行的工作目录，默认为当前进程的工作目录。",
				},
				"shell": map[string]string{
					"type":        "string",
					"description": "可选，指定使用的 shell：`sh`、`bash`、`cmd`、`powershell`。默认根据系统自动选择。",
				},
			},
			"required": []string{"command"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			// 1. 解析参数
			command, ok := args["command"].(string)
			if !ok || command == "" {
				return "", fmt.Errorf("参数 command 必填且必须是非空字符串")
			}

			timeoutSec := 30
			if v, ok := args["timeout_seconds"]; ok {
				switch n := v.(type) {
				case float64:
					timeoutSec = int(n)
				case int:
					timeoutSec = n
				case int64:
					timeoutSec = int(n)
				}
			}
			if timeoutSec <= 0 {
				timeoutSec = 30
			}

			workdir := ""
			if v, ok := args["workdir"].(string); ok {
				workdir = v
			}

			requestedShell, _ := args["shell"].(string)

			// 2. 构造对应平台的 shell 调用
			shellName, shellArgs := buildShellCommand(requestedShell, command)

			// 3. 带超时的 context
			runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
			defer cancel()

			cmd := exec.CommandContext(runCtx, shellName, shellArgs...)
			if workdir != "" {
				cmd.Dir = workdir
			}

			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			// 4. 执行
			runErr := cmd.Run()

			result := ShellResult{
				Stdout:  stdout.String(),
				Stderr:  stderr.String(),
				Command: command,
				Shell:   shellName,
			}

			if runErr != nil {
				// 优先判断 context 状态（超时 / 取消），再判断进程退出错误。
				// 使用 errors.Is 以兼容可能被包装过的错误。
				switch {
				case errors.Is(runCtx.Err(), context.DeadlineExceeded):
					result.ExitCode = -1
					result.Stderr += fmt.Sprintf("\n[exec_shell] 命令超时（>%d 秒）", timeoutSec)
				case errors.Is(runCtx.Err(), context.Canceled):
					result.ExitCode = -1
					result.Stderr += "\n[exec_shell] 命令被取消"
				default:
					// 使用 errors.As 而不是 type assertion，
					// 这样即使 runErr 被 fmt.Errorf("%w", ...) 包装过也能正确解出。
					var exitErr *exec.ExitError
					if errors.As(runErr, &exitErr) {
						result.ExitCode = exitErr.ExitCode()
					} else {
						result.ExitCode = -1
						result.Stderr += fmt.Sprintf("\n[exec_shell] 执行失败: %v", runErr)
					}
				}
			}

			b, err := json.Marshal(result)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	}
}

// buildShellCommand 根据平台和用户偏好返回 (shell 可执行文件, 参数列表)
// 约定：shell 名称使用 -c / /C 来执行一段命令字符串
func buildShellCommand(requestedShell, command string) (string, []string) {
	// 如果用户显式指定，优先使用
	switch requestedShell {
	case "sh":
		return "sh", []string{"-c", command}
	case "bash":
		return "bash", []string{"-c", command}
	case "cmd":
		return "cmd", []string{"/C", command}
	case "powershell", "pwsh":
		return requestedShell, []string{"-NoProfile", "-NonInteractive", "-Command", command}
	}

	// 未指定则按平台自动选择
	if runtime.GOOS == "windows" {
		// Windows 默认用 cmd；如果想要更现代的体验可改成 "powershell"
		return "cmd", []string{"/C", command}
	}
	// Linux/macOS 默认 sh（几乎所有环境都有）
	return "sh", []string{"-c", command}
}
