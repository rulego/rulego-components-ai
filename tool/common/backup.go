package common

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// BackupDir is the default directory name for backups.
	BackupDir = ".history"

	// DefaultCleanupInterval is the default interval between lazy cleanups.
	DefaultCleanupInterval = time.Hour
)

// BackupManager handles file backup operations with lazy cleanup.
type BackupManager struct {
	workspace     string
	backupDir     string
	maxHistory    int
	maxAge        time.Duration     // Maximum age of backups (0 = no age limit)
	cleanInterval time.Duration     // Minimum interval between cleanups
	lastCleanup   time.Time         // Last cleanup time
	cleanupMu     sync.Mutex        // Protects lastCleanup
}

// BackupConfig holds backup configuration.
type BackupConfig struct {
	MaxHistory    int           `json:"maxHistory"`    // Maximum backups per file
	MaxAge        time.Duration `json:"maxAge"`        // Maximum age of backups (0 = no limit)
	CleanInterval time.Duration `json:"cleanInterval"` // Minimum interval between cleanups
}

// DefaultBackupConfig returns default backup configuration.
func DefaultBackupConfig() BackupConfig {
	return BackupConfig{
		MaxHistory:    10,
		MaxAge:        7 * 24 * time.Hour, // 7 days
		CleanInterval: DefaultCleanupInterval,
	}
}

// NewBackupManager creates a new BackupManager.
func NewBackupManager(workspace string, maxHistory int) *BackupManager {
	return &BackupManager{
		workspace:     workspace,
		backupDir:     filepath.Join(workspace, BackupDir),
		maxHistory:    maxHistory,
		maxAge:        0, // Disabled by default
		cleanInterval: DefaultCleanupInterval,
	}
}

// NewBackupManagerWithConfig creates a new BackupManager with full configuration.
func NewBackupManagerWithConfig(workspace string, config BackupConfig) *BackupManager {
	cleanInterval := config.CleanInterval
	if cleanInterval <= 0 {
		cleanInterval = DefaultCleanupInterval
	}

	return &BackupManager{
		workspace:     workspace,
		backupDir:     filepath.Join(workspace, BackupDir),
		maxHistory:    config.MaxHistory,
		maxAge:        config.MaxAge,
		cleanInterval: cleanInterval,
	}
}

// SetMaxAge sets the maximum age for backups.
func (m *BackupManager) SetMaxAge(maxAge time.Duration) {
	m.maxAge = maxAge
}

// SetCleanInterval sets the minimum interval between cleanups.
func (m *BackupManager) SetCleanInterval(interval time.Duration) {
	m.cleanupMu.Lock()
	defer m.cleanupMu.Unlock()
	m.cleanInterval = interval
}

