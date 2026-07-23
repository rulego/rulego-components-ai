// Package glob provides a file name matching tool; ripgrep -- files priority + Go is the backup.
package glob

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	aitool "github.com/rulego/rulego-components-ai/tool"
	"github.com/rulego/rulego-components-ai/tool/common"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

const ToolName = "glob"

const (
	defaultMaxResults = 100
	hardMaxResults    = 500
)

// Config holds glob tool configuration.
type Config struct {
	WorkDir      string `json:"workDir" label:"工作目录" desc:"匹配的默认工作目录"`
	MaxResults   int    `json:"maxResults" label:"最大结果数" desc:"单次返回最大文件数（head_limit 默认值）"`
	HardMaxLimit int    `json:"hardMaxLimit" label:"硬上限" desc:"head_limit 的硬上限"`
}

// DefaultConfig returns default configuration.
func DefaultConfig() Config {
	return Config{
		WorkDir:      ".",
		MaxResults:   defaultMaxResults,
		HardMaxLimit: hardMaxResults,
	}
}

type globTool struct {
	config Config
	cache  *common.ResolverCache
}

// globPathSecurity is the same as read/grep: allows file hiding and excludes global read from directories (not excluded if not set). Effective only on the Resolve layer.
func globPathSecurity() common.PathSecurityConfig {
	cfg := common.DefaultPathSecurityConfig()
	cfg.AllowHiddenFiles = true
	cfg.ExcludeDirs = common.GetDefaultExcludeDirs()
	return cfg
}

// NewTool creates a new glob tool.
func NewTool(config Config) (tool.BaseTool, error) {
	if config.MaxResults <= 0 {
		config.MaxResults = DefaultConfig().MaxResults
	}
	if config.HardMaxLimit <= 0 {
		config.HardMaxLimit = DefaultConfig().HardMaxLimit
	}

	resolver, err := common.NewSecurePathResolver(config.WorkDir, globPathSecurity())
	if err != nil {
		return nil, err
	}
	config.WorkDir = resolver.Workspace()

	cache, err := common.NewResolverCache(config.WorkDir, globPathSecurity())
	if err != nil {
		return nil, err
	}

	return &globTool{
		config: config,
		cache:  cache,
	}, nil
}

// Info returns tool information.
func (t *globTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	props := orderedmap.New[string, *jsonschema.Schema]()

	props.Set("pattern", &jsonschema.Schema{
		Type:        "string",
		Description: "Glob pattern, e.g. \"**/*.go\". Required.",
	})

	props.Set("path", &jsonschema.Schema{
		Type:        "string",
		Description: "Directory to search in (default: workspace).",
	})

	props.Set("head_limit", &jsonschema.Schema{
		Type:        "integer",
		Description: "Max number of files returned (default: 100, hard max: 500).",
	})

	props.Set("sort_by_mtime", &jsonschema.Schema{
		Type:        "boolean",
		Description: "Sort files by modification time descending (default: true).",
	})

	return &schema.ToolInfo{
		Name: ToolName,
		Desc: "Find files by glob pattern (ripgrep --files first, Go fallback).",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: props,
			Required:   []string{"pattern"},
		}),
	}, nil
}

// Params holds search parameters.
type Params struct {
	Pattern     string `json:"pattern"`
	Path        string `json:"path"`
	HeadLimit   int    `json:"head_limit"`
	SortByMtime *bool  `json:"sort_by_mtime"`
}

// fileEntry Matches a single file.
type fileEntry struct {
	relPath string
	mtime   int64
}

// InvokableRun executes the search.
func (t *globTool) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	var params Params
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	if params.Pattern == "" {
		return common.ErrQueryEmpty().Error(), nil
	}

	// Take the valid resolver used this time (ctx injection workDir/allowDirs/cross preferred, simulating read).
	r, err := t.cache.GetWithAllowDirs(common.WorkDirFromCtx(ctx), common.AllowDirsFromCtx(ctx), common.AllowCrossDirFromCtx(ctx))
	if err != nil {
		return common.ErrPathInvalid(err.Error()).Error(), nil
	}
	ws := r.Workspace()

	// Parsing search roots (must be directory)
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
	if !info.IsDir() {
		return common.ErrPathIsDirectory(params.Path).Error(), nil
	}

	// head_limit Analysis and hard upper limits
	headLimit := params.HeadLimit
	if headLimit <= 0 {
		headLimit = t.config.MaxResults
	}
	if headLimit > t.config.HardMaxLimit {
		headLimit = t.config.HardMaxLimit
	}

	// Prioritize ripgrep --files; if missing, use Go as a backup
	// displayBase uses searchPath (not ws): the result path is relative to the "current search root" rather than a fixed workspace,
	// Prevents cross/allowDirs from appearing when searching directories outside the workspace: /.. / Ugly relative path; Consistent with the default behavior of rg --files.
	var entries []fileEntry
	if common.HasRipgrep() {
		entries, err = execRipgrepFiles(ctx, searchPath, params.Pattern)
	} else {
		entries, err = goGlob(ctx, searchPath, params.Pattern)
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
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].mtime > entries[j].mtime
		})
	}

	return t.format(entries, headLimit), nil
}

