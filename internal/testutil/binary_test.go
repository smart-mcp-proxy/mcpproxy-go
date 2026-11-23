package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateTestConfigEscapesWindowsDataDir(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	dataDir := `D:\repo\state\work\data`

	createTestConfig(t, configPath, 12345, dataDir)

	configBytes, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var config struct {
		DataDir string `json:"data_dir"`
	}
	require.NoError(t, json.Unmarshal(configBytes, &config))
	require.Equal(t, dataDir, config.DataDir)
}
