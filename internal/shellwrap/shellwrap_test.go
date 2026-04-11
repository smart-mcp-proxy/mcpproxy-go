package shellwrap

import (
	"os"
	"path/filepath"
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

func TestMergePathUnique(t *testing.T) {
	cases := []struct {
		name                    string
		primary, secondary, sep string
		want                    string
	}{
		{"empty primary", "", "/usr/bin:/bin", ":", "/usr/bin:/bin"},
		{"empty secondary", "/opt/homebrew/bin", "", ":", "/opt/homebrew/bin"},
		{
			"dedup keeps primary order",
			"/opt/homebrew/bin:/usr/local/bin",
			"/usr/bin:/opt/homebrew/bin:/bin",
			":",
			"/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
		},
		{
			"drops empty segments",
			"/opt/homebrew/bin::/usr/local/bin",
			":/bin:",
			":",
			"/opt/homebrew/bin:/usr/local/bin:/bin",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mergePathUnique(tc.primary, tc.secondary, tc.sep))
		})
	}
}

// writeFakeShell writes a POSIX shell script at <dir>/fake-shell that
// ignores its arguments and echoes `wantPath` on stdout. It returns the
// absolute path to the script so tests can point $SHELL at it.
func writeFakeShell(t *testing.T, dir, wantPath string) string {
	t.Helper()
	script := "#!/bin/sh\nprintf %s '" + wantPath + "'\n"
	p := filepath.Join(dir, "fake-shell")
	require.NoError(t, os.WriteFile(p, []byte(script), 0o755))
	return p
}

func TestLoginShellPATH_CapturesFromShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login-shell PATH capture is Unix-only")
	}
	resetLoginShellPathCacheForTest()
	t.Cleanup(resetLoginShellPathCacheForTest)

	dir := t.TempDir()
	want := "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
	fake := writeFakeShell(t, dir, want)
	t.Setenv("SHELL", fake)

	got := LoginShellPATH(nil)
	assert.Equal(t, want, got, "should capture the PATH printed by the login shell")

	// Second call must use the cache even if we break $SHELL.
	t.Setenv("SHELL", "/nonexistent/shell")
	got2 := LoginShellPATH(nil)
	assert.Equal(t, want, got2, "second call must return the cached value")
}

func TestLoginShellPATH_EmptyWhenShellFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	resetLoginShellPathCacheForTest()
	t.Cleanup(resetLoginShellPathCacheForTest)

	t.Setenv("SHELL", "/nonexistent/shell-binary-does-not-exist")
	got := LoginShellPATH(nil)
	assert.Equal(t, "", got, "should return empty string when the login shell cannot be executed")
}

func TestMinimalEnv_PrefersLoginShellPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	resetLoginShellPathCacheForTest()
	t.Cleanup(resetLoginShellPathCacheForTest)

	// Simulate a LaunchAgent-like ambient PATH (missing homebrew/local).
	t.Setenv("PATH", "/usr/bin:/bin")

	// Point $SHELL at a fake shell that prints the real (enriched) PATH.
	dir := t.TempDir()
	fake := writeFakeShell(t, dir, "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin")
	t.Setenv("SHELL", fake)

	env := MinimalEnv()

	var pathVal string
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathVal = strings.TrimPrefix(kv, "PATH=")
			break
		}
	}
	require.NotEmpty(t, pathVal, "MinimalEnv must include PATH")

	// The login-shell entries must come first so that docker's
	// credential-helper lookup can find /opt/homebrew/bin and /usr/local/bin.
	assert.True(t, strings.HasPrefix(pathVal, "/opt/homebrew/bin:/usr/local/bin"),
		"login-shell PATH entries must come before ambient, got %q", pathVal)
	assert.Contains(t, pathVal, "/usr/bin", "ambient PATH entries must still be present")
	assert.Contains(t, pathVal, "/bin", "ambient PATH entries must still be present")

	// No duplicate segments.
	segs := strings.Split(pathVal, ":")
	seen := make(map[string]bool, len(segs))
	for _, s := range segs {
		assert.False(t, seen[s], "duplicate PATH segment %q in %q", s, pathVal)
		seen[s] = true
	}
}

func TestMinimalEnv_FallsBackToAmbientPATHWhenShellBroken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	resetLoginShellPathCacheForTest()
	t.Cleanup(resetLoginShellPathCacheForTest)

	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("SHELL", "/nonexistent/shell-binary-does-not-exist")

	env := MinimalEnv()

	var pathVal string
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathVal = strings.TrimPrefix(kv, "PATH=")
			break
		}
	}
	assert.Equal(t, "/usr/bin:/bin", pathVal, "must fall back to ambient PATH when login shell fails")
}
