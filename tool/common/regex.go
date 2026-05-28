package common

import (
	"regexp"
	"sync"
	"time"
)

const (
	// MaxRegexLength limits regex pattern length to prevent ReDoS attacks.
	MaxRegexLength = 1000

	// MaxRegexCompileTime limits regex compilation time.
	MaxRegexCompileTime = 100 * time.Millisecond
)

// RegexSafetyConfig holds regex safety configuration.
type RegexSafetyConfig struct {
	// MaxPatternLength maximum pattern length
	MaxPatternLength int `json:"maxPatternLength"`

	// MaxCompileTime maximum compilation time
	MaxCompileTime time.Duration `json:"maxCompileTime"`
}

// DefaultRegexSafetyConfig returns default regex safety configuration.
func DefaultRegexSafetyConfig() RegexSafetyConfig {
	return RegexSafetyConfig{
		MaxPatternLength: MaxRegexLength,
		MaxCompileTime:   MaxRegexCompileTime,
	}
}

// SafeRegex wraps regexp with safety checks.
type SafeRegex struct {
	*regexp.Regexp
	pattern string
	config  RegexSafetyConfig
}

// CompileSafeRegex compiles a regex with safety checks.
func CompileSafeRegex(pattern string, config RegexSafetyConfig) (*SafeRegex, error) {
	// Check pattern length
	if len(pattern) > config.MaxPatternLength {
		return nil, NewErrorf(ErrCodeRegexTooLong, "pattern exceeds %d characters", config.MaxPatternLength)
	}

	// Check for dangerous patterns that could cause catastrophic backtracking
	if err := checkDangerousPattern(pattern); err != nil {
		return nil, err
	}

	// Compile with timeout
	re, err := compileWithTimeout(pattern, config.MaxCompileTime)
	if err != nil {
		return nil, NewErrorf(ErrCodeRegexInvalid, "compilation failed: %v", err)
	}

	return &SafeRegex{
		Regexp:  re,
		pattern: pattern,
		config:  config,
	}, nil
}

// CompileRegex compiles a regex with default safety checks.
func CompileRegex(pattern string) (*regexp.Regexp, error) {
	safe, err := CompileSafeRegex(pattern, DefaultRegexSafetyConfig())
	if err != nil {
		return nil, err
	}
	return safe.Regexp, nil
}

// MustCompileRegex compiles a regex or panics.
func MustCompileRegex(pattern string) *regexp.Regexp {
	re, err := CompileRegex(pattern)
	if err != nil {
		panic(err)
	}
	return re
}

// checkDangerousPattern checks for patterns that could cause ReDoS.
func checkDangerousPattern(pattern string) error {
	// Patterns that can cause catastrophic backtracking:
	// - Nested quantifiers like ((a+)+)
	// - Alternations with overlapping patterns like (a|a)+

	// Simple heuristic: check for nested quantifiers
	// This is not comprehensive but catches common cases
	nestedQuantifiers := []string{
		`(\+|\*)\s*\)\s*(\+|\*)`,           // )+ or )*
		`(\+|\*)\s*(\+|\*)`,                // ++ or ** (direct nested)
		`\([^)]*\+\)\s*\+`,                 // (pattern+)+
		`\([^)]*\*\)\s*\*`,                 // (pattern*)*
	}

	for _, dangerous := range nestedQuantifiers {
		if matched, _ := regexp.MatchString(dangerous, pattern); matched {
			return NewError(ErrCodeRegexInvalid, "pattern may cause catastrophic backtracking (nested quantifiers)")
		}
	}

	return nil
}

// compileWithTimeout compiles regex with a timeout.
func compileWithTimeout(pattern string, timeout time.Duration) (*regexp.Regexp, error) {
	if timeout <= 0 {
		return regexp.Compile(pattern)
	}

	var (
		re   *regexp.Regexp
		err  error
		done = make(chan struct{})
	)

	go func() {
		re, err = regexp.Compile(pattern)
		close(done)
	}()

	select {
	case <-done:
		return re, err
	case <-time.After(timeout):
		return nil, NewErrorf(ErrCodeRegexInvalid, "compilation timed out after %v", timeout)
	}
}

// Global regex cache for commonly used patterns
var regexCache = &sync.Map{}

// GetCachedRegex returns a cached regex or compiles and caches a new one.
func GetCachedRegex(pattern string) (*regexp.Regexp, error) {
	if cached, ok := regexCache.Load(pattern); ok {
		return cached.(*regexp.Regexp), nil
	}

	re, err := CompileRegex(pattern)
	if err != nil {
		return nil, err
	}

	// Store in cache (may overwrite if raced, but that's fine)
	regexCache.Store(pattern, re)
	return re, nil
}

// ClearRegexCache clears the regex cache.
func ClearRegexCache() {
	regexCache.Range(func(key, value interface{}) bool {
		regexCache.Delete(key)
		return true
	})
}
