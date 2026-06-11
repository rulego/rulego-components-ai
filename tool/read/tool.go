// Package read provides a reading tool for AI agents.
package read

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	aitool "github.com/rulego/rulego-components-ai/tool"
	"github.com/rulego/rulego-components-ai/tool/common"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

const ToolName = "read"

// maxFullReadSize 全文读取的最大文件大小（10MB）
const maxFullReadSize = 10 * 1024 * 1024

// Config holds read tool configuration.
type Config struct {
	WorkDir          string `json:"workDir" label:"工作目录" desc:"文件操作的默认工作目录"`
	MaxReadLines     int    `json:"maxReadLines" label:"最大读取行数" desc:"单次读取最大行数"`
	MaxSearchResults int    `json:"maxSearchResults" label:"最大搜索结果数" desc:"搜索内容时返回的最大匹配文件数"`
}

// DefaultConfig returns default configuration.
func DefaultConfig() Config {
	return Config{
		WorkDir:          ".",
		MaxReadLines:     1000,
		MaxSearchResults: 30,
	}
}

type readTool struct {
	config   Config
	resolver *common.PathResolver
}

// NewTool creates a new read tool.
func NewTool(config Config) (tool.BaseTool, error) {
	if config.MaxReadLines <= 0 {
		config.MaxReadLines = DefaultConfig().MaxReadLines
	}
	if config.MaxSearchResults <= 0 {
		config.MaxSearchResults = DefaultConfig().MaxSearchResults
	}

	resolver, err := common.NewPathResolver(config.WorkDir)
	if err != nil {
		return nil, err
	}
	config.WorkDir = resolver.Workspace()

	return &readTool{
		config:   config,
		resolver: resolver,
	}, nil
}

// Info returns tool information.
func (t *readTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	props := orderedmap.New[string, *jsonschema.Schema]()

	props.Set("operation", &jsonschema.Schema{
		Type:        "string",
		Description: "Operation type: file (read file), search (search content), list (list directory)",
		Enum:        []any{"file", "search", "list"},
	})

	props.Set("path", &jsonschema.Schema{
		Type:        "string",
		Description: "File or directory path",
	})

	props.Set("query", &jsonschema.Schema{
		Type:        "string",
		Description: "Search keyword (required for search operation)",
	})

	props.Set("pattern", &jsonschema.Schema{
		Type:        "string",
		Description: "File glob pattern for search (optional, default: *.md)",
	})

	props.Set("line_from", &jsonschema.Schema{
		Type:        "integer",
		Description: "Start line number (optional for file operation)",
	})

	props.Set("line_to", &jsonschema.Schema{
		Type:        "integer",
		Description: "End line number (optional for file operation)",
	})

	return &schema.ToolInfo{
		Name: ToolName,
		Desc: "Read files, search content, and list directories.",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: props,
			Required:   []string{"operation"},
		}),
	}, nil
}

// OperationParams holds operation parameters.
type OperationParams struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Query     string `json:"query"`
	Pattern   string `json:"pattern"`
	LineFrom  int    `json:"line_from"`
	LineTo    int    `json:"line_to"`
}

// InvokableRun executes the operation.
func (t *readTool) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	var params OperationParams
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	switch params.Operation {
	case "file":
		return t.readFile(params)
	case "search":
		return t.search(ctx, params)
	case "list":
		return t.list(params)
	default:
		return common.ErrOperationNotSupported(params.Operation).Error(), nil
	}
}

