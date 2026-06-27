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

	// DefaultMaxOutputSize 默认最大输出大小 (64KB)
	DefaultMaxOutputSize = 64 * 1024
)

// SecurityMode defines the security mode for command execution.
type SecurityMode string

const (
	// ModeAllow 允许模式：只允许列表中的命令（默认）
	ModeAllow SecurityMode = "allow"
	// ModeDeny 拒绝模式：允许所有非拒绝列表的命令
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
			// 默认当作 sh 处理
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

	// 根据平台和 shell 类型生成描述
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
	Timeout int      `json:"timeout"` // 超时时间（秒），0 表示使用配置默认值
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

	// 构建完整命令字符串
	var fullCommand string
	if len(params.Args) > 0 {
		fullCommand = params.Command + " " + strings.Join(params.Args, " ")
	} else {
		fullCommand = params.Command
	}

	// 提取命令链中的所有命令进行安全检查
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
	// 超时上限：不超过全局 MaxTimeout，默认不超过 300 秒
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
		// 如果有单独的 args，使用传统方式
		cmdPath, err := exec.LookPath(params.Command)
		if err != nil {
			return common.NewErrorf(common.ErrCodeFileNotFound, "command '%s' not found: %v", params.Command, err).Error(), nil
		}
		cmd = exec.CommandContext(ctx, cmdPath, params.Args...)
	} else {
		// 使用 shell 执行完整命令字符串，支持管道、重定向等
		shellArgs := make([]string, len(t.platform.ShellArgs))
		copy(shellArgs, t.platform.ShellArgs)

		// PowerShell 5.1 不支持 &&，替换为 ; 以兼容 AI 常生成的命令（有损：忽略错误码继续执行）
		if t.platform.ShellType == ShellTypePowerShell {
			if strings.Contains(fullCommand, " && ") {
				fullCommand = strings.ReplaceAll(fullCommand, " && ", "; ")
			}
			// 用户命令拼到 ShellArgs 末尾，接在 platform.go 定义的 [Console]:: 前缀之后
			lastIdx := len(shellArgs) - 1
			shellArgs[lastIdx] = shellArgs[lastIdx] + " " + fullCommand
		} else {
			shellArgs = append(shellArgs, fullCommand)
		}

		cmd = exec.CommandContext(ctx, t.platform.ShellCommand, shellArgs...)
	}

	// Set working directory
	workDir := params.WorkDir
	if workDir == "" {
		workDir = t.config.WorkDir
	}
	if workDir != "" {
		// 安全校验：清理路径并拒绝包含 .. 的路径
		workDir = filepath.Clean(workDir)
		if strings.Contains(workDir, "..") {
			return common.NewErrorf(common.ErrCodePathEscape, "work_dir contains path traversal: %s", params.WorkDir).Error(), nil
		}
		cmd.Dir = workDir
	}

	// 设置进程组属性（Linux/macOS 下设置 Setpgid，用于超时时 kill 整个进程组）
	setSysProcAttr(cmd)

	// ctx 超时/取消时触发 Cancel 杀进程组，WaitDelay 兜底回收残留 IO
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			killProcessGroup(cmd.Process.Pid)
		}
		return os.ErrProcessDone
	}
	cmd.WaitDelay = 5 * time.Second

	// Set environment variables
	cmd.Env = os.Environ()
	// Windows: 设置 UTF-8 编码
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env,
			"PYTHONIOENCODING=utf-8",
			"LANG=en_US.UTF-8",
			"LC_ALL=en_US.UTF-8",
		)
		// 如果使用 Git Bash，还需要设置 CHCP
		if t.platform.ShellType == ShellTypeBash {
			cmd.Env = append(cmd.Env, "MSYSTEM=UCRT64")
		}
	}

	// Capture output with size limit
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// 启动进程
	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动命令失败: %w", err)
	}

	// exec.CommandContext 在 ctx 超时/取消时自动触发上面的 Cancel 杀进程
	err := cmd.Wait()
	executionTime := time.Since(startTime).Milliseconds()

	// Convert encoding and truncate output if needed
	stdoutStr := truncateOutput(convertToUTF8(stdout.Bytes()), t.config.MaxOutputSize)
	stderrStr := truncateOutput(convertToUTF8(stderr.Bytes()), t.config.MaxOutputSize)

	// Determine error message
	errorMsg := ""
	if err != nil {
		// 检查上下文错误（超时或取消）
		if ctx.Err() == context.DeadlineExceeded {
			errorMsg = fmt.Sprintf("命令执行超时（%v）", timeout)
		} else if ctx.Err() == context.Canceled {
			errorMsg = "命令执行被中断"
		} else if _, ok := err.(*exec.ExitError); ok {
			// ExitError 会在后面通过 Exit Code 显示，这里不需要额外信息
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

	// Truncation warning
	stdoutTruncated := len(stdout.String()) > t.config.MaxOutputSize
	stderrTruncated := len(stderr.String()) > t.config.MaxOutputSize

	// Separator
	result.WriteString("---\n")

	// stdout
	if stdoutStr != "" {
		result.WriteString("stdout:\n")
		result.WriteString(stdoutStr)
		if stdoutTruncated {
			result.WriteString(fmt.Sprintf("\n[truncated, original size: %d bytes]", len(stdout.String())))
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
			result.WriteString(fmt.Sprintf("\n[truncated, original size: %d bytes]", len(stderr.String())))
		}
		if !strings.HasSuffix(stderrStr, "\n") {
			result.WriteString("\n")
		}
	}

	return result.String(), nil
}

// truncateOutput 截断输出到指定大小
func truncateOutput(output string, maxSize int) string {
	if len(output) <= maxSize {
		return output
	}
	// 截断并添加提示
	truncateMsg := fmt.Sprintf("\n... [输出已截断，原始大小: %d 字节]", len(output))
	return output[:maxSize-len(truncateMsg)] + truncateMsg
}

// convertToUTF8 检测并转换输出编码为 UTF-8
// Windows 上某些命令可能输出 UTF-16 或其他编码
func convertToUTF8(output []byte) string {
	if len(output) == 0 {
		return ""
	}

	// 1. 检测 BOM (Strongest signal)
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

	// 2. 统计检测 UTF-16 (LE/BE) 及偏移量
	// Windows/WSL 有时会输出带垃圾前缀的 UTF-16 流，或者无 BOM 的 UTF-16
	// 我们检查 0 和 1 两个偏移量，寻找 null 字节的模式
	// UTF-16 LE: Char 00
	// UTF-16 BE: 00 Char

	bestScore := 0
	bestEnc := "" // "LE0", "BE0", "LE1", "BE1"
	sampleLen := 200
	if len(output) < sampleLen {
		sampleLen = len(output)
	}

	// 计算四种组合的得分 (null 字节的数量)
	// 0 偏移
	scoreLE0, scoreBE0 := countNulls(output, 0, sampleLen)
	// 1 偏移
	scoreLE1, scoreBE1 := countNulls(output, 1, sampleLen)

	// 只有当 null 字节占比超过一定阈值 (例如 30%) 时才认为是 UTF-16
	// 正常 UTF-8 文本中 null 字节极少
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
				// 转换成功后，还需要过滤掉可能的残留乱码字符 (如 U+FFFD)
				return strings.ToValidUTF8(string(converted), "�")
			}
		}
	}

	// 3. 尝试作为 UTF-8
	// 即使 utf8.Valid 返回 true，如果前面检测出是 UTF-16 且解码成功，就不会走到这里
	if utf8.Valid(output) {
		return string(output)
	}

	// 4. 无法识别，返回替换后的 UTF-8
	return strings.ToValidUTF8(string(output), "�")
}

