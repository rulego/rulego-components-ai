// Package bash provides a shell execution tool for AI agents.
// It executes shell commands with security controls (whitelist/blacklist).
package bash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	aitool "github.com/rulego/rulego-components-ai/tool"
	"github.com/rulego/rulego-components-ai/tool/common"
	orderedmap "github.com/wk8/go-ordered-map/v2"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
)

const (
	// ToolName is the name of the bash tool.
	ToolName = "bash"

	// DefaultMaxOutputSize Default maximum output size (64KB)
	DefaultMaxOutputSize = 64 * 1024
)

// SecurityMode defines the security mode for command execution.
type SecurityMode string

const (
	// ModeAllow Mode: Only allows commands in the list (default)
	ModeAllow SecurityMode = "allow"
	// ModeDeny Reject Mode: Allows all commands that are not rejected from the list
	ModeDeny SecurityMode = "deny"
)

// Config holds bash tool configuration.
type Config struct {
	WorkDir       string       `json:"workDir" label:"工作目录" desc:"命令执行的默认工作目录"`
	Timeout       int          `json:"timeout" label:"超时时间" desc:"命令执行超时时间（秒）"`
	MaxOutputSize int          `json:"maxOutputSize" label:"最大输出" desc:"输出最大字节数，超过则截断（默认64KB）"`
	Mode          SecurityMode `json:"mode" label:"安全模式" desc:"安全模式：allow(只允许列表中的命令) 或 deny(允许所有，拒绝列表中的命令)" component:"{\"type\":\"select\",\"options\":[{\"label\":\"allow - 白名单模式\",\"value\":\"allow\"},{\"label\":\"deny - 黑名单模式\",\"value\":\"deny\"}]}"`
	Allow         []string     `json:"allow" label:"允许列表" desc:"允许执行的命令列表（allow 模式生效）"`
	Deny          []string     `json:"deny" label:"拒绝列表" desc:"拒绝执行的命令列表"`
	DenyArgs      []string     `json:"denyArgs" label:"拒绝参数" desc:"拒绝的参数模式"`
	ShellPath     string       `json:"shellPath" label:"Shell路径" desc:"指定使用的 Shell 路径（例如 bash.exe 的绝对路径）。如果为空，则自动检测。"`
}

// DefaultConfig returns the default configuration based on the current platform.
func DefaultConfig() Config {
	platform := GetPlatformConfig()
	return Config{
		Timeout:       60,
		MaxOutputSize: DefaultMaxOutputSize,
		Mode:          ModeAllow,
		Allow:         platform.DefaultAllow,
		Deny:          platform.DefaultDeny,
		DenyArgs:      platform.DefaultDenyArgs,
	}
}

type bashTool struct {
	config   Config
	platform PlatformConfig
}

// NewTool creates a new bash tool.
func NewTool(config Config) (tool.BaseTool, error) {
	platform := GetPlatformConfig()

	if config.ShellPath != "" {
		platform.ShellCommand = config.ShellPath
		lowerPath := strings.ToLower(config.ShellPath)
		if strings.Contains(lowerPath, "bash") || strings.Contains(lowerPath, "sh") {
			platform.ShellType = ShellTypeBash
			platform.ShellArgs = []string{"-c"}
		} else if strings.Contains(lowerPath, "powershell") || strings.Contains(lowerPath, "pwsh") {
			platform.ShellType = ShellTypePowerShell
			platform.ShellArgs = []string{
				"-NoProfile",
				"-NonInteractive",
				"-ExecutionPolicy", "Bypass",
				"-Command",
				"[Console]::OutputEncoding=[System.Text.Encoding]::UTF8;",
			}
		} else if strings.Contains(lowerPath, "cmd") {
			platform.ShellType = ShellTypeCMD
			platform.ShellArgs = []string{"/c"}
		} else {
			// By default, it is treated as sh
			platform.ShellType = ShellTypeSh
			platform.ShellArgs = []string{"-c"}
		}
	}

	if config.Timeout <= 0 {
		config.Timeout = DefaultConfig().Timeout
	}
	if config.MaxOutputSize <= 0 {
		config.MaxOutputSize = DefaultMaxOutputSize
	}
	if config.Mode == "" {
		config.Mode = ModeAllow
	}
	if config.Allow == nil {
		config.Allow = platform.DefaultAllow
	}
	if config.Deny == nil {
		config.Deny = platform.DefaultDeny
	}
	if config.DenyArgs == nil {
		config.DenyArgs = platform.DefaultDenyArgs
	}

	return &bashTool{
		config:   config,
		platform: platform,
	}, nil
}

