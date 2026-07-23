//go:build linux || darwin || freebsd || openbsd

package bash

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr sets the process group properties to kill the entire process group during timeout
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup kill the entire process group (send SIGKILL to -pgid)
func killProcessGroup(pid int) {
	// pid is a negative number representing a process group
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
