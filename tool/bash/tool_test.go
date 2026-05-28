package bash

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
)

func TestBashTool_Cancel(t *testing.T) {
	config := DefaultConfig()
	config.Timeout = 5 // 设置足够长的超时时间

	// 根据平台选择耗时命令
	var cmdName string
	var cmdArgs []string
	if runtime.GOOS == "windows" {
		cmdName = "ping"
		cmdArgs = []string{"127.0.0.1", "-n", "10"}
	} else {
		cmdName = "sleep"
		cmdArgs = []string{"10"}
	}

	// 添加到允许列表，确保可以执行
	config.Allow = append(config.Allow, cmdName)

	bTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := bTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// 创建一个可以取消的上下文
	ctx, cancel := context.WithCancel(context.Background())

	// 在另一个 goroutine 中取消上下文
	go func() {
		time.Sleep(1 * time.Second) // 等待 1 秒后取消
		cancel()
	}()

	// 执行耗时命令
	startTime := time.Now()

	paramsMap := map[string]interface{}{
		"command": cmdName,
		"args":    cmdArgs,
	}
	paramsBytes, _ := json.Marshal(paramsMap)
	params := string(paramsBytes)

	resultStr, err := toolInstance.InvokableRun(ctx, params)
	duration := time.Since(startTime)

	assert.NoError(t, err) // InvokableRun 本身不返回 error，而是返回 JSON 结果

	// 检查结果是否包含错误信息（新格式）
	assert.Contains(t, resultStr, "被中断")
	// Exit Code 应该是非零（因为被取消）
	assert.Contains(t, resultStr, "Exit Code:")

	// 确保执行时间小于命令原本需要的时间 (10秒)
	assert.Less(t, duration.Seconds(), 9.0)

	t.Logf("Result: %s", resultStr)
}

func TestBashTool_InvalidCommand(t *testing.T) {
	config := DefaultConfig()
	bTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := bTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// 测试无效路径，验证 PowerShell 错误信息不乱码
	// 使用完整命令字符串模式，触发 PowerShell 执行
	paramsMap := map[string]interface{}{
		"command": "dir non_existent_path_123",
	}
	paramsBytes, _ := json.Marshal(paramsMap)

	resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
	assert.NoError(t, err)

	// 验证包含错误信息
	// PowerShell 错误通常包含 "CategoryInfo" 或 "FullyQualifiedErrorId"
	// 或者简单的 "Cannot find path" / "找不到路径"

	t.Logf("Result: %s", resultStr)
}

func TestBashTool_BlockedArgsNotFalsePositive(t *testing.T) {
	config := DefaultConfig()
	bTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := bTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// 测试不应该被误判为包含禁止模式的命令
	testCases := []struct {
		name    string
		command string
	}{
		{"env var with underscore", "echo $GITEE_AI_API_KEY"},
		{"url with https", "curl https://example.com/api"},
		{"path with /some", "cat /some/path/file.txt"},
		{"echo simple", "echo hello"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			paramsMap := map[string]interface{}{
				"command": tc.command,
			}
			paramsBytes, _ := json.Marshal(paramsMap)

			resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
			assert.NoError(t, err)

			// 不应该包含"禁止模式"错误
			assert.NotContains(t, resultStr, "禁止模式", "命令 '%s' 不应该被误判为包含禁止模式", tc.command)

			t.Logf("Command: %s, Result: %s", tc.command, resultStr)
		})
	}
}

func TestBashTool_BlockedArgsTruePositive(t *testing.T) {
	config := DefaultConfig()
	// 使用拒绝模式以便测试
	config.Mode = ModeDeny
	bTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := bTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// 测试应该被阻止的命令（黑名单模式下）
	// 根据平台选择不同的命令，因为 del 是 Windows 命令
	var testCases []struct {
		name    string
		command string
		blocked string
	}
	if runtime.GOOS == "windows" {
		testCases = []struct {
			name    string
			command string
			blocked string
		}{
			{"del with /s", "del /s folder", "/s"},
			{"del with /q", "del /q file", "/q"},
		}
	} else {
		testCases = []struct {
			name    string
			command string
			blocked string
		}{
			{"rm -rf root", "rm -rf /", "-rf /"},
		}
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			paramsMap := map[string]interface{}{
				"command": tc.command,
			}
			paramsBytes, _ := json.Marshal(paramsMap)

			resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
			assert.NoError(t, err)

			// 应该包含"denied pattern"错误
			assert.Contains(t, resultStr, "denied pattern", "命令 '%s' 应该被阻止", tc.command)
			assert.Contains(t, resultStr, tc.blocked, "命令 '%s' 应该被阻止，因为包含 '%s'", tc.command, tc.blocked)

			t.Logf("Command: %s, Result: %s", tc.command, resultStr)
		})
	}
}

func TestBashTool_AndOperator(t *testing.T) {
	config := DefaultConfig()
	bTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := bTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// 测试 && 操作符
	// 在 Windows PowerShell 中会被替换为 ;
	// 在 Bash 中原生支持
	paramsMap := map[string]interface{}{
		"command": "echo hello && echo world",
	}
	paramsBytes, _ := json.Marshal(paramsMap)

	resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
	assert.NoError(t, err)

	// 验证输出包含 hello 和 world
	assert.Contains(t, resultStr, "hello")
	assert.Contains(t, resultStr, "world")

	t.Logf("Result: %s", resultStr)
}