// Info returns tool information.
func (t *bashTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	props := orderedmap.New[string, *jsonschema.Schema]()

	props.Set("command", &jsonschema.Schema{
		Type:        "string",
		Description: "要执行的 shell 命令。支持管道(|)、链式(&&, ||)、重定向(>)等 shell 语法。例如: 'ls -la', 'find . -name \"*.go\" | head -20'",
	})

	props.Set("args", &jsonschema.Schema{
		Type:        "array",
		Description: "命令参数列表（可选）。如果提供，将与 command 组合执行。大多数情况下直接在 command 中写完整命令即可。",
		Items: &jsonschema.Schema{
			Type: "string",
		},
	})

	props.Set("work_dir", &jsonschema.Schema{
		Type:        "string",
		Description: "工作目录（可选）。默认使用工具配置的工作目录。",
	})

	props.Set("timeout", &jsonschema.Schema{
		Type:        "integer",
		Description: fmt.Sprintf("超时时间（秒，可选）。默认使用配置值 %d 秒。对于长时间运行的命令可设置更大的值。", t.config.Timeout),
	})

	// Generate descriptions based on platform and shell type
	platformDesc := fmt.Sprintf("当前平台: %s, Shell类型: %s。", runtime.GOOS, t.platform.ShellType)
	switch t.platform.ShellType {
	case ShellTypeBash:
		platformDesc += "使用 Unix/Bash 风格命令: ls, cat, grep, find, cp, mv, rm, mkdir, curl -L 等。"
	case ShellTypePowerShell:
		platformDesc += "使用 PowerShell 风格命令: dir, type, findstr, where, copy, move, del, mkdir。网络工具请用 curl.exe 而非 curl。"
	case ShellTypeCMD:
		platformDesc += "使用 Windows CMD 风格命令: dir, type, findstr, where, copy, move, del, mkdir。"
	case ShellTypeSh:
		platformDesc += "使用 Unix/Shell 风格命令: ls, cat, grep, find, cp, mv, rm, mkdir。"
	}

	return &schema.ToolInfo{
		Name: ToolName,
		Desc: fmt.Sprintf("Shell 命令执行工具。执行 shell 命令并返回结果。%s 超时时间: %d秒。输出超过 %d 字节会被截断。",
			platformDesc, t.config.Timeout, t.config.MaxOutputSize),
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: props,
			Required:   []string{"command"},
		}),
	}, nil
}

// OperationParams holds operation parameters.
type OperationParams struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	WorkDir string   `json:"work_dir"`
	Timeout int      `json:"timeout"` // Timeout time (seconds), 0 means using the default configuration value
}

// InvokableRun executes the operation.
func (t *bashTool) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	var params OperationParams
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		return "", fmt.Errorf("解析参数失败: %w", err)
	}

	return t.executeShell(ctx, params)
}

