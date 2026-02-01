package security

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ExpandPath expands environment variables and home directory in a path
// Supports: ~, $HOME, %USERPROFILE%, %APPDATA%, %LOCALAPPDATA%, %SYSTEMROOT%
func ExpandPath(path string) string {
	if path == "" {
		return path
	}

	// Expand ~ to home directory
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = home + path[1:]
		}
	}

	// Expand environment variables
	// Handle both Unix ($VAR) and Windows (%VAR%) style
	path = os.ExpandEnv(path)

	// Handle Windows-style environment variables that weren't expanded
	// (in case running on non-Windows or env var not set)
	windowsEnvPattern := regexp.MustCompile(`%([^%]+)%`)
	path = windowsEnvPattern.ReplaceAllStringFunc(path, func(match string) string {
		varName := match[1 : len(match)-1]
		if val := os.Getenv(varName); val != "" {
			return val
		}
		return match // Keep original if not found
	})

	return path
}

// NormalizePath normalizes a path for the current platform
// - Handles forward/backward slashes
// - Expands environment variables
// - Normalizes case on Windows
func NormalizePath(path string) string {
	path = ExpandPath(path)

	// Normalize slashes
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, "/", "\\")
	} else {
		path = strings.ReplaceAll(path, "\\", "/")
	}

	// Clean the path
	path = filepath.Clean(path)

	// Normalize case on Windows
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}

	return path
}

// MatchesPathPattern checks if the content contains a path matching the pattern
// Uses glob-style matching
func MatchesPathPattern(content, pattern string) bool {
	// Expand pattern
	pattern = ExpandPath(pattern)

	// Extract potential paths from content
	paths := ExtractPaths(content)

	for _, path := range paths {
		// Normalize for comparison
		normalizedPath := NormalizePath(path)
		normalizedPattern := NormalizePath(pattern)

		// Direct match
		if normalizedPath == normalizedPattern {
			return true
		}

		// Glob match
		matched, _ := filepath.Match(normalizedPattern, normalizedPath)
		if matched {
			return true
		}

		// Check if path contains the pattern (for partial matches)
		// Remove leading * for substring matching
		patternBase := strings.TrimPrefix(normalizedPattern, "*")
		if patternBase != "" && strings.Contains(normalizedPath, patternBase) {
			return true
		}
	}

	return false
}

// ExtractPaths extracts potential file paths from content
func ExtractPaths(content string) []string {
	var paths []string
	seen := make(map[string]bool)

	// Unix-style absolute paths
	unixPathPattern := regexp.MustCompile(`(?:^|[\s"'=:])(/[a-zA-Z0-9._\-/]+)`)
	for _, match := range unixPathPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 && !seen[match[1]] {
			seen[match[1]] = true
			paths = append(paths, match[1])
		}
	}

	// Unix home-relative paths
	homePathPattern := regexp.MustCompile(`(?:^|[\s"'=:])(~[a-zA-Z0-9._\-/]*)`)
	for _, match := range homePathPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 && !seen[match[1]] {
			seen[match[1]] = true
			paths = append(paths, match[1])
		}
	}

	// Windows-style paths (C:\..., %USERPROFILE%\...)
	winPathPattern := regexp.MustCompile(`(?:^|[\s"'=:])([A-Za-z]:\\[a-zA-Z0-9._\-\\]+|%[A-Z_]+%[\\\/][a-zA-Z0-9._\-\\\/]+)`)
	for _, match := range winPathPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 && !seen[match[1]] {
			seen[match[1]] = true
			paths = append(paths, match[1])
		}
	}

	// Relative paths with sensitive indicators
	relPathPattern := regexp.MustCompile(`(?:^|[\s"'=:])(\.?[a-zA-Z0-9_\-]+(?:/[a-zA-Z0-9._\-]+)+)`)
	for _, match := range relPathPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 && isSensitiveRelPath(match[1]) && !seen[match[1]] {
			seen[match[1]] = true
			paths = append(paths, match[1])
		}
	}

	// File names that might be sensitive
	fileNamePattern := regexp.MustCompile(`(?:^|[\s"'=:])([a-zA-Z0-9._\-]+\.(?:pem|key|ppk|p12|pfx|jks|keystore|env))`)
	for _, match := range fileNamePattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 && !seen[match[1]] {
			seen[match[1]] = true
			paths = append(paths, match[1])
		}
	}

	return paths
}

// isSensitiveRelPath checks if a relative path contains sensitive indicators
func isSensitiveRelPath(path string) bool {
	sensitiveIndicators := []string{
		".ssh", ".aws", ".azure", ".kube", ".config/gcloud",
		".docker", ".npmrc", ".pypirc", ".netrc", ".git-credentials",
		".env", "secrets", "credentials", "config.json",
	}

	pathLower := strings.ToLower(path)
	for _, indicator := range sensitiveIndicators {
		if strings.Contains(pathLower, indicator) {
			return true
		}
	}
	return false
}

