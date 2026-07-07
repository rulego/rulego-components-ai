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
	"regexp"
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

// binarySampleSize 二进制嗅探读取的前 4KB
const binarySampleSize = 4 * 1024

// binaryNonPrintRatio 非打印字节占比阈值，超过判定为二进制
const binaryNonPrintRatio = 0.3

// binaryExtBlacklist 二进制文件扩展名黑名单（命中即判二进制，不再尝试解码）
var binaryExtBlacklist = map[string]bool{
	".zip": true, ".tar": true, ".gz": true, ".tgz": true, ".bz2": true,
	".7z": true, ".rar": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".a": true, ".lib": true,
	".class": true, ".jar": true, ".wasm": true, ".pyc": true, ".pyo": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".ico": true,
	".webp": true, ".tiff": true, ".tif": true, ".svgz": true,
	".pdf": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".odt": true, ".ods": true, ".odp": true,
	".mp3": true, ".mp4": true, ".avi": true, ".mov": true, ".mkv": true, ".flv": true,
	".wav": true, ".flac": true, ".ogg": true,
	".sqlite": true, ".db": true, ".mdb": true,
	".eot": true, ".ttf": true, ".otf": true, ".woff": true, ".woff2": true,
}

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
	config Config
	cache  *common.ResolverCache
}

// resolverFor 取本次调用的有效 resolver：优先用 ctx 注入的 workDir（common.WorkDirFromCtx，
// 主 agent 派子 agent 时注入），空则回退 config.WorkDir 默认（保留原行为）。
func (t *readTool) resolverFor(ctx context.Context) (*common.SecurePathResolver, error) {
	return t.cache.GetWithAllowDirs(common.WorkDirFromCtx(ctx), common.AllowDirsFromCtx(ctx), common.AllowCrossDirFromCtx(ctx))
}

// readPathSecurity 读取操作的路径安全策略：允许隐藏文件（代码审查需读 .env/node_modules 等），
// 排除目录读全局默认（config.yaml fileAccess.excludeDirs，未设则不排除）。仅 Resolve 单文件校验层生效，
// 不影响 search 内部 walk（避免过度校验引入搜索 bug）。
func readPathSecurity() common.PathSecurityConfig {
	cfg := common.DefaultPathSecurityConfig()
	cfg.AllowHiddenFiles = true
	cfg.ExcludeDirs = common.GetDefaultExcludeDirs()
	return cfg
}

// NewTool creates a new read tool.
func NewTool(config Config) (tool.BaseTool, error) {
	if config.MaxReadLines <= 0 {
		config.MaxReadLines = DefaultConfig().MaxReadLines
	}
	if config.MaxSearchResults <= 0 {
		config.MaxSearchResults = DefaultConfig().MaxSearchResults
	}

	resolver, err := common.NewSecurePathResolver(config.WorkDir, readPathSecurity())
	if err != nil {
		return nil, err
	}
	config.WorkDir = resolver.Workspace()

	cache, err := common.NewResolverCache(config.WorkDir, readPathSecurity())
	if err != nil {
		return nil, err
	}

	return &readTool{
		config: config,
		cache:  cache,
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
		Description: "File glob pattern for search (optional, default: * matching all files)",
	})

	props.Set("line_from", &jsonschema.Schema{
		Type:        "integer",
		Description: "Start line number (optional for file operation)",
	})

	props.Set("line_to", &jsonschema.Schema{
		Type:        "integer",
		Description: "End line number (optional for file operation)",
	})

	props.Set("use_regex", &jsonschema.Schema{
		Type:        "boolean",
		Description: "Treat query as a regex (optional for search, default: false = literal substring)",
	})

	props.Set("context_after", &jsonschema.Schema{
		Type:        "integer",
		Description: "Lines of context after each match (optional for search, like grep -A)",
	})

	props.Set("context_before", &jsonschema.Schema{
		Type:        "integer",
		Description: "Lines of context before each match (optional for search, like grep -B)",
	})

	props.Set("context", &jsonschema.Schema{
		Type:        "integer",
		Description: "Lines of context around each match (optional for search, like grep -C). Overrides context_before/context_after if set.",
	})

	props.Set("output_mode", &jsonschema.Schema{
		Type:        "string",
		Description: "Search output mode: content (lines+numbers, default), files_with_matches (paths only), count (match count per file)",
		Enum:        []any{"content", "files_with_matches", "count"},
	})

	props.Set("head_limit", &jsonschema.Schema{
		Type:        "integer",
		Description: "Max number of result lines/files for search (optional, default: 30)",
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
	Operation     string `json:"operation"`
	Path          string `json:"path"`
	Query         string `json:"query"`
	Pattern       string `json:"pattern"`
	LineFrom      int    `json:"line_from"`
	LineTo        int    `json:"line_to"`
	UseRegex      bool   `json:"use_regex"`
	ContextAfter  int    `json:"context_after"`
	ContextBefore int    `json:"context_before"`
	Context       int    `json:"context"`
	OutputMode    string `json:"output_mode"`
	HeadLimit     int    `json:"head_limit"`
}

// InvokableRun executes the operation.
func (t *readTool) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	var params OperationParams
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	// 取本次调用的有效 resolver（ctx 注入的 workDir 优先，否则 config 默认）。
	r, err := t.resolverFor(ctx)
	if err != nil {
		return common.ErrPathInvalid(err.Error()).Error(), nil
	}

	switch params.Operation {
	case "file":
		return t.readFile(params, r)
	case "search":
		return t.search(ctx, params, r)
	case "list":
		return t.list(params, r)
	default:
		return common.ErrOperationNotSupported(params.Operation).Error(), nil
	}
}

