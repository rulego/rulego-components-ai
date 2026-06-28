package skill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

// osBackend 基于 os 包实现 filesystem.Backend，供 skill middleware 从磁盘读取 SKILL.md。
//
// 背景：eino v0.9+ 起 einoskill.NewLocalBackend 被替换为 NewBackendFromFilesystem，
// 后者要求传入一个 filesystem.Backend；而 adk/filesystem 仅提供 InMemoryBackend，
// 没有读真实磁盘的实现。skill middleware 只用到 GlobInfo（查找 */SKILL.md）与
// Read（读取文件内容），因此这里仅正确实现这两个方法，其余返回 errOSBackendNotSupported。
type osBackend struct{}

// newOSBackend 创建基于本地文件系统的 backend。
func newOSBackend() *osBackend { return &osBackend{} }

// errOSBackendNotSupported 表示该方法在 osBackend 中未实现（skill 不会调用）。
var errOSBackendNotSupported = errors.New("osBackend: operation not supported")

// GlobInfo 按通配符匹配文件，返回匹配项信息。pattern 相对 base 解析（如 base=dir, pattern=*/SKILL.md）。
func (b *osBackend) GlobInfo(_ context.Context, req *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	if req == nil {
		return nil, errors.New("osBackend.GlobInfo: nil request")
	}
	base := req.Path
	if base == "" {
		base = "."
	}
	if req.Pattern == "" {
		return nil, errors.New("osBackend.GlobInfo: empty pattern")
	}
	matches, err := filepath.Glob(filepath.Join(base, req.Pattern))
	if err != nil {
		return nil, err
	}
	result := make([]filesystem.FileInfo, 0, len(matches))
	for _, m := range matches {
		info, statErr := os.Stat(m)
		if statErr != nil {
			continue
		}
		// 返回绝对路径。eino filesystem_backend 对非绝对路径会再拼一次 BaseDir，
		// 若返回带 base 前缀的相对路径（如 data/skills/x/SKILL.md）会被拼成
		// data/skills/data/skills/x/SKILL.md 导致读不到文件。
		abs, absErr := filepath.Abs(m)
		if absErr != nil {
			abs = m
		}
		result = append(result, filesystem.FileInfo{
			Path:       abs,
			IsDir:      info.IsDir(),
			Size:       info.Size(),
			ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

// Read 读取文件内容，支持 1-based 行号 Offset/Limit（Limit=0 表示读取全部）。
func (b *osBackend) Read(_ context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	if req == nil {
		return nil, errors.New("osBackend.Read: nil request")
	}
	data, err := os.ReadFile(req.FilePath)
	if err != nil {
		return nil, err
	}
	content := string(data)
	// 仅在显式指定 Offset/Limit 时按行切片，避免对全量内容做无谓的 Split。
	if req.Offset > 1 || req.Limit > 0 {
		lines := strings.Split(content, "\n")
		start := req.Offset - 1
		if start < 0 {
			start = 0
		}
		if start > len(lines) {
			start = len(lines)
		}
		end := len(lines)
		if req.Limit > 0 && start+req.Limit < end {
			end = start + req.Limit
		}
		content = strings.Join(lines[start:end], "\n")
	}
	return &filesystem.FileContent{Content: content}, nil
}

// LsInfo 未实现（skill 不使用）。
func (b *osBackend) LsInfo(_ context.Context, _ *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	return nil, errOSBackendNotSupported
}

// GrepRaw 未实现（skill 不使用）。
func (b *osBackend) GrepRaw(_ context.Context, _ *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	return nil, errOSBackendNotSupported
}

// Write 未实现（skill 只读）。
func (b *osBackend) Write(_ context.Context, _ *filesystem.WriteRequest) error {
	return errOSBackendNotSupported
}

// Edit 未实现（skill 只读）。
func (b *osBackend) Edit(_ context.Context, _ *filesystem.EditRequest) error {
	return errOSBackendNotSupported
}
