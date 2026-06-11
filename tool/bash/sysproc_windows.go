//go:build windows

package bash

import (
	"os/exec"
)

// setSysProcAttr 在 Windows 上不做额外设置
// Windows 使用 Go 标准的 cmd.Process.Kill() 即可
func setSysProcAttr(cmd *exec.Cmd) {
	// Windows 不支持 Unix 进程组机制，Go 的 exec.CommandContext
	// 会通过 Job Object 自动清理子进程
}

// killProcessGroup 在 Windows 上直接 kill 进程即可
func killProcessGroup(pid int) {
	// Windows 上不做额外操作，由 Go 的 context 取消机制处理
}
