package shellwrap

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// --- helpers -------------------------------------------------------------

// writeFakeLoginEnvShell writes a POSIX shell that exports the supplied
// environment (simulating the user's rc files) BEFORE evaluating its `-c`
// command (captureLoginShellEnv's `env -0`). This lets tests assert that the
// capture/hydration picks up login-only vars that differ from the ambient
// process environment. PATH should include /usr/bin so the fake shell can
// locate the `env` binary.
func writeFakeLoginEnvShell(t *testing.T, dir string, overrides map[string]string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	for k, v := range overrides {
		b.WriteString("export " + k + "='" + v + "'\n")
	}
	b.WriteString(`while [ $# -gt 0 ]; do
  case "$1" in
    -l|--login) shift ;;
    -c) shift; eval "$1"; shift ;;
    *) shift ;;
  esac
done
`)
	p := filepath.Join(dir, "fake-login-env-shell")
	require.NoError(t, os.WriteFile(p, []byte(b.String()), 0o755))
	return p
}

// restoreEnvAfter snapshots the process environment and restores it on
// cleanup, since HydrateFromLoginShell mutates os env directly (not via
// t.Setenv, which auto-restores).
func restoreEnvAfter(t *testing.T) {
	t.Helper()
	saved := os.Environ()
	t.Cleanup(func() {
		os.Clearenv()
		for _, kv := range saved {
			if i := strings.IndexByte(kv, '='); i >= 0 {
				_ = os.Setenv(kv[:i], kv[i+1:])
			}
		}
	})
}

// withHydrationGOOS overrides the macOS-only gate seam so the logic can be
// exercised on Linux CI runners.
func withHydrationGOOS(t *testing.T, goos string) {
	t.Helper()
	prev := hydrationGOOS
	hydrationGOOS = goos
	t.Cleanup(func() { hydrationGOOS = prev })
}

// --- tests ---------------------------------------------------------------

func TestHydrate_GateNoOpOnNonDarwin(t *testing.T) {
	resetLoginShellEnvCacheForTest()
	t.Cleanup(resetLoginShellEnvCacheForTest)
	restoreEnvAfter(t)
	withHydrationGOOS(t, "linux")

	t.Setenv("PATH", "/usr/bin:/bin") // minimal, but non-darwin gate wins

	applied, snapshot := HydrateFromLoginShell(nil)
	assert.False(t, applied, "hydration must be a no-op on non-darwin")
	assert.Empty(t, snapshot)
}

func TestHydrate_GateNoOpOnComprehensivePathAndAllDockerPresent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login-shell hydration is unix-only")
	}
	resetLoginShellEnvCacheForTest()
	t.Cleanup(resetLoginShellEnvCacheForTest)
	restoreEnvAfter(t)
	withHydrationGOOS(t, "darwin")

	// Comprehensive PATH AND all DOCKER_* vars already present ⇒ neither gate
	// condition triggers ⇒ hydration is a no-op.
	t.Setenv("PATH", "/usr/local/bin:/usr/bin:/bin")
	t.Setenv("DOCKER_HOST", "unix:///already/set.sock")
	t.Setenv("DOCKER_CONTEXT", "desktop-linux")
	t.Setenv("DOCKER_CONFIG", "/Users/me/.docker")
	t.Setenv("DOCKER_CERT_PATH", "/Users/me/.docker/certs")
	t.Setenv("DOCKER_TLS_VERIFY", "1")

	dir := t.TempDir()
	fake := writeFakeLoginEnvShell(t, dir, map[string]string{
		"PATH":        "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
		"DOCKER_HOST": "unix:///should/not/be/used.sock",
	})
	t.Setenv("SHELL", fake)

	applied, snapshot := HydrateFromLoginShell(nil)
	assert.False(t, applied, "comprehensive PATH + all docker vars ⇒ no-op")
	assert.Empty(t, snapshot)
	assert.Equal(t, "unix:///already/set.sock", os.Getenv("DOCKER_HOST"),
		"existing DOCKER_HOST must not be touched")
}

