//go:build unix

package secret

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadFileWithPolicy_RefusesHardlinkAfterConfigRename(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	cfgDir := filepath.Join(root, "data")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(cfgDir, 0o700))
	cfgPath := filepath.Join(cfgDir, "mcp_config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte("{}"), 0o600))

	policy := &FilePolicy{
		AllowedRoots: []string{allowed},
		Scope:        ScopeUpstream,
		Deny:         DenyPaths{ConfigFile: cfgPath},
	}

	tmp := cfgPath + ".new"
	require.NoError(t, os.WriteFile(tmp, []byte(`{"x":1}`), 0o600))
	require.NoError(t, os.Rename(tmp, cfgPath))

	link := filepath.Join(allowed, "cfg-link")
	require.NoError(t, os.Link(cfgPath, link))
	_, err := ReadFileWithPolicy(link, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
}

func TestReadFileWithPolicy_RefusesHardlinkToConfigDB(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	dataDir := filepath.Join(root, "data")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(dataDir, 0o700))
	db := filepath.Join(dataDir, "config.db")
	require.NoError(t, os.WriteFile(db, []byte("db"), 0o600))

	policy := &FilePolicy{
		AllowedRoots: []string{allowed},
		Scope:        ScopeUpstream,
		Deny:         DenyPaths{DataDir: dataDir},
	}
	link := filepath.Join(allowed, "db-link")
	require.NoError(t, os.Link(db, link))
	_, err := ReadFileWithPolicy(link, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
}