// readFile reads a file.
func (t *readTool) readFile(params OperationParams) (string, error) {
	if params.Path == "" {
		return common.ErrPathEmpty().Error(), nil
	}

	path := t.resolver.Resolve(params.Path)

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return common.ErrFileNotFound(params.Path).Error(), nil
	}
	if err != nil {
		return "", fmt.Errorf("access file: %w", err)
	}

	// If directory, list contents
	if info.IsDir() {
		return t.list(OperationParams{Path: params.Path})
	}

	lineFrom := params.LineFrom
	lineTo := params.LineTo

	// If line range is specified, use bufio.Scanner for memory efficiency
	if lineFrom > 0 || lineTo > 0 {
		return t.readFileWithScanner(path, params.Path, lineFrom, lineTo)
	}

	// 大文件防护：全文读取前检查文件大小，超过阈值时改用 scanner 逐行读取
	if info.Size() > maxFullReadSize {
		return t.readFileWithScanner(path, params.Path, 1, 0)
	}

	// For full file read, use os.ReadFile
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	totalLines := len(lines)

	// Limit lines
	readLines := lines
	if totalLines > t.config.MaxReadLines {
		readLines = lines[:t.config.MaxReadLines]
	}

	// Build result for LLM
	var result strings.Builder
	result.WriteString(fmt.Sprintf("File: %s (lines 1-%d of %d)\n", params.Path, len(readLines), totalLines))
	result.WriteString("---\n")
	for i, line := range readLines {
		result.WriteString(fmt.Sprintf("%d: %s\n", i+1, line))
	}

	if totalLines > t.config.MaxReadLines {
		result.WriteString(fmt.Sprintf("\n... (truncated, %d more lines)", totalLines-t.config.MaxReadLines))
	}

	return result.String(), nil
}

// readFileWithScanner reads specific line range using bufio.Scanner for memory efficiency.
func (t *readTool) readFileWithScanner(path, displayPath string, lineFrom, lineTo int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	if lineFrom < 1 {
		lineFrom = 1
	}

	var selectedLines []string
	var totalLines int
	currentLine := 0
	maxLines := t.config.MaxReadLines

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		currentLine++
		totalLines = currentLine

		// Skip lines before lineFrom
		if currentLine < lineFrom {
			continue
		}

		// Stop collecting if we've reached lineTo or max lines
		if lineTo > 0 && currentLine > lineTo {
			continue // Continue scanning to count total lines
		}

		// Stop if we've reached max lines limit
		if len(selectedLines) >= maxLines {
			continue
		}

		selectedLines = append(selectedLines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan file: %w", err)
	}

	actualLineTo := lineFrom + len(selectedLines) - 1
	if lineTo > 0 && actualLineTo > lineTo {
		actualLineTo = lineTo
	}

	// Build result for LLM
	var result strings.Builder
	result.WriteString(fmt.Sprintf("File: %s (lines %d-%d of %d)\n", displayPath, lineFrom, actualLineTo, totalLines))
	result.WriteString("---\n")
	for i, line := range selectedLines {
		result.WriteString(fmt.Sprintf("%d: %s\n", lineFrom+i, line))
	}

	return result.String(), nil
}

// search searches in files.
func (t *readTool) search(ctx context.Context, params OperationParams) (string, error) {
	query := strings.ToLower(params.Query)
	if query == "" {
		return common.ErrQueryEmpty().Error(), nil
	}

	pattern := params.Pattern
	if pattern == "" {
		pattern = "*.md"
	}

	searchDir := t.config.WorkDir
	if params.Path != "" {
		searchDir = t.resolver.Resolve(params.Path)
	}

	var results []map[string]interface{}

	// Check if pattern contains ** for recursive matching
	hasDoubleStar := strings.Contains(pattern, "**")

	// Walk directory
	err := filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil || info.IsDir() {
			return nil
		}

		// Check pattern match
		matched := t.matchPattern(pattern, path, searchDir, hasDoubleStar)
		if !matched {
			return nil
		}

		// Read and search (limit read size for efficiency)
		content, err := readLimited(path, 10000)
		if err != nil {
			return nil
		}

		if strings.Contains(strings.ToLower(content), query) {
			relPath, _ := filepath.Rel(t.config.WorkDir, path)
			results = append(results, map[string]interface{}{
				"path":    relPath,
				"preview": common.TruncateString(content, 200),
			})
		}

		return nil
	})

	// If context was cancelled, return early
	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		// Log or handle other errors, but continue with results we have
	}

	// Limit results
	if len(results) > t.config.MaxSearchResults {
		results = results[:t.config.MaxSearchResults]
	}

	// Build result for LLM
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d matches for '%s':\n", len(results), params.Query))
	for i, r := range results {
		result.WriteString(fmt.Sprintf("%d. %s\n", i+1, r["path"]))
	}

	return result.String(), nil
}