// executeShell executes a shell command.
func (t *bashTool) executeShell(ctx context.Context, params OperationParams) (string, error) {
	if params.Command == "" {
		return common.NewError(common.ErrCodeInvalidParams, "command cannot be empty").Error(), nil
	}

	// Build a complete command string
	var fullCommand string
	if len(params.Args) > 0 {
		fullCommand = params.Command + " " + strings.Join(params.Args, " ")
	} else {
		fullCommand = params.Command
	}

	// Extract all commands from the command chain for security checks
	commands := extractAllCommands(fullCommand)

	// Security check: deny commands (always apply)
	for _, cmd := range commands {
		if common.ContainsIgnoreCase(t.config.Deny, cmd) {
			return common.NewErrorf(common.ErrCodePermissionDenied, "command '%s' is denied", cmd).Error(), nil
		}
	}

	// Security check: deny args/patterns in full command (always apply)
	for _, denied := range t.config.DenyArgs {
		if isBlockedPatternMatch(fullCommand, denied) {
			return common.NewErrorf(common.ErrCodePermissionDenied, "command contains denied pattern '%s'", denied).Error(), nil
		}
	}

	// Security check: based on mode
	switch t.config.Mode {
	case ModeAllow:
		// Allow mode: all commands must be in allow list
		for _, cmd := range commands {
			if len(t.config.Allow) > 0 && !common.ContainsIgnoreCase(t.config.Allow, cmd) {
				return common.NewErrorf(common.ErrCodePermissionDenied, "command '%s' not in allow list. Allowed: %v", cmd, t.config.Allow).Error(), nil
			}
		}
	case ModeDeny:
		// Deny mode: allow all commands except denied ones (already checked above)
		// No additional check needed
	}

	// Set timeout (use params timeout if specified, otherwise use config default)
	timeoutSeconds := t.config.Timeout
	if params.Timeout > 0 {
		timeoutSeconds = params.Timeout
	}
	// Timeout limit: not exceeding global MaxTimeout, default not exceeding 300 seconds
	maxAllowed := common.GetTimeoutConfig().MaxTimeout
	if maxAllowed <= 0 {
		maxAllowed = 300 * time.Second
	}
	timeoutDuration := time.Duration(timeoutSeconds) * time.Second
	if timeoutDuration > maxAllowed {
		timeoutDuration = maxAllowed
	}
	timeout := timeoutDuration
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create command using shell to support pipes, redirects, && etc.
	var cmd *exec.Cmd
	if len(params.Args) > 0 {
		// If there are separate ARGS, use the traditional method
		cmdPath, err := exec.LookPath(params.Command)
		if err != nil {
			return common.NewErrorf(common.ErrCodeFileNotFound, "command '%s' not found: %v", params.Command, err).Error(), nil
		}
		cmd = exec.CommandContext(ctx, cmdPath, params.Args...)
	} else {
		// Use shell to execute complete command strings, supporting pipelines, redirects, and more
		shellArgs := make([]string, len(t.platform.ShellArgs))
		copy(shellArgs, t.platform.ShellArgs)

		// PowerShell 5.1 does not support &&, replace it with; Compatible with AI-generated commands (lossy: ignores error codes and continues execution)
		if t.platform.ShellType == ShellTypePowerShell {
			if strings.Contains(fullCommand, " && ") {
				fullCommand = strings.ReplaceAll(fullCommand, " && ", "; ")
			}
			// The user command is spelled to the end of ShellArgs, following the [Console]:: prefix defined by platform.go
			lastIdx := len(shellArgs) - 1
			shellArgs[lastIdx] = shellArgs[lastIdx] + " " + fullCommand
		} else {
			shellArgs = append(shellArgs, fullCommand)
		}

		cmd = exec.CommandContext(ctx, t.platform.ShellCommand, shellArgs...)
	}

	// Set working directory (parsing order: LLM parameters → ctx injection → Configuration write-down)
	workDir := params.WorkDir
	if workDir == "" {
		// General workDir injection: The caller (such as the main agent and sub-agent) specifies the working directory via ctx,
		// Non-dependent configuration is written in a fixed way (the business layer converts projectId and other documents into paths and injects them; the base library does not recognize business fields)
		workDir = common.WorkDirFromCtx(ctx)
	}
	if workDir == "" {
		workDir = t.config.WorkDir
	}
	if workDir != "" {
		// Security verification: Clean up paths and refuse to include: The path
		workDir = filepath.Clean(workDir)
		if strings.Contains(workDir, "..") {
			return common.NewErrorf(common.ErrCodePathEscape, "work_dir contains path traversal: %s", params.WorkDir).Error(), nil
		}
		cmd.Dir = workDir
	}

	// Set process group properties (set Setpgid on Linux/macOS to kill the entire process group when timeout)
	setSysProcAttr(cmd)

	// When ctx times out or cancels, Cancel is triggered to kill the process group, and WaitDelay is used to recover residual IO
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			killProcessGroup(cmd.Process.Pid)
		}
		return os.ErrProcessDone
	}
	cmd.WaitDelay = 5 * time.Second

	// Set environment variables
	cmd.Env = os.Environ()
	// Windows: Set UTF-8 encoding
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env,
			"PYTHONIOENCODING=utf-8",
			"LANG=en_US.UTF-8",
			"LC_ALL=en_US.UTF-8",
		)
		// If using Git Bash, you also need to set up CHCP
		if t.platform.ShellType == ShellTypeBash {
			cmd.Env = append(cmd.Env, "MSYSTEM=UCRT64")
		}
	}

	// Capture output with size limit
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Start the process
	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动命令失败: %w", err)
	}

	// exec.CommandContext automatically triggers the Cancel kill process above when ctx timeout/cancel
	err := cmd.Wait()
	executionTime := time.Since(startTime).Milliseconds()

	// Three steps for output processing:
	//   1) UTF-8 Conversion (Existing Logic)
	//   2) L1 token cleanup chain (progress/ansi/redact/longline + never-worse guard, opt-out via # nofilter/# raw)
	//   3) Error detection: head+tail truncation (when the limit is over, disk is placed at workDir/.tool-output, output to the path)
	rawStdout := convertToUTF8(stdout.Bytes())
	rawStderr := convertToUTF8(stderr.Bytes())

	filteredStdout := rawStdout
	filteredStderr := rawStderr
	if shouldFilter(fullCommand) {
		pipe := newTokenPipeline()
		filteredStdout = pipe.run(rawStdout)
		filteredStderr = pipe.run(rawStderr)
	}

	// Folder: workDir/.tool-output(workDir prioritizes cmd.Dir, reverts config.WorkDir)
	outDir := ""
	if cmd.Dir != "" {
		outDir = cmd.Dir
	} else if t.config.WorkDir != "" {
		outDir = t.config.WorkDir
	}
	if outDir != "" {
		outDir = filepath.Join(outDir, ".tool-output")
	}

	stdoutStr, stdoutDropped := truncateWithDump(filteredStdout, t.config.MaxOutputSize, outDir)
	stderrStr, stderrDropped := truncateWithDump(filteredStderr, t.config.MaxOutputSize, outDir)
	stdoutRawSize := len(filteredStdout)
	stderrRawSize := len(filteredStderr)

	// Determine error message
	errorMsg := ""
	if err != nil {
		// Check for contextual errors (timeout or cancellation)
		if ctx.Err() == context.DeadlineExceeded {
			errorMsg = fmt.Sprintf("命令执行超时（%v）", timeout)
		} else if ctx.Err() == context.Canceled {
			errorMsg = "命令执行被中断"
		} else if _, ok := err.(*exec.ExitError); ok {
			// ExitError will be displayed later via the Exit Code, and no additional information is needed here
		} else {
			errorMsg = err.Error()
		}
	}

	// Build result as text format for LLM consumption
	var result strings.Builder

	// Header: execution info
	if cmd.Dir != "" {
		result.WriteString(fmt.Sprintf("Work Dir: %s\n", cmd.Dir))
	}
	result.WriteString(fmt.Sprintf("Duration: %dms\n", executionTime))

	// Exit status
	if err == nil {
		result.WriteString("Exit Code: 0\n")
	} else {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.WriteString(fmt.Sprintf("Exit Code: %d\n", exitErr.ExitCode()))
		} else {
			result.WriteString("Exit Code: -1\n")
		}
	}

	// Error info (if any)
	if errorMsg != "" {
		result.WriteString(fmt.Sprintf("Error: %s\n", errorMsg))
	}

	// Truncation warning: Judgment based on the actual byte after cleaning
	stdoutTruncated := stdoutDropped != ""
	stderrTruncated := stderrDropped != ""

	// Separator
	result.WriteString("---\n")

	// stdout
	if stdoutStr != "" {
		result.WriteString("stdout:\n")
		result.WriteString(stdoutStr)
		if stdoutTruncated {
			result.WriteString(fmt.Sprintf("\n[truncated, original size: %d bytes, full output dumped to: %s]", stdoutRawSize, stdoutDropped))
		}
		if !strings.HasSuffix(stdoutStr, "\n") {
			result.WriteString("\n")
		}
	}

	// stderr
	if stderrStr != "" {
		result.WriteString("stderr:\n")
		result.WriteString(stderrStr)
		if stderrTruncated {
			result.WriteString(fmt.Sprintf("\n[truncated, original size: %d bytes, full output dumped to: %s]", stderrRawSize, stderrDropped))
		}
		if !strings.HasSuffix(stderrStr, "\n") {
			result.WriteString("\n")
		}
	}

	return result.String(), nil
}

