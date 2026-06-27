//go:build windows

package bash

import (
	"os"
	"os/exec"
)

// setSysProcAttr 在 Windows 上不做额外设置
func setSysProcAttr(cmd *exec.Cmd) {
}

// killProcessGroup 在 Windows 上终止进程。
// Windows 无进程组机制，只能 kill 直接进程；孙进程可能残留（OS 限制）。
func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
