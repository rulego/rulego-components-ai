package bash

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// ShellType defines the type of shell being used.
type ShellType string

const (
	ShellTypeBash       ShellType = "bash"       // Unix/Git Bash
	ShellTypePowerShell ShellType = "powershell" // Windows PowerShell
	ShellTypeCMD        ShellType = "cmd"        // Windows CMD
	ShellTypeSh         ShellType = "sh"         // Unix sh
)

// PlatformConfig holds platform-specific command configurations.
type PlatformConfig struct {
	DefaultAllow    []string
	DefaultDeny     []string
	DefaultDenyArgs []string
	ShellCommand    string
	ShellArgs       []string
	ShellType       ShellType // Type of shell for AI to understand command style
}

// GetPlatformConfig returns the platform-specific configuration.
func GetPlatformConfig() PlatformConfig {
	switch runtime.GOOS {
	case "windows":
		return getWindowsConfig()
	case "darwin", "linux", "freebsd", "openbsd":
		return getUnixConfig()
	default:
		return getUnixConfig() // Default to Unix-like
	}
}

// windowsConfigCache caches the detected Windows configuration
var windowsConfigCache PlatformConfig
var windowsConfigOnce sync.Once

// hasBash checks if a usable bash.exe (Git Bash) is available
func hasBash() (string, bool) {
	// 1. Check if bash.exe is in PATH
	path, err := exec.LookPath("bash.exe")
	if err == nil {
		// Verify if it's usable by running a simple version check
		cmd := exec.Command(path, "--version")
		_, err := cmd.CombinedOutput()
		if err == nil {
			return path, true
		}
	}

	// 2. Try common Git Bash paths if PATH lookup failed or was invalid
	commonPaths := []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		`D:\Program Files\Git\bin\bash.exe`,
	}
	// Try user installation path
	if home, err := os.UserHomeDir(); err == nil {
		commonPaths = append(commonPaths, filepath.Join(home, `AppData\Local\Programs\Git\bin\bash.exe`))
	}

	// 3. Try to find bash via git.exe
	if gitPath, err := exec.LookPath("git.exe"); err == nil {
		// Common layout: .../Git/cmd/git.exe or .../Git/bin/git.exe
		// We want to check .../Git/bin/bash.exe
		dir := filepath.Dir(gitPath)
		// Try bash in the same directory (bin)
		if p := filepath.Join(dir, "bash.exe"); !contains(commonPaths, p) {
			commonPaths = append(commonPaths, p)
		}
		// Try bash in sibling bin directory (from cmd)
		// e.g. C:\Program Files\Git\cmd\git.exe -> C:\Program Files\Git\bin\bash.exe
		if p := filepath.Join(filepath.Dir(dir), "bin", "bash.exe"); !contains(commonPaths, p) {
			commonPaths = append(commonPaths, p)
		}
	}

	for _, p := range commonPaths {
		if _, err := exec.LookPath(p); err == nil {
			// Verify this bash path as well
			cmd := exec.Command(p, "--version")
			_, err := cmd.CombinedOutput()
			if err == nil {
				return p, true
			}
		}
	}

	return "", false
}

// contains checks if a slice contains a string (case-insensitive)
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, item) {
			return true
		}
	}
	return false
}

