package secret

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadFileWithPolicy_AllowedAndRefused(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	keyPath := filepath.Join(allowed, "token")
	require.NoError(t, os.WriteFile(keyPath, []byte("secret\n"), 0o600))

	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("nope\n"), 0o600))

	policy := &FilePolicy{
		AllowedRoots: []string{allowed},
		Scope:        ScopeUpstream,
	}

	got, err := ReadFileWithPolicy(keyPath, policy)
	require.NoError(t, err)
	assert.Equal(t, "secret", got)

	_, err = ReadFileWithPolicy(outside, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)

	_, err = ReadFileWithPolicy(filepath.Join(allowed, "..", "..", "etc", "passwd"), policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
}

func TestReadFileWithPolicy_OptOutModes(t *testing.T) {
	path := writeSecretFile(t, "x\n")
	t.Run("wildcard", func(t *testing.T) {
		_, err := ReadFileWithPolicy(path, NewWildcardFilePolicy(ScopeUpstream))
		require.NoError(t, err)
	})
	t.Run("disabled", func(t *testing.T) {
		_, err := ReadFileWithPolicy(path, NewDisabledFilePolicy(ScopeUpstream))
		require.ErrorIs(t, err, ErrSecretFileDenied)
	})
}

func TestReadFileWithPolicy_DenyPathsEvenInWildcard(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "mcp_config.json")
	require.NoError(t, os.WriteFile(cfg, []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.db"), []byte("db"), 0o600))

	policy := NewWildcardFilePolicy(ScopeUpstream)
	policy.Deny = DenyPaths{
		ConfigFile: cfg,
		DataDir:    root,
		DataSecretsDir: filepath.Join(root, "secrets"),
	}

	_, err := ReadFileWithPolicy(cfg, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
	_, err = ReadFileWithPolicy(filepath.Join(root, "config.db"), policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)

	secrets := filepath.Join(root, "secrets")
	require.NoError(t, os.MkdirAll(secrets, 0o700))
	okPath := filepath.Join(secrets, "key")
	require.NoError(t, os.WriteFile(okPath, []byte("k\n"), 0o600))
	_, err = ReadFileWithPolicy(okPath, policy)
	require.NoError(t, err)
}

func TestReadFileWithPolicy_ErrorTextDoesNotLeakPath(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	policy := &FilePolicy{AllowedRoots: []string{allowed}, Scope: ScopeUpstream}

	outside := filepath.Join(root, "nope")
	_, err := ReadFileWithPolicy(outside, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
	assert.Equal(t, ErrSecretFileDenied.Error(), err.Error())
	assert.NotContains(t, err.Error(), outside)

	missing := filepath.Join(allowed, "missing")
	_, err = ReadFileWithPolicy(missing, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
	assert.Equal(t, err.Error(), ErrSecretFileDenied.Error())
}


func TestPathContainedInRoot_PrefixTrap(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	trap := filepath.Join(root, "secrets-old")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(trap, 0o700))
	file := filepath.Join(trap, "x")
	require.NoError(t, os.WriteFile(file, []byte("a"), 0o600))

	policy := &FilePolicy{AllowedRoots: []string{allowed}, Scope: ScopeUpstream}
	_, err := ReadFileWithPolicy(file, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
}

func TestReadFileWithPolicy_SymlinkEscape(t *testing.T) {
	if os.Getenv("GO_WANT_SYMLINK_TESTS") == "" && testing.Short() {
		// Symlink tests still run by default; keep name for CI toggles.
	}
	root := t.TempDir()
	allowed := filepath.Join(root, "secrets")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.Mkdir(outside, 0o700))
	target := filepath.Join(outside, "key")
	require.NoError(t, os.WriteFile(target, []byte("stolen\n"), 0o600))
	link := filepath.Join(allowed, "link")
	require.NoError(t, os.Symlink(target, link))

	policy := &FilePolicy{AllowedRoots: []string{allowed}, Scope: ScopeUpstream}
	_, err := ReadFileWithPolicy(link, policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
}

func TestReadFileWithPolicy_KubernetesLayout(t *testing.T) {
	root := t.TempDir()
	mount := filepath.Join(root, "mount")
	require.NoError(t, os.MkdirAll(mount, 0o700))
	ts := filepath.Join(mount, "..2026_10_21_00_00_00.000000000")
	require.NoError(t, os.MkdirAll(ts, 0o700))
	real := filepath.Join(ts, "key")
	require.NoError(t, os.WriteFile(real, []byte("kube\n"), 0o600))
	require.NoError(t, os.Symlink(ts, filepath.Join(mount, "..data")))
	require.NoError(t, os.Symlink(filepath.Join("..data", "key"), filepath.Join(mount, "key")))

	policy := &FilePolicy{AllowedRoots: []string{mount}, Scope: ScopeUpstream}
	got, err := ReadFileWithPolicy(filepath.Join(mount, "key"), policy)
	require.NoError(t, err)
	assert.Equal(t, "kube", got)
}

func TestReadFileWithPolicy_TraversalInPath(t *testing.T) {
	policy := NewWildcardFilePolicy(ScopeUpstream)
	_, err := ReadFileWithPolicy("/run/secrets/../etc/passwd", policy)
	require.ErrorIs(t, err, ErrSecretFileDenied)
}

func TestIsReservedMCProxyEnv(t *testing.T) {
	assert.True(t, isReservedMCProxyEnv("MCPROXY_API_KEY"))
	assert.True(t, isReservedMCProxyEnv("MCPROXY_FOO"))
	assert.False(t, isReservedMCProxyEnv("OTHER"))
	assert.False(t, strings.HasPrefix("MCPROXY", "MCPROXY_")) // sanity
}
