// Package edit provides a unified editing tool for AI agents.
// It supports line-level editing, search-replace, and patch operations.
package edit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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

// fileLocks 提供按文件路径的互斥锁，防止并发编辑同一文件时相互覆盖。
// TODO(审查C3): 当前按路径常驻、无 LRU/无清理，长驻 server 编辑海量不同路径会缓慢增长。
// 短期不影响（单次会话编辑路径有限）；长期需加上限 + LRU 淘汰（container/list）或弱引用。
var fileLocks sync.Map // map[string]*sync.Mutex

// lockFile 锁定指定路径的编辑操作，返回解锁函数。
func lockFile(path string) func() {
	actual, _ := fileLocks.LoadOrStore(path, &sync.Mutex{})
	mu := actual.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// isWriteOp 判断是否为写操作（line/search/insert/delete）。
func isWriteOp(op string) bool {
	switch op {
	case "line", "search", "insert", "delete":
		return true
	}
	return false
}

// readBeforeEditScanWindow 扫描最近 N 条 session 消息用于 read-before-edit 检查。
const readBeforeEditScanWindow = 20

// checkReadBeforeEdit 检查 ctx 内 session 最近消息中是否有针对 path 的 read 工具调用。
// 返回非空字符串表示应阻止本次编辑（提示先读取）；返回空串表示放行（含 session 缺失/无法判定的情况）。
func checkReadBeforeEdit(ctx context.Context, targetPath string) string {
	if targetPath == "" {
		return ""
	}
	sess, ok := session.SessionFromContext(ctx)
	if !ok || sess == nil {
		// 无 session 上下文（如独立测试/直接调用），跳过强制，不阻塞。
		return ""
	}
	msgs := sess.Messages
	if len(msgs) == 0 {
		return ""
	}
	// 只扫最近 N 条，避免长会话开销
	start := len(msgs) - readBeforeEditScanWindow
	if start < 0 {
		start = 0
	}
	target := strings.TrimSpace(targetPath)
	for i := start; i < len(msgs); i++ {
		m := msgs[i]
		if m == nil {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.Name != "read" {
				continue
			}
			p := extractPathFromReadArgs(tc.Arguments)
			if p == "" {
				continue
			}
			// 路径规范化后比较，宽松匹配 basename 或全路径
			if sameReadPath(p, target) {
				return ""
			}
		}
	}
	return fmt.Sprintf("Error: READ_BEFORE_EDIT - read the file '%s' first (use the read tool with op=file) before editing it, so you can verify current content.", targetPath)
}

// extractPathFromReadArgs 从 read 工具调用的 JSON arguments 中提取 path 字段。
type readArgsShape struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
}

func extractPathFromReadArgs(arguments string) string {
	if arguments == "" {
		return ""
	}
	var a readArgsShape
	if err := json.Unmarshal([]byte(arguments), &a); err != nil {
		return ""
	}
	return strings.TrimSpace(a.Path)
}

// sameReadPath 比较两个路径是否指向同一文件（归一化后全路径相等）。
// 注意：不用 basename 比较——同名不同目录（如两个 main.go）会绕过 read-before-edit 保护（审查 C4）。
func sameReadPath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	// 归一化（清理 ./ ../ 等）；相对/绝对路径不一致时判不同（宁严勿松，避免 basename 绕过）
	return filepath.Clean(a) == filepath.Clean(b)
}

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
	config Config
	cache  *common.ResolverCache
}

// editPathSecurity 编辑操作的路径安全策略：隐藏文件 + 排除目录均读全局默认（tpclaw config.yaml fileAccess）。
// AllowHiddenFiles = !denyHidden（默认 false→允许编辑隐藏，不限制 agent）；ExcludeDirs 默认版本库元数据。
func editPathSecurity() common.PathSecurityConfig {
	cfg := common.DefaultPathSecurityConfig()
	cfg.AllowHiddenFiles = !common.GetDefaultDenyHidden()
	cfg.ExcludeDirs = common.GetDefaultExcludeDirs() // 读全局；未设返回 nil（不排除），默认值由 config.yaml fileAccess 给
	return cfg
}