// truncateWithDump Use unified truncation service (misperception head+tail) to cut off output;
// If a truncation occurs and outDir is not null, the full original text is placed and the execution path is returned for the agent to retrieve.
// Returns (truncated content, order path/empty).
func truncateWithDump(text string, maxSize int, outDir string) (string, string) {
	if len(text) <= maxSize {
		return text, ""
	}
	maxLines := 0 // 0 means default (2000)
	maxBytes := maxSize
	tr := common.Truncate(text, common.TruncateOptions{
		MaxLines:  &maxLines,
		MaxBytes:  &maxBytes,
		Direction: common.TruncHeadTail,
	})
	if !tr.Truncated {
		return text, ""
	}
	dumped := ""
	if outDir != "" {
		if p, err := common.WriteToTruncationDir(text, outDir); err == nil {
			dumped = p
		}
	}
	return tr.Content, dumped
}

// convertToUTF8 detects and converts the output encoding to UTF-8
// Some commands on Windows may output UTF-16 or other encodings
func convertToUTF8(output []byte) string {
	if len(output) == 0 {
		return ""
	}

	// 1. Detecting BOM (Strongest Signal)
	if len(output) >= 2 {
		if output[0] == 0xFF && output[1] == 0xFE { // UTF-16 LE BOM
			decoder := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder()
			if converted, err := decoder.Bytes(output); err == nil {
				return string(converted)
			}
		}
		if output[0] == 0xFE && output[1] == 0xFF { // UTF-16 BE BOM
			decoder := unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewDecoder()
			if converted, err := decoder.Bytes(output); err == nil {
				return string(converted)
			}
		}
	}

	// 2. Statistical detection of UTF-16 (LE/BE) and offsets
	// Windows/WSL sometimes outputs UTF-16 streams with garbage prefixes, or UTF-16 without BOMs
	// We examine the offsets 0 and 1 to find the pattern of null bytes
	// UTF-16 LE: Char 00
	// UTF-16 BE: 00 Char

	bestScore := 0
	bestEnc := "" // "LE0", "BE0", "LE1", "BE1"
	sampleLen := 200
	if len(output) < sampleLen {
		sampleLen = len(output)
	}

	// Calculate the scores for four combinations (number of null bytes)
	// 0 offset
	scoreLE0, scoreBE0 := countNulls(output, 0, sampleLen)
	// 1. Offset
	scoreLE1, scoreBE1 := countNulls(output, 1, sampleLen)

	// Only when the null byte ratio exceeds a certain threshold (for example, 30%) is it considered UTF-16
	// Normal UTF-8 text contains very few null bytes
	threshold := sampleLen / 2 * 30 / 100

	if scoreLE0 > threshold && scoreLE0 >= bestScore {
		bestScore = scoreLE0
		bestEnc = "LE0"
	}
	if scoreBE0 > threshold && scoreBE0 >= bestScore {
		bestScore = scoreBE0
		bestEnc = "BE0"
	}
	if scoreLE1 > threshold && scoreLE1 >= bestScore {
		bestScore = scoreLE1
		bestEnc = "LE1"
	}
	if scoreBE1 > threshold && scoreBE1 >= bestScore {
		bestScore = scoreBE1
		bestEnc = "BE1"
	}

	if bestEnc != "" {
		var decoder *encoding.Decoder
		var input []byte

		switch bestEnc {
		case "LE0":
			decoder = unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
			input = output
		case "BE0":
			decoder = unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder()
			input = output
		case "LE1":
			decoder = unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
			if len(output) > 1 {
				input = output[1:]
			}
		case "BE1":
			decoder = unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder()
			if len(output) > 1 {
				input = output[1:]
			}
		}

		if decoder != nil && len(input) > 0 {
			if converted, err := decoder.Bytes(input); err == nil {
				// After successful conversion, you also need to filter out any remaining garbled characters (such as U+FFFD).
				return strings.ToValidUTF8(string(converted), "�")
			}
		}
	}

	// 3. Try to be UTF-8
	// Even with utf8.Valid returns true. If it had detected UTF-16 earlier and decoded successfully, it wouldn't have ended here
	if utf8.Valid(output) {
		return string(output)
	}

	// 4. Cannot be recognized, returns the replaced UTF-8
	return strings.ToValidUTF8(string(output), "�")
}

