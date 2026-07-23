package common

import (
	"path/filepath"
	"strings"
)

// MatchWithDoubleStar matches glob patterns containing **; ** Matches any layer directory (including zero layers).
// pattern and relPath are both standardized to positive slashes. Shared by read/grep/glob tools.
func MatchWithDoubleStar(pattern, relPath string) bool {
	relPath = filepath.ToSlash(relPath)
	pattern = filepath.ToSlash(pattern)
	return matchGlobParts(strings.Split(pattern, "/"), strings.Split(relPath, "/"))
}

// matchGlobParts recursively matches pattern segments with path segments (** greedily matching arbitrary layers).
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
