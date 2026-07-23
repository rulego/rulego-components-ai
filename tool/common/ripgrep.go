// ripgrep detection and shared calls are available for reuse by grep/glob tools.
// Only detects system RG, without introducing bundled binaries; If missing, the caller uses Go as a safety net.
package common

import (
	"os/exec"
	"sync"
)

var (
	rgOnce      sync.Once
	rgAvailable bool
)

// HasRipgrep detects whether the system has ripgrep(exec.LookPath("rg")), resulting in the process cache.
// The caller should downgrade to Go as a backup implementation (filepath.WalkDir + regexp) when it is missing.
func HasRipgrep() bool {
	rgOnce.Do(func() {
		_, err := exec.LookPath("rg")
		rgAvailable = err == nil
	})
	return rgAvailable
}

// ResetRipgrepCache resets hasRipgrep cache, for testing purposes only.
// Testing can be done by calling this function + temporarily modifying the PATH to force a shortcut path.
func ResetRipgrepCache() {
	rgOnce = sync.Once{}
	rgAvailable = false
}