// GetCurrentPlatform returns the current OS identifier
func GetCurrentPlatform() string {
	return runtime.GOOS
}

// IsPlatformMatch checks if a platform specifier matches the current OS
// Supports: "all", "linux", "darwin", "windows"
func IsPlatformMatch(platform string) bool {
	if platform == "" || platform == "all" {
		return true
	}
	return platform == runtime.GOOS
}

// GetFilePathPatterns returns the built-in sensitive file path patterns
func GetFilePathPatterns() []*FilePathPattern {
	return []*FilePathPattern{
		// SSH keys
		{
			Name:     "ssh_private_key",
			Category: "ssh",
			Severity: SeverityCritical,
			Patterns: []string{
				"~/.ssh/id_rsa",
				"~/.ssh/id_dsa",
				"~/.ssh/id_ecdsa",
				"~/.ssh/id_ed25519",
				"~/.ssh/*_key",
				"%USERPROFILE%\\.ssh\\id_rsa",
				"%USERPROFILE%\\.ssh\\id_dsa",
				"%USERPROFILE%\\.ssh\\id_ecdsa",
				"%USERPROFILE%\\.ssh\\id_ed25519",
			},
			Platform: "all",
		},
		// AWS credentials
		{
			Name:     "aws_credentials",
			Category: "cloud",
			Severity: SeverityCritical,
			Patterns: []string{
				"~/.aws/credentials",
				"~/.aws/config",
				"%USERPROFILE%\\.aws\\credentials",
				"%USERPROFILE%\\.aws\\config",
			},
			Platform: "all",
		},
		// GCP credentials
		{
			Name:     "gcp_credentials",
			Category: "cloud",
			Severity: SeverityCritical,
			Patterns: []string{
				"~/.config/gcloud/application_default_credentials.json",
				"~/.config/gcloud/credentials.db",
				"*service_account*.json",
			},
			Platform: "all",
		},
		// Azure credentials
		{
			Name:     "azure_credentials",
			Category: "cloud",
			Severity: SeverityCritical,
			Patterns: []string{
				"~/.azure/accessTokens.json",
				"~/.azure/azureProfile.json",
				"%USERPROFILE%\\.azure\\accessTokens.json",
				"%USERPROFILE%\\.azure\\azureProfile.json",
			},
			Platform: "all",
		},
		// Kubernetes
		{
			Name:     "kubeconfig",
			Category: "cloud",
			Severity: SeverityHigh,
			Patterns: []string{
				"~/.kube/config",
				"%USERPROFILE%\\.kube\\config",
			},
			Platform: "all",
		},
		// Docker
		{
			Name:     "docker_config",
			Category: "cloud",
			Severity: SeverityHigh,
			Patterns: []string{
				"~/.docker/config.json",
				"%USERPROFILE%\\.docker\\config.json",
			},
			Platform: "all",
		},
		// Environment files
		{
			Name:     "env_file",
			Category: "env",
			Severity: SeverityHigh,
			Patterns: []string{
				".env",
				".env.local",
				".env.production",
				".env.development",
				"*.env",
			},
			Platform: "all",
		},
		// Private key files
		{
			Name:     "private_key_file",
			Category: "keys",
			Severity: SeverityCritical,
			Patterns: []string{
				"*.pem",
				"*.key",
				"*.ppk",
				"*.p12",
				"*.pfx",
			},
			Platform: "all",
		},
		// Git credentials
		{
			Name:     "git_credentials",
			Category: "vcs",
			Severity: SeverityHigh,
			Patterns: []string{
				"~/.git-credentials",
				"~/.gitconfig",
				"%USERPROFILE%\\.git-credentials",
				"%USERPROFILE%\\.gitconfig",
			},
			Platform: "all",
		},
		// NPM/PyPI credentials
		{
			Name:     "package_registry_credentials",
			Category: "registry",
			Severity: SeverityHigh,
			Patterns: []string{
				"~/.npmrc",
				"~/.pypirc",
				"%USERPROFILE%\\.npmrc",
				"%USERPROFILE%\\.pypirc",
			},
			Platform: "all",
		},
		// macOS specific
		{
			Name:     "macos_keychain",
			Category: "keychain",
			Severity: SeverityCritical,
			Patterns: []string{
				"~/Library/Keychains/*",
				"/Library/Keychains/*",
			},
			Platform: "darwin",
		},
		// Windows specific
		{
			Name:     "windows_credentials",
			Category: "windows",
			Severity: SeverityCritical,
			Patterns: []string{
				"%LOCALAPPDATA%\\Microsoft\\Credentials\\*",
				"%APPDATA%\\Microsoft\\Credentials\\*",
			},
			Platform: "windows",
		},
		// Linux specific
		{
			Name:     "linux_shadow",
			Category: "linux",
			Severity: SeverityCritical,
			Patterns: []string{
				"/etc/shadow",
				"/etc/passwd",
				"/etc/sudoers",
			},
			Platform: "linux",
		},
	}
}
