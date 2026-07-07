// Package write provides a writing tool for AI agents.
package write

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	aitool "github.com/rulego/rulego-components-ai/tool"
	"github.com/rulego/rulego-components-ai/tool/common"
	"github.com/rulego/rulego/utils/maps"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

const ToolName = "write"

// Config holds write tool configuration.
type Config struct {
	WorkDir     string `json:"workDir" label:"工作目录" desc:"文件操作的默认工作目录"`
	MaxFileSize int64  `json:"maxFileSize" label:"最大文件大小" desc:"单次写入最大字节数，0表示不限制"`
}

// DefaultConfig returns default configuration.
func DefaultConfig() Config {
	return Config{
		WorkDir:     ".",
		MaxFileSize: 10 * 1024 * 1024, // 默认 10MB 上限
	}
}

type writeTool struct {
	config Config
	cache  *common.ResolverCache
}

// writePathSecurity 写入操作的路径安全策略：隐藏文件 + 排除目录均读全局默认（tpclaw config.yaml fileAccess）。
// AllowHiddenFiles = !denyHidden（默认 false→允许写隐藏，不限制 agent）；ExcludeDirs 默认版本库元数据。
func writePathSecurity() common.PathSecurityConfig {
	cfg := common.DefaultPathSecurityConfig()
	cfg.AllowHiddenFiles = !common.GetDefaultDenyHidden()
	cfg.ExcludeDirs = common.GetDefaultExcludeDirs() // 读全局；未设返回 nil（不排除），默认值由 config.yaml fileAccess 给
	return cfg
}

// NewTool creates a new write tool.
func NewTool(config Config) (tool.BaseTool, error) {
	sec := writePathSecurity()
	resolver, err := common.NewSecurePathResolver(config.WorkDir, sec)
	if err != nil {
		return nil, err
	}
	config.WorkDir = resolver.Workspace()

	cache, err := common.NewResolverCache(config.WorkDir, sec)
	if err != nil {
		return nil, err
	}

	return &writeTool{
		config: config,
		cache:  cache,
	}, nil
}

// Info returns tool information.
func (t *writeTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	props := orderedmap.New[string, *jsonschema.Schema]()

	props.Set("operation", &jsonschema.Schema{
		Type:        "string",
		Description: "Operation type: file (write to file)",
		Enum:        []any{"file"},
	})

	props.Set("path", &jsonschema.Schema{
		Type:        "string",
		Description: "File path",
	})

	props.Set("content", &jsonschema.Schema{
		Type:        "string",
		Description: "Content to write",
	})

	props.Set("mode", &jsonschema.Schema{
		Type:        "string",
		Description: "Write mode: create (new file), overwrite (replace), append (add to end)",
		Enum:        []any{"create", "overwrite", "append"},
	})

	return &schema.ToolInfo{
		Name: ToolName,
		Desc: "Write content to files. Supports create, overwrite, and append modes.",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: props,
			Required:   []string{"operation", "path", "content"},
		}),
	}, nil
}

// OperationParams holds operation parameters.
type OperationParams struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Mode      string `json:"mode"`
}

// InvokableRun executes the operation.
func (t *writeTool) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	var params OperationParams
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	if params.Path == "" {
		return common.ErrPathEmpty().Error(), nil
	}

	if params.Content == "" {
		return common.ErrContentEmpty().Error(), nil
	}

	// Check file size limit
	if t.config.MaxFileSize > 0 && int64(len(params.Content)) > t.config.MaxFileSize {
		return common.ErrFileTooLarge(int64(len(params.Content)), t.config.MaxFileSize).Error(), nil
	}

	// 取本次调用的有效 resolver + workDir（ctx 注入优先，否则 config 默认）。
	r, err := t.cache.GetWithAllowDirs(common.WorkDirFromCtx(ctx), common.AllowDirsFromCtx(ctx), common.AllowCrossDirFromCtx(ctx))
	if err != nil {
		return common.ErrPathInvalid(err.Error()).Error(), nil
	}
	effWd := r.Workspace()

	// Ensure workspace exists
	if err := common.EnsureDir(effWd); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}

	return t.writeFile(params, r)
}

// writeFile writes to a file.
func (t *writeTool) writeFile(params OperationParams, r *common.SecurePathResolver) (string, error) {
	path, err := r.Resolve(params.Path)
	if err != nil {
		return "", err
	}

	// Check if path is a directory
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return common.ErrPathIsDirectory(params.Path).Error(), nil
	}

	// Ensure directory exists
	if err := common.EnsureParentDir(path); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	mode := params.Mode
	if mode == "" {
		mode = "create"
	}

	var content []byte
	var message string

	switch mode {
	case "overwrite":
		content = []byte(params.Content)
		message = fmt.Sprintf("Overwrote %d bytes to %s", len(content), params.Path)

	case "append":
		existing, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("read existing file: %w", err)
		}

		existingStr := string(existing)
		if len(existingStr) > 0 && !strings.HasSuffix(existingStr, "\n") {
			existingStr += "\n"
		}

		// Add separator with timestamp
		separator := fmt.Sprintf("\n---\nTime: %s\n---\n", common.FormatTime())
		content = []byte(existingStr + separator + params.Content + "\n")
		message = fmt.Sprintf("Appended %d bytes to %s", len(params.Content), params.Path)

	default: // create
		if _, err := os.Stat(path); err == nil {
			return common.ErrFileExists(params.Path).Error(), nil
		}
		content = []byte(params.Content)
		message = fmt.Sprintf("Created %s (%d bytes)", params.Path, len(content))
	}

	// Use atomic write: write to temp file first, then rename
	if err := atomicWriteFile(path, content); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return common.Success(message), nil
}

// atomicWriteFile writes data to a file atomically using temp file + rename pattern.
// This prevents data corruption if the write operation is interrupted.
func atomicWriteFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".write-tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	// Ensure cleanup on failure
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(content); err != nil {
		tmpFile.Close()
		return err
	}

	if err := tmpFile.Chmod(0644); err != nil {
		tmpFile.Close()
		return err
	}

	if err := tmpFile.Close(); err != nil {
		return err
	}

	// Rename is atomic on most filesystems
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}

	success = true
	return nil
}

// Register registers the write tool with custom configuration.
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
		Desc:   "Write (Create) - Write content to files",
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
