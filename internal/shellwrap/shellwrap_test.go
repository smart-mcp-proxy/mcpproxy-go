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

// writeFakeDocker writes a no-op executable at <dir>/docker so ResolveDockerPath
// can find "docker" on a controlled PATH.
func writeFakeDocker(t *testing.T, dir string) string {
	t.Helper()
	script := "#!/bin/sh\nexit 0\n"
	p := filepath.Join(dir, "docker")
	require.NoError(t, os.WriteFile(p, []byte(script), 0o755))
	return p
}

// TestResolveDockerPath_FastPath verifies that when docker is present on
// ambient PATH, ResolveDockerPath returns it without invoking the login
// shell fallback.
func TestResolveDockerPath_FastPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only fixture")
	}
	resetDockerPathCacheForTest()
	t.Cleanup(resetDockerPathCacheForTest)

	dir := t.TempDir()
	want := writeFakeDocker(t, dir)
	t.Setenv("PATH", dir)
	// If the fast path were skipped and the shell fallback ran, it would
	// invoke this (broken) shell and fail — the assertion below would catch it.
	t.Setenv("SHELL", "/nonexistent/shell-must-not-be-invoked")

	got, err := ResolveDockerPath(nil)
	require.NoError(t, err)
	assert.Equal(t, want, got, "should resolve docker via exec.LookPath without shell fallback")
}

// TestResolveDockerPath_ShellFallback simulates the tray/LaunchAgent scenario:
// docker is NOT on the ambient PATH but IS on the user's login-shell PATH.
// The shell fallback must recover the absolute path.
func TestResolveDockerPath_ShellFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login-shell fallback is unix-only")
	}
	resetDockerPathCacheForTest()
	t.Cleanup(resetDockerPathCacheForTest)

	// Stash the fake docker somewhere that is NOT on ambient PATH.
	dockerDir := t.TempDir()
	dockerPath := writeFakeDocker(t, dockerDir)

	// Ambient PATH deliberately excludes dockerDir (mimicking launchd minimal PATH).
	ambient := t.TempDir()
	t.Setenv("PATH", ambient)

	// Stub well-known paths to nothing so the shell fallback is the path under test.
	prev := wellKnownDockerPathsFn
	wellKnownDockerPathsFn = func() []string { return nil }
	t.Cleanup(func() { wellKnownDockerPathsFn = prev })

	// Fake $SHELL emits the absolute path via `command -v docker` — the
	// fallback issues `<shell> -l -c 'command -v docker'` and trims the output.
	shellDir := t.TempDir()
	fakeShell := writeFakeShell(t, shellDir, dockerPath)
	t.Setenv("SHELL", fakeShell)

	got, err := ResolveDockerPath(nil)
	require.NoError(t, err, "shell fallback must succeed when login shell emits the docker path")
	assert.Equal(t, dockerPath, got, "should return the path emitted by the login shell")
}

// TestResolveDockerPath_NotFoundAnywhere verifies the error path when docker
// is missing from ambient PATH, the login shell, and well-known install
// locations.
func TestResolveDockerPath_NotFoundAnywhere(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only fixture")
	}
	resetDockerPathCacheForTest()
	t.Cleanup(resetDockerPathCacheForTest)

	t.Setenv("PATH", t.TempDir())
	// Login shell that emits nothing — mimics `command -v docker` returning empty.
	shellDir := t.TempDir()
	fakeShell := writeFakeShell(t, shellDir, "")
	t.Setenv("SHELL", fakeShell)

	// Stub the well-known paths to a list that cannot exist.
	prev := wellKnownDockerPathsFn
	wellKnownDockerPathsFn = func() []string { return []string{filepath.Join(t.TempDir(), "no-such-docker")} }
	t.Cleanup(func() { wellKnownDockerPathsFn = prev })

	got, err := ResolveDockerPath(nil)
	assert.Error(t, err, "should error when docker is missing from PATH, login shell, and well-known paths")
	assert.Empty(t, got)
}

// TestResolveDockerPath_NegativeTTLRetry verifies that a failed resolution
// is retried after dockerPathNegativeTTL elapses, so a transient install-time
// failure does not permanently disable docker discovery for the daemon.
func TestResolveDockerPath_NegativeTTLRetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only fixture")
	}
	resetDockerPathCacheForTest()
	t.Cleanup(resetDockerPathCacheForTest)

	// Make negative cache effectively immediate so the second call re-probes.
	prevTTL := dockerPathNegativeTTL
	dockerPathNegativeTTL = 0
	t.Cleanup(func() { dockerPathNegativeTTL = prevTTL })

	// Empty well-known list during the FIRST call so it fails.
	prevPaths := wellKnownDockerPathsFn
	wellKnownDockerPathsFn = func() []string { return nil }
	t.Cleanup(func() { wellKnownDockerPathsFn = prevPaths })

	t.Setenv("PATH", t.TempDir())
	shellDir := t.TempDir()
	fakeShell := writeFakeShell(t, shellDir, "")
	t.Setenv("SHELL", fakeShell)

	_, err := ResolveDockerPath(nil)
	require.Error(t, err, "first call should fail (no docker anywhere)")

	// Now publish a fake docker via well-known paths. The cached failure
	// must NOT block the retry.
	dockerDir := t.TempDir()
	want := writeFakeDocker(t, dockerDir)
	wellKnownDockerPathsFn = func() []string { return []string{want} }

	got, err := ResolveDockerPath(nil)
	require.NoError(t, err, "second call must retry after negative TTL elapses")
	assert.Equal(t, want, got)
}

// TestResolveDockerPath_WellKnownPathFallback simulates a PKG-installer
// context: docker is missing from $PATH and the login shell emits nothing,
// but Docker Desktop installed a binary at a well-known path. The fallback
// must find it without invoking the shell.
func TestResolveDockerPath_WellKnownPathFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("well-known path fallback is unix-only")
	}
	resetDockerPathCacheForTest()
	t.Cleanup(resetDockerPathCacheForTest)

	dockerDir := t.TempDir()
	want := writeFakeDocker(t, dockerDir)

	// Ambient PATH excludes dockerDir.
	t.Setenv("PATH", t.TempDir())

	// Stub well-known paths to point at our fake docker.
	prev := wellKnownDockerPathsFn
	wellKnownDockerPathsFn = func() []string { return []string{want} }
	t.Cleanup(func() { wellKnownDockerPathsFn = prev })

	// Sabotage the shell so we can prove the well-known fallback ran first.
	t.Setenv("SHELL", "/nonexistent/shell-must-not-be-invoked")

	got, err := ResolveDockerPath(nil)
	require.NoError(t, err, "well-known fallback must succeed when docker exists at a known path")
	assert.Equal(t, want, got)
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