// countNulls Counts the number of null bytes in both even and odd bits
// startOffset: 0 or 1
// return (oddNulls, evenNulls) relative to the pair start
// For LE (Char 00), null is at the high level (index+1), i.e., relative odd
// For BE (00 Char), null is at the low level (index), i.e., relative even
func countNulls(data []byte, startOffset int, limit int) (leScore, beScore int) {
	if startOffset >= len(data) {
		return 0, 0
	}
	end := startOffset + limit
	if end > len(data) {
		end = len(data)
	}

	// Ensure the full 2-byte pair is handled
	limitLen := end - startOffset
	pairs := limitLen / 2

	for i := 0; i < pairs; i++ {
		base := startOffset + i*2
		b0 := data[base]
		b1 := data[base+1]

		// LE: Char 00 -> b1 is 0
		if b1 == 0x00 {
			leScore++
		}
		// BE: 00 Char -> b0 is 0
		if b0 == 0x00 {
			beScore++
		}
	}
	return
}

// isBlockedPatternMatch checks whether the command contains a prohibited pattern
// Use more precise matching rules to avoid accidental hits
func isBlockedPatternMatch(fullCommand, blocked string) bool {
	// For Windows CMD-style parameters (with
	// Only matches cases that occur as independent parameters
	if strings.HasPrefix(blocked, "/") && len(blocked) <= 3 {
		return isIndependentArg(fullCommand, blocked)
	}

	// For other patterns, include matching but require word boundaries
	// For example, "-rf /" should match "rm -rf /" but should not match "rm -rf /home/user"
	if strings.Contains(blocked, " ") {
		// Mode with spaces, check if it is within the parameter boundary
		return strings.Contains(fullCommand, blocked) && isAtWordBoundary(fullCommand, blocked)
	}

	// For other short modes (such as /dev/sd), use inclusion matching
	return strings.Contains(fullCommand, blocked)
}

