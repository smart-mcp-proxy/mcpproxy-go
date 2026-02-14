//go:build windows

package secureenv

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadWindowsRegistryPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	path, err := readWindowsRegistryPath()

	// Registry read should succeed
	require.NoError(t, err, "Reading Windows registry PATH should not fail")
	require.NotEmpty(t, path, "Registry PATH should not be empty")

	// Path should contain system directories
	assert.Contains(t, strings.ToLower(path), `c:\windows\system32`,
		"Registry PATH should contain System32")

	// Path should be expanded (no %USERPROFILE% etc.)
	assert.NotContains(t, path, "%USERPROFILE%",
		"PATH should have %USERPROFILE% expanded")
	assert.NotContains(t, path, "%APPDATA%",
		"PATH should have %APPDATA% expanded")
	assert.NotContains(t, path, "%LOCALAPPDATA%",
		"PATH should have %LOCALAPPDATA% expanded")

	t.Logf("Registry PATH length: %d characters", len(path))
	pathParts := strings.Split(path, string(os.PathListSeparator))
	t.Logf("Registry PATH contains %d directories", len(pathParts))

	// Log first few paths for debugging
	for i, part := range pathParts {
		if i >= 5 {
			break
		}
		t.Logf("  [%d] %s", i, part)
	}
}

func TestDiscoverWindowsPathsFromRegistry(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	paths := discoverWindowsPathsFromRegistry()

	// Should return at least some paths
	assert.NotEmpty(t, paths, "Should discover at least some paths from registry")

	// All returned paths should exist
	for _, path := range paths {
		info, err := os.Stat(path)
		assert.NoError(t, err, "Path should exist: %s", path)
		if err == nil {
			assert.True(t, info.IsDir(), "Path should be a directory: %s", path)
		}
	}

	// Should contain common system paths
	hasSystem32 := false
	for _, path := range paths {
		if strings.Contains(strings.ToLower(path), `system32`) {
			hasSystem32 = true
			break
		}
	}
	assert.True(t, hasSystem32, "Should contain System32 directory")

	t.Logf("Discovered %d paths from registry", len(paths))
}

func TestWindowsPathExpansion(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "USERPROFILE expansion",
			input:    `%USERPROFILE%\.cargo\bin`,
			contains: `\Users\`,
		},
		{
			name:     "APPDATA expansion",
			input:    `%APPDATA%\npm`,
			contains: `\AppData\Roaming\`,
		},
		{
			name:     "LOCALAPPDATA expansion",
			input:    `%LOCALAPPDATA%\Programs`,
			contains: `\AppData\Local\`,
		},
		{
			name:     "PROGRAMFILES expansion",
			input:    `%PROGRAMFILES%\Git`,
			contains: `\Program Files\`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expanded := os.ExpandEnv(tt.input)

			// Should not contain % after expansion
			assert.NotContains(t, expanded, "%",
				"Expanded path should not contain %%: %s", expanded)

			// Should contain expected substring
			assert.Contains(t, expanded, tt.contains,
				"Expanded path should contain %s: %s", tt.contains, expanded)

			t.Logf("Input:  %s", tt.input)
			t.Logf("Output: %s", expanded)
		})
	}
}

func TestDiscoverWindowsPathsWithEmptyEnvironment(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	// Save original PATH
	originalPath := os.Getenv("PATH")
	defer os.Setenv("PATH", originalPath)

	// Simulate empty PATH scenario (installer/service launch)
	os.Setenv("PATH", "")

	// Create a manager
	manager := NewManager(nil)

	// Discovery should still work via registry
	paths := manager.pathDiscovery.DiscoveredPaths
	assert.NotEmpty(t, paths,
		"Should discover paths from registry even when PATH env is empty")

	// Should contain system paths
	hasSystemPath := false
	for _, path := range paths {
		lowerPath := strings.ToLower(path)
		if strings.Contains(lowerPath, "system32") || strings.Contains(lowerPath, "windows") {
			hasSystemPath = true
			break
		}
	}
	assert.True(t, hasSystemPath,
		"Should contain Windows system paths even when PATH env is empty")

	t.Logf("Discovered %d paths with empty PATH env", len(paths))
}

func TestManagerBuildSecureEnvironmentWithRegistryPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	// Save original PATH
	originalPath := os.Getenv("PATH")
	defer os.Setenv("PATH", originalPath)

	// Simulate minimal PATH scenario
	os.Setenv("PATH", `C:\Windows\System32`)

	// Create manager and build environment
	manager := NewManager(nil)
	env := manager.BuildSecureEnvironment()

	// Extract PATH from environment
	var builtPath string
	for _, envVar := range env {
		if strings.HasPrefix(envVar, "PATH=") {
			builtPath = strings.TrimPrefix(envVar, "PATH=")
			break
		}
	}

	assert.NotEmpty(t, builtPath, "Built environment should have PATH")

	// PATH should be more comprehensive than minimal input
	pathParts := strings.Split(builtPath, string(os.PathListSeparator))
	assert.Greater(t, len(pathParts), 5,
		"Built PATH should have more than 5 directories (got %d)", len(pathParts))

	t.Logf("Built PATH has %d directories", len(pathParts))
}