// Backup creates a backup of the file before editing.
// Returns the backup file path and version number.
// sessionId is optional - if provided, backups are isolated per session.
func (m *BackupManager) Backup(filePath string, sessionId string) (backupPath string, version int, err error) {
	// Ensure backup directory exists
	if err := EnsureDir(m.backupDir); err != nil {
		return "", 0, fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Get relative path from workspace
	relPath, err := filepath.Rel(m.workspace, filePath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		// File is outside workspace - convert absolute path to safe relative path
		// Example: D:/a/b/file.txt -> D_/a/b/file.txt
		absPath := filepath.Clean(filePath)
		// Remove leading slashes/backslashes
		relPath = strings.TrimLeft(absPath, "/\\")
		// Replace colon (Windows drive letter) with underscore
		relPath = strings.ReplaceAll(relPath, ":", "_")
	}

	// Create backup subdirectory structure (with session isolation if provided)
	var backupSubDir string
	if sessionId != "" {
		backupSubDir = filepath.Join(m.backupDir, sessionId, filepath.Dir(relPath))
	} else {
		backupSubDir = filepath.Join(m.backupDir, filepath.Dir(relPath))
	}
	if err := EnsureDir(backupSubDir); err != nil {
		return "", 0, fmt.Errorf("failed to create backup subdirectory: %w", err)
	}

	// Find next version number
	version = m.getNextVersion(relPath, sessionId)
	baseName := filepath.Base(relPath)
	backupPath = filepath.Join(backupSubDir, fmt.Sprintf("%s.v%d", baseName, version))

	// Copy file to backup
	if err := copyFile(filePath, backupPath); err != nil {
		return "", 0, fmt.Errorf("failed to create backup: %w", err)
	}

	// Clean up old backups if exceeding maxHistory
	m.cleanupOldBackups(relPath, sessionId)

	// Lazy cleanup of expired backups
	m.maybeCleanup()

	return backupPath, version, nil
}

// getNextVersion returns the next version number for a file.
func (m *BackupManager) getNextVersion(relPath string, sessionId string) int {
	var backupSubDir string
	if sessionId != "" {
		backupSubDir = filepath.Join(m.backupDir, sessionId, filepath.Dir(relPath))
	} else {
		backupSubDir = filepath.Join(m.backupDir, filepath.Dir(relPath))
	}
	baseName := filepath.Base(relPath)

	entries, err := os.ReadDir(backupSubDir)
	if err != nil {
		return 1
	}

	maxVersion := 0
	prefix := baseName + ".v"
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) {
			// Parse version number from filename like "file.txt.v1"
			versionStr := strings.TrimPrefix(name, prefix)
			var version int
			if _, err := fmt.Sscanf(versionStr, "%d", &version); err == nil {
				if version > maxVersion {
					maxVersion = version
				}
			}
		}
	}

	return maxVersion + 1
}

// cleanupOldBackups removes old backups if exceeding maxHistory.
func (m *BackupManager) cleanupOldBackups(relPath string, sessionId string) {
	if m.maxHistory <= 0 {
		return
	}

	var backupSubDir string
	if sessionId != "" {
		backupSubDir = filepath.Join(m.backupDir, sessionId, filepath.Dir(relPath))
	} else {
		backupSubDir = filepath.Join(m.backupDir, filepath.Dir(relPath))
	}
	baseName := filepath.Base(relPath)

	entries, err := os.ReadDir(backupSubDir)
	if err != nil {
		return
	}

	// Find all backup files for this file
	var backups []backupInfo
	prefix := baseName + ".v"
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) {
			versionStr := strings.TrimPrefix(name, prefix)
			var version int
			if _, err := fmt.Sscanf(versionStr, "%d", &version); err == nil {
				info, err := entry.Info()
				if err != nil {
					continue
				}
				backups = append(backups, backupInfo{
					path:    filepath.Join(backupSubDir, name),
					version: version,
					modTime: info.ModTime(),
				})
			}
		}
	}

	// Sort by version descending (newest first)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].version > backups[j].version
	})

	// Remove old backups exceeding maxHistory
	for i := m.maxHistory; i < len(backups); i++ {
		os.Remove(backups[i].path)
	}
}

// backupInfo holds information about a backup file.
type backupInfo struct {
	path    string
	version int
	modTime time.Time
}

// ListBackups returns all backup versions for a file.
// sessionId is optional - if provided, only returns backups for that session.
func (m *BackupManager) ListBackups(filePath string, sessionId string) ([]BackupInfo, error) {
	relPath, err := filepath.Rel(m.workspace, filePath)
	if err != nil {
		relPath = filepath.Base(filePath)
	}

	var backupSubDir string
	if sessionId != "" {
		backupSubDir = filepath.Join(m.backupDir, sessionId, filepath.Dir(relPath))
	} else {
		backupSubDir = filepath.Join(m.backupDir, filepath.Dir(relPath))
	}
	baseName := filepath.Base(relPath)

	entries, err := os.ReadDir(backupSubDir)
	if err != nil {
		return nil, nil // No backups yet
	}

	var backups []BackupInfo
	prefix := baseName + ".v"
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) {
			versionStr := strings.TrimPrefix(name, prefix)
			var version int
			if _, err := fmt.Sscanf(versionStr, "%d", &version); err == nil {
				info, err := entry.Info()
				if err != nil {
					continue
				}
				backups = append(backups, BackupInfo{
					Path:    filepath.Join(backupSubDir, name),
					Version: version,
					ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
					Size:    info.Size(),
				})
			}
		}
	}

	// Sort by version descending
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Version > backups[j].Version
	})

	return backups, nil
}

