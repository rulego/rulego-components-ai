//go:build linux || darwin || freebsd || openbsd

package bash

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr 设置进程组属性，用于在超时时 kill 整个进程组
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup kill 整个进程组（发送 SIGKILL 给 -pgid）
func killProcessGroup(pid int) {
	// pid 为负数表示进程组
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