// isIndependentArg checks whether the mode appears as an independent parameter
func isIndependentArg(fullCommand, arg string) bool {
	idx := strings.Index(fullCommand, arg)
	for idx != -1 {
		// Check if the front is a space or a command beginning
		beforeOK := idx == 0 || fullCommand[idx-1] == ' ' || fullCommand[idx-1] == '\t'

		// Check if the next sign is a space, command termination, or parameter terminator
		afterIdx := idx + len(arg)
		afterOK := afterIdx >= len(fullCommand) ||
			fullCommand[afterIdx] == ' ' ||
			fullCommand[afterIdx] == '\t' ||
			fullCommand[afterIdx] == '\n' ||
			fullCommand[afterIdx] == '\r' ||
			fullCommand[afterIdx] == ';' ||
			fullCommand[afterIdx] == '&' ||
			fullCommand[afterIdx] == '|'

		if beforeOK && afterOK {
			return true
		}

		// Keep searching for the next match
		nextIdx := strings.Index(fullCommand[idx+1:], arg)
		if nextIdx != -1 {
			idx = idx + 1 + nextIdx
		} else {
			idx = -1
		}
	}
	return false
}

// isAtWordBoundary checks whether the pattern is at the word boundary
func isAtWordBoundary(fullCommand, pattern string) bool {
	idx := strings.Index(fullCommand, pattern)
	for idx != -1 {
		// Check if the mode starts with a space or command
		beforeOK := idx == 0 || fullCommand[idx-1] == ' ' || fullCommand[idx-1] == '\t'

		// Check if the mode is a space, command termination, or path separator
		afterIdx := idx + len(pattern)
		afterOK := afterIdx >= len(fullCommand) ||
			fullCommand[afterIdx] == ' ' ||
			fullCommand[afterIdx] == '\t' ||
			fullCommand[afterIdx] == '\n' ||
			fullCommand[afterIdx] == '\r' ||
			fullCommand[afterIdx] == ';' ||
			fullCommand[afterIdx] == '&' ||
			fullCommand[afterIdx] == '|' ||
			fullCommand[afterIdx] == '"' ||
			fullCommand[afterIdx] == '\''

		if beforeOK && afterOK {
			return true
		}

		// Keep searching for the next match
		nextIdx := strings.Index(fullCommand[idx+1:], pattern)
		if nextIdx != -1 {
			idx = idx + 1 + nextIdx
		} else {
			idx = -1
		}
	}
	return false
}