// readLimited reads a file with size limit for efficiency.
func readLimited(path string, maxBytes int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	buf := make([]byte, maxBytes)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	return string(buf[:n]), nil
}

// matchPattern matches a file path against a pattern, supporting ** for recursive matching.
func (t *readTool) matchPattern(pattern, path, searchDir string, hasDoubleStar bool) bool {
	if pattern == "*" {
		return true
	}

	relPath, err := filepath.Rel(searchDir, path)
	if err != nil {
		return false
	}

	// If pattern contains **, handle recursive matching
	if hasDoubleStar {
		return matchWithDoubleStar(pattern, relPath)
	}

	// Simple pattern matching on filename only
	matched, _ := filepath.Match(pattern, filepath.Base(path))
	return matched
}

// matchWithDoubleStar implements ** glob pattern matching.
// ** matches any number of directories (including zero).
func matchWithDoubleStar(pattern, relPath string) bool {
	// Normalize paths to use forward slashes
	relPath = filepath.ToSlash(relPath)
	pattern = filepath.ToSlash(pattern)

	// Split pattern and path into parts
	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(relPath, "/")

	return matchParts(patternParts, pathParts)
}

// matchParts recursively matches pattern parts against path parts.
func matchParts(patternParts, pathParts []string) bool {
	// If both are empty, we have a match
	if len(patternParts) == 0 && len(pathParts) == 0 {
		return true
	}

	// If pattern is empty but path is not, no match
	if len(patternParts) == 0 {
		return false
	}

	// If path is empty but pattern is not, check if remaining pattern is all **
	if len(pathParts) == 0 {
		for _, p := range patternParts {
			if p != "**" {
				return false
			}
		}
		return true
	}

	// Handle ** pattern
	if patternParts[0] == "**" {
		// ** can match zero or more directories
		// Try matching zero directories (skip **)
		if matchParts(patternParts[1:], pathParts) {
			return true
		}
		// Try matching one or more directories (consume path part)
		return matchParts(patternParts, pathParts[1:])
	}

	// Handle * and other patterns
	matched, _ := filepath.Match(patternParts[0], pathParts[0])
	if !matched {
		return false
	}

	return matchParts(patternParts[1:], pathParts[1:])
}

// list lists directory contents.
func (t *readTool) list(params OperationParams) (string, error) {
	path := t.config.WorkDir
	displayPath := params.Path
	if params.Path != "" {
		path = t.resolver.Resolve(params.Path)
	}

	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return fmt.Sprintf("Error: directory not found: %s (resolved: %s)", displayPath, path), nil
	}
	if err != nil {
		return fmt.Sprintf("Error: read directory: %v (path: %s)", err, path), nil
	}

	// Build result for LLM
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Directory: %s (%d items)\n", params.Path, len(entries)))
	result.WriteString("---\n")

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		typeMarker := " "
		if entry.IsDir() {
			typeMarker = "d"
		}

		size := formatSize(info.Size())
		modTime := info.ModTime().Format("2006-01-02 15:04")
		result.WriteString(fmt.Sprintf("[%s] %s  %s  %s\n", typeMarker, size, modTime, entry.Name()))
	}

	return result.String(), nil
}

// formatSize formats file size in human-readable format.
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1fG", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1fM", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1fK", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// Register registers the read tool with custom configuration.
func Register(config Config) error {
	t, err := NewTool(config)
	if err != nil {
		return err
	}
	return aitool.Registry.Register(t)
}

// RegisterDefault registers with default configuration using simplified template.
func RegisterDefault() error {
	return aitool.RegisterTool(ToolName, "Read (Perception) - Read files, search content, list directories", DefaultConfig(), NewTool)
}

func init() {
	_ = RegisterDefault()
}
