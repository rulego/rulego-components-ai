package common

import (
	"encoding/json"
	"sync"
)

// ResolverCache 按工作目录缓存 SecurePathResolver，供 read/write/edit 等文件工具在
// per-call workDir 覆盖（common.WorkDirFromCtx 注入）时复用，避免每次重建 resolver
// （NewSecurePathResolver 会做 EvalSymlinks，是系统调用）。
//
// 默认 resolver 在 NewResolverCache 时由 config.WorkDir 建立，保留原行为；
// Get("") 返回默认 resolver；Get(nonEmpty) 按 workDir 取/建缓存实例。
type ResolverCache struct {
	mu       sync.RWMutex
	defaultR *SecurePathResolver
	byDir    map[string]*SecurePathResolver
	sec      PathSecurityConfig
}

// NewResolverCache 用默认 workDir 与安全策略建缓存。默认 resolver 建立失败则返回错误
// （与原 NewSecurePathResolver 行为一致）。
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

// Default 返回 config.WorkDir 对应的默认 resolver。
func (c *ResolverCache) Default() *SecurePathResolver { return c.defaultR }

// Get 返回 workDir 对应的 resolver：空 → 默认 resolver（老行为）；非空 → 按 workDir 缓存取/建。
// 线程安全；NewSecurePathResolver 失败时返回错误（调用方决定如何处理）。
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
	// double-check：持锁期间可能已被其他 goroutine 建好
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

// GetWithAllowDirs 取 workDir + allowDirs + allowCross 对应的 resolver（多根 + 跨目录放行）。
// 退化条件：allowDirs 空 且 allowCross=false 且 c.sec 自身也无 AllowDirs/AllowCross（默认收紧场景）
// → 退化为 Get(workDir)，复用默认 resolver（避免重复建）。任一条件不满足时走 key 编码路径，
// 按 JSON 序列化的三元组作 cache key（防分隔符碰撞——此前用 \x00/\x01 拼接，路径含这些字节理论撞 key）。
// resolver 内 AllowCrossDir/allowedDirs 建好即不可变，cross 翻转或 allowDirs 变化都不复用（防 stale）。
// workDir 空时用默认 resolver 的 workspace 作主根，保证 allowDirs / cross 仍生效。
func (c *ResolverCache) GetWithAllowDirs(workDir string, allowDirs []string, allowCross bool) (*SecurePathResolver, error) {
	// 退化：caller 不要多根/cross，且 cache 自身的 sec 也未设这些维度——此时 c.sec 与 caller 意图一致，
	// 复用 defaultR/byDir[workDir] 安全。若 c.sec 自带 AllowDirs/AllowCross 则不能退化（语义不一致）。
	if len(allowDirs) == 0 && !allowCross && !c.sec.AllowCrossDir && len(c.sec.AllowDirs) == 0 {
		return c.Get(workDir)
	}
	// JSON 序列化作 key：完全消除分隔符碰撞（路径含 \x00/\x01/引号等都被正确转义）。
	keyBytes, err := json.Marshal(struct {
		Wd    string
		Dirs  []string
		Cross bool
	}{workDir, allowDirs, allowCross})
	if err != nil {
		// 不会发生（结构体仅含基础类型）；防御性兜底：用退化路径，宁可少缓存不出错。
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
