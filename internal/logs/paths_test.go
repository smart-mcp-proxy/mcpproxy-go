package logs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLogDir(t *testing.T) {
	logDir, err := GetLogDir()
	require.NoError(t, err)
	require.NotEmpty(t, logDir)

	// Verify it contains mcpproxy somewhere in the path
	assert.Contains(t, logDir, "mcpproxy")

	// Verify the path is absolute
	assert.True(t, filepath.IsAbs(logDir))
}

func TestOSSpecificLogDirs(t *testing.T) {
	tests := []struct {
		name     string
		os       string
		expected []string // Possible path components that should be present
	}{
		{
			name:     "Windows",
			os:       "windows",
			expected: []string{"mcpproxy", "logs"},
		},
		{
			name:     "macOS",
			os:       "darwin",
			expected: []string{"Library", "Logs", "mcpproxy"},
		},
		{
			name:     "Linux",
			os:       "linux",
			expected: []string{"mcpproxy", "logs"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip if not running on the target OS
			if runtime.GOOS != tt.os {
				t.Skipf("Skipping %s test on %s", tt.name, runtime.GOOS)
			}

			logDir, err := GetLogDir()
			require.NoError(t, err)

			// Check that expected path components are present
			for _, component := range tt.expected {
				assert.Contains(t, logDir, component,
					"Log directory should contain %s: %s", component, logDir)
			}
		})
	}
}

func TestGetWindowsLogDir(t *testing.T) {
	// Save original environment variables
	originalLocalAppData := os.Getenv("LOCALAPPDATA")
	originalUserProfile := os.Getenv("USERPROFILE")

	defer func() {
		// Restore original environment variables
		if originalLocalAppData != "" {
			os.Setenv("LOCALAPPDATA", originalLocalAppData)
		} else {
			os.Unsetenv("LOCALAPPDATA")
		}

		if originalUserProfile != "" {
			os.Setenv("USERPROFILE", originalUserProfile)
		} else {
			os.Unsetenv("USERPROFILE")
		}
	}()

	t.Run("with LOCALAPPDATA", func(t *testing.T) {
		testPath := filepath.Join("C:", "Users", "testuser", "AppData", "Local")
		os.Setenv("LOCALAPPDATA", testPath)

		logDir, err := getWindowsLogDir()
		require.NoError(t, err)

		expected := filepath.Join(testPath, "mcpproxy", "logs")
		assert.Equal(t, expected, logDir)
	})

	t.Run("with USERPROFILE fallback", func(t *testing.T) {
		os.Unsetenv("LOCALAPPDATA")
		testUserProfile := filepath.Join("C:", "Users", "testuser")
		os.Setenv("USERPROFILE", testUserProfile)

		logDir, err := getWindowsLogDir()
		require.NoError(t, err)

		expected := filepath.Join(testUserProfile, "AppData", "Local", "mcpproxy", "logs")
		assert.Equal(t, expected, logDir)
	})

	t.Run("fallback to default", func(t *testing.T) {
		os.Unsetenv("LOCALAPPDATA")
		os.Unsetenv("USERPROFILE")

		logDir, err := getWindowsLogDir()
		require.NoError(t, err)

		// Should fallback to default which includes mcpproxy
		assert.Contains(t, logDir, "mcpproxy")
	})
}

func TestGetMacOSLogDir(t *testing.T) {
	logDir, err := getMacOSLogDir()
	require.NoError(t, err)

	// Should contain Library/Logs/mcpproxy
	assert.Contains(t, logDir, "Library")
	assert.Contains(t, logDir, "Logs")
	assert.Contains(t, logDir, "mcpproxy")
	assert.True(t, strings.HasSuffix(logDir, filepath.Join("Library", "Logs", "mcpproxy")))
}

func TestGetLinuxLogDir(t *testing.T) {
	// Save original environment variable
	originalXDGStateHome := os.Getenv("XDG_STATE_HOME")

	defer func() {
		if originalXDGStateHome != "" {
			os.Setenv("XDG_STATE_HOME", originalXDGStateHome)
		} else {
			os.Unsetenv("XDG_STATE_HOME")
		}
	}()

	t.Run("regular user with XDG_STATE_HOME", func(t *testing.T) {
		// Skip if running as root
		if os.Getuid() == 0 {
			t.Skip("Skipping regular user test when running as root")
		}

		testStateDir := "/tmp/test-xdg-state"
		os.Setenv("XDG_STATE_HOME", testStateDir)

		logDir, err := getLinuxLogDir()
		require.NoError(t, err)

		expected := filepath.Join(testStateDir, "mcpproxy", "logs")
		assert.Equal(t, expected, logDir)
	})

	t.Run("regular user without XDG_STATE_HOME", func(t *testing.T) {
		// Skip if running as root
		if os.Getuid() == 0 {
			t.Skip("Skipping regular user test when running as root")
		}

		os.Unsetenv("XDG_STATE_HOME")

		logDir, err := getLinuxLogDir()
		require.NoError(t, err)

		// Should use ~/.local/state/mcpproxy/logs
		assert.Contains(t, logDir, ".local")
		assert.Contains(t, logDir, "state")
		assert.Contains(t, logDir, "mcpproxy")
		assert.Contains(t, logDir, "logs")
	})
}

