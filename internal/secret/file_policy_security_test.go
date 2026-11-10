//go:build unix

package secret

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadFileWithPolicy_AdminKeyInsideAllowedRootDenied(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	admin := filepath.Join(root, "admin-key")
	require.NoError(t, os.WriteFile(admin, []byte("admin\n"), 0o600))
	link := filepath.Join(allowed, "via-symlink")
	require.NoError(t, os.Symlink(admin, link))

	id, ok := StatDenyIdentity(admin)
	require.True(t, ok)
	policy := &FilePolicy{
		AllowedRoots:   []string{allowed},
		Scope:          ScopeUpstream,
		DenyIdentities: []DenyIdentity{id},
	}

	_, err := ReadFileWithPolicy(link, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
}

func TestReadFileWithPolicy_HardlinkToConfigDenied(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	cfg := filepath.Join(root, "mcp_config.json")
	require.NoError(t, os.WriteFile(cfg, []byte("x"), 0o600))
	hard := filepath.Join(allowed, "alias")
	require.NoError(t, os.Link(cfg, hard))

	id, ok := StatDenyIdentity(cfg)
	require.True(t, ok)
	policy := &FilePolicy{
		AllowedRoots:   []string{allowed},
		Scope:          ScopeUpstream,
		DenyIdentities: []DenyIdentity{id},
	}
	_, err := ReadFileWithPolicy(hard, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
}

func TestReadFileWithPolicy_FIFOInAllowedRootDoesNotHang(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	fifo := filepath.Join(allowed, "token.fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	policy := &FilePolicy{AllowedRoots: []string{allowed}, Scope: ScopeUpstream}

	done := make(chan error, 1)
	go func() { _, err := ReadFileWithPolicy(fifo, policy); done <- err }()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("ReadFileWithPolicy blocked on a FIFO")
	}
}

func TestReadFileWithPolicy_ParentDirSymlinkEscapeDenied(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.Mkdir(outside, 0o700))
	secret := filepath.Join(outside, "key")
	require.NoError(t, os.WriteFile(secret, []byte("stolen\n"), 0o600))
	// key -> ..data/key style escape via symlink to outside file
	require.NoError(t, os.Symlink(secret, filepath.Join(allowed, "escape")))

	policy := &FilePolicy{AllowedRoots: []string{allowed}, Scope: ScopeUpstream}
	_, err := ReadFileWithPolicy(filepath.Join(allowed, "escape"), policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
}
