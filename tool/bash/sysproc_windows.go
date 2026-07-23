//go:build windows

package bash

import (
	"os"
	"os/exec"
)

// setSysProcAttr does not make any additional settings on Windows
func setSysProcAttr(cmd *exec.Cmd) {
}

// killProcessGroup terminates processes on Windows.
// Windows has a no-process group mechanism, so you can only kill processes directly; Grandchild processes may remain (OS limitations).
func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