// readFile reads a file.
func (t *readTool) readFile(params OperationParams, r *common.SecurePathResolver) (string, error) {
	if params.Path == "" {
		return common.ErrPathEmpty().Error(), nil
	}

	path, err := r.Resolve(params.Path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return common.ErrFileNotFound(params.Path).Error(), nil
	}
	if err != nil {
		return "", fmt.Errorf("access file: %w", err)
	}

	// If directory, list contents
	if info.IsDir() {
		return t.list(OperationParams{Path: params.Path}, r)
	}

	lineFrom := params.LineFrom
	lineTo := params.LineTo

	// 二进制嗅探：命中黑名单扩展名或字节启发式判定为二进制，返回友好说明而非乱码
	if isBinaryFile(path) {
		return fmt.Sprintf("File: %s\n---\nThis looks like a binary file (image/archive/executable/etc). The read tool does not decode binary content into text. Use a dedicated tool or extract text first.", params.Path), nil
	}

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

	// byte + line 双闸截断：复用统一截断服务（Head 方向，行数/字节任一超限即截断）。
	// MaxReadLines 作为行预算；字节预算用统一默认（50KB）。
	maxLines := t.config.MaxReadLines
	tr := common.Truncate(string(content), common.TruncateOptions{
		MaxLines:  &maxLines,
		Direction: common.TruncHead,
	})
	body := tr.Content
	bodyLines := strings.Split(body, "\n")

	// Build result for LLM
	var result strings.Builder
	if tr.Truncated {
		result.WriteString(fmt.Sprintf("File: %s (showing first %d of %d lines)\n", params.Path, len(bodyLines), totalLines))
	} else {
		result.WriteString(fmt.Sprintf("File: %s (%d lines)\n", params.Path, totalLines))
	}
	result.WriteString("---\n")
	for i, line := range bodyLines {
		result.WriteString(fmt.Sprintf("%d: %s\n", i+1, line))
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
func (t *readTool) search(ctx context.Context, params OperationParams, r *common.SecurePathResolver) (string, error) {
	query := params.Query
	if query == "" {
		return common.ErrQueryEmpty().Error(), nil
	}

	// 编译正则（use_regex=true）或构造字面子串匹配器
	var lineMatcher func(line string) bool
	if params.UseRegex {
		if len(query) > 1000 {
			return common.ErrRegexTooLong().Error(), nil
		}
		re, err := regexp.Compile(query)
		if err != nil {
			return common.ErrRegexInvalid(err.Error()).Error(), nil
		}
		lineMatcher = func(line string) bool { return re.MatchString(line) }
	} else {
		needle := strings.ToLower(query)
		lineMatcher = func(line string) bool { return strings.Contains(strings.ToLower(line), needle) }
	}

	pattern := params.Pattern
	if pattern == "" {
		pattern = "*" // 默认匹配所有文件，避免误导模型只搜 markdown
	}

	// 上下文行设置：context 同时设置则覆盖 context_before/after
	before, after := params.ContextBefore, params.ContextAfter
	if params.Context > 0 {
		before = params.Context
		after = params.Context
	}

	outputMode := params.OutputMode
	if outputMode == "" {
		outputMode = "content"
	}

	headLimit := params.HeadLimit
	if headLimit <= 0 {
		headLimit = t.config.MaxSearchResults
		if headLimit <= 0 {
			headLimit = 30
		}
	}

	searchDir := r.Workspace()
	if params.Path != "" {
		resolved, err := r.Resolve(params.Path)
		if err != nil {
			return "", err
		}
		searchDir = resolved
	}

	type fileHit struct {
		relPath string
		matches []int // 命中的 1-indexed 行号
		lines   []string
	}
	var hits []fileHit
	// real* 为遍历到的真实总数，不受 head_limit 截断影响；shown* 为实际展示数量。
	shownMatches := 0
	realMatchTotal := 0
	realFileTotal := 0
	truncated := false

	hasDoubleStar := strings.Contains(pattern, "**")

	err := filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err != nil || info.IsDir() {
			return nil
		}
		// 跳过软链/junction 逃出 searchDir 的路径
		if !isWithinResolved(path, searchDir) {
			return nil
		}
		if !t.matchPattern(pattern, path, searchDir, hasDoubleStar) {
			return nil
		}
		if isBinaryFile(path) {
			return nil
		}

		// 仅读取前 100KB；大文件尾部不参与搜索，全文搜索请用 grep 工具
		content, rerr := readLimited(path, 100000)
		if rerr != nil {
			return nil
		}
		lines := strings.Split(content, "\n")
		var matched []int
		for i, ln := range lines {
			if lineMatcher(ln) {
				matched = append(matched, i+1)
			}
		}
		if len(matched) == 0 {
			return nil
		}
		realMatchTotal += len(matched)
		realFileTotal++
		relPath, _ := filepath.Rel(r.Workspace(), path)
		// content 模式 head_limit 按匹配行数计，其它模式按文件数计
		if outputMode == "content" {
			if shownMatches >= headLimit {
				truncated = true
				return nil
			}
			remaining := headLimit - shownMatches
			if remaining < len(matched) {
				matched = matched[:remaining]
				truncated = true
			}
			shownMatches += len(matched)
		} else {
			if len(hits) >= headLimit {
				truncated = true
				return nil
			}
		}
		hits = append(hits, fileHit{relPath: relPath, matches: matched, lines: lines})
		return nil
	})
	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		// 忽略遍历过程中的单文件错误，继续返回已收集结果
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d match(es) for '%s' in %d file(s)", realMatchTotal, query, realFileTotal))
	if truncated {
		result.WriteString(fmt.Sprintf(" (results limited, head_limit=%d)", headLimit))
	}
	result.WriteString("\n---\n")

	switch outputMode {
	case "files_with_matches":
		for i, h := range hits {
			result.WriteString(fmt.Sprintf("%d. %s (%d match(es))\n", i+1, h.relPath, len(h.matches)))
		}
	case "count":
		for i, h := range hits {
			result.WriteString(fmt.Sprintf("%d. %s: %d\n", i+1, h.relPath, len(h.matches)))
		}
	default: // content
		for _, h := range hits {
			result.WriteString(fmt.Sprintf("%s:\n", h.relPath))
			writeMatchesMerged(&result, h.lines, h.matches, before, after)
		}
	}

	return result.String(), nil
}

