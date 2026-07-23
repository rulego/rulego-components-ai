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

// osBackend Implement filesystem.Backend based on OS packages, allowing skill middleware to read SKILL.md from disk.
//
// Background: Starting from eino v0.9+, einoskill.NewLocalBackend was replaced by NewBackendFromFilesystem,
// The latter requires passing a filesystem.Backend;  whereas adk/filesystem only provides InMemoryBackend,
// No implementation of the real disk is read. skill middleware only uses GlobInfo (lookup */SKILL.md) and
// Read (reads file content), so only these two methods are correctly implemented here; the rest return errOSBackendNotSupported.
type osBackend struct{}

// newOSBackend creates a backend based on the local file system.
func newOSBackend() *osBackend { return &osBackend{} }

// errOSBackendNotSupported means the method is not implemented in osBackend (skill will not be called).
var errOSBackendNotSupported = errors.New("osBackend: operation not supported")

// GlobInfo matches the file by wildcard and returns match information. pattern relative to base analysis (e.g., base=dir, pattern=*/SKILL.md).
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
		// Return to the absolute path. eino filesystem_backend BaseDir is used again for non-absolute paths,
		// If you return a relative path with the base prefix (such as data/skills/x/SKILL.md), it will be concatenated
		// data/skills/data/skills/x/SKILL.md causes the file to be unreadable.
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

// Read reads file content, supports 1-based line number Offset/Limit (Limit=0 means reading everything).
func (b *osBackend) Read(_ context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	if req == nil {
		return nil, errors.New("osBackend.Read: nil request")
	}
	data, err := os.ReadFile(req.FilePath)
	if err != nil {
		return nil, err
	}
	content := string(data)
	// Only slice line by line when explicitly specifying Offset/Limit to avoid unnecessary splits of all content.
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

// LsInfo not implemented (skill not used).
func (b *osBackend) LsInfo(_ context.Context, _ *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	return nil, errOSBackendNotSupported
}

// GrepRaw is not implemented (skill is not used).
func (b *osBackend) GrepRaw(_ context.Context, _ *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	return nil, errOSBackendNotSupported
}

// Write not implemented (skill read-only).
func (b *osBackend) Write(_ context.Context, _ *filesystem.WriteRequest) error {
	return errOSBackendNotSupported
}

// Edit not implemented (skill read-only).
func (b *osBackend) Edit(_ context.Context, _ *filesystem.EditRequest) error {
	return errOSBackendNotSupported
}
