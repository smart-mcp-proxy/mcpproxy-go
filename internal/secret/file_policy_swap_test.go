//go:build unix

package secret

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadFileWithPolicy_SwapHooks(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.Mkdir(outside, 0o700))
	target := filepath.Join(allowed, "token")
	require.NoError(t, os.WriteFile(target, []byte("ok\n"), 0o600))

	policy := &FilePolicy{AllowedRoots: []string{allowed}, Scope: ScopeUpstream}

	t.Run("final file swapped to symlink outside root", func(t *testing.T) {
		outFile := filepath.Join(outside, "outer")
		require.NoError(t, os.WriteFile(outFile, []byte("stolen\n"), 0o600))
		SetBeforeSecretOpenHookForTest(func() {
			require.NoError(t, os.Remove(target))
			require.NoError(t, os.Symlink(outFile, target))
		})
		t.Cleanup(func() { SetBeforeSecretOpenHookForTest(nil) })
		_, err := ReadFileWithPolicy(target, policy)
		require.ErrorIs(t, err, ErrSecretFileDenied)
	})

	t.Run("final file swapped to admin key inside root", func(t *testing.T) {
		admin := filepath.Join(allowed, "admin")
		require.NoError(t, os.WriteFile(admin, []byte("key\n"), 0o600))
		p2 := *policy
		p2.Deny.AdminKeyPaths = []string{admin}
		require.NoError(t, os.WriteFile(target, []byte("ok\n"), 0o600))
		SetBeforeSecretOpenHookForTest(func() {
			require.NoError(t, os.Remove(target))
			require.NoError(t, os.Symlink(admin, target))
		})
		t.Cleanup(func() { SetBeforeSecretOpenHookForTest(nil) })
		_, err := ReadFileWithPolicy(target, &p2)
		require.ErrorIs(t, err, ErrSecretFileDenied)
	})

	t.Run("parent directory swapped to symlink outside root", func(t *testing.T) {
		inner := filepath.Join(root, "inner", "secrets")
		require.NoError(t, os.MkdirAll(inner, 0o700))
		innerFile := filepath.Join(inner, "k")
		require.NoError(t, os.WriteFile(innerFile, []byte("x\n"), 0o600))
		p3 := &FilePolicy{AllowedRoots: []string{inner}, Scope: ScopeUpstream, Wildcard: true}
		SetBeforeSecretOpenHookForTest(func() {
			require.NoError(t, os.RemoveAll(filepath.Join(root, "inner")))
			require.NoError(t, os.Symlink(outside, filepath.Join(root, "inner")))
		})
		t.Cleanup(func() { SetBeforeSecretOpenHookForTest(nil) })
		_, err := ReadFileWithPolicy(innerFile, p3)
		require.ErrorIs(t, err, ErrSecretFileDenied)
	})

	t.Run("final file swapped to FIFO", func(t *testing.T) {
		require.NoError(t, os.WriteFile(target, []byte("ok\n"), 0o600))
		SetBeforeSecretOpenHookForTest(func() {
			require.NoError(t, os.Remove(target))
			require.NoError(t, syscall.Mkfifo(target, 0o600))
		})
		t.Cleanup(func() { SetBeforeSecretOpenHookForTest(nil) })
		_, err := ReadFileWithPolicy(target, policy)
		require.ErrorIs(t, err, ErrSecretFileDenied)
	})
}
