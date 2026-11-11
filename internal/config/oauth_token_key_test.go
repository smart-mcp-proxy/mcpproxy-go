package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 T030: the oauth_token_encryption_key resolution table.

func newTestOAuthKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(raw)
}

func writeOAuthKeyFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

// clearOAuthKeyEnv makes every case hermetic: an empty variable counts as
// unset, like MCPPROXY_API_KEY / _FILE.
func clearOAuthKeyEnv(t *testing.T) {
	t.Setenv(EnvOAuthTokenKey, "")
	t.Setenv(EnvOAuthTokenKeyFile, "")
}

func kidOf(t *testing.T, b64 string) string {
	t.Helper()
	k, err := tokenseal.ParseKey(b64)
	require.NoError(t, err)
	return k.ID()
}

func TestResolveOAuthTokenKey_Table(t *testing.T) {
	keyA := newTestOAuthKey(t)
	keyB := newTestOAuthKey(t)
	secretDir := t.TempDir()
	dataDir := t.TempDir()
	fileA := writeOAuthKeyFile(t, secretDir, "oauth-key", keyA+"\n")
	fileB := writeOAuthKeyFile(t, secretDir, "oauth-key-b", keyB)
	// Absolute on every OS (a POSIX-rooted literal has no drive letter, so
	// filepath.IsAbs rejects it on Windows), and never created.
	missingFile := filepath.Join(t.TempDir(), "nonexistent", "key")

	cases := []struct {
		name    string
		field   string
		env     string
		envFile string
		envRef  [2]string // name, value for a ${env:} reference
		wantKID string
		want    KeySourceKind
	}{
		{name: "empty field and no env is off", want: KeySourceNone},
		{name: "literal", field: keyA, wantKID: kidOf(t, keyA), want: KeySourceLiteral},
		{name: "literal with surrounding whitespace", field: "  " + keyA + "\n", wantKID: kidOf(t, keyA), want: KeySourceLiteral},
		{name: "file reference", field: "${file:" + fileA + "}", wantKID: kidOf(t, keyA), want: KeySourceRefFile},
		{name: "env reference", field: "${env:SPEC123_TEST_KEY}", envRef: [2]string{"SPEC123_TEST_KEY", keyB}, wantKID: kidOf(t, keyB), want: KeySourceRefEnv},
		{name: "env value", env: keyB, wantKID: kidOf(t, keyB), want: KeySourceEnv},
		{name: "env file", envFile: fileB, wantKID: kidOf(t, keyB), want: KeySourceEnvFile},
		{name: "env value beats the field", field: "${file:" + fileA + "}", env: keyB, wantKID: kidOf(t, keyB), want: KeySourceEnv},
		{name: "env file beats the field", field: keyA, envFile: fileB, wantKID: kidOf(t, keyB), want: KeySourceEnvFile},
		{name: "env file beats an unreadable field reference", field: "${file:" + missingFile + "}", envFile: fileB, wantKID: kidOf(t, keyB), want: KeySourceEnvFile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearOAuthKeyEnv(t)
			if tc.env != "" {
				t.Setenv(EnvOAuthTokenKey, tc.env)
			}
			if tc.envFile != "" {
				t.Setenv(EnvOAuthTokenKeyFile, tc.envFile)
			}
			if tc.envRef[0] != "" {
				t.Setenv(tc.envRef[0], tc.envRef[1])
			}
			cfg := &Config{DataDir: dataDir, OAuthTokenEncryptionKey: tc.field}
			key, src, err := ResolveOAuthTokenKey(cfg)
			require.NoError(t, err)
			assert.Equal(t, tc.want, src.Kind)
			assert.False(t, src.InDataDir)
			if tc.wantKID == "" {
				assert.Nil(t, key)
				return
			}
			require.NotNil(t, key)
			assert.Equal(t, tc.wantKID, key.ID())
			// Resolution never rewrites the field: saves keep the reference.
			assert.Equal(t, tc.field, cfg.OAuthTokenEncryptionKey)
		})
	}
}

