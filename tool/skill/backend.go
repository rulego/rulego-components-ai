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

// dirCache 缓存单个目录的解析结果及特征值
type dirCache struct {
	backend     einoskill.Backend
	fingerprint uint64
}

// MultiBackend 聚合多个 Backend，支持全局+用户技能目录
// 优先级：前面的目录优先级更高（用户目录应该放在前面）
type MultiBackend struct {
	dirs           []string
	disabledNames  map[string]bool
	cache          map[string]*dirCache
	mu             sync.RWMutex
}

// NewMultiBackend 创建一个支持多目录的 Backend
// dirs 按优先级排列，前面的目录优先级更高
func NewMultiBackend(dirs []string) *MultiBackend {
	return &MultiBackend{
		dirs:          dirs,
		disabledNames: make(map[string]bool),
		cache:         make(map[string]*dirCache),
	}
}

// SetDisabledSkills 设置禁用的技能名称列表
func (m *MultiBackend) SetDisabledSkills(names []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disabledNames = make(map[string]bool, len(names))
	for _, name := range names {
		m.disabledNames[name] = true
	}
}

// getDirFingerprint 获取目录下所有文件的特征值（基于路径、大小和修改时间的哈希）
// 这样即使修改了较旧的文件（不改变最大修改时间），特征值也会发生变化
func getDirFingerprint(dir string) (uint64, error) {
	h := fnv.New64a()
	b := make([]byte, 16)

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// 忽略无法访问的文件或目录
			return nil
		}

		// 忽略隐藏文件和子目录（如 .git），避免无意义的扫描
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

// getBackends 获取 backends，结合惰性缓存与基于特征值的热更新机制
func (m *MultiBackend) getBackends() []einoskill.Backend {
	var backends []einoskill.Backend

	// 遍历配置的目录
	for _, dir := range m.dirs {
		// 检查目录是否存在
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		// 1. 尝试从缓存中获取 (读锁)
		m.mu.RLock()
		c, ok := m.cache[dir]
		m.mu.RUnlock()

		// 2. 如果缓存存在，才去计算当前特征值并比较
		if ok {
			currentFingerprint, err := getDirFingerprint(dir)
			if err == nil && c.fingerprint == currentFingerprint {
				backends = append(backends, c.backend)
				continue
			}
		}

		// 3. 缓存失效或不存在，需要重新解析 (写锁 + 双重检查)
		m.mu.Lock()
		// 再次检查缓存是否存在，防止并发请求已经更新了缓存
		c, ok = m.cache[dir]
		if ok {
			currentFingerprint, err := getDirFingerprint(dir)
			if err == nil && c.fingerprint == currentFingerprint {
				m.mu.Unlock()
				backends = append(backends, c.backend)
				continue
			}
		}

		// 4. 解析目录
		backend, err := einoskill.NewLocalBackend(&einoskill.LocalBackendConfig{BaseDir: dir})
		if err != nil {
			m.mu.Unlock()
			continue
		}

		// 5. 获取最新特征值并更新缓存
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

// List 获取所有技能元数据列表，合并所有目录的技能
// 同名技能只保留优先级最高的那个（出现在前面的目录）
func (m *MultiBackend) List(ctx context.Context) ([]einoskill.FrontMatter, error) {
	backends := m.getBackends()
	if len(backends) == 0 {
		return []einoskill.FrontMatter{}, nil
	}

	// 用于去重的 map
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
				// 过滤禁用的技能
				if !m.disabledNames[skill.Name] {
					result = append(result, skill)
				}
			}
		}
	}

	return result, nil
}

// Get 根据名称获取技能详情，按优先级顺序查找
func (m *MultiBackend) Get(ctx context.Context, name string) (einoskill.Skill, error) {
	// 检查技能是否被禁用
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
