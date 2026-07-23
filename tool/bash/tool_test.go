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
	config.Timeout = 5 // Set a sufficiently long timeout

	// Select time-consuming commands based on the platform
	var cmdName string
	var cmdArgs []string
	if runtime.GOOS == "windows" {
		cmdName = "ping"
		cmdArgs = []string{"127.0.0.1", "-n", "10"}
	} else {
		cmdName = "sleep"
		cmdArgs = []string{"10"}
	}

	// Add to the allowlist to ensure it can be executed
	config.Allow = append(config.Allow, cmdName)

	bTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := bTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// Create a context that can be canceled
	ctx, cancel := context.WithCancel(context.Background())

	// Remove context in another goroutine
	go func() {
		time.Sleep(1 * time.Second) // Wait 1 second and then cancel
		cancel()
	}()

	// Execute time-consuming commands
	startTime := time.Now()

	paramsMap := map[string]interface{}{
		"command": cmdName,
		"args":    cmdArgs,
	}
	paramsBytes, _ := json.Marshal(paramsMap)
	params := string(paramsBytes)

	resultStr, err := toolInstance.InvokableRun(ctx, params)
	duration := time.Since(startTime)

	assert.NoError(t, err) // InvokableRun itself does not return an error but returns a JSON result

	// Check whether the results contain error messages (new format)
	assert.Contains(t, resultStr, "被中断")
	// The exit code should be non-zero (because it was canceled).
	assert.Contains(t, resultStr, "Exit Code:")

	// Ensure execution time is less than the command originally requires (10 seconds)
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

	// Test for invalid paths and verify that PowerShell error messages are not garbled
	// Use the full command string mode to trigger PowerShell execution
	paramsMap := map[string]interface{}{
		"command": "dir non_existent_path_123",
	}
	paramsBytes, _ := json.Marshal(paramsMap)

	resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
	assert.NoError(t, err)

	// Verification contains error information
	// PowerShell errors usually include "CategoryInfo" or "FullyQualifiedErrorId"
	// Or simply say "Cannot find path" / "Can't find path"

	t.Logf("Result: %s", resultStr)
}

func TestBashTool_BlockedArgsNotFalsePositive(t *testing.T) {
	config := DefaultConfig()
	bTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := bTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// Testing should not be mistaken for commands containing prohibited modes
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

			// The "Disabled Mode" error should not be included
			assert.NotContains(t, resultStr, "禁止模式", "命令 '%s' 不应该被误判为包含禁止模式", tc.command)

			t.Logf("Command: %s, Result: %s", tc.command, resultStr)
		})
	}
}

func TestBashTool_BlockedArgsTruePositive(t *testing.T) {
	config := DefaultConfig()
	// Use a denial mode for testing
	config.Mode = ModeDeny
	bTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := bTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// Test commands that should be blocked (in blacklist mode)
	// Choose different commands depending on the platform, since del is a Windows command
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

			// It should contain the "denied pattern" error
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

	// Test & Operator
	// In Windows PowerShell, it is replaced with;
	// Native support from Bash
	paramsMap := map[string]interface{}{
		"command": "echo hello && echo world",
	}
	paramsBytes, _ := json.Marshal(paramsMap)

	resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
	assert.NoError(t, err)

	// The validation output contains hello and world
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

	// Test grep commands containing regular expressions
	// This command format is similar to the user's provided format: grep -n "agentId\|getSessionsByAgent" file1 file2
	paramsMap := map[string]interface{}{
		"command": `grep -n "agentId\|getSessionsByAgent" tool.go tool_test.go`,
	}
	paramsBytes, _ := json.Marshal(paramsMap)

	resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
	assert.NoError(t, err)

	// Verify command execution successfully (grep is on the whitelist)
	// New format check: Exit Code: 0 indicates success
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

	// Use the current working directory instead of the hardcoded path
	wd, err := os.Getwd()
	assert.NoError(t, err)

	// Test grep commands containing relative paths
	paramsMap := map[string]interface{}{
		"command":  `grep -n "extractAllCommands\|InvokableRun" tool.go tool_test.go`,
		"work_dir": wd,
	}
	paramsBytes, _ := json.Marshal(paramsMap)

	resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
	assert.NoError(t, err)

	// Verify that the command execution was successful
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

	// Use the current working directory instead of the hardcoded path
	wd, err := os.Getwd()
	assert.NoError(t, err)

	// Test the cat command to read files in the current directory
	paramsMap := map[string]interface{}{
		"command":  `cat tool.go`,
		"work_dir": wd,
	}
	paramsBytes, _ := json.Marshal(paramsMap)

	resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
	assert.NoError(t, err)

	// Verify that the command execution was successful
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
		// Edge case testing
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
		// find -exec test (\\; should not be used as a separator)
		{"find exec simple", "find . -name '*.go' -exec grep pattern {} \\;", []string{"find"}},
		{"find exec with redirect", "find . -type f -exec grep -l pattern {} \\; 2>/dev/null | head -10", []string{"find", "head"}},
		{"find exec complex", "find /path -type f \\( -name '*.vue' -o -name '*.ts' \\) -exec grep -l 'pattern' {} \\; 2>/dev/null", []string{"find"}},
		// Multi-line curl command test (commands containing newlines; the -H parameter should not be treated as a command)
		{"multiline curl with -H flags", "curl -X POST https://example.com/api \\\n  -H 'Content-Type: application/json' \\\n  -H 'Authorization: Bearer TOKEN' \\\n  -d '{\"test\": \"value\"}'", []string{"curl"}},
		// Single-line curl command test
		{"single line curl", "curl -X POST https://example.com/api -H 'Content-Type: application/json' -H 'Authorization: Bearer TOKEN'", []string{"curl"}},
		// File paths starting with - (with path separators) should be correctly extracted
		{"path with leading dash", "/path/to/-script.sh arg1", []string{"-script.sh"}},
		{"relative path with leading dash", "./-script.sh arg1", []string{"-script.sh"}},
		// Pure options should be filtered (return nil instead of empty slices)
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
