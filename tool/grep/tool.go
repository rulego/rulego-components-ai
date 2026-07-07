// Package grep 提供独立的词法搜索工具（对标 rg/grep），ripgrep 优先 + Go 兜底。
// 设计依据：docs/plans/工具层优化方案.md §3.1。
package grep

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	aitool "github.com/rulego/rulego-components-ai/tool"
	"github.com/rulego/rulego-components-ai/tool/common"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

const ToolName = "grep"

// 单行截断长度上限，避免输出超长行污染上下文。
const maxLineLength = 2000

// 默认/硬上限。
const (
	defaultMaxResults = 100
	hardMaxResults    = 500
)

// Config holds grep tool configuration.
type Config struct {
	WorkDir      string `json:"workDir" label:"工作目录" desc:"搜索的默认工作目录"`
	MaxResults   int    `json:"maxResults" label:"最大结果数" desc:"单次返回最大匹配数（head_limit 默认值）"`
	HardMaxLimit int    `json:"hardMaxLimit" label:"硬上限" desc:"head_limit 的硬上限，超过会被截断"`
}

// DefaultConfig returns default configuration.
func DefaultConfig() Config {
	return Config{
		WorkDir:      ".",
		MaxResults:   defaultMaxResults,
		HardMaxLimit: hardMaxResults,
	}
}

type grepTool struct {
	config Config
	cache  *common.ResolverCache
}

// grepPathSecurity 与 read 一致：允许隐藏文件（代码审查需访问 .env/vendor 等），
// 排除目录读全局默认（未设则不排除）。仅 Resolve 层生效，不影响 walk 内部遍历。
func grepPathSecurity() common.PathSecurityConfig {
	cfg := common.DefaultPathSecurityConfig()
	cfg.AllowHiddenFiles = true
	cfg.ExcludeDirs = common.GetDefaultExcludeDirs()
	return cfg
}

// NewTool creates a new grep tool.
func NewTool(config Config) (tool.BaseTool, error) {
	if config.MaxResults <= 0 {
		config.MaxResults = DefaultConfig().MaxResults
	}
	if config.HardMaxLimit <= 0 {
		config.HardMaxLimit = DefaultConfig().HardMaxLimit
	}

	resolver, err := common.NewSecurePathResolver(config.WorkDir, grepPathSecurity())
	if err != nil {
		return nil, err
	}
	config.WorkDir = resolver.Workspace()

	cache, err := common.NewResolverCache(config.WorkDir, grepPathSecurity())
	if err != nil {
		return nil, err
	}

	return &grepTool{
		config: config,
		cache:  cache,
	}, nil
}

// Info returns tool information.
func (t *grepTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	props := orderedmap.New[string, *jsonschema.Schema]()

	props.Set("pattern", &jsonschema.Schema{
		Type:        "string",
		Description: "Regular expression pattern (ripgrep syntax). Required.",
	})

	props.Set("path", &jsonschema.Schema{
		Type:        "string",
		Description: "Directory or file to search (default: workspace).",
	})

	props.Set("include", &jsonschema.Schema{
		Type:        "string",
		Description: "File glob to include, e.g. \"*.go\" or \"*.{ts,tsx}\".",
	})

	props.Set("exclude", &jsonschema.Schema{
		Type:        "string",
		Description: "File glob to exclude, e.g. \"vendor/**\".",
	})

	props.Set("output_mode", &jsonschema.Schema{
		Type:        "string",
		Description: "Output mode: content (lines+numbers, default), files_with_matches (paths only), count (match count per file).",
		Enum:        []any{"content", "files_with_matches", "count"},
	})

	props.Set("-A", &jsonschema.Schema{
		Type:        "integer",
		Description: "Lines of context after each match (like grep -A).",
	})

	props.Set("-B", &jsonschema.Schema{
		Type:        "integer",
		Description: "Lines of context before each match (like grep -B).",
	})

	props.Set("-C", &jsonschema.Schema{
		Type:        "integer",
		Description: "Lines of context around each match (like grep -C). Overrides -A/-B if set.",
	})

	props.Set("head_limit", &jsonschema.Schema{
		Type:        "integer",
		Description: "Max number of result lines/files (default: 100, hard max: 500).",
	})

	props.Set("sort_by_mtime", &jsonschema.Schema{
		Type:        "boolean",
		Description: "Sort matched files by modification time descending (default: true).",
	})

	return &schema.ToolInfo{
		Name: ToolName,
		Desc: "Lexical search across files (ripgrep-first with Go fallback).",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: props,
			Required:   []string{"pattern"},
		}),
	}, nil
}

