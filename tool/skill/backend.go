package skill

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
)

// dirCache caches the parsing results and eigenvalues of a single directory
type dirCache struct {
	backend     einoskill.Backend
	fingerprint uint64
}

// MultiBackend aggregates multiple Backends, supporting global + user skill directories
// Priority: The directories at the front have higher priority (user directories should be placed first)
type MultiBackend struct {
	dirs          []string
	disabledNames map[string]bool
	cache         map[string]*dirCache
	mu            sync.RWMutex
}

// NewMultiBackend creates a backend that supports multiple directories
// Dirs are ranked by priority, with the earlier directories having higher priority
func NewMultiBackend(dirs []string) *MultiBackend {
	return &MultiBackend{
		dirs:          dirs,
		disabledNames: make(map[string]bool),
		cache:         make(map[string]*dirCache),
	}
}

// SetDisabledSkills sets the list of disabled skill names
func (m *MultiBackend) SetDisabledSkills(names []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disabledNames = make(map[string]bool, len(names))
	for _, name := range names {
		m.disabledNames[name] = true
	}
}

// getDirFingerprint retrieves the characteristic values of all files in the directory (hashes based on path, size, and modification time)
// This way, even if older files are modified (without changing the maximum modification time), the eigenvalues will still change
func getDirFingerprint(dir string) (uint64, error) {
	h := fnv.New64a()
	b := make([]byte, 16)

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Ignore files or directories that are inaccessible
			return nil
		}

		// Ignore hidden files and subdirectories (such as.git) to avoid meaningless scans
		if strings.HasPrefix(d.Name(), ".") && path != dir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		h.Write([]byte(path))
		binary.LittleEndian.PutUint64(b[0:8], uint64(info.Size()))
		binary.LittleEndian.PutUint64(b[8:16], uint64(info.ModTime().UnixNano()))
		h.Write(b)

		return nil
	})
	return h.Sum64(), err
}

// getBackends retrieves backends, combining lazy caching with a hot update mechanism based on eigenvalues
func (m *MultiBackend) getBackends() []einoskill.Backend {
	var backends []einoskill.Backend

	// Traverse the configured directory
	for _, dir := range m.dirs {
		// Check if the directory exists
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		// 1. Try to retrieve (read lock) from cache
		m.mu.RLock()
		c, ok := m.cache[dir]
		m.mu.RUnlock()

		// 2. If cache exists, then calculate the current eigenvalue and compare
		if ok {
			currentFingerprint, err := getDirFingerprint(dir)
			if err == nil && c.fingerprint == currentFingerprint {
				backends = append(backends, c.backend)
				continue
			}
		}

		// 3. Cache failure or absence requires re-parsing (write lock + double-check)
		m.mu.Lock()
		// Check again whether the cache exists to prevent concurrent requests from having already updated the cache
		c, ok = m.cache[dir]
		if ok {
			currentFingerprint, err := getDirFingerprint(dir)
			if err == nil && c.fingerprint == currentFingerprint {
				m.mu.Unlock()
				backends = append(backends, c.backend)
				continue
			}
		}

		// 4. Parse the table of contents
		// Starting from eino v0.9+, NewLocalBackend is changed to NewBackendFromFilesystem (requires filesystem.Backend).
		// osBackend provides an OS-based disk read implementation (skill uses only GlobInfo + Read).
		backend, err := einoskill.NewBackendFromFilesystem(context.Background(), &einoskill.BackendFromFilesystemConfig{
			Backend: newOSBackend(),
			BaseDir: dir,
		})
		if err != nil {
			m.mu.Unlock()
			continue
		}

		// 5. Get the latest feature values and update the cache
		newFingerprint, _ := getDirFingerprint(dir)
		m.cache[dir] = &dirCache{
			backend:     backend,
			fingerprint: newFingerprint,
		}
		m.mu.Unlock()

		backends = append(backends, backend)
	}
	return backends
}

// List retrieves all skill metadata lists and merges skills from all directories
// Only the skill with the same name is kept with the highest priority (appears in the previous directory).
func (m *MultiBackend) List(ctx context.Context) ([]einoskill.FrontMatter, error) {
	backends := m.getBackends()
	if len(backends) == 0 {
		return []einoskill.FrontMatter{}, nil
	}

	// A map used for deduplication
	seen := make(map[string]bool)
	var result []einoskill.FrontMatter

	for _, backend := range backends {
		skills, err := backend.List(ctx)
		if err != nil {
			continue
		}

		for _, skill := range skills {
			if !seen[skill.Name] {
				seen[skill.Name] = true
				// Filter out disabled skills
				if !m.disabledNames[skill.Name] {
					result = append(result, skill)
				}
			}
		}
	}

	return result, nil
}

// Get skill details by name and search in order of priority
func (m *MultiBackend) Get(ctx context.Context, name string) (einoskill.Skill, error) {
	// Check if skills are disabled
	if m.disabledNames[name] {
		return einoskill.Skill{}, fmt.Errorf("skill %s is disabled", name)
	}

	backends := m.getBackends()
	if len(backends) == 0 {
		return einoskill.Skill{}, fmt.Errorf("no skill backends available")
	}

	var lastErr error
	for _, backend := range backends {
		skill, err := backend.Get(ctx, name)
		if err == nil {
			return skill, nil
		}
		lastErr = err
	}

	return einoskill.Skill{}, fmt.Errorf("skill %s not found: %v", name, lastErr)
}
