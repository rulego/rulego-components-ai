// Package grep provides a file content search tool, with ripgrep prioritized + Go as a backup.
package grep

import (
	"bufio"
	"bytes"
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

// Single-line truncation length limit to avoid over-long lines from polluting context.
const maxLineLength = 2000

// Default/Hard Limit.
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

// grepPathSecurity is the same as read: allows hidden files (code review requires access to.env/vendor, etc.),
// Excluding directory reading by global default (if not set, it is not excluded). Only the Resolve layer is effective, does not affect internal traversal of walk.
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
	Pattern       string `json:"pattern"`
	Path          string `json:"path"`
	Include       string `json:"include"`
	Exclude       string `json:"exclude"`
	OutputMode    string `json:"output_mode"`
	ContextAfter  int    `json:"-A"`
	ContextBefore int    `json:"-B"`
	Context       int    `json:"-C"`
	HeadLimit     int    `json:"head_limit"`
	SortByMtime   *bool  `json:"sort_by_mtime"`
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

	// Parse output mode
	outputMode := params.OutputMode
	if outputMode == "" {
		outputMode = "content"
	}
	switch outputMode {
	case "content", "files_with_matches", "count":
	default:
		outputMode = "content"
	}

	// Context line: -C is set simultaneously to override -A/-B
	before, after := params.ContextBefore, params.ContextAfter
	if params.Context > 0 {
		before = params.Context
		after = params.Context
	}

	// head_limit Analysis and hard upper limits
	headLimit := params.HeadLimit
	if headLimit <= 0 {
		headLimit = t.config.MaxResults
	}
	if headLimit > t.config.HardMaxLimit {
		headLimit = t.config.HardMaxLimit
	}

	// Take the valid resolver used this time (ctx injection workDir/allowDirs/cross preferred, simulating read).
	r, err := t.cache.GetWithAllowDirs(common.WorkDirFromCtx(ctx), common.AllowDirsFromCtx(ctx), common.AllowCrossDirFromCtx(ctx))
	if err != nil {
		return common.ErrPathInvalid(err.Error()).Error(), nil
	}
	ws := r.Workspace()

	// Parse and search the root: directory or single file
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

	// Prioritize ripgrep (directory only scene); If a single file or rg is missing, Go is the backup
	// displayBase uses searchPath (not ws): the result path is relative to the "current search root" rather than a fixed workspace,
	// Prevents cross/allowDirs from appearing when searching directories outside the workspace: /.. / Ugly relative path; Consistent with RG's default behavior.
	var matches []fileMatch
	if common.HasRipgrep() && info.IsDir() {
		matches, err = execRipgrep(ctx, searchPath, params.Pattern, params.Include, params.Exclude, outputMode == "content")
	} else {
		matches, err = goGrep(ctx, searchPath, re, params.Include, params.Exclude)
	}
	if err != nil {
		return common.NewErrorf(common.ErrCodeSearchFailed, "%v", err).Error(), nil
	}

	// mtime sort (default true by default)
	sortByMtime := true
	if params.SortByMtime != nil {
		sortByMtime = *params.SortByMtime
	}
	if sortByMtime {
		sortMatchesByMtime(matches)
	}

	return t.format(matches, outputMode, before, after, headLimit), nil
}

// fileMatch: The result of matching a single file.
type fileMatch struct {
	relPath string
	lines   []string
	matched []int // Hit 1-indexed line number
	mtime   int64
}

// execRipgrep calls the system rg to get the line number hit; When needLines=true(content mode) is used, the file line is read for rendering,
// files_with_matches/count skips to avoid full read.
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

	// Aggregate hit line numbers by absolute path
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
	// Ignore exit code: rg returns 1 when there is no match, considered normal
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

// readLines reads all lines of the file (returns nil on failure, which will skip the formatting phase).
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

