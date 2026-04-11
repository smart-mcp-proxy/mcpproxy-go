package shellwrap

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShellescape_TrickyArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TestShellescape_TrickyArgs uses POSIX quoting expectations")
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "''"},
		{"simple", "hello", "hello"},
		{"space", "hello world", "'hello world'"},
		{"single quote", "it's", "'it'\"'\"'s'"},
		{"dollar", "$HOME", "'$HOME'"},
		{"backticks", "`whoami`", "'`whoami`'"},
		{"glob star", "*.go", "'*.go'"},
		{"pipe", "a|b", "'a|b'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Shellescape(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestWrapWithUserShell_RespectsShellEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("$SHELL override is only meaningful on Unix-like systems")
	}

	t.Setenv("SHELL", "/opt/homebrew/bin/zsh")

	shell, args := WrapWithUserShell(nil, "docker", []string{"ps", "-a"})
	assert.Equal(t, "/opt/homebrew/bin/zsh", shell, "should honor $SHELL override")
	require.Len(t, args, 3, "unix shell wrapping should produce 3 args")
	assert.Equal(t, "-l", args[0])
	assert.Equal(t, "-c", args[1])
	// Command string should contain the docker invocation with escaped args.
	assert.Contains(t, args[2], "docker")
	assert.Contains(t, args[2], "ps")
	assert.Contains(t, args[2], "-a")
}

func TestWrapWithUserShell_FallbackWhenShellUnset(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix fallback test")
	}
	t.Setenv("SHELL", "")
	shell, args := WrapWithUserShell(nil, "docker", []string{"version"})
	assert.Equal(t, "/bin/bash", shell, "should fall back to /bin/bash when $SHELL is empty")
	require.Len(t, args, 3)
	assert.Equal(t, "-l", args[0])
	assert.Equal(t, "-c", args[1])
}

func TestResolveDockerPath_CachedAcrossCalls(t *testing.T) {
	resetDockerPathCacheForTest()
	t.Cleanup(resetDockerPathCacheForTest)

	first, firstErr := ResolveDockerPath(nil)

	// Sabotage PATH after the first resolve. If the cache works the second
	// call must return the same value (or same error) without re-probing.
	origPath := t.TempDir()
	t.Setenv("PATH", origPath)

	second, secondErr := ResolveDockerPath(nil)

	assert.Equal(t, first, second, "cached docker path should survive PATH changes")
	if firstErr == nil {
		assert.NoError(t, secondErr, "cached success must not suddenly error")
	} else {
		assert.Error(t, secondErr, "cached error must remain an error")
	}
}

func TestMinimalEnv_DropsSecrets(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA_test_dummy_value_00000000")
	t.Setenv("GITHUB_TOKEN", "ghp_dummy_test_token_1234567890abcdef")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-dummy-not-real-value")
	t.Setenv("OPENAI_API_KEY", "sk-test-dummy-not-real-value")

	env := MinimalEnv()

	joined := strings.Join(env, "\n")
	assert.NotContains(t, joined, "AWS_ACCESS_KEY_ID", "AWS creds must not leak into minimal env")
	assert.NotContains(t, joined, "GITHUB_TOKEN", "github token must not leak")
	assert.NotContains(t, joined, "ANTHROPIC_API_KEY", "anthropic key must not leak")
	assert.NotContains(t, joined, "OPENAI_API_KEY", "openai key must not leak")

	// But PATH must still be present so docker itself can be found.
	hasPath := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			hasPath = true
			break
		}
	}
	assert.True(t, hasPath, "minimal env must contain PATH")
}
