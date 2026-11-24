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
			_ = os.Remove(target) // a previous subtest left a symlink here
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

// A large early-sorting directory must not use up the budget before the
// state directories that were always compared (certs, index.bleve, tpa-lkg).
func TestDenyPathsAtRead_StateDirsNotStarvedByBudget(t *testing.T) {
	dataDir := t.TempDir()
	big := filepath.Join(dataDir, "aaa-big")
	require.NoError(t, os.Mkdir(big, 0o700))
	for i := 0; i < maxDenyWalkEntries+50; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(big, fmt.Sprintf("f%05d", i)), nil, 0o600))
	}
	lkg := filepath.Join(dataDir, "tpa-lkg")
	require.NoError(t, os.Mkdir(lkg, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(lkg, "bundle.json"), nil, 0o600))
	p := &FilePolicy{Scope: ScopeUpstream, Deny: DenyPaths{DataDir: dataDir}}
	require.Contains(t, p.denyPathsAtRead(), filepath.Join(lkg, "bundle.json"))
}

// The deeper pass lists regular files only: a symlink below the data dir is
// not followed into the deny set.
func TestDenyPathsAtRead_DeepPassIgnoresSymlinks(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	allowed := filepath.Join(root, "allowed")
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "sub"), 0o700))
	require.NoError(t, os.Mkdir(allowed, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "k"), nil, 0o600))
	link := filepath.Join(dataDir, "sub", "link")
	require.NoError(t, os.Symlink(filepath.Join(allowed, "k"), link))
	p := &FilePolicy{Scope: ScopeUpstream, Deny: DenyPaths{DataDir: dataDir}}
	require.NotContains(t, p.denyPathsAtRead(), link)
}

// Empty directories are not free: each visit costs budget, so thousands of
// them cannot force unbounded opens on every read.
func TestDenyPathsAtRead_EmptyDirsCostBudget(t *testing.T) {
	dataDir := t.TempDir()
	for i := 0; i < maxDenyWalkEntries+100; i++ {
		require.NoError(t, os.Mkdir(filepath.Join(dataDir, fmt.Sprintf("d%05d", i)), 0o700))
	}
	p := &FilePolicy{Scope: ScopeUpstream, Deny: DenyPaths{DataDir: dataDir}}
	budget := maxDenyWalkEntries
	var paths []string
	var queue []denyWalkLevel
	for i := 0; i < maxDenyWalkEntries+100 && budget > 0; i++ {
		budget = walkDenyDir(denyWalkLevel{filepath.Join(dataDir, fmt.Sprintf("d%05d", i)), 2, false}, budget, &paths, &queue)
	}
	require.Equal(t, 0, budget)
	_ = p
}

// A large state directory already covered by pass 1 must not use up the
// budget meant for nested directories.
func TestDenyPathsAtRead_CoveredDirsDoNotChargeBudget(t *testing.T) {
	dataDir := t.TempDir()
	idx := filepath.Join(dataDir, "index.bleve")
	require.NoError(t, os.Mkdir(idx, 0o700))
	for i := 0; i < maxDenyWalkEntries+10; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(idx, fmt.Sprintf("s%05d", i)), nil, 0o600))
	}
	nested := filepath.Join(dataDir, "zz", "deep")
	require.NoError(t, os.MkdirAll(nested, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "state.db"), nil, 0o600))
	p := &FilePolicy{Scope: ScopeUpstream, Deny: DenyPaths{DataDir: dataDir}}
	require.Contains(t, p.denyPathsAtRead(), filepath.Join(nested, "state.db"))
}
