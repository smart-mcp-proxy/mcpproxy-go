package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistableConfig_SecretFileDirsRestoredFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFileName)
	onDisk := &Config{DataDir: dir, Listen: "127.0.0.1:8080"}
	wildcard := SecretFileDirs{"*"}
	onDisk.SecretFileDirs = &wildcard
	require.NoError(t, SaveConfig(onDisk, path))

	effective := &Config{DataDir: dir, Listen: "127.0.0.1:8080"}
	other := SecretFileDirs{"/tmp/nope"}
	effective.SecretFileDirs = &other
	effective.ToolsLimit = 9

	persisted := PersistableConfig(effective, path)
	assert.Equal(t, 9, persisted.ToolsLimit)
	assert.True(t, SecretFileDirsWildcard([]string(*persisted.SecretFileDirs)))
}

func TestSecretFileDirsChanged_detectsManagementEdit(t *testing.T) {
	base := &Config{}
	a := SecretFileDirs{"/run/secrets"}
	b := SecretFileDirs{"*"}
	base.SecretFileDirs = &a
	edited := &Config{}
	edited.SecretFileDirs = &b
	assert.True(t, SecretFileDirsChanged(base, edited))
}

func TestSecretFileDirsManagementEditRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFileName)
	base := &Config{DataDir: dir, Listen: ":8080"}
	w := SecretFileDirs{"/run/secrets"}
	base.SecretFileDirs = &w
	require.NoError(t, SaveConfig(base, path))

	wild := SecretFileDirs{"*"}
	incoming := &Config{DataDir: dir, Listen: ":8080", SecretFileDirs: &wild}
	assert.True(t, SecretFileDirsManagementEditRefused(path, incoming))

	same := &Config{DataDir: dir, Listen: ":8080", SecretFileDirs: &w, ToolsLimit: 2}
	assert.False(t, SecretFileDirsManagementEditRefused(path, same))
}

// Review #6 (PR #1884): an on-disk config that cannot be decoded must not
// let a management edit through; there is nothing to compare it with.
func TestSecretFileDirsManagementEditRefused_UndecodableFileFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	wild := SecretFileDirs{"*"}
	incoming := &Config{DataDir: dir, Listen: ":8080", SecretFileDirs: &wild}
	assert.True(t, SecretFileDirsManagementEditRefused(path, incoming))

	// Even an incoming config that leaves the key unset is refused: the file's
	// setting is unknown, so no edit can be shown not to change it.
	assert.True(t, SecretFileDirsManagementEditRefused(path, &Config{DataDir: dir, Listen: ":8080"}))
}

// A config file that does not exist yet holds no secret_file_dirs: an edit
// that sets one is refused, an edit that leaves it unset is not.
func TestSecretFileDirsManagementEditRefused_MissingFileComparesWithUnset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFileName)

	wild := SecretFileDirs{"*"}
	assert.True(t, SecretFileDirsManagementEditRefused(path, &Config{DataDir: dir, SecretFileDirs: &wild}))
	assert.False(t, SecretFileDirsManagementEditRefused(path, &Config{DataDir: dir}))
}

func TestApplyPatchMap_RejectsSecretFileDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFileName)
	base := &Config{DataDir: dir, Listen: ":8080"}
	w := SecretFileDirs{"/run/secrets"}
	base.SecretFileDirs = &w
	require.NoError(t, SaveConfig(base, path))

	onDisk, err := DecodeConfigFile(path)
	require.NoError(t, err)

	patch := map[string]interface{}{"secret_file_dirs": []interface{}{"*"}}
	raw, err := json.Marshal(onDisk)
	require.NoError(t, err)
	var baseMap map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &baseMap))
	deepMergeJSONMap(baseMap, patch)
	mergedBytes, err := json.Marshal(baseMap)
	require.NoError(t, err)
	var merged Config
	require.NoError(t, json.Unmarshal(mergedBytes, &merged))
	assert.True(t, SecretFileDirsChanged(onDisk, &merged))
}

// deepMergeJSONMap is the test-local merge helper (same semantics as httpapi deepMergeJSON).
func deepMergeJSONMap(base, patch map[string]interface{}) {
	for k, v := range patch {
		base[k] = v
	}
}
