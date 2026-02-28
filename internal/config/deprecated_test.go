package config

import (
	"os"
	"path/filepath"
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
