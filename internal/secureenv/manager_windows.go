//go:build windows

package secureenv

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// expandWindowsEnvVars expands Windows-style %VAR% environment variables.
// os.ExpandEnv only handles $VAR/${VAR} syntax, NOT Windows %VAR%.
// registry.ExpandString calls the Windows ExpandEnvironmentStrings API.
func expandWindowsEnvVars(s string) string {
	expanded, err := registry.ExpandString(s)
	if err != nil {
		return s // fallback to unexpanded
	}
	return expanded
}

// readWindowsRegistryPath reads the PATH environment variable from Windows registry
// This is necessary because when mcpproxy is launched via installer/service,
// it doesn't inherit the user's PATH environment variable.
// The registry is the source of truth for Windows PATH configuration.
func readWindowsRegistryPath() (string, error) {
	var paths []string

	// 1. Read USER PATH from HKEY_CURRENT_USER\Environment\Path
	// This contains user-specific PATH additions (e.g., .cargo\bin, go\bin)
	userKey, err := registry.OpenKey(registry.CURRENT_USER,
		`Environment`, registry.QUERY_VALUE)
	if err == nil {
		defer userKey.Close()

		userPath, _, err := userKey.GetStringValue("Path")
		if err == nil && userPath != "" {
			// CRITICAL: Expand Windows %VAR% environment variables
			// Registry stores paths as REG_EXPAND_SZ with embedded %USERPROFILE% etc.
			paths = append(paths, expandWindowsEnvVars(userPath))
		}
	}

	// 2. Read SYSTEM PATH from HKEY_LOCAL_MACHINE\...\Environment\Path
	// This contains system-wide PATH (e.g., C:\Windows\System32, Program Files)
	sysKey, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Session Manager\Environment`,
		registry.QUERY_VALUE)
	if err == nil {
		defer sysKey.Close()

		systemPath, _, err := sysKey.GetStringValue("Path")
		if err == nil && systemPath != "" {
			paths = append(paths, expandWindowsEnvVars(systemPath))
		}
	}

	// Combine User PATH + System PATH (user takes precedence)
	fullPath := strings.Join(paths, string(os.PathListSeparator))

	if fullPath == "" {
		// If both registry reads failed, return error
		return "", registry.ErrNotExist
	}

	return fullPath, nil
}

// discoverWindowsPathsFromRegistry reads PATH from registry and returns as slice
// This replaces the hardcoded discovery list when registry is available
func discoverWindowsPathsFromRegistry() []string {
	registryPath, err := readWindowsRegistryPath()
	if err != nil {
		// Registry read failed, return empty slice (caller will use hardcoded fallback)
		return nil
	}

	// Split the combined PATH into individual directories
	parts := strings.Split(registryPath, string(os.PathListSeparator))

	// Filter to only existing directories
	var existingPaths []string
	for _, path := range parts {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		// Check if directory exists
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			existingPaths = append(existingPaths, path)
		}
	}

	return existingPaths
}
