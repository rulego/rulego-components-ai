// Package edit provides a unified editing tool for AI agents.
// It supports line-level editing, search-replace, and patch operations.
package edit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	aitool "github.com/rulego/rulego-components-ai/tool"
	"github.com/rulego/rulego-components-ai/tool/common"
	"github.com/rulego/rulego-components-ai/session"
	"github.com/rulego/rulego/utils/maps"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

const (
	// ToolName is the name of the edit tool.
	ToolName = "edit"

	// MaxRegexLength limits regex pattern length to prevent ReDoS attacks.
	MaxRegexLength = 1000
)

// Config holds edit tool configuration.
type Config struct {
	WorkDir    string `json:"workDir" label:"工作目录" desc:"文件操作的默认工作目录"`
	MaxHistory int    `json:"maxHistory" label:"最大历史数" desc:"保留的最大历史版本数"`
}

// DefaultConfig returns default configuration.
func DefaultConfig() Config {
	return Config{
		WorkDir:    "",
		MaxHistory: 10,
	}
}

type editTool struct {
	config   Config
	resolver *common.SecurePathResolver
	backup   *common.BackupManager
}

// editPathSecurity 编辑操作的路径安全策略：禁止隐藏文件、排除版本库元数据目录
func editPathSecurity() common.PathSecurityConfig {
	cfg := common.DefaultPathSecurityConfig()
	cfg.ExcludeDirs = []string{".git", ".svn", ".hg"}
	return cfg
}

// NewTool creates a new edit tool.
func NewTool(config Config) (tool.BaseTool, error) {
	if config.MaxHistory <= 0 {
		config.MaxHistory = DefaultConfig().MaxHistory
	}

	resolver, err := common.NewSecurePathResolver(config.WorkDir, editPathSecurity())
	if err != nil {
		return nil, err
	}
	config.WorkDir = resolver.Workspace()

	return &editTool{
		config:   config,
		resolver: resolver,
		backup:   common.NewBackupManager(config.WorkDir, config.MaxHistory),
	}, nil
}

// Info returns tool information.
func (t *editTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	props := orderedmap.New[string, *jsonschema.Schema]()

	props.Set("operation", &jsonschema.Schema{
		Type:        "string",
		Description: "Operation type: line (edit line), search (search-replace), insert (insert content), delete (delete lines), restore (restore backup), list_backups (list backups)",
		Enum:        []any{"line", "search", "insert", "delete", "restore", "list_backups"},
	})

	props.Set("path", &jsonschema.Schema{
		Type:        "string",
		Description: "File path",
	})

	props.Set("line_number", &jsonschema.Schema{
		Type:        "integer",
		Description: "Line number (required for line/delete operations)",
	})

	props.Set("new_content", &jsonschema.Schema{
		Type:        "string",
		Description: "New content (required for line/insert operations)",
	})

	props.Set("search", &jsonschema.Schema{
		Type:        "string",
		Description: "Search content (required for search operation)",
	})

	props.Set("replace", &jsonschema.Schema{
		Type:        "string",
		Description: "Replace content (required for search operation)",
	})

	props.Set("global", &jsonschema.Schema{
		Type:        "boolean",
		Description: "Global replace (optional for search, default: false)",
	})

	props.Set("use_regex", &jsonschema.Schema{
		Type:        "boolean",
		Description: "Use regex for search (optional for search, default: false)",
	})

	props.Set("insert_after", &jsonschema.Schema{
		Type:        "string",
		Description: "Insert after this content (required for insert operation)",
	})

	props.Set("insert_before", &jsonschema.Schema{
		Type:        "string",
		Description: "Insert before this content (required for insert operation)",
	})

	props.Set("delete_lines", &jsonschema.Schema{
		Type:        "array",
		Description: "Line numbers to delete (required for delete operation)",
		Items: &jsonschema.Schema{
			Type: "integer",
		},
	})

	props.Set("version", &jsonschema.Schema{
		Type:        "integer",
		Description: "Backup version number (required for restore operation)",
	})

	return &schema.ToolInfo{
		Name: ToolName,
		Desc: "Edit files with line-level editing, search-replace, insert, and delete operations. Supports backup and restore.",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: props,
			Required:   []string{"operation", "path"},
		}),
	}, nil
}

// OperationParams holds operation parameters.
type OperationParams struct {
	Operation    string `json:"operation"`
	Path         string `json:"path"`
	LineNumber   int    `json:"line_number"`
	NewContent   string `json:"new_content"`
	Search       string `json:"search"`
	Replace      string `json:"replace"`
	Global       bool   `json:"global"`
	UseRegex     bool   `json:"use_regex"`
	InsertAfter  string `json:"insert_after"`
	InsertBefore string `json:"insert_before"`
	DeleteLines  []int  `json:"delete_lines"`
	Version      int    `json:"version"`
}