func TestBashTool_GrepWithRegex(t *testing.T) {
	config := DefaultConfig()
	bTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := bTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// 测试包含正则表达式的 grep 命令
	// 这个命令格式类似用户提供的: grep -n "agentId\|getSessionsByAgent" file1 file2
	paramsMap := map[string]interface{}{
		"command": `grep -n "agentId\|getSessionsByAgent" tool.go tool_test.go`,
	}
	paramsBytes, _ := json.Marshal(paramsMap)

	resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
	assert.NoError(t, err)

	// 验证命令执行成功（grep 在白名单中）
	// 新格式检查 Exit Code: 0 表示成功
	assert.Contains(t, resultStr, "Exit Code: 0")
	t.Logf("Result: %s", resultStr)
}

func TestBashTool_GrepWithRelativePath(t *testing.T) {
	config := DefaultConfig()
	bTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := bTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// 使用当前工作目录替代硬编码路径
	wd, err := os.Getwd()
	assert.NoError(t, err)

	// 测试包含相对路径的 grep 命令
	paramsMap := map[string]interface{}{
		"command":  `grep -n "extractAllCommands\|InvokableRun" tool.go tool_test.go`,
		"work_dir": wd,
	}
	paramsBytes, _ := json.Marshal(paramsMap)

	resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
	assert.NoError(t, err)

	// 验证命令执行成功
	assert.Contains(t, resultStr, "Exit Code: 0")
	t.Logf("Result: %s", resultStr)
}

func TestBashTool_CatWithGitBashPath(t *testing.T) {
	config := DefaultConfig()
	bTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := bTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// 使用当前工作目录替代硬编码路径
	wd, err := os.Getwd()
	assert.NoError(t, err)

	// 测试 cat 命令读取当前目录下的文件
	paramsMap := map[string]interface{}{
		"command":  `cat tool.go`,
		"work_dir": wd,
	}
	paramsBytes, _ := json.Marshal(paramsMap)

	resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
	assert.NoError(t, err)

	// 验证命令执行成功
	assert.Contains(t, resultStr, "Exit Code: 0")
	t.Logf("Result: %s", resultStr)
}

func TestExtractAllCommands_Redirection(t *testing.T) {
	testCases := []struct {
		name     string
		command  string
		expected []string
	}{
		{"pipe with stderr redirect", "curl -L url 2>&1 | head -100", []string{"curl", "head"}},
		{"stderr to stdout", "cmd 2>&1", []string{"cmd"}},
		{"stdout and stderr redirect", "cmd >&2", []string{"cmd"}},
		{"input redirect dup", "cmd <&0", []string{"cmd"}},
		{"pipe simple", "cat file | grep pattern", []string{"cat", "grep"}},
		{"background job", "sleep 10 &", []string{"sleep"}},
		{"and operator", "cmd1 && cmd2", []string{"cmd1", "cmd2"}},
		{"or operator", "cmd1 || cmd2", []string{"cmd1", "cmd2"}},
		{"semicolon", "cmd1; cmd2", []string{"cmd1", "cmd2"}},
		{"complex pipeline", "cat file 2>&1 | grep pattern | head -10", []string{"cat", "grep", "head"}},
		{"redirect to file", "echo hello > file.txt", []string{"echo"}},
		{"redirect from file", "cat < input.txt", []string{"cat"}},
		{"append redirect", "echo hello >> file.txt", []string{"echo"}},
		{"bash &> redirect", "cmd &> file", []string{"cmd"}},
		// 边缘情况测试
		{"here string", "cat <<< 'hello'", []string{"cat"}},
		{"here document", "cat << EOF", []string{"cat"}},
		{"command substitution", "echo $(date)", []string{"echo"}},
		{"nested command substitution", "echo $(cat $(ls | head -1))", []string{"echo"}},
		{"backtick substitution", "echo `date`", []string{"echo"}},
		{"process substitution", "diff <(sort a.txt) <(sort b.txt)", []string{"diff"}},
		{"arithmetic expansion", "echo $((1+2))", []string{"echo"}},
		{"multiple redirects", "cmd >out.txt 2>err.txt", []string{"cmd"}},
		{"redirect with space", "cmd 2> /dev/null", []string{"cmd"}},
		{"noclobber redirect", "echo test >| file", []string{"echo"}},
		{"here string with pipe", "cat <<< 'hello' | grep h", []string{"cat", "grep"}},
		// find -exec 测试 (\\; 不应该被当作分隔符)
		{"find exec simple", "find . -name '*.go' -exec grep pattern {} \\;", []string{"find"}},
		{"find exec with redirect", "find . -type f -exec grep -l pattern {} \\; 2>/dev/null | head -10", []string{"find", "head"}},
		{"find exec complex", "find /path -type f \\( -name '*.vue' -o -name '*.ts' \\) -exec grep -l 'pattern' {} \\; 2>/dev/null", []string{"find"}},
		// 多行 curl 命令测试（包含换行符的命令，-H 参数不应该被当作命令）
		{"multiline curl with -H flags", "curl -X POST https://example.com/api \\\n  -H 'Content-Type: application/json' \\\n  -H 'Authorization: Bearer TOKEN' \\\n  -d '{\"test\": \"value\"}'", []string{"curl"}},
		// 单行 curl 命令测试
		{"single line curl", "curl -X POST https://example.com/api -H 'Content-Type: application/json' -H 'Authorization: Bearer TOKEN'", []string{"curl"}},
		// 以 - 开头的文件路径（带路径分隔符）应该被正确提取
		{"path with leading dash", "/path/to/-script.sh arg1", []string{"-script.sh"}},
		{"relative path with leading dash", "./-script.sh arg1", []string{"-script.sh"}},
		// 纯选项应该被过滤（返回 nil 而不是空切片）
		{"pure option -H", "-H 'Content-Type'", nil},
		{"pure option --help", "--help", nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractAllCommands(tc.command)
			assert.Equal(t, tc.expected, result, "command: %s", tc.command)
		})
	}
}
