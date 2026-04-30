package secureenv

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLaunchdMinimalPath_Enhancement reproduces issue #439: when MCPProxy is
// launched from Finder / Dock / a LoginItem, launchd hands the process a
// 4-element PATH ("/usr/bin:/bin:/usr/sbin:/sbin"). Pre-fix, the
// `len(pathParts) <= 2` gate in buildEnhancedPath blocked enhancement, so
// stdio servers couldn't find tools like uvx/npx in /usr/local/bin or
// /opt/homebrew/bin.
func TestLaunchdMinimalPath_Enhancement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("launchd PATH semantics are macOS/Linux-only")
	}

	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	// Override loginShellPATHFn so the test does not actually fork a shell
	// and the captured value is deterministic.
	t.Cleanup(withFakeLoginShellPath(""))

	// Exact PATH that launchd hands a GUI-launched app on macOS — 4 entries,
	// none of which contain uvx/npx/brew binaries.
	os.Clearenv()
	os.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	os.Setenv("HOME", "/tmp/test-home")

	manager := NewManager(&EnvConfig{
		InheritSystemSafe: true,
		AllowedSystemVars: []string{"PATH", "HOME"},
		EnhancePath:       true,
	})

	envVars := manager.BuildSecureEnvironment()
	pathVal := getPATH(envVars)
	require.NotEmpty(t, pathVal, "PATH must be present in built environment")

	parts := strings.Split(pathVal, ":")
	assert.Greater(t, len(parts), 4, "launchd 4-element PATH must be enhanced — gate was blocking enhancement before the fix")

	// At least one of /usr/local/bin or /opt/homebrew/bin should be in the
	// result on a normal dev machine. We accept either to keep the test
	// portable across Intel and Apple Silicon hosts (and CI).
	hasHomebrew := false
	for _, dir := range []string{"/usr/local/bin", "/opt/homebrew/bin"} {
		if _, err := os.Stat(dir); err == nil {
			assert.Contains(t, pathVal, dir,
				"existing tool directory %s must be added to enhanced PATH", dir)
			hasHomebrew = true
		}
	}
	if !hasHomebrew {
		t.Skip("no /usr/local/bin or /opt/homebrew/bin on this system; cannot assert tool-dir presence")
	}

	// Original launchd entries must be preserved.
	assert.Contains(t, pathVal, "/usr/bin")
	assert.Contains(t, pathVal, "/bin")
}

// TestLaunchdMinimalPath_LoginShellPathPriority verifies that user-specific
// tool paths (Colima / mise / asdf / pyenv shims / custom installs) captured
// from the user's interactive login shell get merged into the enhanced PATH
// and rank ahead of the launchd-minimal entries.
func TestLaunchdMinimalPath_LoginShellPathPriority(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("launchd PATH semantics are macOS/Linux-only")
	}

	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	// Pretend the user's login shell exposes mise + a custom dir that no
	// static-discovery list would ever guess.
	loginShellPATH := "/Users/test/.local/share/mise/shims:/Users/test/.colima/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
	t.Cleanup(withFakeLoginShellPath(loginShellPATH))

	os.Clearenv()
	os.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	os.Setenv("HOME", "/tmp/test-home")

	manager := NewManager(&EnvConfig{
		InheritSystemSafe: true,
		AllowedSystemVars: []string{"PATH", "HOME"},
		EnhancePath:       true,
	})

	pathVal := getPATH(manager.BuildSecureEnvironment())
	require.NotEmpty(t, pathVal)

	// The mise shim path must be in the result — only the login-shell
	// capture would have surfaced it.
	assert.Contains(t, pathVal, "/Users/test/.local/share/mise/shims",
		"login-shell PATH entries must be merged into the enhanced PATH (issue #439)")
	assert.Contains(t, pathVal, "/Users/test/.colima/bin",
		"login-shell PATH entries must be merged into the enhanced PATH (issue #439)")

	// Login-shell entries must rank ahead of launchd-minimal entries so
	// `which uvx` resolves the user's preferred toolchain version.
	parts := strings.Split(pathVal, ":")
	idxMise := indexOf(parts, "/Users/test/.local/share/mise/shims")
	idxBin := indexOf(parts, "/bin")
	require.GreaterOrEqual(t, idxMise, 0, "mise shim must be present")
	require.GreaterOrEqual(t, idxBin, 0, "/bin must be present")
	assert.Less(t, idxMise, idxBin, "login-shell entries must come before launchd-minimal entries")

	// No duplicates.
	seen := make(map[string]bool)
	for _, p := range parts {
		assert.False(t, seen[p], "duplicate PATH segment %q in %q", p, pathVal)
		seen[p] = true
	}
}

// TestLaunchdMinimalPath_NoLoginShellAvailable covers the "fresh macOS
// account, no rc files" case — login-shell capture returns "" so we must
// still produce a usable PATH from static discovery alone.
func TestLaunchdMinimalPath_NoLoginShellAvailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("launchd PATH semantics are macOS/Linux-only")
	}

	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	t.Cleanup(withFakeLoginShellPath(""))

	os.Clearenv()
	os.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	os.Setenv("HOME", "/tmp/test-home")

	manager := NewManager(&EnvConfig{
		InheritSystemSafe: true,
		AllowedSystemVars: []string{"PATH", "HOME"},
		EnhancePath:       true,
	})

	pathVal := getPATH(manager.BuildSecureEnvironment())
	require.NotEmpty(t, pathVal)
	parts := strings.Split(pathVal, ":")
	assert.Greater(t, len(parts), 4, "even without login-shell capture, static discovery must enhance the launchd-minimal PATH")
}

// TestLaunchdMinimalPath_AlreadyComprehensive verifies the existing
// behaviour: when PATH is already comprehensive (contains /usr/local/bin or
// /opt/homebrew/bin), do not touch it. Same as the existing
// "PATH enhancement skipped for comprehensive paths" test, restated for
// clarity in the launchd context.
func TestLaunchdMinimalPath_AlreadyComprehensive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("launchd PATH semantics are macOS/Linux-only")
	}

	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	// Even if the user has a rich login-shell PATH, a process started from a
	// terminal (with /usr/local/bin already on PATH) should be left alone.
	t.Cleanup(withFakeLoginShellPath("/Users/test/.local/share/mise/shims:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"))

	comprehensivePath := "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	os.Clearenv()
	os.Setenv("PATH", comprehensivePath)
	os.Setenv("HOME", "/tmp/test-home")

	manager := NewManager(&EnvConfig{
		InheritSystemSafe: true,
		AllowedSystemVars: []string{"PATH", "HOME"},
		EnhancePath:       true,
	})

	pathVal := getPATH(manager.BuildSecureEnvironment())
	assert.Equal(t, comprehensivePath, pathVal,
		"comprehensive PATH must be returned unchanged — terminal-launched processes should not be polluted by login-shell capture")
}

// --- test helpers --------------------------------------------------------

// withFakeLoginShellPath swaps loginShellPATHFn for a stub returning `path`.
// Returns a cleanup function suitable for t.Cleanup.
func withFakeLoginShellPath(path string) func() {
	prev := loginShellPATHFn
	loginShellPATHFn = func() string { return path }
	return func() { loginShellPATHFn = prev }
}

func getPATH(envVars []string) string {
	for _, kv := range envVars {
		if strings.HasPrefix(kv, "PATH=") {
			return strings.TrimPrefix(kv, "PATH=")
		}
	}
	return ""
}

func indexOf(parts []string, target string) int {
	for i, p := range parts {
		if p == target {
			return i
		}
	}
	return -1
}
