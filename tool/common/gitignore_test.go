package common

import "testing"

func TestGitignore_CommonPatterns(t *testing.T) {
	m := CompileIgnoreLines(
		"node_modules/",
		"*.log",
		"dist/",
		"/build",    // 锚定根
		"*.tmp",
		"!keep.tmp", // 取反
		"# comment",
		"",
	)
	cases := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"node_modules", true, true},          // 目录模式
		{"node_modules", false, false},        // 目录模式不匹配文件
		{"app.log", false, true},              // *.log
		{"logs/app.log", false, true},         // *.log 任意层
		{"dist", true, true},                  // dist/ 目录
		{"build", true, true},                 // /build 锚定根
		{"sub/build", true, false},            // /build 只根
		{"a.tmp", false, true},                // *.tmp
		{"keep.tmp", false, false},            // !keep.tmp 取反
		{"main.go", false, false},             // 不忽略
		{"src/main.go", false, false},         // 不忽略
	}
	for _, c := range cases {
		if got := m.Ignored(c.path, c.isDir); got != c.want {
			t.Errorf("Ignored(%q, isDir=%v) = %v, want %v", c.path, c.isDir, got, c.want)
		}
	}
}

func TestGitignore_NilSafe(t *testing.T) {
	var m *GitignoreMatcher
	if m.Ignored("any", false) {
		t.Error("nil matcher should not ignore")
	}
}

func TestGitignore_DoubleStar(t *testing.T) {
	m := CompileIgnoreLines("**/cache/")
	cases := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"cache", true, true},
		{"a/cache", true, true},
		{"a/b/cache", true, true},
		{"a/b/cache/x", false, false}, // 文件，dirOnly 不匹配
	}
	for _, c := range cases {
		if got := m.Ignored(c.path, c.isDir); got != c.want {
			t.Errorf("Ignored(%q, isDir=%v) = %v, want %v", c.path, c.isDir, got, c.want)
		}
	}
}
