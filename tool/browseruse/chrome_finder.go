package browseruse

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// findChromePath attempts to find the Chrome executable path on the current system.
// It checks common installation directories and the system PATH.
// findChromePath 尝试在当前系统上查找 Chrome 可执行文件路径。
// 它检查常见的安装目录和系统 PATH。
func findChromePath() string {
	var paths []string

	switch runtime.GOOS {
	case "windows":
		paths = []string{
			os.Getenv("ProgramFiles") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("ProgramFiles(x86)") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("LocalAppData") + `\Google\Chrome\Application\chrome.exe`,
			filepath.Join(os.Getenv("USERPROFILE"), `AppData\Local\Google\Chrome\Application\chrome.exe`),
		}
	case "darwin":
		paths = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
		}
	case "linux":
		paths = []string{
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/snap/bin/chromium",
		}
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Try to find in PATH
	// 尝试在 PATH 中查找
	exeNames := []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"}
	if runtime.GOOS == "windows" {
		exeNames = []string{"chrome.exe"}
	}

	for _, name := range exeNames {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}

	return ""
}