// NewTool creates a new edit tool.
func NewTool(config Config) (tool.BaseTool, error) {
	if config.MaxHistory <= 0 {
		config.MaxHistory = DefaultConfig().MaxHistory
	}

	sec := editPathSecurity()
	resolver, err := common.NewSecurePathResolver(config.WorkDir, sec)
	if err != nil {
		return nil, err
	}
	config.WorkDir = resolver.Workspace()

	cache, err := common.NewResolverCache(config.WorkDir, sec)
	if err != nil {
		return nil, err
	}

	return &editTool{
		config: config,
		cache:  cache,
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

	// 取本次调用的有效 resolver + workDir（ctx 注入优先，否则 config 默认）。
	r, err := t.cache.GetWithAllowDirs(common.WorkDirFromCtx(ctx), common.AllowDirsFromCtx(ctx), common.AllowCrossDirFromCtx(ctx))
	if err != nil {
		return common.ErrPathInvalid(err.Error()).Error(), nil
	}
	effWd := r.Workspace()
	// backup 按 effWd 构造（NewBackupManager 无后台协程，per-call 构造廉价）。
	backup := common.NewBackupManager(effWd, t.config.MaxHistory)

	path, err := r.Resolve(params.Path)
	if err != nil {
		return "", err
	}

	// Get session key from context for session-isolated backups
	sessionKey, _ := session.SessionKeyFromContext(ctx)

	// 修改类操作按文件加锁，防止并发编辑同一文件相互覆盖（list_backups 只读不需锁）
	if params.Operation != "list_backups" {
		defer lockFile(path)()
	}

	// 先 Read 强制（read-before-edit）：对写操作，若能从 ctx 取到 session 且其最近 N 条消息中
	// 未发现针对该 path 的 read 工具调用，则提示 "Read the file first"。
	// 限制：仅在能确认 session 上下文时校验；session 缺失或无法判定 path 时跳过，不阻塞执行。
	// TODO(session)：当前依赖 SessionMessage.ToolCalls + Arguments JSON 反推 path，
	// 跨工具约定较弱；后续若 session 包提供结构化的"已读文件集合"状态，改为查表即可。
	if isWriteOp(params.Operation) {
		if msg := checkReadBeforeEdit(ctx, params.Path); msg != "" {
			return msg, nil
		}
	}

	// Handle operations that don't require file to exist
	switch params.Operation {
	case "list_backups":
		return t.listBackups(path, params, sessionKey, backup)
	case "restore":
		return t.editRestore(path, params, sessionKey, backup)
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
		rr, e := t.editLine(path, params, sessionKey, backup)
		return t.withDiagnostics(path, rr, e)
	case "search":
		rr, e := t.editSearch(path, params, sessionKey, backup)
		return t.withDiagnostics(path, rr, e)
	case "insert":
		rr, e := t.editInsert(path, params, sessionKey, backup)
		return t.withDiagnostics(path, rr, e)
	case "delete":
		rr, e := t.editDelete(path, params, sessionKey, backup)
		return t.withDiagnostics(path, rr, e)
	default:
		return common.ErrOperationNotSupported(params.Operation).Error(), nil
	}
}

// atomicWriteFile writes content to a file atomically using temp file + rename.
// This prevents data corruption if the write operation is interrupted.
// 写入前会嗅探旧文件（若存在）的原生行尾，把新内容统一为该行尾，避免 Windows CRLF 文件被破坏。
func atomicWriteFile(path string, content []byte) error {
	// 嗅探原生行尾：旧文件存在则按旧文件，否则按 content 自身
	eol := "\n"
	if old, err := os.ReadFile(path); err == nil && len(old) > 0 {
		eol = detectLineEnding(old)
	} else if bytes.IndexByte(content, '\r') >= 0 {
		eol = detectLineEnding(content)
	}
	normalized := []byte(normalizeLineEndings(string(content), eol))

	tmpPath := path + ".tmp"

	// Write to temp file first
	if err := os.WriteFile(tmpPath, normalized, 0644); err != nil {
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
func (t *editTool) editLine(path string, params OperationParams, sessionId string, backup *common.BackupManager) (string, error) {
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
	backupPath, version, err := backup.Backup(path, sessionId)
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

// locateMatches 返回 search 在 content 中前 limit 处匹配所在行号与行预览，
// 用于唯一性失败时帮助 agent 看清匹配位置，从而决定是提供更长 context 还是重新读取核对。
func locateMatches(content, search string, limit int) string {
	if search == "" || limit <= 0 {
		return ""
	}
	var found []string
	for i, ln := range strings.Split(content, "\n") {
		if len(found) >= limit {
			break
		}
		if strings.Contains(ln, search) {
			found = append(found, fmt.Sprintf("  line %d: %s", i+1, common.TruncateString(strings.TrimSpace(ln), 100)))
		}
	}
	if len(found) == 0 {
		return ""
	}
	return strings.Join(found, "\n")
}

// suggestReread 返回"建议重新读取文件"的提示，用于编辑失败时引导 agent 先核对实际内容再重试。
func suggestReread() string {
	return " Suggestion: the file may have changed since you last read it, or the search string does not match exactly (whitespace/indentation/line-endings). Re-read the file (read op=file) to verify actual content before retrying."
}

// findClosestMatch 在 0 匹配时扫描文件，找出与 search 最相似的 1-3 行作为提示。
// 仅作错误信息增强（Did you mean），不影响 apply。相似度用大小写不敏感的子串重叠长度近似（无需 Levenshtein 库）。
// search 会按非空白 token 拆分，对每行累计命中 token 的总长度作为相似得分。
func findClosestMatch(content, search string) []string {
	const maxHits = 3
	const minScore = 3
	tokens := tokenizeForMatch(search)
	if len(tokens) == 0 {
		return nil
	}
	type cand struct {
		lineNo int
		text   string
		score  int
	}
	var cands []cand
	for i, ln := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		score := 0
		for _, tok := range tokens {
			if strings.Contains(lower, tok) {
				score += len(tok)
			}
		}
		if score >= minScore {
			cands = append(cands, cand{lineNo: i + 1, text: trimmed, score: score})
		}
	}
	// 取得分最高的前 3 个（稳定排序：先按分数降序，再按行号升序）
	for i := 1; i < len(cands); i++ {
		for j := i; j > 0 && (cands[j].score > cands[j-1].score); j-- {
			cands[j], cands[j-1] = cands[j-1], cands[j]
		}
	}
	if len(cands) > maxHits {
		cands = cands[:maxHits]
	}
	var out []string
	for _, c := range cands {
		out = append(out, fmt.Sprintf("line %d: %s", c.lineNo, common.TruncateString(c.text, 2000)))
	}
	return out
}

// tokenizeForMatch 把搜索串拆成用于相似度匹配的小写 token（保留长度>=2 的字母数字片段）。
func tokenizeForMatch(s string) []string {
	var toks []string
	for _, field := range strings.Fields(s) {
		f := strings.ToLower(strings.Trim(field, ".,;:()[]{}\"'`"))
		if len(f) >= 2 {
			toks = append(toks, f)
		}
	}
	return toks
}

// closestMatchHint 把 findClosestMatch 结果格式化成 "Did you mean" 提示串。
func closestMatchHint(content, search string) string {
	hits := findClosestMatch(content, search)
	if len(hits) == 0 {
		return ""
	}
	return " Did you mean around:\n  " + strings.Join(hits, "\n  ")
}

// detectLineEnding 嗅探文件原生行尾：CRLF 占优返回 "\r\n"，否则 "\n"。
// 用于写入时保持文件原生行尾，避免 Windows CRLF 文件被破坏成 LF。
func detectLineEnding(content []byte) string {
	// 按真正行尾位置统计：\n 的前一字节是否为 \r。
	// 不能用 bytes.Count(content, []byte("\r\n"))——它会误判字面字符串（如源码/文档里的 "\r\n" 文本）。
	crlf := 0
	lf := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			if i > 0 && content[i-1] == '\r' {
				crlf++
			} else {
				lf++
			}
		}
	}
	if crlf > lf {
		return "\r\n"
	}
	return "\n"
}

// normalizeLineEndings 把 text 的行尾统一为 target（CRLF 或 LF）。
func normalizeLineEndings(text, target string) string {
	if target == "\n" {
		// 统一去掉 \r
		return strings.ReplaceAll(text, "\r\n", "\n")
	}
	// target == \r\n：先把已有的 \r\n 规整成 \n，再统一加 \r
	unified := strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(unified, "\n", "\r\n")
}

// withDiagnostics 包装写操作结果，附加诊断（按注册的 provider；未注册=默认关闭）。
func (t *editTool) withDiagnostics(path string, result string, err error) (string, error) {
	if err != nil {
		return result, err
	}
	return result + t.reportDiagnostics(path), nil
}

// reportDiagnostics 编辑后跑诊断：按注册的 DiagnosticProvider（未注册返回空=默认关闭）。
func (t *editTool) reportDiagnostics(path string) string {
	p := common.LookupDiagnosticProvider(path)
	if p == nil {
		return ""
	}
	diags, err := p.Report(path)
	if err != nil || len(diags) == 0 {
		return ""
	}
	report := common.DiagnosticReport(path, diags, 10)
	if report == "" {
		return ""
	}
	return "\n\nLSP errors detected in this file, please fix:\n" + report
}

// editSearch performs search and replace.
func (t *editTool) editSearch(path string, params OperationParams, sessionId string, backup *common.BackupManager) (string, error) {
	if params.Search == "" {
		return common.ErrSearchEmpty().Error(), nil
	}

	// 无意义替换防呆：search==replace 时不执行，避免空转浪费一轮。
	// 仅 literal 模式判 no-op：regex 模式下 search 是 pattern、replace 是模板，二者字符串相等
	// 不代表无改动（如 search="(foo)" replace="(foo)" 会把 foo 改成 (foo)）。
	if !params.UseRegex && params.Search == params.Replace {
		return "Error: NO_CHANGE - search and replace are identical, nothing to change. 若无需修改请直接结束，不要做无意义替换。", nil
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

		matches := re.FindAllStringIndex(contentStr, -1)
		if params.Global {
			// Replace all matches
			replaceCount = len(matches)
			contentStr = re.ReplaceAllString(contentStr, params.Replace)
		} else {
			// 唯一性校验：非全局替换要求搜索串唯一定位，多处匹配则拒绝，避免改错位置
			if len(matches) == 0 {
				// no match, replaceCount stays 0
			} else if len(matches) > 1 {
				return common.ErrSearchNotUnique(len(matches)).Error(), nil
			} else {
				loc := matches[0]
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
			// 唯一性校验：非全局替换要求搜索串唯一定位，多处匹配则拒绝
			count := strings.Count(contentStr, params.Search)
			if count == 0 {
				// no match, replaceCount stays 0
			} else if count > 1 {
				return fmt.Sprintf("Error: SEARCH_NOT_UNIQUE - found %d matches. Provide a longer search string to uniquely locate, or set global=true to replace all. First matches at:\n%s", count, locateMatches(contentStr, params.Search, 3)), nil
			} else {
				contentStr = strings.Replace(contentStr, params.Search, params.Replace, 1)
				replaceCount = 1
			}
		}
	}

	if replaceCount == 0 {
		return fmt.Sprintf("No matches found for search string (len=%d): %s.%s%s",
			len(params.Search), common.TruncateString(params.Search, 80), suggestReread(),
			closestMatchHint(contentStr, params.Search)), nil
	}

	// Create backup before editing
	backupPath, version, err := backup.Backup(path, sessionId)
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
func (t *editTool) editInsert(path string, params OperationParams, sessionId string, backup *common.BackupManager) (string, error) {
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
		// 唯一性校验：insert_after 在文件中多处匹配时报错，避免插到错误位置；提示用更长的锚点。
		if count := strings.Count(contentStr, params.InsertAfter); count > 1 {
			return fmt.Sprintf("Error: INSERT_ANCHOR_NOT_UNIQUE - insert_after found %d matches. Provide a longer insert_after anchor to uniquely locate. First matches at:\n%s",
				count, locateMatches(contentStr, params.InsertAfter, 3)), nil
		}
		if strings.Contains(contentStr, params.InsertAfter) {
			contentStr = strings.Replace(contentStr, params.InsertAfter, params.InsertAfter+"\n"+params.NewContent, 1)
			inserted = true
			insertPos = "after: " + common.TruncateString(params.InsertAfter, 30)
		}
	} else if params.InsertBefore != "" {
		if count := strings.Count(contentStr, params.InsertBefore); count > 1 {
			return fmt.Sprintf("Error: INSERT_ANCHOR_NOT_UNIQUE - insert_before found %d matches. Provide a longer insert_before anchor to uniquely locate. First matches at:\n%s",
				count, locateMatches(contentStr, params.InsertBefore, 3)), nil
		}
		if strings.Contains(contentStr, params.InsertBefore) {
			contentStr = strings.Replace(contentStr, params.InsertBefore, params.NewContent+"\n"+params.InsertBefore, 1)
			inserted = true
			insertPos = "before: " + common.TruncateString(params.InsertBefore, 30)
		}
	}

	if !inserted {
		anchor := params.InsertAfter
		if anchor == "" {
			anchor = params.InsertBefore
		}
		return fmt.Sprintf("Error: %s.%s%s",
			common.ErrInsertPosNotFound().Error(), suggestReread(),
			closestMatchHint(contentStr, anchor)), nil
	}

	// Create backup before editing
	backupPath, version, err := backup.Backup(path, sessionId)
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
func (t *editTool) editDelete(path string, params OperationParams, sessionId string, backup *common.BackupManager) (string, error) {
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
	backupPath, version, err := backup.Backup(path, sessionId)
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
func (t *editTool) listBackups(path string, params OperationParams, sessionId string, backup *common.BackupManager) (string, error) {
	backups, err := backup.ListBackups(path, sessionId)
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
func (t *editTool) editRestore(path string, params OperationParams, sessionId string, backup *common.BackupManager) (string, error) {
	if params.Version <= 0 {
		return common.ErrVersionInvalid().Error(), nil
	}

	if err := backup.Restore(path, params.Version, sessionId); err != nil {
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
