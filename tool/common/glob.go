package common

import (
	"path/filepath"
	"strings"
)

// MatchWithDoubleStar 匹配含 ** 的 glob 模式；** 匹配任意层目录（含零层）。
// pattern 与 relPath 均标准化为正斜杠。供 read/grep/glob 工具共用。
func MatchWithDoubleStar(pattern, relPath string) bool {
	relPath = filepath.ToSlash(relPath)
	pattern = filepath.ToSlash(pattern)
	return matchGlobParts(strings.Split(pattern, "/"), strings.Split(relPath, "/"))
}

// matchGlobParts 递归匹配 pattern 段与 path 段（** 贪心匹配任意层）。
func matchGlobParts(patternParts, pathParts []string) bool {
	if len(patternParts) == 0 && len(pathParts) == 0 {
		return true
	}
	if len(patternParts) == 0 {
		return false
	}
	if len(pathParts) == 0 {
		for _, p := range patternParts {
			if p != "**" {
				return false
			}
		}
		return true
	}
	if patternParts[0] == "**" {
		if matchGlobParts(patternParts[1:], pathParts) {
			return true
		}
		return matchGlobParts(patternParts, pathParts[1:])
	}
	matched, _ := filepath.Match(patternParts[0], pathParts[0])
	if !matched {
		return false
	}
	return matchGlobParts(patternParts[1:], pathParts[1:])
}