// Params holds search parameters.
type Params struct {
	Pattern      string `json:"pattern"`
	Path         string `json:"path"`
	Include      string `json:"include"`
	Exclude      string `json:"exclude"`
	OutputMode   string `json:"output_mode"`
	ContextAfter int    `json:"-A"`
	ContextBefore int   `json:"-B"`
	Context      int    `json:"-C"`
	HeadLimit    int    `json:"head_limit"`
	SortByMtime  *bool  `json:"sort_by_mtime"`
}

// InvokableRun executes the search.
func (t *grepTool) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	var params Params
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	if params.Pattern == "" {
		return common.ErrQueryEmpty().Error(), nil
	}
	if len(params.Pattern) > 1000 {
		return common.ErrRegexTooLong().Error(), nil
	}
	re, err := regexp.Compile(params.Pattern)
	if err != nil {
		return common.ErrRegexInvalid(err.Error()).Error(), nil
	}

	// 解析输出模式
	outputMode := params.OutputMode
	if outputMode == "" {
		outputMode = "content"
	}
	switch outputMode {
	case "content", "files_with_matches", "count":
	default:
		outputMode = "content"
	}

	// 上下文行：-C 同时设置则覆盖 -A/-B
	before, after := params.ContextBefore, params.ContextAfter
	if params.Context > 0 {
		before = params.Context
		after = params.Context
	}

	// head_limit 解析与硬上限
	headLimit := params.HeadLimit
	if headLimit <= 0 {
		headLimit = t.config.MaxResults
	}
	if headLimit > t.config.HardMaxLimit {
		headLimit = t.config.HardMaxLimit
	}

	// 取本次调用的有效 resolver（ctx 注入的 workDir/allowDirs/cross 优先，仿 read）。
	r, err := t.cache.GetWithAllowDirs(common.WorkDirFromCtx(ctx), common.AllowDirsFromCtx(ctx), common.AllowCrossDirFromCtx(ctx))
	if err != nil {
		return common.ErrPathInvalid(err.Error()).Error(), nil
	}
	ws := r.Workspace()

	// 解析搜索根：目录或单文件
	searchPath := ws
	if params.Path != "" {
		resolved, err := r.Resolve(params.Path)
		if err != nil {
			return "", err
		}
		searchPath = resolved
	}
	info, err := os.Stat(searchPath)
	if err != nil {
		if os.IsNotExist(err) {
			return common.ErrFileNotFound(params.Path).Error(), nil
		}
		return "", fmt.Errorf("access path: %w", err)
	}

	// 优先 ripgrep（仅目录场景）；单文件或 rg 缺失走 Go 兜底
	// displayBase 用 searchPath（非 ws）：结果路径相对"本次搜索根"而非固定 workspace，
	// 避免 cross/allowDirs 搜索工作区外目录时出现 ../../ 丑陋相对路径；与 rg 默认行为一致。
	var matches []fileMatch
	if common.HasRipgrep() && info.IsDir() {
		matches, err = execRipgrep(ctx, searchPath, params.Pattern, params.Include, params.Exclude, outputMode == "content")
	} else {
		matches, err = goGrep(ctx, searchPath, re, params.Include, params.Exclude)
	}
	if err != nil {
		return common.NewErrorf(common.ErrCodeSearchFailed, "%v", err).Error(), nil
	}

	// mtime 排序（默认 true）
	sortByMtime := true
	if params.SortByMtime != nil {
		sortByMtime = *params.SortByMtime
	}
	if sortByMtime {
		sortMatchesByMtime(matches)
	}

	return t.format(matches, outputMode, before, after, headLimit), nil
}

// fileMatch 单个文件的匹配结果。
type fileMatch struct {
	relPath string
	lines   []string
	matched []int // 命中的 1-indexed 行号
	mtime   int64
}

// execRipgrep 调用系统 rg 获取命中行号；needLines=true（content 模式）时读文件行用于渲染，
// files_with_matches/count 跳过避免全量读。
func execRipgrep(ctx context.Context, searchPath, pattern, include, exclude string, needLines bool) ([]fileMatch, error) {
	args := []string{
		"--line-number", "--color=never", "--no-heading",
		"-e", pattern,
	}
	if include != "" {
		args = append(args, "-g", include)
	}
	if exclude != "" {
		args = append(args, "-g", "!"+exclude)
	}
	args = append(args, searchPath)

	cmd := exec.CommandContext(ctx, "rg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// 按绝对路径聚合命中行号
	byFile := map[string]*fileMatch{}
	order := []string{}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		absPath, lineNo, _, ok := splitRipgrepLine(line)
		if !ok {
			continue
		}
		fm, exists := byFile[absPath]
		if !exists {
			relPath, _ := filepath.Rel(searchPath, absPath)
			relPath = filepath.ToSlash(relPath)
			mtime := int64(0)
			if mi, e := os.Stat(absPath); e == nil {
				mtime = mi.ModTime().Unix()
			}
			fm = &fileMatch{relPath: relPath, mtime: mtime}
			byFile[absPath] = fm
			order = append(order, absPath)
		}
		fm.matched = append(fm.matched, lineNo)
	}
	// 忽略退出码：rg 在无匹配时返回 1，视为正常
	_ = cmd.Wait()

	result := make([]fileMatch, 0, len(order))
	for _, abs := range order {
		fm := *byFile[abs]
		if needLines {
			fm.lines = readLines(abs)
		}
		result = append(result, fm)
	}
	return result, nil
}

