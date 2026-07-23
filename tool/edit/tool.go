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
	"github.com/rulego/rulego-components-ai/session"
	aitool "github.com/rulego/rulego-components-ai/tool"
	"github.com/rulego/rulego-components-ai/tool/common"
	"github.com/rulego/rulego/utils/maps"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

const (
	// ToolName is the name of the edit tool.
	ToolName = "edit"

	// MaxRegexLength limits regex pattern length to prevent ReDoS attacks.
	MaxRegexLength = 1000
)

// fileLocks provide mutex locks by file path to prevent overlapping when editing the same file concurrently.
// TODO (Review C3): Currently resident by path, no LRU/no cleanup, and the server is always edited with a large number of different paths, which will slowly grow.
// No short-term impact (limited editing path for a single session); Long-term need to add limits + LRU elimination (container/list) or weak references.
var fileLocks sync.Map // map[string]*sync.Mutex

// lockFile locks the edit operation at the specified path and returns the unlock function.
func lockFile(path string) func() {
	actual, _ := fileLocks.LoadOrStore(path, &sync.Mutex{})
	mu := actual.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// isWriteOp checks whether a write operation is (line/search/insert/delete).
func isWriteOp(op string) bool {
	switch op {
	case "line", "search", "insert", "delete":
		return true
	}
	return false
}

// readBeforeEditScanWindow scans the most recent N session messages for read-before-edit checks.
const readBeforeEditScanWindow = 20

// checkReadBeforeEdit checks whether the recent messages of the session in ctx have a call to the read tool for the path.
// Returning a non-empty string means the current edit should be blocked (prompt to read first); Returning an empty string indicates release (including cases where sessions are missing or cannot be determined).
func checkReadBeforeEdit(ctx context.Context, targetPath string) string {
	if targetPath == "" {
		return ""
	}
	sess, ok := session.SessionFromContext(ctx)
	if !ok || sess == nil {
		// No session context (such as independent testing/direct call), skipping enforcement, and no blocking.
		return ""
	}
	msgs := sess.Messages
	if len(msgs) == 0 {
		return ""
	}
	// Only scan the most recent N messages to avoid long conversation overhead
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
			// After normalizing paths, the comparison is loosely matched with either basename or full path
			if sameReadPath(p, target) {
				return ""
			}
		}
	}
	return fmt.Sprintf("Error: READ_BEFORE_EDIT - read the file '%s' first (use the read tool with op=file) before editing it, so you can verify current content.", targetPath)
}

// extractPathFromReadArgs extracts the path field from the JSON arguments called by the read tool.
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

// sameReadPath compares whether two paths point to the same file (all normalized paths are equal).
// Note: Do not use basename for comparison—directories with the same name but different ones (e.g., two main.go) will bypass read-before-edit protection (review C4).
func sameReadPath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	// Normalization (cleanup)./.. / etc.); Different judgments when relative or absolute paths are inconsistent (better to be strict than relaxed, avoid bypassing basenames)
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