// splitRipgrepLine parses rg single-line output "path:line:text".
// Positions two colon separators from right to left, compatible with Windows drive letter paths (C:/dir:5:text).
func splitRipgrepLine(line string) (path string, lineNo int, text string, ok bool) {
	// Find the last colon (text, starting point)
	idx2 := strings.LastIndex(line, ":")
	if idx2 < 0 {
		return "", 0, "", false
	}
	rest := line[:idx2]
	text = line[idx2+1:]
	// rest is as in "path:line"
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

// goGrep pure Go implementation: WalkDir + regexp + self-implementing ** glob matching.
// Refer to matchWithDoubleStar/matchParts in tool/read/tool.go.
// relPath renders relative to searchPath (i.e., "this search root"), and matches the output of the rg backup path.
func goGrep(ctx context.Context, searchPath string, re *regexp.Regexp, include, exclude string) ([]fileMatch, error) {
	// Pre-parse glob mode
	hasInclude := include != ""
	hasExclude := exclude != ""
	hasDoubleStarInc := strings.Contains(include, "**")
	hasDoubleStarExc := strings.Contains(exclude, "**")
	// .gitignore (valid only for directories; Single file path read failure returns nil)
	gitignore := common.LoadGitignore(searchPath)

	// Determine whether searchPath is a file or a directory
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
		// The path relative to the searchPath root is used for gitignore + glob matching
		rel, rerr := filepath.Rel(searchPath, path)
		if rerr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		// .gitignore: Skips the entire directory (SkipDir), skips the file
		if gitignore != nil && gitignore.Ignored(relSlash, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		// include/exclude matching (for full rel paths)
		if hasInclude && !matchGlob(include, relSlash, hasDoubleStarInc) {
			return nil
		}
		if hasExclude && matchGlob(exclude, relSlash, hasDoubleStarExc) {
			return nil
		}

		// Read and match by line
		f, ferr := os.Open(path)
		if ferr != nil {
			return nil
		}
		defer f.Close()
		// Binary check: The first 1024 bytes containing NUL are considered binary, skip (align with RG to avoid garbled text)
		if isBinaryFile(f) {
			return nil
		}
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

		// relPath: Display path relative to searchPath (this search root)
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
		// Single file: Directly call walkFn (forge rel as the filename)
		walkFn(searchPath, dirEntryFromFile(searchPath), nil)
	}
	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		// Single-file errors during traversal are ignored, and the collected results are returned
	}
	return result, nil
}

// isBinaryFile reads the first 1024 bytes to detect the NUL (binary flag), and seeks to return 0.
func isBinaryFile(f *os.File) bool {
	buf := make([]byte, 1024)
	n, _ := f.Read(buf)
	_, _ = f.Seek(0, 0)
	return bytes.IndexByte(buf[:n], 0) >= 0
}

// format renders the final output, branches output_mode, head_limit truncation, and uniformly truncates as a backup.
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

	// Counting and truncating
	truncated := false
	switch outputMode {
	case "content":
		// head_limit is calculated based on the "total number of matching rows."
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
		// head_limit counted by number of documents
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

	// In the end, the damage was taken over by the unified cutoff to secure the bottom
	maxLines := common.TruncateDefaultMaxLines
	tr := common.Truncate(b.String(), common.TruncateOptions{
		MaxLines:  &maxLines,
		Direction: common.TruncHead,
	})
	return tr.Content
}

// writeMatchWithCtx writes a line of matching and its context (-A/-B/-C semantics, similar to grep output).
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

// sortMatchesByMtime descends by file mtime (most recent modifications come first).
func sortMatchesByMtime(matches []fileMatch) {
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].mtime > matches[j].mtime
	})
}

// matchGlob self-implements glob matching (supports **), grep include/exclude semantics.
// grep include uses ripgrep semantics: pattern matches any level filename when /(e.g., "*.go") is missing,
// Unlike the "top-only" semantics of the glob tool—because rg -g '*.go' recuts recursively across all directories.
// Both pattern and path have been standardized to positive slashes.
func matchGlob(pattern, relPath string, hasDoubleStar bool) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if hasDoubleStar {
		return common.MatchWithDoubleStar(pattern, relPath)
	}
	// If there is no **, the file name is matched first; If the pattern contains /, it matches the complete rel path
	if strings.Contains(pattern, "/") {
		matched, _ := filepath.Match(pattern, relPath)
		return matched
	}
	matched, _ := filepath.Match(pattern, filepath.Base(relPath))
	return matched
}

// dirEntryFromFile using os.Stat forges a DirEntry to allow single-file scenarios to reuse walkFn.
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