// InvokableRun executes the operation.
func (t *editTool) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	var params OperationParams
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	if params.Path == "" {
		return common.ErrPathEmpty().Error(), nil
	}

	path, err := t.resolver.Resolve(params.Path)
	if err != nil {
		return "", err
	}

	// Get session key from context for session-isolated backups
	sessionKey, _ := session.SessionKeyFromContext(ctx)

	// Handle operations that don't require file to exist
	switch params.Operation {
	case "list_backups":
		return t.listBackups(path, params, sessionKey)
	case "restore":
		return t.editRestore(path, params, sessionKey)
	}

	// Check file exists for edit operations
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return common.ErrFileNotFound(params.Path).Error(), nil
	}
	if err != nil {
		return "", fmt.Errorf("access file: %w", err)
	}

	// Check if path is a directory
	if info.IsDir() {
		return common.ErrPathIsDirectory(params.Path).Error(), nil
	}

	switch params.Operation {
	case "line":
		return t.editLine(path, params, sessionKey)
	case "search":
		return t.editSearch(path, params, sessionKey)
	case "insert":
		return t.editInsert(path, params, sessionKey)
	case "delete":
		return t.editDelete(path, params, sessionKey)
	default:
		return common.ErrOperationNotSupported(params.Operation).Error(), nil
	}
}

