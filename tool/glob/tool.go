// Package glob 提供文件名匹配工具（对标 rg --files / fd），ripgrep --files 优先 + Go 兜底。
// 设计依据：docs/plans/工具层优化方案.md §3.2。
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

// globPathSecurity 与 read/grep 一致：允许隐藏文件，排除目录读全局默认（未设则不排除）。仅 Resolve 层生效。
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

// fileEntry 单个匹配文件。
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

	// 取本次调用的有效 resolver（ctx 注入的 workDir/allowDirs/cross 优先，仿 read）。
	r, err := t.cache.GetWithAllowDirs(common.WorkDirFromCtx(ctx), common.AllowDirsFromCtx(ctx), common.AllowCrossDirFromCtx(ctx))
	if err != nil {
		return common.ErrPathInvalid(err.Error()).Error(), nil
	}
	ws := r.Workspace()

	// 解析搜索根（必须目录）
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

	// head_limit 解析与硬上限
	headLimit := params.HeadLimit
	if headLimit <= 0 {
		headLimit = t.config.MaxResults
	}
	if headLimit > t.config.HardMaxLimit {
		headLimit = t.config.HardMaxLimit
	}

	// 优先 ripgrep --files，缺失走 Go 兜底
	// displayBase 用 searchPath（非 ws）：结果路径相对"本次搜索根"而非固定 workspace，
	// 避免 cross/allowDirs 搜索工作区外目录时出现 ../../ 丑陋相对路径；与 rg --files 默认行为一致。
	var entries []fileEntry
	if common.HasRipgrep() {
		entries, err = execRipgrepFiles(ctx, searchPath, params.Pattern)
	} else {
		entries, err = goGlob(ctx, searchPath, params.Pattern)
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
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].mtime > entries[j].mtime
		})
	}

	return t.format(entries, headLimit), nil
}

// execRipgrepFiles 调用 rg --files -g <pattern>，逐行收集。
// relPath 相对 searchPath（本次搜索根）渲染，与 rg 默认行为一致。
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

// goGlob 纯 Go 实现：WalkDir + 自实现 ** glob 匹配。
// relPath 相对 searchPath（本次搜索根）渲染，与 rg --files 兜底路径输出一致。
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
		// .gitignore：目录 SkipDir，文件跳过
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
		// 忽略遍历过程中的单文件错误
	}
	return entries, nil
}

// format 渲染输出，head_limit 截断，统一截断兜底。
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

// matchGlob 自实现 glob 匹配（支持 **），与 read/grep 工具语义一致。
// 约定：pattern 不含 / 且不含 ** 时（如 "*.go"），仅匹配顶层文件（relPath 不含 /）。
// pattern 含 / 时按完整 rel 路径匹配；含 ** 时走递归匹配。
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
	// 纯文件名模式：仅匹配顶层
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