func TestHydrate_ComprehensivePathStillHydratesDockerIfMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login-shell hydration is unix-only")
	}
	resetLoginShellEnvCacheForTest()
	t.Cleanup(resetLoginShellEnvCacheForTest)
	restoreEnvAfter(t)
	withHydrationGOOS(t, "darwin")

	// PATH is comprehensive (pre-seeded by /etc/paths or similar), but DOCKER_HOST
	// is absent — the second gate condition triggers hydration for curated vars
	// but must NOT merge PATH.
	t.Setenv("PATH", "/usr/local/bin:/usr/bin:/bin")
	os.Unsetenv("DOCKER_HOST")
	os.Unsetenv("DOCKER_CONTEXT")
	os.Unsetenv("DOCKER_CONFIG")
	os.Unsetenv("DOCKER_CERT_PATH")
	os.Unsetenv("DOCKER_TLS_VERIFY")

	dir := t.TempDir()
	fake := writeFakeLoginEnvShell(t, dir, map[string]string{
		"PATH":        "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
		"DOCKER_HOST": "unix:///Users/me/.docker/run/docker.sock",
	})
	t.Setenv("SHELL", fake)

	applied, snapshot := HydrateFromLoginShell(nil)
	require.True(t, applied, "missing DOCKER_HOST must trigger hydration")

	assert.Equal(t, "unix:///Users/me/.docker/run/docker.sock", os.Getenv("DOCKER_HOST"))

	// PATH must not be modified — it was already comprehensive.
	assert.Equal(t, "/usr/local/bin:/usr/bin:/bin", os.Getenv("PATH"),
		"comprehensive PATH must not be merged")
	assert.NotContains(t, snapshot, "PATH",
		"PATH must not appear in the snapshot when unchanged")
	assert.Contains(t, snapshot, "DOCKER_HOST")
}

func TestHydrate_MinimalPathHydratesCuratedSet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login-shell hydration is unix-only")
	}
	resetLoginShellEnvCacheForTest()
	t.Cleanup(resetLoginShellEnvCacheForTest)
	restoreEnvAfter(t)
	withHydrationGOOS(t, "darwin")

	t.Setenv("PATH", "/usr/bin:/bin") // launchd-minimal
	os.Unsetenv("DOCKER_HOST")
	os.Unsetenv("HOMEBREW_PREFIX")
	os.Unsetenv("GITHUB_TOKEN")

	dir := t.TempDir()
	fake := writeFakeLoginEnvShell(t, dir, map[string]string{
		"PATH":            "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
		"DOCKER_HOST":     "unix:///Users/me/.docker/run/docker.sock",
		"HOMEBREW_PREFIX": "/opt/homebrew",
		"GITHUB_TOKEN":    "ghp_secret_must_not_be_imported",
		// Proxy vars must NOT be imported — they are out of scope for this fix.
		"HTTPS_PROXY": "http://proxy.corp:8080",
	})
	t.Setenv("SHELL", fake)

	applied, snapshot := HydrateFromLoginShell(nil)
	require.True(t, applied, "launchd-minimal macOS launch must hydrate")

	// PATH merged login-first, ambient preserved.
	assert.True(t, strings.HasPrefix(os.Getenv("PATH"), "/opt/homebrew/bin:/usr/local/bin"),
		"PATH must be enriched login-first, got %q", os.Getenv("PATH"))
	assert.Contains(t, os.Getenv("PATH"), "/usr/bin")

	// Curated container / tool-home vars present in the process env.
	assert.Equal(t, "unix:///Users/me/.docker/run/docker.sock", os.Getenv("DOCKER_HOST"))
	assert.Equal(t, "/opt/homebrew", os.Getenv("HOMEBREW_PREFIX"))

	// Proxy vars are NOT in the allow-list and must never be hydrated.
	assert.Empty(t, os.Getenv("HTTPS_PROXY"), "proxy vars must never be hydrated (out of scope)")
	_, proxyLeaked := snapshot["HTTPS_PROXY"]
	assert.False(t, proxyLeaked, "proxy var must not appear in the diagnostic snapshot")

	// Secrets are NOT in the allow-list and must never be hydrated.
	assert.Empty(t, os.Getenv("GITHUB_TOKEN"), "secrets must never be hydrated into the daemon")
	_, leaked := snapshot["GITHUB_TOKEN"]
	assert.False(t, leaked, "secret must not appear in the diagnostic snapshot")

	assert.Contains(t, snapshot, "PATH")
	assert.Contains(t, snapshot, "DOCKER_HOST")
}