// BackupInfo contains information about a backup file.
type BackupInfo struct {
	Path    string `json:"path"`
	Version int    `json:"version"`
	ModTime string `json:"mod_time"`
	Size    int64  `json:"size"`
}

// Restore restores a file from a backup.
// sessionId is optional - if provided, looks for backup in that session's directory.
func (m *BackupManager) Restore(filePath string, version int, sessionId string) error {
	relPath, err := filepath.Rel(m.workspace, filePath)
	if err != nil {
		relPath = filepath.Base(filePath)
	}

	var backupSubDir string
	if sessionId != "" {
		backupSubDir = filepath.Join(m.backupDir, sessionId, filepath.Dir(relPath))
	} else {
		backupSubDir = filepath.Join(m.backupDir, filepath.Dir(relPath))
	}
	baseName := filepath.Base(relPath)
	backupPath := filepath.Join(backupSubDir, fmt.Sprintf("%s.v%d", baseName, version))

	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return fmt.Errorf("backup version %d not found", version)
	}

	return atomicCopyFile(backupPath, filePath)
}

// atomicCopyFile copies a file from src to dst atomically using temp file + rename.
func atomicCopyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	tmpPath := dst + ".tmp"

	// Write to temp file first
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Rename temp file to target (atomic operation on most filesystems)
	if err := os.Rename(tmpPath, dst); err != nil {
		// Clean up temp file on failure
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename file: %w", err)
	}

	return nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// maybeCleanup performs lazy cleanup of expired backups.
// This is called after each backup operation to clean up old backups.
func (m *BackupManager) maybeCleanup() {
	// Check if age-based cleanup is enabled
	if m.maxAge <= 0 {
		return
	}

	// Limit cleanup frequency to avoid performance impact
	m.cleanupMu.Lock()
	shouldClean := time.Since(m.lastCleanup) >= m.cleanInterval
	if shouldClean {
		m.lastCleanup = time.Now()
	}
	m.cleanupMu.Unlock()

	if !shouldClean {
		return
	}

	// Perform cleanup asynchronously to not block the backup operation
	go m.cleanupExpired()
}

// cleanupExpired removes backups older than maxAge.
func (m *BackupManager) cleanupExpired() int {
	if m.maxAge <= 0 {
		return 0
	}

	cutoff := time.Now().Add(-m.maxAge)
	removed := 0

	// Walk the entire backup directory
	filepath.Walk(m.backupDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// Check if file is a backup file (has .v in name)
		base := filepath.Base(path)
		if strings.Contains(base, ".v") && info.ModTime().Before(cutoff) {
			if os.Remove(path) == nil {
				removed++
			}
		}
		return nil
	})

	return removed
}

// CleanupNow forces an immediate cleanup of expired backups.
func (m *BackupManager) CleanupNow() int {
	if m.maxAge <= 0 {
		return 0
	}
	return m.cleanupExpired()
}

// GetBackupStats returns statistics about backups.
func (m *BackupManager) GetBackupStats() (BackupStats, error) {
	stats := BackupStats{}

	_, err := os.Stat(m.backupDir)
	if os.IsNotExist(err) {
		return stats, nil
	}

	err = filepath.Walk(m.backupDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if !info.IsDir() {
			stats.TotalFiles++
			stats.TotalSize += info.Size()
			if stats.OldestBackup.IsZero() || info.ModTime().Before(stats.OldestBackup) {
				stats.OldestBackup = info.ModTime()
			}
			if info.ModTime().After(stats.NewestBackup) {
				stats.NewestBackup = info.ModTime()
			}
		}

		return nil
	})

	return stats, err
}

// BackupStats holds backup statistics.
type BackupStats struct {
	TotalFiles   int       `json:"totalFiles"`
	TotalSize    int64     `json:"totalSize"`
	OldestBackup time.Time `json:"oldestBackup"`
	NewestBackup time.Time `json:"newestBackup"`
}
