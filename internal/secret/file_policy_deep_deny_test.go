//go:build unix

package secret

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A proxy state file nested deeper than the old "top level plus three named
// subdirectories" walk must still be refused by identity when a path swap
// (symlink) lands the handle on it after the path checks.
func TestReadFileWithPolicy_DeepDataDirFileRefusedByIdentity(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	deep := filepath.Join(dataDir, "a", "b", "c", "d", "state.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(deep), 0o700))
	require.NoError(t, os.WriteFile(deep, []byte("proxy-state\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "secrets"), 0o700))

	other := filepath.Join(root, "other")
	require.NoError(t, os.Mkdir(other, 0o700))
	target := filepath.Join(other, "token")
	require.NoError(t, os.WriteFile(target, []byte("ok\n"), 0o600))

	for _, wildcard := range []bool{false, true} {
		t.Run(fmt.Sprintf("wildcard=%v", wildcard), func(t *testing.T) {
			policy := &FilePolicy{
				Wildcard: wildcard, AllowedRoots: []string{other}, Scope: ScopeUpstream,
				Deny: DenyPaths{DataDir: dataDir, DataSecretsDir: filepath.Join(dataDir, "secrets")},
			}
			require.NoError(t, os.WriteFile(target, []byte("ok\n"), 0o600))
			SetBeforeSecretOpenHookForTest(func() {
				require.NoError(t, os.Remove(target))
				require.NoError(t, os.Symlink(deep, target))
			})
			t.Cleanup(func() { SetBeforeSecretOpenHookForTest(nil) })
			_, err := ReadFileWithPolicy(target, policy)
			require.ErrorIs(t, err, ErrSecretFileDenied)
		})
	}
}

// The walk is bounded: a data dir with more files than the entry budget costs
// at most the budget in stats, never the whole tree.
func TestDenyPathsAtRead_WalkIsBounded(t *testing.T) {
	dataDir := t.TempDir()
	sub := filepath.Join(dataDir, "big")
	require.NoError(t, os.Mkdir(sub, 0o700))
	for i := 0; i < maxDenyWalkEntries+200; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(sub, fmt.Sprintf("f%05d", i)), nil, 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "top.db"), nil, 0o600))
	p := &FilePolicy{Scope: ScopeUpstream, Deny: DenyPaths{DataDir: dataDir}}
	paths := p.denyPathsAtRead()
	require.LessOrEqual(t, len(paths), maxDenyWalkEntries+1)
	require.Contains(t, paths, filepath.Join(dataDir, "top.db"), "shallow files come first")
}

// Wildcard mode binds the deny check to the opened descriptor: a path swapped
// to the config after the path-level checks is still refused.
func TestReadFileWithPolicy_WildcardSwapToConfigRefused(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "mcp_config.json")
	require.NoError(t, os.WriteFile(cfg, []byte(`{"api_key":"k"}`), 0o600))
	target := filepath.Join(root, "token")
	require.NoError(t, os.WriteFile(target, []byte("ok\n"), 0o600))
	policy := &FilePolicy{Wildcard: true, Scope: ScopeUpstream, Deny: DenyPaths{ConfigFile: cfg}}
	SetBeforeSecretOpenHookForTest(func() {
		require.NoError(t, os.Remove(target))
		require.NoError(t, os.Symlink(cfg, target))
	})
	t.Cleanup(func() { SetBeforeSecretOpenHookForTest(nil) })
	_, err := ReadFileWithPolicy(target, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
}
