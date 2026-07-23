package common

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GitignoreMatcher parses.gitignore to check if the path has been ignored.
// Follow Git's official ignore rules (http://git-scm.com/docs/gitignore) for self-implementation.
// Support: # Comments, blank lines,! Reverse the index, directory ending /(directory only), * (not cross/), ** (cross/),? (Single character), anchor (including / from root).
// Semantics: last-match-wins (later defined! The former can be canceled).
type GitignoreMatcher struct {
	patterns []gitignorePattern
}

type gitignorePattern struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool // Tail /: Matches only the directory
}

// LoadGitignore reads.gitignore from the base directory (returns nil if no existence or no valid mode is present).
func LoadGitignore(base string) *GitignoreMatcher {
	data, err := os.ReadFile(filepath.Join(base, ".gitignore"))
	if err != nil {
		return nil
	}
	m := CompileIgnoreLines(strings.Split(string(data), "\n")...)
	if len(m.patterns) == 0 {
		return nil
	}
	return m
}

// CompileIgnoreLines compiles the.gitignore line into a matcher.
func CompileIgnoreLines(lines ...string) *GitignoreMatcher {
	m := &GitignoreMatcher{}
	for _, line := range lines {
		if p, ok := gitignoreLineToPattern(line); ok {
			m.patterns = append(m.patterns, p)
		}
	}
	return m
}

// gitignoreLineToPattern converts a line of gitignore to regexp.
func gitignoreLineToPattern(line string) (gitignorePattern, bool) {
	line = strings.TrimRight(line, "\r")
	line = strings.Trim(line, " ")
	if line == "" || strings.HasPrefix(line, "#") {
		return gitignorePattern{}, false
	}
	negate := false
	if strings.HasPrefix(line, "!") {
		negate = true
		line = line[1:]
	}
	// Escape Beginning \# \! (Literal #!)
	if strings.HasPrefix(line, "\\#") || strings.HasPrefix(line, "\\!") {
		line = line[1:]
	}
	dirOnly := strings.HasSuffix(line, "/")
	if dirOnly {
		line = strings.TrimSuffix(line, "/")
	}
	leadingSlash := strings.HasPrefix(line, "/")
	if leadingSlash {
		line = line[1:]
	}
	// **/ Prefix = Any layer (including zero layers), equivalent to non-anchoring
	leadingAny := strings.HasPrefix(line, "**/")
	// Anchoring: Pre-order / or with /(but **/ prefix counts as any layer)
	anchored := leadingSlash || (strings.Contains(line, "/") && !leadingAny)
	if leadingAny {
		line = strings.TrimPrefix(line, "**/")
	}

	// glob → regex: Use placeholders to avoid * **? Escaped by QuoteMeta
	const star, dstar, ques = "\x00", "\x01", "\x02"
	s := strings.ReplaceAll(line, "**", dstar)
	s = strings.ReplaceAll(s, "*", star)
	s = strings.ReplaceAll(s, "?", ques)
	s = regexp.QuoteMeta(s)
	s = strings.ReplaceAll(s, dstar, ".*")   // ** Cross /
	s = strings.ReplaceAll(s, star, "[^/]*") // * Not crossing /
	s = strings.ReplaceAll(s, ques, "[^/]")  // ? Single character (not crossing /)

	expr := s + "(/.*)?$"
	if anchored {
		expr = "^" + expr
	} else {
		expr = "^(|.*/)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return gitignorePattern{}, false
	}
	return gitignorePattern{re: re, negate: negate, dirOnly: dirOnly}, true
}

// Ignored checks whether relPath (relative to the.gitignore root, positive slash) is ignored.
// isDir distinguishes directories/files (the directory end / mode only matches directories).
func (m *GitignoreMatcher) Ignored(relPath string, isDir bool) bool {
	if m == nil {
		return false
	}
	relPath = filepath.ToSlash(relPath)
	ignored := false
	for _, p := range m.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if p.re.MatchString(relPath) {
			ignored = !p.negate
		}
	}
	return ignored
}