func TestEnsureLogDir(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	testLogDir := filepath.Join(tempDir, "test", "logs")

	// Ensure the directory doesn't exist initially
	_, err := os.Stat(testLogDir)
	assert.True(t, os.IsNotExist(err))

	// Create the directory
	err = EnsureLogDir(testLogDir)
	require.NoError(t, err)

	// Verify the directory exists
	info, err := os.Stat(testLogDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Verify permissions (Windows has different permission handling)
	if runtime.GOOS == "windows" {
		// On Windows, permissions are different and less granular
		assert.True(t, info.Mode().IsDir())
	} else {
		// On Unix-like systems, verify exact permissions (should be 0755)
		assert.Equal(t, os.FileMode(0755), info.Mode().Perm())
	}
}

func TestGetLogFilePath(t *testing.T) {
	filename := "test.log"

	logFilePath, err := GetLogFilePath(filename)
	require.NoError(t, err)

	// Should be absolute path
	assert.True(t, filepath.IsAbs(logFilePath))

	// Should end with the filename
	assert.True(t, strings.HasSuffix(logFilePath, filename))

	// Should contain mcpproxy in the path
	assert.Contains(t, logFilePath, "mcpproxy")

	// Directory should exist after calling GetLogFilePath
	logDir := filepath.Dir(logFilePath)
	info, err := os.Stat(logDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestGetLogDirInfo(t *testing.T) {
	info, err := GetLogDirInfo()
	require.NoError(t, err)
	require.NotNil(t, info)

	// Verify required fields are set
	assert.NotEmpty(t, info.Path)
	assert.NotEmpty(t, info.OS)
	assert.NotEmpty(t, info.Description)
	assert.NotEmpty(t, info.Standard)

	// Verify OS matches runtime
	assert.Equal(t, runtime.GOOS, info.OS)

	// Verify path is absolute
	assert.True(t, filepath.IsAbs(info.Path))

	// Verify path contains mcpproxy
	assert.Contains(t, info.Path, "mcpproxy")
}

func TestGetLogDirInfoOSSpecific(t *testing.T) {
	info, err := GetLogDirInfo()
	require.NoError(t, err)

	switch runtime.GOOS {
	case "windows":
		assert.Contains(t, info.Description, "Windows")
		assert.Contains(t, info.Standard, "Windows Application Data")
	case "darwin":
		assert.Contains(t, info.Description, "macOS")
		assert.Contains(t, info.Standard, "macOS File System")
	case "linux":
		assert.Contains(t, info.Description, "Linux")
		assert.Contains(t, info.Standard, "XDG Base Directory")
	default:
		assert.Contains(t, info.Description, "Fallback")
		assert.Contains(t, info.Standard, "Default")
	}
}

// TestIntegrationLogDirCreation tests the complete flow of creating and using log directories
func TestIntegrationLogDirCreation(t *testing.T) {
	// This is an integration test that verifies the complete flow

	// Get log directory info
	info, err := GetLogDirInfo()
	require.NoError(t, err)

	// Get a log file path
	logFile, err := GetLogFilePath("integration-test.log")
	require.NoError(t, err)

	// Verify the file path is within the info path
	assert.True(t, strings.HasPrefix(logFile, info.Path))

	// Create a test file to ensure write permissions
	testContent := "integration test log entry\n"
	err = os.WriteFile(logFile, []byte(testContent), 0644)
	require.NoError(t, err)

	// Verify we can read it back
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)
	assert.Equal(t, testContent, string(content))

	// Cleanup
	err = os.Remove(logFile)
	require.NoError(t, err)
}

// BenchmarkGetLogDir benchmarks the log directory resolution
func BenchmarkGetLogDir(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := GetLogDir()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetLogFilePath benchmarks getting a complete log file path
func BenchmarkGetLogFilePath(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := GetLogFilePath("benchmark.log")
		if err != nil {
			b.Fatal(err)
		}
	}
}