// editPathSecurity: Path security policy for the edit operation: hide files + exclude directory reads by global default (tpclaw config.yaml fileAccess).
// AllowHiddenFiles =!denyHidden (default false→ allows editing to hide, does not restrict agent); ExcludeDirs: Default version repository metadata.
func editPathSecurity() common.PathSecurityConfig {
	cfg := common.DefaultPathSecurityConfig()
	cfg.AllowHiddenFiles = !common.GetDefaultDenyHidden()
	cfg.ExcludeDirs = common.GetDefaultExcludeDirs() // Read the big picture; No return nil is set (not excluded); the default value is provided by config.yaml fileAccess
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

	// Take the valid resolver + workDir used this time (ctx injection first, otherwise config defaults).
	r, err := t.cache.GetWithAllowDirs(common.WorkDirFromCtx(ctx), common.AllowDirsFromCtx(ctx), common.AllowCrossDirFromCtx(ctx))
	if err != nil {
		return common.ErrPathInvalid(err.Error()).Error(), nil
	}
	effWd := r.Workspace()
	// Backup is constructed according to effWd (NewBackupManager has no background coroutine, and per-call construction is cheap).
	backup := common.NewBackupManager(effWd, t.config.MaxHistory)

	path, err := r.Resolve(params.Path)
	if err != nil {
		return "", err
	}

	// Get session key from context for session-isolated backups
	sessionKey, _ := session.SessionKeyFromContext(ctx)

	// Modify operations lock by file to prevent overlapping the same file during concurrent editing (list_backups read-only, no lock)
	if params.Operation != "list_backups" {
		defer lockFile(path)()
	}

	// Read Before Edit: Write operation if a session can be retrieved from ctx and its most recent N messages
	// If no read tool call is found for this path, prompt "Read the file first".
	// Limitation: Validation only when the session context can be acknowledged; Skips when a session is missing or cannot determine the path, without blocking execution.
	// TODO(session): Currently depends on SessionMessage.ToolCalls + Arguments JSON to reverse the path,
	// Weak cross-tool agreements; If the session package later provides a structured "Collection of Read" states, you can switch to lookup tables.
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
// Before writing, it sniffs the native line endings of old files (if they exist), unifying the new content to those lines to prevent Windows CRLF files from being corrupted.
func atomicWriteFile(path string, content []byte) error {
	// Sniff native line tailers: If an old file exists, use the old file; otherwise, use the content itself
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

// locateMatches returns search matching the row number to the row preview at the first limit in the content,
// Used to help agents clearly see match positions when uniqueness fails, deciding whether to provide a longer context or reread verification.
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

// suggestReread returns the prompt "Suggest rereading the file" to guide the agent to verify the actual content before trying again when editing fails.
func suggestReread() string {
	return " Suggestion: the file may have changed since you last read it, or the search string does not match exactly (whitespace/indentation/line-endings). Re-read the file (read op=file) to verify actual content before retrying."
}

// findClosestMatch scans the file when it matches 0, finding the 1-3 rows most similar to search as a hint.
// Only for error information amplification (Did you mean) does not affect apply. Similarity is approximated by the overlap length of case-insensitive substrings (no need for the Levenshtein library).
// Search splits the tokens by non-blank tokens, and the total length of each line of tokens hit is used as a similar score.
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
	// Top 3 highest scores (stable sort: descending by score, then ascending by line number)
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

// tokenizeForMatch splits the search string into lowercase tokens for similarity matching (retaining a length>=2 alphanumeric fragment).
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

// closestMatchHint formats the findClosestMatch result into a "Did you mean" prompt string.
func closestMatchHint(content, search string) string {
	hits := findClosestMatch(content, search)
	if len(hits) == 0 {
		return ""
	}
	return " Did you mean around:\n  " + strings.Join(hits, "\n  ")
}

// detectLineEnding detects the native line tail of the sniff file: CRLF preferentially returns "\r\n", otherwise "\n".
// Used to keep the file's native line ending during writing to prevent Windows CRLF files from being corrupted to LF.
func detectLineEnding(content []byte) string {
	// Count based on the actual line stub: Is the byte before \n \r?
	// You can't use bytes.Count(content, []byte("\r\n")))—it will misinterpret literal strings (such as the "\r\n" text in source code/documentation).
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

// normalizeLineEndings unifies the line endings of text as the target (CRLF or LF).
func normalizeLineEndings(text, target string) string {
	if target == "\n" {
		// Uniformly remove \r
		return strings.ReplaceAll(text, "\r\n", "\n")
	}
	// target == \r\n: First, refine the existing \r\n to \n, then uniformly add \r
	unified := strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(unified, "\n", "\r\n")
}

// withDiagnostics wraps and writes the operation results, attaches diagnostics (according to the registered provider; Not registered = closed by default).
func (t *editTool) withDiagnostics(path string, result string, err error) (string, error) {
	if err != nil {
		return result, err
	}
	return result + t.reportDiagnostics(path), nil
}

// After editing reportDiagnostics, run diagnostics: According to the registered DiagnosticProvider (unregistered returns empty = closed by default).
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

	// Meaningless replacement for foolproofing: does not execute search==replace to avoid wasting a round of idle gameplay.
	// Only literal mode is used to determine no-op: In regex mode, search is pattern, replace is template, and both strings are equal
	// This does not mean there are no changes (for example, search="(foo)" replace="(foo)" will change foo to (foo)).
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
			// Uniqueness verification: Non-global replacement requires searching for unique positions in the string; multiple matches are rejected to avoid correcting incorrect positions
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
			// Uniqueness verification: Non-global replacement requires searching for a unique position in the thread; multiple matches are rejected
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
		// Uniqueness verification: insert_after Errors occur when multiple matches occur in the file, avoiding insertion in the wrong position; Tips with longer anchors.
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
