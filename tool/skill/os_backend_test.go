package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"
)

// TestOSBackend_Read Verify reading the actual content of the file.
func TestOSBackend_Read(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b := newOSBackend()
	fc, err := b.Read(context.Background(), &filesystem.ReadRequest{FilePath: path})
	if err != nil {
		t.Fatalf("Read Failure: %v", err)
	}
	if fc.Content != "hello\nworld\n" {
		t.Fatalf("Expect hello\\nworld\\n, got %q", fc.Content)
	}
}

// TestOSBackend_ReadWithOffsetLimit Verify reading by line Offset/Limit (1-based).
func TestOSBackend_ReadWithOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b := newOSBackend()
	// Offset=2 starts from line 2, Limit=2 reads 2 lines → "b\nc"
	fc, err := b.Read(context.Background(), &filesystem.ReadRequest{FilePath: path, Offset: 2, Limit: 2})
	if err != nil {
		t.Fatalf("Read Failure: %v", err)
	}
	if fc.Content != "b\nc" {
		t.Fatalf("Expect b\\nc, got %q", fc.Content)
	}
}

// TestOSBackend_ReadNonExistent Verify that there is no file return error in read.
func TestOSBackend_ReadNonExistent(t *testing.T) {
	b := newOSBackend()
	_, err := b.Read(context.Background(), &filesystem.ReadRequest{FilePath: filepath.Join(t.TempDir(), "no-such-file.md")})
	if err == nil {
		t.Error("Expect to read without file errors")
	}
}

// TestOSBackend_GlobInfo Verify */SKILL.md wildmatch (skill middleware lookup pattern).
func TestOSBackend_GlobInfo(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"skill1", "skill2"} {
		sub := filepath.Join(dir, name)
		if err := os.Mkdir(sub, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "SKILL.md"), []byte("# "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Disruptive files: should not be matched by */SKILL.md
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// SKILL.md in secondary directories should not be wildmatched by primary wildcards
	nested := filepath.Join(dir, "skill3", "deep")
	os.MkdirAll(nested, 0755)
	os.WriteFile(filepath.Join(nested, "SKILL.md"), []byte("nested"), 0644)

	b := newOSBackend()
	infos, err := b.GlobInfo(context.Background(), &filesystem.GlobInfoRequest{
		Pattern: "*/SKILL.md",
		Path:    dir,
	})
	if err != nil {
		t.Fatalf("GlobInfo Failure: %v", err)
	}
	if len(infos) != 2 {
		names := make([]string, 0, len(infos))
		for _, i := range infos {
			names = append(names, i.Path)
		}
		t.Fatalf("Expect to match 2 Level 1 SKILL.md, got %d: %s", len(infos), strings.Join(names, ", "))
	}
	for _, info := range infos {
		if filepath.Base(info.Path) != "SKILL.md" {
			t.Errorf("Expect SKILL.md, got %s", info.Path)
		}
		if info.IsDir {
			t.Errorf("It should not be a table of contents: %s", info.Path)
		}
		if info.Size == 0 {
			t.Errorf("Size should not be 0:%s", info.Path)
		}
	}
}

// TestOSBackend_GlobInfo_RelativeBase Anti-regression: When base is a relative path, GlobInfo must return an absolute path.
// Otherwise, eino filesystem_backend will treat "halfpaths with the base prefix" as relative paths and concatenate BaseDir again,
// Getting a repeat path like data/skills/data/skills/x/SKILL.md doesn't read the file.
func TestOSBackend_GlobInfo_RelativeBase(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "evolve")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# evolve"), 0644); err != nil {
		t.Fatal(err)
	}

	// Switch to dir and use relative base "skills" to reproduce the original bug trigger condition
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	b := newOSBackend()
	infos, err := b.GlobInfo(context.Background(), &filesystem.GlobInfoRequest{
		Pattern: "*/SKILL.md",
		Path:    "skills",
	})
	if err != nil {
		t.Fatalf("GlobInfo Failure: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("Expect to match 1, got %d", len(infos))
	}
	if !filepath.IsAbs(infos[0].Path) {
		t.Errorf("When relative to base, Path must be an absolute path, got %s", infos[0].Path)
	}
}

// TestOSBackend_UnsupportedMethods Verify that methods not used by skill return not-supported.
func TestOSBackend_UnsupportedMethods(t *testing.T) {
	b := newOSBackend()
	ctx := context.Background()

	if _, err := b.LsInfo(ctx, &filesystem.LsInfoRequest{}); err == nil {
		t.Error("LsInfo should return not-supported")
	}
	if _, err := b.GrepRaw(ctx, &filesystem.GrepRequest{}); err == nil {
		t.Error("GrepRaw should return not-supported")
	}
	if err := b.Write(ctx, &filesystem.WriteRequest{}); err == nil {
		t.Error("Write should return not-supported")
	}
	if err := b.Edit(ctx, &filesystem.EditRequest{}); err == nil {
		t.Error("Edit should return not-supported")
	}
}

// TestOSBackend_NilRequestGuard Verify the protection of NIL requests.
func TestOSBackend_NilRequestGuard(t *testing.T) {
	b := newOSBackend()
	if _, err := b.Read(context.Background(), nil); err == nil {
		t.Error("Read(nil) Should report an error")
	}
	if _, err := b.GlobInfo(context.Background(), nil); err == nil {
		t.Error("GlobInfo(nil) Should report an error")
	}
}