// atomicWriteFile writes content to a file atomically using temp file + rename.
// This prevents data corruption if the write operation is interrupted.
func atomicWriteFile(path string, content []byte) error {
	tmpPath := path + ".tmp"

	// Write to temp file first
	if err := os.WriteFile(tmpPath, content, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	// Rename temp file to target (atomic operation on most filesystems)
	if err := os.Rename(tmpPath, path); err != nil {
		// Clean up temp file on failure
		os.Remove(tmpPath)
		return fmt.Errorf("rename file: %w", err)
	}

	return nil
}

// editLine edits a specific line.
func (t *editTool) editLine(path string, params OperationParams, sessionId string) (string, error) {
	if params.LineNumber < 1 {
		return common.ErrLineNumberInvalid().Error(), nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(content), "\n")

	if params.LineNumber > len(lines) {
		return common.ErrLineOutOfRange(params.LineNumber, len(lines)).Error(), nil
	}

	// Save original for history
	originalLine := lines[params.LineNumber-1]

	// Create backup before editing
	backupPath, version, err := t.backup.Backup(path, sessionId)
	if err != nil {
		return "", fmt.Errorf("create backup: %w", err)
	}

	// Replace line
	lines[params.LineNumber-1] = params.NewContent

	newContent := strings.Join(lines, "\n")
	if err := atomicWriteFile(path, []byte(newContent)); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("Success: Modified line %d in %s\n  Before: %s\n  After: %s\n  Backup: %s (v%d)",
		params.LineNumber, params.Path,
		common.TruncateString(originalLine, 50),
		common.TruncateString(params.NewContent, 50),
		backupPath, version), nil
}

// editSearch performs search and replace.
func (t *editTool) editSearch(path string, params OperationParams, sessionId string) (string, error) {
	if params.Search == "" {
		return common.ErrSearchEmpty().Error(), nil
	}

	// Validate regex length for security
	if params.UseRegex && len(params.Search) > MaxRegexLength {
		return common.ErrRegexTooLong().Error(), nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	contentStr := string(content)
	replaceCount := 0

	if params.UseRegex {
		// Use safe regex for search and replace
		re, err := common.CompileRegex(params.Search)
		if err != nil {
			return common.ErrRegexInvalid(err.Error()).Error(), nil
		}

		if params.Global {
			// Count all matches
			matches := re.FindAllStringIndex(contentStr, -1)
			replaceCount = len(matches)
			contentStr = re.ReplaceAllString(contentStr, params.Replace)
		} else {
			// Replace only first match
			loc := re.FindStringIndex(contentStr)
			if loc != nil {
				contentStr = contentStr[:loc[0]] + params.Replace + contentStr[loc[1]:]
				replaceCount = 1
			}
		}
	} else {
		// Use literal string replacement
		if params.Global {
			replaceCount = strings.Count(contentStr, params.Search)
			contentStr = strings.ReplaceAll(contentStr, params.Search, params.Replace)
		} else {
			if strings.Contains(contentStr, params.Search) {
				contentStr = strings.Replace(contentStr, params.Search, params.Replace, 1)
				replaceCount = 1
			}
		}
	}

	if replaceCount == 0 {
		return fmt.Sprintf("No matches found for: %s", params.Search), nil
	}

	// Create backup before editing
	backupPath, version, err := t.backup.Backup(path, sessionId)
	if err != nil {
		return "", fmt.Errorf("create backup: %w", err)
	}

	if err := atomicWriteFile(path, []byte(contentStr)); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("Success: Replaced %d occurrence(s) in %s\n  Search: %s\n  Replace: %s\n  Backup: %s (v%d)",
		replaceCount, params.Path,
		common.TruncateString(params.Search, 30),
		common.TruncateString(params.Replace, 30),
		backupPath, version), nil
}

// editInsert inserts content.
func (t *editTool) editInsert(path string, params OperationParams, sessionId string) (string, error) {
	if params.NewContent == "" {
		return common.ErrContentEmpty().Error(), nil
	}

	if params.InsertAfter == "" && params.InsertBefore == "" {
		return common.ErrInsertPosEmpty().Error(), nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	contentStr := string(content)
	inserted := false
	insertPos := ""

	if params.InsertAfter != "" {
		if strings.Contains(contentStr, params.InsertAfter) {
			contentStr = strings.Replace(contentStr, params.InsertAfter, params.InsertAfter+"\n"+params.NewContent, 1)
			inserted = true
			insertPos = "after: " + common.TruncateString(params.InsertAfter, 30)
		}
	} else if params.InsertBefore != "" {
		if strings.Contains(contentStr, params.InsertBefore) {
			contentStr = strings.Replace(contentStr, params.InsertBefore, params.NewContent+"\n"+params.InsertBefore, 1)
			inserted = true
			insertPos = "before: " + common.TruncateString(params.InsertBefore, 30)
		}
	}

	if !inserted {
		return common.ErrInsertPosNotFound().Error(), nil
	}

	// Create backup before editing
	backupPath, version, err := t.backup.Backup(path, sessionId)
	if err != nil {
		return "", fmt.Errorf("create backup: %w", err)
	}

	if err := atomicWriteFile(path, []byte(contentStr)); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("Success: Inserted content in %s\n  Position: %s\n  Backup: %s (v%d)",
		params.Path, insertPos, backupPath, version), nil
}

// editDelete deletes lines.
func (t *editTool) editDelete(path string, params OperationParams, sessionId string) (string, error) {
	if len(params.DeleteLines) == 0 {
		return common.ErrDeleteLinesEmpty().Error(), nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(content), "\n")

	// Sort delete lines in descending order to avoid index shifting
	sortedLines := make([]int, len(params.DeleteLines))
	copy(sortedLines, params.DeleteLines)
	sort.Sort(sort.Reverse(sort.IntSlice(sortedLines)))

	deletedCount := 0
	for _, lineNum := range sortedLines {
		if lineNum >= 1 && lineNum <= len(lines) {
			lines = append(lines[:lineNum-1], lines[lineNum:]...)
			deletedCount++
		}
	}

	if deletedCount == 0 {
		return common.ErrDeleteLinesInvalid().Error(), nil
	}

	// Create backup before editing
	backupPath, version, err := t.backup.Backup(path, sessionId)
	if err != nil {
		return "", fmt.Errorf("create backup: %w", err)
	}

	newContent := strings.Join(lines, "\n")
	if err := atomicWriteFile(path, []byte(newContent)); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("Success: Deleted %d line(s) from %s\n  Lines: %v\n  Remaining: %d lines\n  Backup: %s (v%d)",
		deletedCount, params.Path, params.DeleteLines, len(lines), backupPath, version), nil
}

// listBackups lists all backup versions for a file.
func (t *editTool) listBackups(path string, params OperationParams, sessionId string) (string, error) {
	backups, err := t.backup.ListBackups(path, sessionId)
	if err != nil {
		return "", fmt.Errorf("list backups: %w", err)
	}

	if len(backups) == 0 {
		return fmt.Sprintf("No backups found for %s", params.Path), nil
	}

	// Build result for LLM
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Backups for %s (%d versions):\n", params.Path, len(backups)))
	result.WriteString("---\n")

	for _, b := range backups {
		result.WriteString(fmt.Sprintf("v%d: %s (%s)\n", b.Version, b.ModTime, formatSize(int64(b.Size))))
	}

	return result.String(), nil
}

// formatSize formats file size.
func formatSize(bytes int64) string {
	const KB = 1024
	if bytes >= KB {
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(KB))
	}
	return fmt.Sprintf("%dB", bytes)
}

// editRestore restores a file from a backup.
func (t *editTool) editRestore(path string, params OperationParams, sessionId string) (string, error) {
	if params.Version <= 0 {
		return common.ErrVersionInvalid().Error(), nil
	}

	if err := t.backup.Restore(path, params.Version, sessionId); err != nil {
		return common.ErrRestoreFailed(err.Error()).Error(), nil
	}

	return fmt.Sprintf("Success: Restored %s to version v%d", params.Path, params.Version), nil
}

// Register registers the edit tool with custom configuration.
func Register(config Config) error {
	t, err := NewTool(config)
	if err != nil {
		return err
	}
	return aitool.Registry.Register(t)
}

// RegisterDefault registers with default configuration.
func RegisterDefault() error {
	def := aitool.ToolDefinition{
		Name:   ToolName,
		Desc:   "Edit (Evolution) - Line-level editing, search-replace, and incremental modifications",
		Config: Config{},
		Factory: func(config map[string]interface{}) (tool.BaseTool, error) {
			var cfg Config
			if err := maps.Map2Struct(config, &cfg); err != nil {
				return nil, err
			}
			return NewTool(cfg)
		},
	}

	instance, err := NewTool(DefaultConfig())
	if err != nil {
		return err
	}
	def.Instance = instance

	return aitool.Registry.RegisterDef(def)
}

func init() {
	_ = RegisterDefault()
}