// execRipgrepFiles calls rg --files <pattern>-g to collect line by line.
// relPath renders relative to searchPath (the root of this search), consistent with the default behavior of rg.
func execRipgrepFiles(ctx context.Context, searchPath, pattern string) ([]fileEntry, error) {
	args := []string{"--files", "--color=never"}
	args = append(args, "-g", pattern)
	args = append(args, searchPath)

	cmd := exec.CommandContext(ctx, "rg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var entries []fileEntry
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		absPath := scanner.Text()
		if absPath == "" {
			continue
		}
		relPath, _ := filepath.Rel(searchPath, absPath)
		relPath = filepath.ToSlash(relPath)
		mtime := int64(0)
		if mi, e := os.Stat(absPath); e == nil {
			mtime = mi.ModTime().Unix()
		}
		entries = append(entries, fileEntry{relPath: relPath, mtime: mtime})
	}
	_ = cmd.Wait()
	return entries, nil
}

// goGlob pure Go implementation: WalkDir + self-implementing ** glob matching.
// relPath is rendered relative to searchPath (the root of this search), and the output is consistent with the backup path output of rg --files.
func goGlob(ctx context.Context, searchPath, pattern string) ([]fileEntry, error) {
	hasDoubleStar := strings.Contains(pattern, "**")
	gitignore := common.LoadGitignore(searchPath)
	var entries []fileEntry

	err := filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(searchPath, path)
		if rerr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		// .gitignore: SkipDir directory, skips files
		if gitignore != nil && gitignore.Ignored(relSlash, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !matchGlob(pattern, relSlash, hasDoubleStar) {
			return nil
		}
		relPath, _ := filepath.Rel(searchPath, path)
		relPath = filepath.ToSlash(relPath)
		mtime := int64(0)
		if mi, e := d.Info(); e == nil {
			mtime = mi.ModTime().Unix()
		}
		entries = append(entries, fileEntry{relPath: relPath, mtime: mtime})
		return nil
	})
	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		// Single-file errors during traversal are ignored
	}
	return entries, nil
}

// format rendering output, head_limit truncation, uniformly truncating as a backup.
func (t *globTool) format(entries []fileEntry, headLimit int) string {
	var b strings.Builder
	truncated := false
	shown := entries
	if len(entries) > headLimit {
		shown = entries[:headLimit]
		truncated = true
	}
	b.WriteString(fmt.Sprintf("Found %d file(s)", len(entries)))
	if truncated {
		b.WriteString(fmt.Sprintf(" (showing first %d, head_limit=%d)", len(shown), headLimit))
	}
	b.WriteString("\n---\n")
	for i, e := range shown {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, e.relPath))
	}

	maxLines := common.TruncateDefaultMaxLines
	tr := common.Truncate(b.String(), common.TruncateOptions{
		MaxLines:  &maxLines,
		Direction: common.TruncHead,
	})
	return tr.Content
}

// matchGlob self-implements glob matching (supports **), semantic consistency with read/grep tools.
// Convention: When pattern does not include / and does not ** (e.g., "*.go"), only matches top-level files (relPath does not include /).
// pattern contains
func matchGlob(pattern, relPath string, hasDoubleStar bool) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if hasDoubleStar {
		return common.MatchWithDoubleStar(pattern, relPath)
	}
	if strings.Contains(pattern, "/") {
		matched, _ := filepath.Match(pattern, relPath)
		return matched
	}
	// Pure filename mode: matches only the top layer
	if strings.Contains(relPath, "/") {
		return false
	}
	matched, _ := filepath.Match(pattern, relPath)
	return matched
}

// Register registers the glob tool with custom configuration.
func Register(config Config) error {
	t, err := NewTool(config)
	if err != nil {
		return err
	}
	return aitool.Registry.Register(t)
}

// RegisterDefault registers with default configuration using simplified template.
func RegisterDefault() error {
	return aitool.RegisterTool(ToolName, "Glob (Find Files) - Match files by glob pattern (ripgrep --files first, Go fallback)", DefaultConfig(), NewTool)
}

func init() {
	_ = RegisterDefault()
}