// writeMatchesMerged 输出多行匹配及其上下文（-A/-B/-C 语义）。
// 相邻或重叠的上下文区间合并，行号不重复，匹配行用 "> " 前缀标记。
func writeMatchesMerged(w *strings.Builder, lines []string, matches []int, before, after int) {
	if len(matches) == 0 {
		return
	}
	matchSet := make(map[int]struct{}, len(matches))
	for _, m := range matches {
		matchSet[m] = struct{}{}
	}

	// 复制后排序，避免修改入参
	sorted := make([]int, len(matches))
	copy(sorted, matches)
	sortInts(sorted)

	// 计算每个 match 的 [start,end]，合并相邻/重叠区间
	type span struct{ start, end int }
	var spans []span
	for _, m := range sorted {
		start := m - before
		if start < 1 {
			start = 1
		}
		end := m + after
		if end > len(lines) {
			end = len(lines)
		}
		if len(spans) > 0 && start <= spans[len(spans)-1].end+1 {
			if end > spans[len(spans)-1].end {
				spans[len(spans)-1].end = end
			}
			continue
		}
		spans = append(spans, span{start, end})
	}

	for _, sp := range spans {
		for i := sp.start; i <= sp.end; i++ {
			marker := "  "
			if _, ok := matchSet[i]; ok {
				marker = "> "
			}
			w.WriteString(fmt.Sprintf("%sLine %d: %s\n", marker, i, lines[i-1]))
		}
		if before > 0 || after > 0 {
			w.WriteString("---\n")
		}
	}
}

