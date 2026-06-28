package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"
)

// TestOSBackend_Read 验证读取真实文件内容。
func TestOSBackend_Read(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b := newOSBackend()
	fc, err := b.Read(context.Background(), &filesystem.ReadRequest{FilePath: path})
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if fc.Content != "hello\nworld\n" {
		t.Fatalf("期望 hello\\nworld\\n，got %q", fc.Content)
	}
}

// TestOSBackend_ReadWithOffsetLimit 验证按行 Offset/Limit 读取（1-based）。
func TestOSBackend_ReadWithOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b := newOSBackend()
	// Offset=2 从第 2 行起，Limit=2 读 2 行 → "b\nc"
	fc, err := b.Read(context.Background(), &filesystem.ReadRequest{FilePath: path, Offset: 2, Limit: 2})
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if fc.Content != "b\nc" {
		t.Fatalf("期望 b\\nc，got %q", fc.Content)
	}
}

// TestOSBackend_ReadNonExistent 验证读不存在文件返回错误。
func TestOSBackend_ReadNonExistent(t *testing.T) {
	b := newOSBackend()
	_, err := b.Read(context.Background(), &filesystem.ReadRequest{FilePath: filepath.Join(t.TempDir(), "no-such-file.md")})
	if err == nil {
		t.Error("期望读不存在文件报错")
	}
}

// TestOSBackend_GlobInfo 验证 */SKILL.md 通配匹配（skill middleware 的查找模式）。
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
	// 干扰文件：不应被 */SKILL.md 匹配
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// 二级目录的 SKILL.md 不应被一级通配匹配
	nested := filepath.Join(dir, "skill3", "deep")
	os.MkdirAll(nested, 0755)
	os.WriteFile(filepath.Join(nested, "SKILL.md"), []byte("nested"), 0644)

	b := newOSBackend()
	infos, err := b.GlobInfo(context.Background(), &filesystem.GlobInfoRequest{
		Pattern: "*/SKILL.md",
		Path:    dir,
	})
	if err != nil {
		t.Fatalf("GlobInfo 失败: %v", err)
	}
	if len(infos) != 2 {
		names := make([]string, 0, len(infos))
		for _, i := range infos {
			names = append(names, i.Path)
		}
		t.Fatalf("期望匹配 2 个一级 SKILL.md，got %d: %s", len(infos), strings.Join(names, ", "))
	}
	for _, info := range infos {
		if filepath.Base(info.Path) != "SKILL.md" {
			t.Errorf("期望 SKILL.md，got %s", info.Path)
		}
		if info.IsDir {
			t.Errorf("不应是目录: %s", info.Path)
		}
		if info.Size == 0 {
			t.Errorf("Size 不应为 0: %s", info.Path)
		}
	}
}

// TestOSBackend_GlobInfo_RelativeBase 防回归：base 为相对路径时，GlobInfo 必须返回绝对路径。
// 否则 eino filesystem_backend 会把"带 base 前缀的半路径"当相对路径再拼一次 BaseDir，
// 得到 data/skills/data/skills/x/SKILL.md 这样的重复路径，读不到文件。
func TestOSBackend_GlobInfo_RelativeBase(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "evolve")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# evolve"), 0644); err != nil {
		t.Fatal(err)
	}

	// 切到 dir，用相对 base "skills" 复现原 bug 触发条件
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
		t.Fatalf("GlobInfo 失败: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("期望匹配 1 个，got %d", len(infos))
	}
	if !filepath.IsAbs(infos[0].Path) {
		t.Errorf("相对 base 时 Path 必须为绝对路径，got %s", infos[0].Path)
	}
}

// TestOSBackend_UnsupportedMethods 验证 skill 不用的方法返回 not-supported。
func TestOSBackend_UnsupportedMethods(t *testing.T) {
	b := newOSBackend()
	ctx := context.Background()

	if _, err := b.LsInfo(ctx, &filesystem.LsInfoRequest{}); err == nil {
		t.Error("LsInfo 应返回 not-supported")
	}
	if _, err := b.GrepRaw(ctx, &filesystem.GrepRequest{}); err == nil {
		t.Error("GrepRaw 应返回 not-supported")
	}
	if err := b.Write(ctx, &filesystem.WriteRequest{}); err == nil {
		t.Error("Write 应返回 not-supported")
	}
	if err := b.Edit(ctx, &filesystem.EditRequest{}); err == nil {
		t.Error("Edit 应返回 not-supported")
	}
}

// TestOSBackend_NilRequestGuard 验证 nil 请求的保护。
func TestOSBackend_NilRequestGuard(t *testing.T) {
	b := newOSBackend()
	if _, err := b.Read(context.Background(), nil); err == nil {
		t.Error("Read(nil) 应报错")
	}
	if _, err := b.GlobInfo(context.Background(), nil); err == nil {
		t.Error("GlobInfo(nil) 应报错")
	}
}
