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

// A hardlink made before a save keeps pointing at the old config inode (which
// still holds api_key) after SaveConfig's temp-file + rename gives the live
// config a new inode. The deny list only knows the new inode, so the link
// count is what refuses it.
func TestReadFileWithPolicy_RefusesHardlinkToEarlierConfigInode(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	cfgDir := filepath.Join(root, "data")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(cfgDir, 0o700))
	cfgPath := filepath.Join(cfgDir, "mcp_config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"api_key":"old"}`), 0o600))

	policy := &FilePolicy{
		AllowedRoots: []string{allowed},
		Scope:        ScopeUpstream,
		Deny:         DenyPaths{ConfigFile: cfgPath, DataDir: cfgDir},
	}

	link := filepath.Join(allowed, "cfg-link")
	require.NoError(t, os.Link(cfgPath, link))

	// SaveConfig: note the old identity, write a temp file, rename it over
	// the config. After the rename the old inode has one link (cfg-link), so
	// the link count alone cannot see it.
	NoteSupersededProxyFile(cfgPath)
	tmp := cfgPath + ".tmp"
	require.NoError(t, os.WriteFile(tmp, []byte(`{"api_key":"new"}`), 0o600))
	require.NoError(t, os.Rename(tmp, cfgPath))

	_, err := ReadFileWithPolicy(link, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)

	// Wildcard mode refuses it too.
	wild := &FilePolicy{Wildcard: true, Scope: ScopeUpstream, Deny: policy.Deny}
	_, err = ReadFileWithPolicy(link, wild)
	require.ErrorIs(t, err, ErrSecretFileDenied)
}

// logs/ is outside the per-file identity deny list; a hardlink into it is
// refused by the link count.
func TestReadFileWithPolicy_RefusesHardlinkIntoLogs(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	dataDir := filepath.Join(root, "data")
	logs := filepath.Join(dataDir, "logs")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(logs, 0o700))
	logFile := filepath.Join(logs, "main.log")
	require.NoError(t, os.WriteFile(logFile, []byte("log line"), 0o600))

	policy := &FilePolicy{
		AllowedRoots: []string{allowed},
		Scope:        ScopeUpstream,
		Deny:         DenyPaths{DataDir: dataDir},
	}
	link := filepath.Join(allowed, "log-link")
	require.NoError(t, os.Link(logFile, link))
	_, err := ReadFileWithPolicy(link, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
}

// The link-count rule is upstream-only: a proxy-scope read of a hardlinked
// file still works, and a plain upstream secret (one link) still works.
func TestReadFileWithPolicy_LinkCountRuleScope(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	plain := filepath.Join(allowed, "token")
	require.NoError(t, os.WriteFile(plain, []byte("v1\n"), 0o600))

	up := &FilePolicy{AllowedRoots: []string{allowed}, Scope: ScopeUpstream}
	got, err := ReadFileWithPolicy(plain, up)
	require.NoError(t, err)
	require.Equal(t, "v1", got)

	require.NoError(t, os.Link(plain, filepath.Join(allowed, "token2")))
	_, err = ReadFileWithPolicy(plain, up)
	require.ErrorIs(t, err, ErrSecretFileDenied)

	proxy := &FilePolicy{AllowedRoots: []string{allowed}, Scope: ScopeProxy}
	got, err = ReadFileWithPolicy(plain, proxy)
	require.NoError(t, err)
	require.Equal(t, "v1", got)
}
