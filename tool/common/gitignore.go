package common

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GitignoreMatcher 解析 .gitignore，判断路径是否被忽略。
// 遵循 git 官方 ignore 规则（http://git-scm.com/docs/gitignore）自实现。
// 支持：# 注释、空行、! 取反、目录尾 /（仅目录）、* (不跨/)、** (跨/)、? (单字符)、锚定（含 / 从根）。
// 语义：last-match-wins（后定义的 ! 可取消前者的忽略）。
type GitignoreMatcher struct {
	patterns []gitignorePattern
}

type gitignorePattern struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool // 尾部 / ：仅匹配目录
}

// LoadGitignore 从 base 目录读 .gitignore（不存在或无有效模式返回 nil）。
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

// CompileIgnoreLines 把 .gitignore 行编译成 matcher。
func CompileIgnoreLines(lines ...string) *GitignoreMatcher {
	m := &GitignoreMatcher{}
	for _, line := range lines {
		if p, ok := gitignoreLineToPattern(line); ok {
			m.patterns = append(m.patterns, p)
		}
	}
	return m
}

// gitignoreLineToPattern 把一行 gitignore 转 regexp。
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
	// 转义开头 \# \!（字面 # !）
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
	// **/ 前缀 = 任意层（含零层），相当于非锚定
	leadingAny := strings.HasPrefix(line, "**/")
	// 锚定：前导 / 或含 /（但 **/ 前缀算任意层）
	anchored := leadingSlash || (strings.Contains(line, "/") && !leadingAny)
	if leadingAny {
		line = strings.TrimPrefix(line, "**/")
	}

	// glob → regex：用占位符避免 * ** ? 被 QuoteMeta 转义
	const star, dstar, ques = "\x00", "\x01", "\x02"
	s := strings.ReplaceAll(line, "**", dstar)
	s = strings.ReplaceAll(s, "*", star)
	s = strings.ReplaceAll(s, "?", ques)
	s = regexp.QuoteMeta(s)
	s = strings.ReplaceAll(s, dstar, ".*")   // ** 跨 /
	s = strings.ReplaceAll(s, star, "[^/]*") // * 不跨 /
	s = strings.ReplaceAll(s, ques, "[^/]")  // ? 单字符（不跨 /）

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

// Ignored 判断 relPath（相对 .gitignore 根，正斜杠）是否被忽略。
// isDir 区分目录/文件（目录尾 / 的模式只匹配目录）。
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
