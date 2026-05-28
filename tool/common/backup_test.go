package common

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackupManager_Backup(t *testing.T) {
	// Create temp workspace
	tmpWorkspace := filepath.Join(os.TempDir(), "test_backup_manager")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	// Create test file
	testFile := filepath.Join(tmpWorkspace, "test.txt")
	err = os.WriteFile(testFile, []byte("original content"), 0644)
	require.NoError(t, err)

	manager := NewBackupManager(tmpWorkspace, 5)

	// Create first backup
	backupPath, version, err := manager.Backup(testFile, "")
	require.NoError(t, err)
	assert.Equal(t, 1, version)
	assert.Contains(t, backupPath, ".history")
	assert.FileExists(t, backupPath)

	// Verify backup content
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err)
	assert.Equal(t, "original content", string(backupContent))

	// Modify file and create second backup
	err = os.WriteFile(testFile, []byte("modified content"), 0644)
	require.NoError(t, err)

	backupPath2, version2, err := manager.Backup(testFile, "")
	require.NoError(t, err)
	assert.Equal(t, 2, version2)
	assert.NotEqual(t, backupPath, backupPath2)
}

func TestBackupManager_BackupSubdirectory(t *testing.T) {
	tmpWorkspace := filepath.Join(os.TempDir(), "test_backup_subdir")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	// Create test file in subdirectory
	subDir := filepath.Join(tmpWorkspace, "sub", "dir")
	err = os.MkdirAll(subDir, 0755)
	require.NoError(t, err)

	testFile := filepath.Join(subDir, "test.txt")
	err = os.WriteFile(testFile, []byte("content"), 0644)
	require.NoError(t, err)

	manager := NewBackupManager(tmpWorkspace, 5)

	backupPath, version, err := manager.Backup(testFile, "")
	require.NoError(t, err)
	assert.Equal(t, 1, version)

	// Verify backup preserves directory structure
	expectedBackupDir := filepath.Join(tmpWorkspace, BackupDir, "sub", "dir")
	assert.Contains(t, backupPath, expectedBackupDir)
}

func TestBackupManager_MaxHistory(t *testing.T) {
	tmpWorkspace := filepath.Join(os.TempDir(), "test_backup_max_history")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	testFile := filepath.Join(tmpWorkspace, "test.txt")
	err = os.WriteFile(testFile, []byte("initial"), 0644)
	require.NoError(t, err)

	maxHistory := 3
	manager := NewBackupManager(tmpWorkspace, maxHistory)

	// Create more backups than maxHistory
	for i := 1; i <= 5; i++ {
		err = os.WriteFile(testFile, []byte("content "+string(rune('0'+i))), 0644)
		require.NoError(t, err)

		_, version, err := manager.Backup(testFile, "")
		require.NoError(t, err)
		assert.Equal(t, i, version)
	}

	// List backups - should only have maxHistory backups
	backups, err := manager.ListBackups(testFile, "")
	require.NoError(t, err)
	assert.Len(t, backups, maxHistory)

	// Verify oldest backups are removed
	// Versions 1 and 2 should be cleaned up, versions 3, 4, 5 should remain
	versions := make(map[int]bool)
	for _, b := range backups {
		versions[b.Version] = true
	}
	assert.False(t, versions[1], "version 1 should be cleaned up")
	assert.False(t, versions[2], "version 2 should be cleaned up")
	assert.True(t, versions[3], "version 3 should exist")
	assert.True(t, versions[4], "version 4 should exist")
	assert.True(t, versions[5], "version 5 should exist")
}

func TestBackupManager_ListBackups(t *testing.T) {
	tmpWorkspace := filepath.Join(os.TempDir(), "test_backup_list")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	testFile := filepath.Join(tmpWorkspace, "test.txt")
	err = os.WriteFile(testFile, []byte("content"), 0644)
	require.NoError(t, err)

	manager := NewBackupManager(tmpWorkspace, 10)

	// Initially no backups
	backups, err := manager.ListBackups(testFile, "")
	require.NoError(t, err)
	assert.Empty(t, backups)

	// Create backups
	for i := 1; i <= 3; i++ {
		_, _, err := manager.Backup(testFile, "")
		require.NoError(t, err)
	}

	// List backups
	backups, err = manager.ListBackups(testFile, "")
	require.NoError(t, err)
	assert.Len(t, backups, 3)

	// Verify sorted by version descending (newest first)
	assert.Equal(t, 3, backups[0].Version)
	assert.Equal(t, 2, backups[1].Version)
	assert.Equal(t, 1, backups[2].Version)
}

func TestBackupManager_Restore(t *testing.T) {
	tmpWorkspace := filepath.Join(os.TempDir(), "test_backup_restore")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	testFile := filepath.Join(tmpWorkspace, "test.txt")
	err = os.WriteFile(testFile, []byte("original"), 0644)
	require.NoError(t, err)

	manager := NewBackupManager(tmpWorkspace, 10)

	// Create backup
	_, _, err = manager.Backup(testFile, "")
	require.NoError(t, err)

	// Modify file
	err = os.WriteFile(testFile, []byte("modified"), 0644)
	require.NoError(t, err)

	// Verify modified content
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, "modified", string(content))

	// Restore
	err = manager.Restore(testFile, 1, "")
	require.NoError(t, err)

	// Verify restored content
	content, err = os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, "original", string(content))
}