// readLines 读取文件全部行（失败返回 nil，格式化阶段会跳过）。
func readLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

// splitRipgrepLine 解析 rg 单行输出 "path:line:text"。
// 从右向左定位两个冒号分隔符，兼容 Windows 盘符路径（C:/dir:5:text）。
func splitRipgrepLine(line string) (path string, lineNo int, text string, ok bool) {
	// 找最后一个冒号（text 起点）
	idx2 := strings.LastIndex(line, ":")
	if idx2 < 0 {
		return "", 0, "", false
	}
	rest := line[:idx2]
	text = line[idx2+1:]
	// rest 形如 "path:line"
	idx1 := strings.LastIndex(rest, ":")
	if idx1 < 0 {
		return "", 0, "", false
	}
	path = rest[:idx1]
	lineStr := rest[idx1+1:]
	n := 0
	for _, c := range lineStr {
		if c < '0' || c > '9' {
			return "", 0, "", false
		}
		n = n*10 + int(c-'0')
	}
	return path, n, text, true
}

// goGrep 纯 Go 实现：WalkDir + regexp + 自实现 ** glob 匹配。
// 参考 tool/read/tool.go 的 matchWithDoubleStar/matchParts。
// relPath 相对 searchPath 渲染（即"本次搜索根"），与 rg 兜底路径输出一致。
func goGrep(ctx context.Context, searchPath string, re *regexp.Regexp, include, exclude string) ([]fileMatch, error) {
	// 预解析 glob 模式
	hasInclude := include != ""
	hasExclude := exclude != ""
	hasDoubleStarInc := strings.Contains(include, "**")
	hasDoubleStarExc := strings.Contains(exclude, "**")

	// 判断 searchPath 是文件还是目录
	info, err := os.Stat(searchPath)
	if err != nil {
		return nil, err
	}

	var result []fileMatch

	walkFn := func(path string, d os.DirEntry, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// 相对 searchPath 根的路径用于 glob 匹配
		rel, rerr := filepath.Rel(searchPath, path)
		if rerr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		// include/exclude 匹配（对完整 rel 路径）
		if hasInclude && !matchGlob(include, relSlash, hasDoubleStarInc) {
			return nil
		}
		if hasExclude && matchGlob(exclude, relSlash, hasDoubleStarExc) {
			return nil
		}

		// 读取并按行匹配
		f, ferr := os.Open(path)
		if ferr != nil {
			return nil
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var lines []string
		var matched []int
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			lines = append(lines, scanner.Text())
			if re.MatchString(scanner.Text()) {
				matched = append(matched, lineNo)
			}
		}
		if len(matched) == 0 {
			return nil
		}

		// relPath：相对 searchPath 的展示路径（本次搜索根）
		relPath, _ := filepath.Rel(searchPath, path)
		relPath = filepath.ToSlash(relPath)
		mtime := int64(0)
		if mi, e := d.Info(); e == nil {
			mtime = mi.ModTime().Unix()
		}
		result = append(result, fileMatch{
			relPath: relPath,
			lines:   lines,
			matched: matched,
			mtime:   mtime,
		})
		return nil
	}

	if info.IsDir() {
		err = filepath.WalkDir(searchPath, walkFn)
	} else {
		// 单文件：直接调用 walkFn（伪造 rel 为文件名）
		walkFn(searchPath, dirEntryFromFile(searchPath), nil)
	}
	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		// 忽略遍历过程中的单文件错误，继续返回已收集结果
	}
	return result, nil
}