// countNulls 统计偶数位和奇数位的 null 字节数
// startOffset: 0 或 1
// return (oddNulls, evenNulls) relative to the pair start
// 对于 LE (Char 00)，null 在高位 (index+1)，即 relative odd
// 对于 BE (00 Char)，null 在低位 (index)，即 relative even
func countNulls(data []byte, startOffset int, limit int) (leScore, beScore int) {
	if startOffset >= len(data) {
		return 0, 0
	}
	end := startOffset + limit
	if end > len(data) {
		end = len(data)
	}

	// 确保处理完整的 2 字节对
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

// isBlockedPatternMatch 检查命令是否包含禁止模式
// 使用更精确的匹配规则，避免误伤
func isBlockedPatternMatch(fullCommand, blocked string) bool {
	// 对于 Windows CMD 风格的参数（以 / 开头的短参数，如 /s, /q, /f）
	// 只匹配作为独立参数出现的情况
	if strings.HasPrefix(blocked, "/") && len(blocked) <= 3 {
		return isIndependentArg(fullCommand, blocked)
	}

	// 对于其他模式，使用包含匹配，但要求在单词边界
	// 例如 "-rf /" 应该匹配 "rm -rf /" 但不应该匹配 "rm -rf /home/user"
	if strings.Contains(blocked, " ") {
		// 带空格的模式，检查是否在参数边界
		return strings.Contains(fullCommand, blocked) && isAtWordBoundary(fullCommand, blocked)
	}

	// 对于其他短模式（如 /dev/sd），使用包含匹配
	return strings.Contains(fullCommand, blocked)
}

// isIndependentArg 检查模式是否作为独立参数出现
func isIndependentArg(fullCommand, arg string) bool {
	idx := strings.Index(fullCommand, arg)
	for idx != -1 {
		// 检查前面是否是空格或命令开头
		beforeOK := idx == 0 || fullCommand[idx-1] == ' ' || fullCommand[idx-1] == '\t'

		// 检查后面是否是空格、命令结束或参数结束符
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

		// 继续搜索下一个匹配
		nextIdx := strings.Index(fullCommand[idx+1:], arg)
		if nextIdx != -1 {
			idx = idx + 1 + nextIdx
		} else {
			idx = -1
		}
	}
	return false
}

// isAtWordBoundary 检查模式是否在单词边界
func isAtWordBoundary(fullCommand, pattern string) bool {
	idx := strings.Index(fullCommand, pattern)
	for idx != -1 {
		// 检查模式前是否是空格或命令开头
		beforeOK := idx == 0 || fullCommand[idx-1] == ' ' || fullCommand[idx-1] == '\t'

		// 检查模式后是否是空格、命令结束或路径分隔符
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

		// 继续搜索下一个匹配
		nextIdx := strings.Index(fullCommand[idx+1:], pattern)
		if nextIdx != -1 {
			idx = idx + 1 + nextIdx
		} else {
			idx = -1
		}
	}
	return false
}

// extractAllCommands 从命令字符串中提取所有命令名
// 支持: |, &&, ||, ;, $() 等分隔符
func extractAllCommands(cmd string) []string {
	cmd = strings.TrimSpace(cmd)

	// 移除常见的 shell 前缀
	for _, prefix := range []string{"sudo ", "su -c ", "bash -c ", "sh -c "} {
		if strings.HasPrefix(cmd, prefix) {
			cmd = strings.TrimSpace(cmd[len(prefix):])
			// 如果是引号包裹的，提取内容
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
	inSubshell := 0 // 跟踪 $() 嵌套

	for i := 0; i < len(cmd); i++ {
		c := cmd[i]

		// 处理引号
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

		// 处理 $() 子 shell
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

		// 如果在引号或子 shell 中，直接添加字符
		if inQuote || inSubshell > 0 {
			current.WriteByte(c)
			continue
		}

		// 检查分隔符
		isSeparator := false
		separatorLen := 0

		switch {
		case c == '|':
			// 检查是 || 还是 |
			if i+1 < len(cmd) && cmd[i+1] == '|' {
				separatorLen = 2
				isSeparator = true
			} else if isRedirectionPipe(cmd, i) {
				// | 是重定向的一部分 (如 >|)，不是管道分隔符
				current.WriteByte(c)
				continue
			} else {
				separatorLen = 1
				isSeparator = true
			}
		case c == '&':
			// 检查是 && 还是 &
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				separatorLen = 2
				isSeparator = true
			} else if isRedirectionAmpersand(cmd, i) {
				// & 是重定向的一部分 (如 2>&1)，不是分隔符
				current.WriteByte(c)
				continue
			} else {
				// 单个 & 是后台运行，也作为分隔符
				separatorLen = 1
				isSeparator = true
			}
		case c == ';':
			// 检查是否是 find -exec 的 \; 终止符
			// \; 不应该被当作分隔符
			if i > 0 && cmd[i-1] == '\\' {
				current.WriteByte(c)
				continue
			}
			separatorLen = 1
			isSeparator = true
		case c == '\n':
			// 检查是否是命令续行符（\ 后跟换行）
			// 如果前一个字符是 \，则这是续行符，不是分隔符
			if i > 0 && cmd[i-1] == '\\' {
				// 移除之前添加的 \，续行符不应该是命令的一部分
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
			// 提取当前命令
			if cmdStr := strings.TrimSpace(current.String()); cmdStr != "" {
				if firstCmd := extractFirstCommand(cmdStr); firstCmd != "" {
					commands = append(commands, firstCmd)
				}
			}
			current.Reset()
			i += separatorLen - 1 // -1 因为循环会 +1
		} else {
			current.WriteByte(c)
		}
	}

	// 添加最后一个命令
	if cmdStr := strings.TrimSpace(current.String()); cmdStr != "" {
		if firstCmd := extractFirstCommand(cmdStr); firstCmd != "" {
			commands = append(commands, firstCmd)
		}
	}

	return commands
}

// isRedirectionAmpersand 检查 & 是否是重定向的一部分 (如 2>&1, <&0, >&2)
// 重定向模式: N>&M, N<&M, >&M, <&M, &>file (bash)
func isRedirectionAmpersand(cmd string, pos int) bool {
	if pos <= 0 {
		return false
	}

	// 检查前一个字符
	prev := cmd[pos-1]

	// 模式 1: >& 或 <& (如 2>&1, <&0)
	if prev == '>' || prev == '<' {
		return true
	}

	// 模式 2: &> (bash 的 &>file 重定向)
	// 检查 & 后面是否跟着 >
	if pos+1 < len(cmd) && cmd[pos+1] == '>' {
		// 确保不是 &&> (这应该是 && 然后 >)
		if pos > 0 && cmd[pos-1] == '&' {
			return false // 这是 &&>
		}
		return true
	}

	return false
}

// isRedirectionPipe 检查 | 是否是重定向的一部分 (如 >|)
// 重定向模式: >| (bash noclobber 强制覆盖)
func isRedirectionPipe(cmd string, pos int) bool {
	if pos <= 0 {
		return false
	}

	// 检查前一个字符是否是 >
	// >| 是 bash 的 noclobber 强制覆盖重定向
	prev := cmd[pos-1]
	return prev == '>'
}

// extractFirstCommand 从单个命令字符串中提取命令名
func extractFirstCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)

	// 过滤以 - 开头的纯选项参数（它们是命令选项，不是命令名）
	// 例如: -H 'Content-Type' 应该被过滤掉
	// 但不过滤带路径的文件，如 /path/to/-script 或 ./-script.sh
	if strings.HasPrefix(cmd, "-") && !strings.Contains(cmd, "/") && !strings.Contains(cmd, "\\") {
		return ""
	}

	// 处理带引号的命令
	if strings.HasPrefix(cmd, "'") || strings.HasPrefix(cmd, "\"") {
		quote := cmd[0]
		if end := strings.IndexByte(cmd[1:], quote); end > 0 {
			cmd = cmd[1 : end+1]
		}
	}

	// 提取第一个空格或特殊字符前的部分
	if idx := strings.IndexAny(cmd, " \t|&;<>()"); idx > 0 {
		cmd = cmd[:idx]
	}

	// 处理路径，只取命令名
	if strings.Contains(cmd, "/") || strings.Contains(cmd, "\\") {
		parts := strings.FieldsFunc(cmd, func(r rune) bool {
			return r == '/' || r == '\\'
		})
		if len(parts) > 0 {
			cmd = parts[len(parts)-1]
		}
	}

	// Windows 下处理 .exe 后缀
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