func TestBackupManager_RestoreNonExistent(t *testing.T) {
	tmpWorkspace := filepath.Join(os.TempDir(), "test_backup_restore_ne")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	testFile := filepath.Join(tmpWorkspace, "test.txt")
	err = os.WriteFile(testFile, []byte("content"), 0644)
	require.NoError(t, err)

	manager := NewBackupManager(tmpWorkspace, 10)

	// Try to restore non-existent version
	err = manager.Restore(testFile, 999, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestBackupManager_ZeroMaxHistory(t *testing.T) {
	tmpWorkspace := filepath.Join(os.TempDir(), "test_backup_zero_max")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	testFile := filepath.Join(tmpWorkspace, "test.txt")
	err = os.WriteFile(testFile, []byte("content"), 0644)
	require.NoError(t, err)

	// MaxHistory = 0 means no cleanup
	manager := NewBackupManager(tmpWorkspace, 0)

	// Create backups
	for i := 1; i <= 5; i++ {
		_, _, err := manager.Backup(testFile, "")
		require.NoError(t, err)
	}

	// All backups should remain
	backups, err := manager.ListBackups(testFile, "")
	require.NoError(t, err)
	assert.Len(t, backups, 5)
}

func TestBackupManager_FileOutsideWorkspace(t *testing.T) {
	// Create temp workspace
	tmpWorkspace := filepath.Join(os.TempDir(), "test_backup_outside")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	// Create a file OUTSIDE the workspace (in a sibling directory)
	outsideDir := filepath.Join(os.TempDir(), "test_backup_outside_sibling")
	err = os.MkdirAll(outsideDir, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(outsideDir)

	outsideFile := filepath.Join(outsideDir, "config.json")
	err = os.WriteFile(outsideFile, []byte(`{"key": "value"}`), 0644)
	require.NoError(t, err)

	manager := NewBackupManager(tmpWorkspace, 5)

	// Create backup of file outside workspace
	backupPath, version, err := manager.Backup(outsideFile, "")
	require.NoError(t, err)
	assert.Equal(t, 1, version)

	// Backup should be inside .history directory
	assert.Contains(t, backupPath, ".history")
	assert.FileExists(t, backupPath)

	// Verify backup content
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err)
	assert.Equal(t, `{"key": "value"}`, string(backupContent))

	t.Logf("Outside file: %s", outsideFile)
	t.Logf("Backup path: %s", backupPath)
}

func TestBackupManager_DifferentDrivesNoConflict(t *testing.T) {
	// Test that files with same name from different directories don't conflict
	tmpWorkspace := filepath.Join(os.TempDir(), "test_backup_no_conflict")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	// Create two directories with same-named files
	dir1 := filepath.Join(os.TempDir(), "project_a", "config")
	dir2 := filepath.Join(os.TempDir(), "project_b", "config")
	err = os.MkdirAll(dir1, 0755)
	require.NoError(t, err)
	err = os.MkdirAll(dir2, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(filepath.Join(os.TempDir(), "project_a"))
	defer os.RemoveAll(filepath.Join(os.TempDir(), "project_b"))

	file1 := filepath.Join(dir1, "settings.json")
	file2 := filepath.Join(dir2, "settings.json")
	err = os.WriteFile(file1, []byte(`{"project": "A"}`), 0644)
	require.NoError(t, err)
	err = os.WriteFile(file2, []byte(`{"project": "B"}`), 0644)
	require.NoError(t, err)

	manager := NewBackupManager(tmpWorkspace, 10)

	// Create backups for both files
	backup1, v1, err := manager.Backup(file1, "")
	require.NoError(t, err)
	assert.Equal(t, 1, v1)

	backup2, v2, err := manager.Backup(file2, "")
	require.NoError(t, err)
	assert.Equal(t, 1, v2)

	// Backups should be in different directories (preserving original path structure)
	assert.NotEqual(t, backup1, backup2, "Backups should have different paths")
	assert.Contains(t, backup1, ".history")
	assert.Contains(t, backup2, ".history")

	// Verify backup contents are different
	content1, err := os.ReadFile(backup1)
	require.NoError(t, err)
	content2, err := os.ReadFile(backup2)
	require.NoError(t, err)
	assert.NotEqual(t, string(content1), string(content2))

	t.Logf("File1 backup: %s", backup1)
	t.Logf("File2 backup: %s", backup2)
}

func TestBackupManager_SessionIsolation(t *testing.T) {
	// Create temp workspace
	tmpWorkspace := filepath.Join(os.TempDir(), "test_backup_session")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	testFile := filepath.Join(tmpWorkspace, "test.txt")
	err = os.WriteFile(testFile, []byte("content"), 0644)
	require.NoError(t, err)

	manager := NewBackupManager(tmpWorkspace, 10)

	// Create backups in different sessions
	backup1, v1, err := manager.Backup(testFile, "session1")
	require.NoError(t, err)
	assert.Equal(t, 1, v1)
	assert.Contains(t, backup1, filepath.Join(".history", "session1"))

	backup2, v2, err := manager.Backup(testFile, "session2")
	require.NoError(t, err)
	assert.Equal(t, 1, v2)
	assert.Contains(t, backup2, filepath.Join(".history", "session2"))

	// Each session has its own version counter
	assert.NotEqual(t, backup1, backup2)
}

func TestEnsureParentDir_FileAsParent(t *testing.T) {
	// Test that EnsureParentDir returns a clear error when trying to create
	// a directory under a file path
	tmpWorkspace := filepath.Join(os.TempDir(), "test_ensure_parent_dir")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	// Create a file
	existingFile := filepath.Join(tmpWorkspace, "existing_file.md")
	err = os.WriteFile(existingFile, []byte("content"), 0644)
	require.NoError(t, err)

	// Try to create a path that would require a file to be a directory
	invalidPath := filepath.Join(existingFile, "subdir", "file.txt")
	err = EnsureParentDir(invalidPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")
	assert.Contains(t, err.Error(), "existing_file.md")
}
