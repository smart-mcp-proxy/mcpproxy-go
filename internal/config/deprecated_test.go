package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckDeprecatedFields(t *testing.T) {
	tests := []struct {
		name         string
		configJSON   string
		expectedKeys []string
	}{
		{
			name:         "no deprecated fields",
			configJSON:   `{"listen": ":8080", "tools_limit": 15}`,
			expectedKeys: nil,
		},
		{
			name:         "top_k present",
			configJSON:   `{"listen": ":8080", "top_k": 5}`,
			expectedKeys: []string{"top_k"},
		},
		{
			name:         "enable_tray present",
			configJSON:   `{"listen": ":8080", "enable_tray": true}`,
			expectedKeys: []string{"enable_tray"},
		},
		{
			name:         "features present",
			configJSON:   `{"listen": ":8080", "features": {}}`,
			expectedKeys: []string{"features"},
		},
		{
			name:         "all deprecated fields present",
			configJSON:   `{"listen": ":8080", "top_k": 5, "enable_tray": true, "features": {"enable_runtime": true}}`,
			expectedKeys: []string{"top_k", "enable_tray", "features"},
		},
		{
			name:         "activity fields are NOT deprecated",
			configJSON:   `{"listen": ":8080", "activity_retention_days": 90, "activity_max_records": 100000}`,
			expectedKeys: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write temp config file
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "mcp_config.json")
			err := os.WriteFile(cfgPath, []byte(tt.configJSON), 0644)
			require.NoError(t, err)

			found := CheckDeprecatedFields(cfgPath)

			if tt.expectedKeys == nil {
				assert.Empty(t, found)
				return
			}

			assert.Len(t, found, len(tt.expectedKeys))
			foundKeys := make([]string, len(found))
			for i, f := range found {
				foundKeys[i] = f.JSONKey
			}
			for _, key := range tt.expectedKeys {
				assert.Contains(t, foundKeys, key)
			}
		})
	}
}

func TestCheckDeprecatedFields_MissingFile(t *testing.T) {
	found := CheckDeprecatedFields("/nonexistent/config.json")
	assert.Nil(t, found)
}

func TestCheckDeprecatedFields_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.json")
	err := os.WriteFile(cfgPath, []byte("not json"), 0644)
	require.NoError(t, err)

	found := CheckDeprecatedFields(cfgPath)
	assert.Nil(t, found)
}

func TestCleanDeprecatedFields(t *testing.T) {
	tests := []struct {
		name         string
		configJSON   string
		wantRemoved  []string
		wantKeepKeys []string
		wantBackup   bool
		wantNoModify bool // file should not be touched
	}{
		{
			name:         "removes all deprecated fields",
			configJSON:   `{"listen": ":8080", "top_k": 5, "enable_tray": true, "features": {"enable_runtime": true}}`,
			wantRemoved:  []string{"top_k", "enable_tray", "features"},
			wantKeepKeys: []string{"listen"},
			wantBackup:   true,
		},
		{
			name:         "removes only top_k",
			configJSON:   `{"listen": ":8080", "tools_limit": 15, "top_k": 5}`,
			wantRemoved:  []string{"top_k"},
			wantKeepKeys: []string{"listen", "tools_limit"},
			wantBackup:   true,
		},
		{
			name:         "no deprecated fields - no backup created",
			configJSON:   `{"listen": ":8080", "tools_limit": 15}`,
			wantRemoved:  nil,
			wantKeepKeys: []string{"listen", "tools_limit"},
			wantNoModify: true,
		},
		{
			name:         "preserves nested objects and arrays",
			configJSON:   `{"listen": ":8080", "top_k": 5, "mcpServers": [{"name": "github", "url": "https://example.com"}]}`,
			wantRemoved:  []string{"top_k"},
			wantKeepKeys: []string{"listen", "mcpServers"},
			wantBackup:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "mcp_config.json")
			err := os.WriteFile(cfgPath, []byte(tt.configJSON), 0644)
			require.NoError(t, err)

			removed, err := CleanDeprecatedFields(cfgPath)
			require.NoError(t, err)

			// Check removed fields
			if tt.wantRemoved == nil {
				assert.Empty(t, removed)
			} else {
				removedKeys := make([]string, len(removed))
				for i, r := range removed {
					removedKeys[i] = r.JSONKey
				}
				assert.ElementsMatch(t, tt.wantRemoved, removedKeys)
			}

			// Check backup file
			backupPath := cfgPath + ".bak"
			if tt.wantBackup {
				backupData, err := os.ReadFile(backupPath)
				require.NoError(t, err, "backup file should exist")
				assert.JSONEq(t, tt.configJSON, string(backupData), "backup should match original")
			} else {
				_, err := os.Stat(backupPath)
				assert.True(t, os.IsNotExist(err), "backup should not exist when no fields removed")
			}

			// Check cleaned config still has expected keys
			cleanedData, err := os.ReadFile(cfgPath)
			require.NoError(t, err)
			var cleanedMap map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(cleanedData, &cleanedMap))

			for _, key := range tt.wantKeepKeys {
				assert.Contains(t, cleanedMap, key, "key %q should be preserved", key)
			}
			for _, key := range tt.wantRemoved {
				assert.NotContains(t, cleanedMap, key, "key %q should be removed", key)
			}
		})
	}
}

func TestCleanDeprecatedFields_MissingFile(t *testing.T) {
	removed, err := CleanDeprecatedFields("/nonexistent/config.json")
	assert.NoError(t, err)
	assert.Nil(t, removed)
}

func TestCleanDeprecatedFields_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.json")
	err := os.WriteFile(cfgPath, []byte("not json"), 0644)
	require.NoError(t, err)

	removed, err := CleanDeprecatedFields(cfgPath)
	assert.NoError(t, err)
	assert.Nil(t, removed)
}

func TestCleanDeprecatedFields_PreservesFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permissions not applicable on Windows")
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	err := os.WriteFile(cfgPath, []byte(`{"listen": ":8080", "top_k": 5}`), 0600)
	require.NoError(t, err)

	removed, err := CleanDeprecatedFields(cfgPath)
	require.NoError(t, err)
	assert.Len(t, removed, 1)

	// Check that file permissions are preserved
	info, err := os.Stat(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestCleanDeprecatedFields_ReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory permissions not applicable on Windows")
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	err := os.WriteFile(cfgPath, []byte(`{"listen": ":8080", "top_k": 5}`), 0644)
	require.NoError(t, err)

	// Make directory read-only so backup/tmp writes fail.
	require.NoError(t, os.Chmod(dir, 0555))
	t.Cleanup(func() { os.Chmod(dir, 0755) }) // restore for cleanup

	_, err = CleanDeprecatedFields(cfgPath)
	assert.Error(t, err, "should fail when directory is read-only")
}

func TestCleanDeprecatedFields_OutputIsValidJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	original := `{"listen":":8080","api_key":"secret","top_k":5,"enable_tray":true,"mcpServers":[{"name":"test","url":"http://localhost"}]}`
	err := os.WriteFile(cfgPath, []byte(original), 0644)
	require.NoError(t, err)

	_, err = CleanDeprecatedFields(cfgPath)
	require.NoError(t, err)

	// Verify the output is valid, parseable JSON
	cleanedData, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(cleanedData, &parsed), "cleaned config must be valid JSON")

	// Verify it's nicely indented (contains newlines)
	assert.Contains(t, string(cleanedData), "\n", "output should be indented")
}
