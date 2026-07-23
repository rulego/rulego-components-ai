package common

import "testing"

func TestGitignore_CommonPatterns(t *testing.T) {
	m := CompileIgnoreLines(
		"node_modules/",
		"*.log",
		"dist/",
		"/build", // Anchor root
		"*.tmp",
		"!keep.tmp", // and took Fan
		"# comment",
		"",
	)
	cases := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"node_modules", true, true},   // Directory mode
		{"node_modules", false, false}, // Directory mode does not match files
		{"app.log", false, true},       // *.log
		{"logs/app.log", false, true},  // *.log Any layer
		{"dist", true, true},           // dist/ Directory
		{"build", true, true},          // /build anchor root
		{"sub/build", true, false},     // /build Only root
		{"a.tmp", false, true},         // *.tmp
		{"keep.tmp", false, false},     // keep.tmp Taken as the opposite
		{"main.go", false, false},      // Don't overlook it
		{"src/main.go", false, false},  // Don't overlook it
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
		{"a/b/cache/x", false, false}, // file, dirOnly does not match
	}
	for _, c := range cases {
		if got := m.Ignored(c.path, c.isDir); got != c.want {
			t.Errorf("Ignored(%q, isDir=%v) = %v, want %v", c.path, c.isDir, got, c.want)
		}
	}
}
