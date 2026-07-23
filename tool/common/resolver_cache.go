package common

import (
	"encoding/json"
	"sync"
)

// ResolverCache caches SecurePathResolver by working directory, providing file tools for read/write/edit files
// per-call workDir override (common.WorkDirFromCtx injection) to avoid rebuilding the resolver each time
// (NewSecurePathResolver performs EvalSymlinks, which is a system call.)
//
// The default resolver is created by config.WorkDir at NewResolverCache, preserving the original behavior;
// Get("") returns the default resolver; Get(nonEmpty) Uses the workDir to fetch/create cache instances.
type ResolverCache struct {
	mu       sync.RWMutex
	defaultR *SecurePathResolver
	byDir    map[string]*SecurePathResolver
	sec      PathSecurityConfig
}

// NewResolverCache builds the cache using the default workDir and security policies. If the default resolver fails to be built, an error is returned
// (Consistent with the original NewSecurePathResolver behavior).
func NewResolverCache(workDir string, sec PathSecurityConfig) (*ResolverCache, error) {
	r, err := NewSecurePathResolver(workDir, sec)
	if err != nil {
		return nil, err
	}
	return &ResolverCache{
		defaultR: r,
		byDir:    make(map[string]*SecurePathResolver),
		sec:      sec,
	}, nil
}

// Default returns the default resolver corresponding to config.WorkDir.
func (c *ResolverCache) Default() *SecurePathResolver { return c.defaultR }

// Get returns the resolver corresponding to the workDir: empty → default resolver (old behavior); Non-null → Cache/create/access by clicking workDir.
// thread safety; NewSecurePathResolver returns an error when it fails (the caller decides how to handle it).
func (c *ResolverCache) Get(workDir string) (*SecurePathResolver, error) {
	if workDir == "" {
		return c.defaultR, nil
	}
	c.mu.RLock()
	if r, ok := c.byDir[workDir]; ok {
		c.mu.RUnlock()
		return r, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	// double-check: During locking, another goroutine may have been established
	if r, ok := c.byDir[workDir]; ok {
		return r, nil
	}
	r, err := NewSecurePathResolver(workDir, c.sec)
	if err != nil {
		return nil, err
	}
	c.byDir[workDir] = r
	return r, nil
}

// GetWithAllowDirs takes the resolver corresponding to workDir + allowDirs + allowCross (multiple roots + cross-directory release).
// Degradation condition: allowDirs empty and allowCross=false, and c.sec itself does not have AllowDirs/AllowCross (default tightening scenario)
// → Degenerates into Get (workDir), reusing the default resolver (to avoid duplicate builds). If any condition is not met, the key encoding path is applied,
// Use a JSON serialized triplet to create cache keys (prevent separator collisions—previously, \x00/\x01 concatenation was used, and paths containing these bytes were theoretically used to collide keys).
// In resolver, AllowCrossDir/allowedDirs are created and cannot be changed; cross flipping or allowDirs changes are not reused (stale prevention).
// When workDir is empty, use the default resolver's workspace as the primary root to ensure allowDirs / cross remains effective.
func (c *ResolverCache) GetWithAllowDirs(workDir string, allowDirs []string, allowCross bool) (*SecurePathResolver, error) {
	// Degeneration: The caller does not need multiple roots/cross, and the cache's sec itself does not set these dimensions—at this point, c.sec and caller intentions are consistent.
	// Reuse defaultR/byDir[workDir] for safety. If c.sec includes AllowDirs/AllowCross, it cannot degenerate (semantic inconsistency).
	if len(allowDirs) == 0 && !allowCross && !c.sec.AllowCrossDir && len(c.sec.AllowDirs) == 0 {
		return c.Get(workDir)
	}
	// JSON serialization to key: completely eliminates separator collisions (paths containing \x00/\x01/quotes are all correctly escaped).
	keyBytes, err := json.Marshal(struct {
		Wd    string
		Dirs  []string
		Cross bool
	}{workDir, allowDirs, allowCross})
	if err != nil {
		// It will not happen (structs only include base types); Defensive Bottom Line: Use the degeneration path, better to cache less and avoid errors.
		return c.Get(workDir)
	}
	key := string(keyBytes)
	c.mu.RLock()
	if r, ok := c.byDir[key]; ok {
		c.mu.RUnlock()
		return r, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	// double-check
	if r, ok := c.byDir[key]; ok {
		return r, nil
	}
	sec := c.sec
	sec.AllowDirs = allowDirs
	sec.AllowCrossDir = allowCross
	wd := workDir
	if wd == "" {
		wd = c.defaultR.Workspace()
	}
	r, err := NewSecurePathResolver(wd, sec)
	if err != nil {
		return nil, err
	}
	c.byDir[key] = r
	return r, nil
}