func TestResolveOAuthTokenKey_Failures(t *testing.T) {
	key := newTestOAuthKey(t)
	secretDir := t.TempDir()
	missing := filepath.Join(secretDir, "missing-spec123-key")
	empty := writeOAuthKeyFile(t, secretDir, "empty-key", "")
	newlineOnly := writeOAuthKeyFile(t, secretDir, "newline-key", "\n")
	short := writeOAuthKeyFile(t, secretDir, "short-key", base64.StdEncoding.EncodeToString(make([]byte, 31)))
	urlAlphabet := writeOAuthKeyFile(t, secretDir, "url-key", strings.NewReplacer("+", "-", "/", "_").Replace(key)+"--__")

	cases := []struct {
		name     string
		field    string
		env      string
		envFile  string
		sentence string
	}{
		{name: "missing file reference", field: "${file:" + missing + "}", sentence: MsgOAuthTokenKeyUnresolvable},
		{name: "unset env reference", field: "${env:SPEC123_DEFINITELY_UNSET}", sentence: MsgOAuthTokenKeyUnresolvable},
		{name: "empty file reference", field: "${file:" + empty + "}", sentence: MsgOAuthTokenKeyEmpty},
		{name: "newline-only file reference", field: "${file:" + newlineOnly + "}", sentence: MsgOAuthTokenKeyEmpty},
		{name: "malformed literal", field: "not-a-key", sentence: MsgOAuthTokenKeyMalformed},
		{name: "whitespace-only literal", field: "   ", sentence: MsgOAuthTokenKeyEmpty},
		{name: "31-byte file reference", field: "${file:" + short + "}", sentence: MsgOAuthTokenKeyMalformed},
		{name: "url alphabet file reference", field: "${file:" + urlAlphabet + "}", sentence: MsgOAuthTokenKeyMalformed},
		{name: "missing env file", envFile: missing, sentence: MsgOAuthTokenKeyUnresolvable},
		{name: "relative env file", envFile: "relative/key", sentence: MsgOAuthTokenKeyUnresolvable},
		// Review point: a selected source that resolves empty is an error,
		// never "encryption off", even with an empty field.
		{name: "empty env file with empty field", envFile: empty, sentence: MsgOAuthTokenKeyEmpty},
		{name: "empty env file with a field", field: key, envFile: empty, sentence: MsgOAuthTokenKeyEmpty},
		{name: "malformed env value", env: "short", sentence: MsgOAuthTokenKeyMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearOAuthKeyEnv(t)
			if tc.env != "" {
				t.Setenv(EnvOAuthTokenKey, tc.env)
			}
			if tc.envFile != "" {
				t.Setenv(EnvOAuthTokenKeyFile, tc.envFile)
			}
			k, _, err := ResolveOAuthTokenKey(&Config{DataDir: t.TempDir(), OAuthTokenEncryptionKey: tc.field})
			require.Error(t, err)
			assert.Nil(t, k)

			var se *StartupError
			require.True(t, errors.As(err, &se), "error must be a *StartupError, got %T", err)
			assert.Equal(t, ExitCodeOAuthTokenKeyError, se.ExitCode)
			assert.Equal(t, 4, se.ExitCode)
			assert.True(t, strings.HasSuffix(err.Error(), tc.sentence), "message %q must end with the fixed sentence %q", err.Error(), tc.sentence)

			// SC-004: the error never carries the path, the reference or the key.
			msg := err.Error()
			for _, leak := range []string{secretDir, missing, empty, short, urlAlphabet, key, "${", "SPEC123_DEFINITELY_UNSET", "not-a-key", "relative/key"} {
				assert.NotContains(t, msg, leak)
			}
		})
	}
}

func TestResolveOAuthTokenKey_BothEnvSet(t *testing.T) {
	clearOAuthKeyEnv(t)
	key := newTestOAuthKey(t)
	f := writeOAuthKeyFile(t, t.TempDir(), "k", key)
	t.Setenv(EnvOAuthTokenKey, key)
	t.Setenv(EnvOAuthTokenKeyFile, f)

	_, _, err := ResolveOAuthTokenKey(&Config{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOAuthTokenKeyBothSet)
	var se *StartupError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, 4, se.ExitCode)
	assert.NotContains(t, err.Error(), key)
	assert.NotContains(t, err.Error(), f)
}

