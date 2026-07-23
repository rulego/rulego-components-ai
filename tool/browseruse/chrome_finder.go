package browseruse

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// findChromePath attempts to find the Chrome executable path on the current system.
// It checks common installation directories and the system PATH.
// findChromePath attempts to find the path to the Chrome executable on your current system.
// It checks common installation directories and system PATHs.
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
	// Try to find it in PATH
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
