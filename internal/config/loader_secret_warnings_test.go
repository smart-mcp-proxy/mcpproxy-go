package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_WarnsMissingSecretFileDirRoot(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "no-such-root")
	cfg := &Config{
		DataDir:        dir,
		Listen:         "127.0.0.1:8080",
		SecretFileDirs: &SecretFileDirs{missing},
	}
	path := filepath.Join(dir, ConfigFileName)
	require.NoError(t, SaveConfig(cfg, path))

	var rec []LoadWarning
	SetLoadWarningRecorder(func(message, key, value string) {
		rec = append(rec, LoadWarning{Message: message, Key: key, Value: value})
	})
	t.Cleanup(func() { SetLoadWarningRecorder(nil) })

	_, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.True(t, hasLoadWarning(rec, "secret_file_dirs root does not exist yet", missing))
}

func TestLoad_WarnsWildcardSecretFileDirs(t *testing.T) {
	dir := t.TempDir()
	wild := SecretFileDirs{"*"}
	cfg := &Config{DataDir: dir, Listen: "127.0.0.1:8080", SecretFileDirs: &wild}
	path := filepath.Join(dir, ConfigFileName)
	require.NoError(t, SaveConfig(cfg, path))

	var rec []LoadWarning
	SetLoadWarningRecorder(func(message, key, value string) {
		rec = append(rec, LoadWarning{Message: message, Key: key, Value: value})
	})
	t.Cleanup(func() { SetLoadWarningRecorder(nil) })

	_, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.True(t, hasLoadWarning(rec, "secret_file_dirs is wildcard compatibility mode", "*"))
}

func hasLoadWarning(rec []LoadWarning, message, value string) bool {
	for _, w := range rec {
		if w.Message == message && w.Value == value {
			return true
		}
	}
	return false
}