func TestHydrate_SetIfEmptyNeverClobbers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login-shell hydration is unix-only")
	}
	resetLoginShellEnvCacheForTest()
	t.Cleanup(resetLoginShellEnvCacheForTest)
	restoreEnvAfter(t)
	withHydrationGOOS(t, "darwin")

	t.Setenv("PATH", "/usr/bin:/bin")
	// Operator already set DOCKER_HOST on the daemon — hydration must not clobber.
	t.Setenv("DOCKER_HOST", "tcp://operator-set:2375")

	dir := t.TempDir()
	fake := writeFakeLoginEnvShell(t, dir, map[string]string{
		"PATH":        "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
		"DOCKER_HOST": "unix:///login/value.sock",
	})
	t.Setenv("SHELL", fake)

	applied, snapshot := HydrateFromLoginShell(nil)
	require.True(t, applied, "PATH enrichment still applies")

	assert.Equal(t, "tcp://operator-set:2375", os.Getenv("DOCKER_HOST"),
		"operator-set DOCKER_HOST must never be clobbered")
	_, inSnap := snapshot["DOCKER_HOST"]
	assert.False(t, inSnap, "un-applied (clobber-skipped) key must not be in snapshot")
}

func TestHydrate_NeverTouchesHomeUserShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login-shell hydration is unix-only")
	}
	resetLoginShellEnvCacheForTest()
	t.Cleanup(resetLoginShellEnvCacheForTest)
	restoreEnvAfter(t)
	withHydrationGOOS(t, "darwin")

	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/real/home")
	t.Setenv("USER", "realuser")

	dir := t.TempDir()
	fake := writeFakeLoginEnvShell(t, dir, map[string]string{
		"PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
		"HOME": "/evil/home",
		"USER": "eviluser",
	})
	t.Setenv("SHELL", fake)

	_, _ = HydrateFromLoginShell(nil)

	assert.Equal(t, "/real/home", os.Getenv("HOME"), "HOME must never be hydrated")
	assert.Equal(t, "realuser", os.Getenv("USER"), "USER must never be hydrated")
}

func TestHydrate_NeverLogsValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login-shell hydration is unix-only")
	}
	resetLoginShellEnvCacheForTest()
	t.Cleanup(resetLoginShellEnvCacheForTest)
	restoreEnvAfter(t)
	withHydrationGOOS(t, "darwin")

	t.Setenv("PATH", "/usr/bin:/bin")
	os.Unsetenv("DOCKER_HOST")

	const secretVal = "unix:///super/secret/docker-socket-value.sock"
	dir := t.TempDir()
	fake := writeFakeLoginEnvShell(t, dir, map[string]string{
		"PATH":        "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
		"DOCKER_HOST": secretVal,
	})
	t.Setenv("SHELL", fake)

	core, recorded := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	applied, _ := HydrateFromLoginShell(logger)
	require.True(t, applied)

	for _, e := range recorded.All() {
		assert.NotContains(t, e.Message, secretVal, "log message must never contain a hydrated value")
		for k, v := range e.ContextMap() {
			assert.NotContains(t, fmt.Sprintf("%v", v), secretVal,
				"log field %q must never contain a hydrated value", k)
		}
	}
}

func TestHydrate_CaptureFailureIsNoOp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login-shell hydration is unix-only")
	}
	resetLoginShellEnvCacheForTest()
	t.Cleanup(resetLoginShellEnvCacheForTest)
	restoreEnvAfter(t)
	withHydrationGOOS(t, "darwin")

	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("SHELL", "/nonexistent/shell-binary-does-not-exist")

	applied, snapshot := HydrateFromLoginShell(nil)
	assert.False(t, applied, "a failed login-shell capture must degrade to a no-op")
	assert.Empty(t, snapshot)
}