// extractAllCommands extracts all command names from the command string
// Support: |, &&, ||,;, $() and other separators
func extractAllCommands(cmd string) []string {
	cmd = strings.TrimSpace(cmd)

	// Removed common shell prefixes
	for _, prefix := range []string{"sudo ", "su -c ", "bash -c ", "sh -c "} {
		if strings.HasPrefix(cmd, prefix) {
			cmd = strings.TrimSpace(cmd[len(prefix):])
			// If it is wrapped in quotation marks, extract the content
			if len(cmd) >= 2 && (cmd[0] == '\'' || cmd[0] == '"') {
				quote := cmd[0]
				if end := strings.LastIndexByte(cmd, quote); end > 0 {
					cmd = cmd[1:end]
				}
			}
		}
	}

	var commands []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)
	inSubshell := 0 // Track $() nesting

	for i := 0; i < len(cmd); i++ {
		c := cmd[i]

		// Handle quotation marks
		if (c == '\'' || c == '"') && inSubshell == 0 {
			if !inQuote {
				inQuote = true
				quoteChar = c
			} else if c == quoteChar {
				inQuote = false
				quoteChar = 0
			}
			current.WriteByte(c)
			continue
		}

		// Handle the $() subshell
		if c == '$' && i+1 < len(cmd) && cmd[i+1] == '(' && !inQuote {
			inSubshell++
			current.WriteString("$(")
			i++
			continue
		}
		if c == ')' && inSubshell > 0 && !inQuote {
			inSubshell--
			current.WriteByte(c)
			continue
		}

		// If in quotes or subshells, add characters directly
		if inQuote || inSubshell > 0 {
			current.WriteByte(c)
			continue
		}

		// Check the separator
		isSeparator := false
		separatorLen := 0

		switch {
		case c == '|':
			// The check is || Or |
			if i+1 < len(cmd) && cmd[i+1] == '|' {
				separatorLen = 2
				isSeparator = true
			} else if isRedirectionPipe(cmd, i) {
				// | is part of a redirect (e.g., >|), not a pipe delimiter
				current.WriteByte(c)
				continue
			} else {
				separatorLen = 1
				isSeparator = true
			}
		case c == '&':
			// Check whether it's && or &
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				separatorLen = 2
				isSeparator = true
			} else if isRedirectionAmpersand(cmd, i) {
				// & is part of a redirect (e.g., 2>&1), not a separator
				current.WriteByte(c)
				continue
			} else {
				// Single & is run in the background and also serves as a separator
				separatorLen = 1
				isSeparator = true
			}
		case c == ';':
			// Check if it is the \ of find -exec; termination symbol
			// \; It should not be used as a separator
			if i > 0 && cmd[i-1] == '\\' {
				current.WriteByte(c)
				continue
			}
			separatorLen = 1
			isSeparator = true
		case c == '\n':
			// Check if it is a command continuation character (\ followed by a line break)
			// If the previous character is \, then this is a continuer, not a separator
			if i > 0 && cmd[i-1] == '\\' {
				// Remove the previously added \ and continue characters should not be part of the command
				currentStr := current.String()
				if len(currentStr) > 0 && currentStr[len(currentStr)-1] == '\\' {
					current.Reset()
					current.WriteString(currentStr[:len(currentStr)-1])
				}
				continue
			}
			separatorLen = 1
			isSeparator = true
		}

		if isSeparator {
			// Extract the current command
			if cmdStr := strings.TrimSpace(current.String()); cmdStr != "" {
				if firstCmd := extractFirstCommand(cmdStr); firstCmd != "" {
					commands = append(commands, firstCmd)
				}
			}
			current.Reset()
			i += separatorLen - 1 // -1 because the loop will increase +1
		} else {
			current.WriteByte(c)
		}
	}

	// Add the last command
	if cmdStr := strings.TrimSpace(current.String()); cmdStr != "" {
		if firstCmd := extractFirstCommand(cmdStr); firstCmd != "" {
			commands = append(commands, firstCmd)
		}
	}

	return commands
}