// format 渲染最终输出，按 output_mode 分支，head_limit 截断，统一截断兜底。
func (t *grepTool) format(matches []fileMatch, outputMode string, before, after, headLimit int) string {
	var b strings.Builder
	totalFiles := len(matches)
	totalMatches := 0
	for _, m := range matches {
		totalMatches += len(m.matched)
	}
	if totalMatches == 0 {
		return "No matches found.\n"
	}

	// 计数与截断
	truncated := false
	switch outputMode {
	case "content":
		// 按"匹配行总数"计 head_limit
		allowedLines := headLimit
		b.WriteString(fmt.Sprintf("Found %d match(es) in %d file(s)", totalMatches, totalFiles))
		used := 0
		for _, m := range matches {
			if used >= allowedLines {
				truncated = true
				break
			}
			b.WriteString(fmt.Sprintf("\n%s:\n", m.relPath))
			for _, lineNo := range m.matched {
				if used >= allowedLines {
					truncated = true
					break
				}
				writeMatchWithCtx(&b, m.lines, lineNo, before, after)
				used++
			}
		}
	case "files_with_matches":
		// 按文件数计 head_limit
		b.WriteString(fmt.Sprintf("Found %d match(es) in %d file(s)", totalMatches, totalFiles))
		limit := headLimit
		if len(matches) > limit {
			truncated = true
		}
		shown := matches
		if len(matches) > limit {
			shown = matches[:limit]
		}
		for i, m := range shown {
			b.WriteString(fmt.Sprintf("\n%d. %s (%d match(es))", i+1, m.relPath, len(m.matched)))
		}
	case "count":
		b.WriteString(fmt.Sprintf("Found %d match(es) in %d file(s)", totalMatches, totalFiles))
		limit := headLimit
		if len(matches) > limit {
			truncated = true
		}
		shown := matches
		if len(matches) > limit {
			shown = matches[:limit]
		}
		for i, m := range shown {
			b.WriteString(fmt.Sprintf("\n%d. %s: %d", i+1, m.relPath, len(m.matched)))
		}
	}
	if truncated {
		b.WriteString(fmt.Sprintf("\n... (results limited, head_limit=%d)", headLimit))
	}
	b.WriteString("\n")

	// 最终输出过统一截断兜底
	maxLines := common.TruncateDefaultMaxLines
	tr := common.Truncate(b.String(), common.TruncateOptions{
		MaxLines:  &maxLines,
		Direction: common.TruncHead,
	})
	return tr.Content
}

// writeMatchWithCtx 写入一行匹配及其上下文（-A/-B/-C 语义，类似 grep 输出）。
func writeMatchWithCtx(w *strings.Builder, lines []string, lineNo, before, after int) {
	start := lineNo - before
	if start < 1 {
		start = 1
	}
	end := lineNo + after
	if end > len(lines) {
		end = len(lines)
	}
	for i := start; i <= end; i++ {
		marker := "  "
		if i == lineNo {
			marker = "> "
		}
		text := lines[i-1]
		if len(text) > maxLineLength {
			text = text[:maxLineLength] + "... [line-truncated]"
		}
		w.WriteString(fmt.Sprintf("%sLine %d: %s\n", marker, i, text))
	}
	if before > 0 || after > 0 {
		w.WriteString("---\n")
	}
}

// sortMatchesByMtime 按文件 mtime 降序（最近修改在前）。
func sortMatchesByMtime(matches []fileMatch) {
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].mtime > matches[j].mtime
	})
}

// matchGlob 自实现 glob 匹配（支持 **），grep include/exclude 语义。
// grep include 采用 ripgrep 语义：pattern 无 /（如 "*.go"）时匹配任意层级文件名，
// 与 glob 工具的"仅顶层"语义不同——因为 rg -g '*.go' 在所有目录递归生效。
// pattern 与 path 都已标准化为正斜杠。
func matchGlob(pattern, relPath string, hasDoubleStar bool) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if hasDoubleStar {
		return common.MatchWithDoubleStar(pattern, relPath)
	}
	// 无 ** 时优先匹配文件名；若 pattern 含 /，则匹配完整 rel 路径
	if strings.Contains(pattern, "/") {
		matched, _ := filepath.Match(pattern, relPath)
		return matched
	}
	matched, _ := filepath.Match(pattern, filepath.Base(relPath))
	return matched
}


// dirEntryFromFile 用 os.Stat 伪造一个 DirEntry，供单文件场景复用 walkFn。
type statDirEntry struct {
	info os.FileInfo
}

func (s statDirEntry) Name() string               { return s.info.Name() }
func (s statDirEntry) IsDir() bool                { return s.info.IsDir() }
func (s statDirEntry) Type() os.FileMode          { return s.info.Mode().Type() }
func (s statDirEntry) Info() (os.FileInfo, error) { return s.info, nil }

func dirEntryFromFile(path string) os.DirEntry {
	info, err := os.Stat(path)
	if err != nil {
		info = nil
	}
	return statDirEntry{info: info}
}

// Register registers the grep tool with custom configuration.
func Register(config Config) error {
	t, err := NewTool(config)
	if err != nil {
		return err
	}
	return aitool.Registry.Register(t)
}

// RegisterDefault registers with default configuration using simplified template.
func RegisterDefault() error {
	return aitool.RegisterTool(ToolName, "Grep (Search) - Lexical search across files (ripgrep-first, Go fallback)", DefaultConfig(), NewTool)
}

func init() {
	_ = RegisterDefault()
}