func TestResolveOAuthTokenKey_InDataDir(t *testing.T) {
	clearOAuthKeyEnv(t)
	key := newTestOAuthKey(t)
	dataDir := t.TempDir()
	inside := writeOAuthKeyFile(t, dataDir, "oauth-key", key)
	outsideDir := t.TempDir()
	outside := writeOAuthKeyFile(t, outsideDir, "oauth-key", key)
	// A Kubernetes-style symlink outside the data dir whose target is inside.
	link := filepath.Join(outsideDir, "linked-key")
	if err := os.Symlink(inside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks need a privilege this Windows runner lacks: %v", err)
		}
		require.NoError(t, err)
	}

	for _, tc := range []struct {
		name   string
		field  string
		env    string
		inside bool
	}{
		{name: "file reference inside", field: "${file:" + inside + "}", inside: true},
		{name: "file reference outside", field: "${file:" + outside + "}", inside: false},
		{name: "file reference through a symlink", field: "${file:" + link + "}", inside: true},
		{name: "env file through a symlink", env: link, inside: true},
		{name: "env file outside", env: outside, inside: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearOAuthKeyEnv(t)
			if tc.env != "" {
				t.Setenv(EnvOAuthTokenKeyFile, tc.env)
			}
			k, src, err := ResolveOAuthTokenKey(&Config{DataDir: dataDir, OAuthTokenEncryptionKey: tc.field})
			require.NoError(t, err)
			require.NotNil(t, k)
			assert.Equal(t, tc.inside, src.InDataDir)
		})
	}
}

// FR-020: validation checks the reference syntax only. A well-formed reference
// to a file that does not exist loads; a malformed reference fails as a
// *StartupError with exit 4, and the error never quotes the value.
func TestConfigValidate_OAuthTokenKeySyntaxOnly(t *testing.T) {
	clearOAuthKeyEnv(t)
	// Absolute on every OS: a POSIX-rooted literal is relative on Windows.
	missingFile := filepath.Join(t.TempDir(), "nonexistent", "oauth-key")
	for _, ok := range []string{
		"",
		"${file:" + missingFile + "}",
		"${env:SPEC123_DEFINITELY_UNSET}",
		"${keyring:mcpproxy-oauth-key}",
		"some-literal-that-is-checked-at-open",
	} {
		t.Run("accepts "+ok, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.OAuthTokenEncryptionKey = ok
			require.NoError(t, cfg.Validate())
		})
	}
	for _, bad := range []string{
		"${file:relative/path}",
		"${vault:secret/x}",
		"${file:/a}${file:/b}",
		"prefix-${file:/abs/key}",
		"${file:/abs/key",
	} {
		t.Run("rejects "+bad, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.OAuthTokenEncryptionKey = bad
			err := cfg.Validate()
			require.Error(t, err)
			var se *StartupError
			require.True(t, errors.As(err, &se), "got %T", err)
			assert.Equal(t, 4, se.ExitCode)
			assert.NotContains(t, err.Error(), bad)
		})
	}
}

// The field is saved as written: the resolved key never lands on Config.
func TestOAuthTokenKey_SaveKeepsReference(t *testing.T) {
	clearOAuthKeyEnv(t)
	dir := t.TempDir()
	keyFile := writeOAuthKeyFile(t, t.TempDir(), "k", newTestOAuthKey(t))
	ref := "${file:" + keyFile + "}"
	cfg := DefaultConfig()
	cfg.DataDir = dir
	cfg.OAuthTokenEncryptionKey = ref
	path := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, SaveConfig(cfg, path))

	loaded, err := LoadFromFile(path)
	require.NoError(t, err)
	_, _, err = ResolveOAuthTokenKey(loaded)
	require.NoError(t, err)
	require.NoError(t, SaveConfig(loaded, path))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"oauth_token_encryption_key": "${file:`+keyFile+`}"`)
}
