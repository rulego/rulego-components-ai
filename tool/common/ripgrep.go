// ripgrep 探测与共享调用，供 grep/glob 工具复用。
// 仅探测系统 rg，不引入 bundled 二进制；缺失时由调用方走 Go 兜底实现。
package common

import (
	"os/exec"
	"sync"
)

var (
	rgOnce    sync.Once
	rgAvailable bool
)

// HasRipgrep 探测系统是否安装了 ripgrep（exec.LookPath("rg")），结果进程内缓存。
// 调用方应在缺失时降级到 Go 兜底实现（filepath.WalkDir + regexp）。
func HasRipgrep() bool {
	rgOnce.Do(func() {
		_, err := exec.LookPath("rg")
		rgAvailable = err == nil
	})
	return rgAvailable
}

// ResetRipgrepCache 重置 hasRipgrep 缓存，仅供测试使用。
// 测试可通过调用此函数 + 临时修改 PATH 强制走兜底路径。
func ResetRipgrepCache() {
	rgOnce = sync.Once{}
	rgAvailable = false
}
