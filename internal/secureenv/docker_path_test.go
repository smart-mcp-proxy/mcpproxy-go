package secureenv

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerPathEnhancement(t *testing.T) {
	// Skip on Windows for this Docker-specific test
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Docker PATH test on Windows")
	}

	// Save original environment
	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	// Set up minimal Launchd-like environment (missing /usr/local/bin where Docker is typically installed)
	os.Clearenv()
	os.Setenv("PATH", "/usr/bin:/bin")
	os.Setenv("HOME", "/tmp/test-home")

	t.Run("PATH enhancement disabled by default", func(t *testing.T) {
		manager := NewManager(&EnvConfig{
			InheritSystemSafe: true,
			AllowedSystemVars: []string{"PATH", "HOME"},
			EnhancePath:       false, // Explicitly disabled
		})

		envVars := manager.BuildSecureEnvironment()

		// Convert to map for easier checking
		envMap := make(map[string]string)
		for _, envVar := range envVars {
			parts := strings.SplitN(envVar, "=", 2)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}

		// Should NOT be enhanced when disabled
		assert.Equal(t, "/usr/bin:/bin", envMap["PATH"], "PATH should not be enhanced when EnhancePath is false")
	})

	t.Run("PATH enhancement enabled for Docker scenarios", func(t *testing.T) {
		manager := NewManager(&EnvConfig{
			InheritSystemSafe: true,
			AllowedSystemVars: []string{"PATH", "HOME"},
			EnhancePath:       true, // Explicitly enabled
		})

		envVars := manager.BuildSecureEnvironment()

		// Convert to map for easier checking
		envMap := make(map[string]string)
		for _, envVar := range envVars {
			parts := strings.SplitN(envVar, "=", 2)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}

		// Should be enhanced to include common tool paths
		enhancedPath := envMap["PATH"]
		assert.Contains(t, enhancedPath, "/usr/local/bin", "Enhanced PATH should include /usr/local/bin for Docker")
		assert.Contains(t, enhancedPath, "/usr/bin", "Enhanced PATH should preserve original /usr/bin")
		assert.Contains(t, enhancedPath, "/bin", "Enhanced PATH should preserve original /bin")

		// Should prioritize discovered paths (they should come first)
		pathParts := strings.Split(enhancedPath, ":")
		assert.True(t, len(pathParts) > 2, "Enhanced PATH should have more entries than original")

		// /usr/local/bin should come before the original paths for priority
		localBinIndex := -1
		usrBinIndex := -1
		for i, part := range pathParts {
			if part == "/usr/local/bin" {
				localBinIndex = i
			}
			if part == "/usr/bin" {
				usrBinIndex = i
			}
		}

		assert.True(t, localBinIndex >= 0, "/usr/local/bin should be in the PATH")
		assert.True(t, usrBinIndex >= 0, "/usr/bin should be in the PATH")
		assert.True(t, localBinIndex < usrBinIndex, "/usr/local/bin should come before /usr/bin for priority")
	})

	t.Run("PATH enhancement skipped for comprehensive paths", func(t *testing.T) {
		// Set up a comprehensive PATH that already includes common tool directories
		os.Setenv("PATH", "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin")

		manager := NewManager(&EnvConfig{
			InheritSystemSafe: true,
			AllowedSystemVars: []string{"PATH", "HOME"},
			EnhancePath:       true, // Enabled, but should not enhance comprehensive paths
		})

		envVars := manager.BuildSecureEnvironment()

		// Convert to map for easier checking
		envMap := make(map[string]string)
		for _, envVar := range envVars {
			parts := strings.SplitN(envVar, "=", 2)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}

		// Should NOT be enhanced because it already has /usr/local/bin
		assert.Equal(t, "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin", envMap["PATH"],
			"Comprehensive PATH should not be enhanced")
	})
}

func TestDockerCommandScenario(t *testing.T) {
	// This test simulates the exact scenario reported: Docker command failing due to missing PATH
	// Skip on Windows
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Docker command test on Windows")
	}

	// Save original environment
	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	// Simulate Launchd environment with minimal PATH
	os.Clearenv()
	os.Setenv("PATH", "/usr/bin") // Very minimal, like Launchd might provide
	os.Setenv("HOME", "/tmp/test-home")

	// Create environment config with path enhancement enabled (like our fix does)
	manager := NewManager(&EnvConfig{
		InheritSystemSafe: true,
		AllowedSystemVars: []string{"PATH", "HOME", "USER", "TMPDIR"},
		EnhancePath:       true,
	})

	envVars := manager.BuildSecureEnvironment()

	// Verify the enhanced environment would help Docker be found
	var enhancedPath string
	for _, envVar := range envVars {
		if strings.HasPrefix(envVar, "PATH=") {
			enhancedPath = envVar[5:] // Remove "PATH=" prefix
			break
		}
	}

	require.NotEmpty(t, enhancedPath, "PATH should be present in environment")

	// The enhanced PATH should include directories where Docker is commonly installed
	expectedDirs := []string{"/usr/local/bin", "/opt/homebrew/bin"}
	for _, expectedDir := range expectedDirs {
		// Only check if the directory actually exists on the system
		if _, err := os.Stat(expectedDir); err == nil {
			assert.Contains(t, enhancedPath, expectedDir,
				"Enhanced PATH should include %s for Docker discovery", expectedDir)
		}
	}

	// Should still include the original minimal path
	assert.Contains(t, enhancedPath, "/usr/bin", "Enhanced PATH should preserve original paths")
}