// getWindowsConfig returns the platform-specific configuration for Windows
func getWindowsConfig() PlatformConfig {
	windowsConfigOnce.Do(func() {
		// Common allow list for both shells
		commonAllow := []string{
			// Cross-platform tools
			"git", "npm", "npx", "node", "yarn", "pnpm", "bun", "deno",
			"go", "python", "python3", "pip", "pip3",
			"docker", "kubectl", "helm",
			// Rust tools
			"cargo", "rustc",
			// Build tools
			"make", "cmake", "gradle", "mvn",
			// Cloud tools
			"aws", "gcloud", "az", "terraform",
			// AI tools
			"openteam",
			// Data processing
			"jq", "yq",
			// Network tools
			"netstat", "ping",
			// System info (read-only)
			"ps", "df", "du", "uname", "whoami", "hostname",
			// File utilities
			"file",
		}

		// Common deny commands
		commonDeny := []string{
			"format", "diskpart", "chkdsk", "sfc",
			"reg", "regedit", "gpedit",
		}

		bashPath, ok := hasBash()
		if ok {
			// Git Bash available - use Unix-style commands
			windowsConfigCache = PlatformConfig{
				DefaultAllow: append(commonAllow,
					"ls", "pwd", "echo", "cat", "head", "tail",
					"grep", "find", "mkdir", "touch",
					"cp", "mv", "rm", "sed", "awk", "wc",
					"curl", "wget", "tar", "unzip", "cd",
				),
				DefaultDeny: append(commonDeny, "powershell", "pwsh", "cmd"),
				DefaultDenyArgs: []string{
					"-rf /", "-rf /*", "--no-preserve-root", "/dev/sd",
					"/s", "/q", "/f", "del /", "format /",
				},
				ShellCommand: bashPath, // Use detected path
				ShellArgs:    []string{"-c"},
				ShellType:    ShellTypeBash,
			}
		} else {
			// No Git Bash - fallback to PowerShell
			windowsConfigCache = PlatformConfig{
				DefaultAllow: append(commonAllow,
					// PowerShell aliases (Unix-like)
					"ls", "pwd", "echo", "cat", "head", "tail",
					"grep", "mkdir", "touch", "cp", "mv", "rm",
					// Windows native commands
					"dir", "type", "copy", "move", "del", "rmdir",
					"findstr", "where", "certutil", "cd",
					// Network tools (use .exe to bypass alias)
					"curl.exe", "wget.exe",
				),
				DefaultDeny: append(commonDeny, "bash", "sh"),
				DefaultDenyArgs: []string{
					"-recurse -force", "-executionpolicy",
					"invoke-expression", "iex",
					"downloadstring", "downloadfile",
					"/s", "/q", "/f", "del /", "format /",
				},
				ShellCommand: "powershell.exe",
				// Use OutputEncoding to support UTF-8 for PowerShell output (sent to Go)
				// Keep InputEncoding default to correctly read legacy command output (e.g. ping in GBK)
				ShellArgs: []string{
					"-NoProfile",
					"-NonInteractive",
					"-ExecutionPolicy", "Bypass",
					"-Command",
					"[Console]::OutputEncoding=[System.Text.Encoding]::UTF8;",
				},
				ShellType: ShellTypePowerShell,
			}
		}
	})

	return windowsConfigCache
}

// getUnixConfig returns the platform-specific configuration for Unix-like systems
func getUnixConfig() PlatformConfig {
	return PlatformConfig{
		DefaultAllow: []string{
			// Cross-platform tools
			"git", "npm", "npx", "node", "yarn", "pnpm", "bun", "deno",
			"go", "python", "python3", "pip", "pip3",
			"docker", "kubectl", "helm",
			// Rust tools
			"cargo", "rustc",
			// Build tools
			"make", "cmake", "gradle", "mvn",
			// Cloud tools
			"aws", "gcloud", "az", "terraform",
			// AI tools
			"openteam",
			// Data processing
			"jq", "yq",
			// Network tools
			"netstat", "ping",
			// System info (read-only)
			"ps", "df", "du", "uname", "whoami", "hostname",
			// Unix native commands
			"ls", "pwd", "echo", "cat", "head", "tail",
			"grep", "find", "wc", "mkdir", "touch",
			"cp", "mv", "sed", "awk", "rm", "cd",
		},
		DefaultDeny: []string{
			"su", "chmod", "chown",
			"dd", "mkfs", "fdisk", "shutdown", "reboot",
		},
		DefaultDenyArgs: []string{
			// 危险路径（禁止删除/操作系统关键目录）
			"-rf /",
			"-rf /*",
			"--no-preserve-root",
			"/dev/sd",
			"-rf /etc",
			"-rf /usr",
			"-rf /var",
			"-rf /home",
			"-rf /root",
			"-rf /bin",
			"-rf /sbin",
			"-rf /lib",
			"-rf /boot",
		},
		ShellCommand: "/bin/sh",
		ShellArgs:    []string{"-c"},
		ShellType:    ShellTypeSh,
	}
}