// isRedirectionAmpersand checks whether & are part of a redirect (e.g., 2>&1, <&0, >&2)
// Redirect mode: N>&M, N<&M, >&M, <&M, &>file (bash)
func isRedirectionAmpersand(cmd string, pos int) bool {
	if pos <= 0 {
		return false
	}

	// Check the previous character
	prev := cmd[pos-1]

	// Pattern 1: >& or <& (e.g., 2>&1, <&0)
	if prev == '>' || prev == '<' {
		return true
	}

	// Mode 2: &> (Bash's &>file redirect)
	// Check if the > is following behind
	if pos+1 < len(cmd) && cmd[pos+1] == '>' {
		// Make sure it's not &&> (this should be && then >)
		if pos > 0 && cmd[pos-1] == '&' {
			return false // This is &>
		}
		return true
	}

	return false
}

// isRedirectionPipe checks | Is it part of a redirect (e.g., >|)
// Redirect mode: >| (bash noclobber forced override)
func isRedirectionPipe(cmd string, pos int) bool {
	if pos <= 0 {
		return false
	}

	// Check if the previous character is >
	// >| It is a noclobber redirect for bash forced override
	prev := cmd[pos-1]
	return prev == '>'
}

// extractFirstCommand extracts command names from a single command string
func extractFirstCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)

	// Filter pure option parameters starting with - (these are command options, not command names)
	// For example: -H 'Content-Type' should be filtered
	// However, files with paths are not filtered, such as /path/to/-script or./-script.sh
	if strings.HasPrefix(cmd, "-") && !strings.Contains(cmd, "/") && !strings.Contains(cmd, "\\") {
		return ""
	}

	// Handle commands with quotes
	if strings.HasPrefix(cmd, "'") || strings.HasPrefix(cmd, "\"") {
		quote := cmd[0]
		if end := strings.IndexByte(cmd[1:], quote); end > 0 {
			cmd = cmd[1 : end+1]
		}
	}

	// Extract the part before the first space or special character
	if idx := strings.IndexAny(cmd, " \t|&;<>()"); idx > 0 {
		cmd = cmd[:idx]
	}

	// Handle the path, and only take the command name
	if strings.Contains(cmd, "/") || strings.Contains(cmd, "\\") {
		parts := strings.FieldsFunc(cmd, func(r rune) bool {
			return r == '/' || r == '\\'
		})
		if len(parts) > 0 {
			cmd = parts[len(parts)-1]
		}
	}

	// Handles.exe suffix on Windows
	if runtime.GOOS == "windows" {
		cmd = strings.TrimSuffix(cmd, ".exe")
	}

	return strings.ToLower(cmd)
}

// Register registers the bash tool with custom configuration.
func Register(config Config) error {
	t, err := NewTool(config)
	if err != nil {
		return err
	}
	return aitool.Registry.Register(t)
}

// RegisterDefault registers with default configuration using simplified template.
func RegisterDefault() error {
	return aitool.RegisterTool(ToolName, fmt.Sprintf("Shell 命令执行工具 - 执行 shell 命令。平台: %s", runtime.GOOS), DefaultConfig(), NewTool)
}

func init() {
	_ = RegisterDefault()
}