// sortInts 原地升序排序（插入排序，集合小）
func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// readLimited reads a file with size limit for efficiency.
func readLimited(path string, maxBytes int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	if maxBytes <= 0 {
		maxBytes = 100000
	}
	buf := make([]byte, maxBytes)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	return string(buf[:n]), nil
}

// isBinaryFile 综合扩展名黑名单 + 字节启发式判定是否为二进制文件。
func isBinaryFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if binaryExtBlacklist[ext] {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sample := make([]byte, binarySampleSize)
	n, _ := f.Read(sample)
	if n == 0 {
		return false
	}
	return isBinaryBytes(sample[:n])
}

// isBinaryBytes 判定字节样本是否为二进制：含 NUL 直接判定；
// 否则统计无效 UTF-8 字节与控制字符占比，超过 binaryNonPrintRatio 即二进制。
// 合法的 UTF-8 多字节序列（含中文）不计入非打印。
func isBinaryBytes(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	if bytesIndex(b, 0) >= 0 {
		return true
	}
	nonPrint := 0
	i := 0
	for i < len(b) {
		c := b[i]
		if c == '\t' || c == '\n' || c == '\r' || c == '\f' {
			i++
			continue
		}
		if c < 0x20 { // 其他 C0 控制字符
			nonPrint++
			i++
			continue
		}
		if c >= 0x7f { // DEL 或多字节首字节
			r, size := decodeRune(b[i:])
			if r == 0xFFFD && size == 1 { // 非法 UTF-8 字节
				nonPrint++
				i++
				continue
			}
			// 合法多字节序列整体跳过（含中文等）
			i += size
			continue
		}
		// 普通 ASCII 可打印 (0x20-0x7e)
		i++
	}
	return float64(nonPrint)/float64(len(b)) > binaryNonPrintRatio
}

// decodeRune 解码单个 UTF-8 rune，返回 rune 与消耗字节数。非法字节返回 (0xFFFD, 1)。
func decodeRune(b []byte) (rune, int) {
	if len(b) == 0 {
		return 0xFFFD, 0
	}
	c0 := b[0]
	switch {
	case c0 < 0x80:
		return rune(c0), 1
	case c0 < 0xC2: // 0x80-0xC1：续字节或非法首字节
		return 0xFFFD, 1
	case c0 < 0xE0:
		if len(b) < 2 || b[1]&0xC0 != 0x80 {
			return 0xFFFD, 1
		}
		return rune(c0&0x1F)<<6 | rune(b[1]&0x3F), 2
	case c0 < 0xF0:
		if len(b) < 3 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 {
			return 0xFFFD, 1
		}
		return rune(c0&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F), 3
	case c0 < 0xF5:
		if len(b) < 4 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 || b[3]&0xC0 != 0x80 {
			return 0xFFFD, 1
		}
		return rune(c0&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F), 4
	default: // 0xF5-0xFF：超出 Unicode 范围
		return 0xFFFD, 1
	}
}

// bytesIndex 兼容 helper：返回字节 c 在 b 中首次出现的下标，-1 表示不存在。
func bytesIndex(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// isWithinResolved 校验 path 解析符号链接后仍位于 base 之下。
// 用于 search walk 回调，阻止跟随工作区内软链/junction 逃出工作区。
func isWithinResolved(path, base string) bool {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false // 路径无法解析，保守视为越界
	}
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		realBase = base
	}
	realPath = filepath.Clean(realPath)
	realBase = filepath.Clean(realBase)
	if realPath == realBase {
		return true
	}
	// 必须以 base + 分隔符 为前缀，避免 /foo/barb 与 /foo/bar 的伪前缀匹配
	sep := string(filepath.Separator)
	return strings.HasPrefix(realPath, realBase+sep)
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
		return common.MatchWithDoubleStar(pattern, relPath)
	}

	// Simple pattern matching on filename only
	matched, _ := filepath.Match(pattern, filepath.Base(path))
	return matched
}

// list lists directory contents.
func (t *readTool) list(params OperationParams, r *common.SecurePathResolver) (string, error) {
	path := r.Workspace()
	displayPath := params.Path
	if params.Path != "" {
		resolved, err := r.Resolve(params.Path)
		if err != nil {
			return "", err
		}
		path = resolved
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
