package upstream

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestCheckDockerAvailability_UsesShellwrapResolver verifies that a launchd-style
// minimal PATH (the situation when mcpproxy is launched from /Applications/...app
// or a LoginItem) does not break docker lookup, because checkDockerAvailability
// now goes through dockerResolverFn (shellwrap-backed) instead of relying on
// $PATH for the bare "docker" binary.
//
// We exercise the real resolver indirectly: the test installs a fake docker
// shim into a temp dir, points dockerResolverFn at it, drops $PATH to the
// launchd minimum, and asserts that checkDockerAvailability still succeeds.
// Without the fix, exec.Command("docker") would error with `executable file
// not found in $PATH` regardless of how the resolver was wired.
func TestCheckDockerAvailability_UsesShellwrapResolver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("launchd PATH scenario is macOS/Linux only")
	}

	tmpDir := t.TempDir()
	dockerPath := filepath.Join(tmpDir, "docker")

	// Fake docker that prints a server version when invoked with `info`.
	// Anything else exits non-zero so we know the call shape was correct.
	script := `#!/bin/sh
case "$1" in
  info) printf '"24.0.0"\n'; exit 0 ;;
  *)    exit 99 ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	// Simulate launchd's minimal PATH — fake docker is NOT on it. If the
	// production code resolved via os.Getenv("PATH") we would fail here.
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

	original := dockerResolverFn
	t.Cleanup(func() { dockerResolverFn = original })
	dockerResolverFn = func(*zap.Logger) (string, error) {
		return dockerPath, nil
	}

	m := &Manager{logger: zap.NewNop()}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := m.checkDockerAvailability(ctx); err != nil {
		t.Fatalf("expected docker to be reachable via resolver, got: %v", err)
	}
}

// TestCheckDockerAvailability_FallbackOnResolverFailure ensures that even when
// the resolver itself errors out we still attempt a bare-name exec — preserving
// the original behaviour for hosts where the shellwrap probes legitimately
// cannot find docker but it IS on the parent's PATH.
func TestCheckDockerAvailability_FallbackOnResolverFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/bin/sh-style fake docker is POSIX only")
	}

	tmpDir := t.TempDir()
	dockerPath := filepath.Join(tmpDir, "docker")
	script := `#!/bin/sh
exit 0
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	// Resolver fails — the fallback should still exec via bare PATH.
	t.Setenv("PATH", tmpDir)

	original := dockerResolverFn
	t.Cleanup(func() { dockerResolverFn = original })
	dockerResolverFn = func(*zap.Logger) (string, error) {
		return "", errors.New("simulated resolver failure")
	}

	m := &Manager{logger: zap.NewNop()}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := m.checkDockerAvailability(ctx); err != nil {
		t.Fatalf("expected fallback bare-name exec to succeed, got: %v", err)
	}
}

// TestFreshenLoadedDockerRecoveryState verifies that a state loaded from the
// previous process gets its per-process retry counters reset to zero so the
// new process has a fresh retry budget.
func TestFreshenLoadedDockerRecoveryState(t *testing.T) {
	now := time.Now()
	staleErr := "previous process error"
	state := &storage.DockerRecoveryState{
		LastAttempt:      now.Add(-5 * time.Minute),
		FailureCount:     10, // exhausted previous budget
		DockerAvailable:  false,
		RecoveryMode:     true,
		LastError:        staleErr,
		AttemptsSinceUp:  17,
		LastSuccessfulAt: now.Add(-1 * time.Hour),
	}

	freshenLoadedDockerRecoveryState(state)

	// Per-process counters cleared.
	if state.FailureCount != 0 {
		t.Errorf("FailureCount: want 0, got %d", state.FailureCount)
	}
	if state.AttemptsSinceUp != 0 {
		t.Errorf("AttemptsSinceUp: want 0, got %d", state.AttemptsSinceUp)
	}
	if state.RecoveryMode {
		t.Error("RecoveryMode: want false, got true")
	}

	// Telemetry fields preserved.
	if state.LastError != staleErr {
		t.Errorf("LastError should be preserved: got %q", state.LastError)
	}
	if state.LastSuccessfulAt.IsZero() {
		t.Error("LastSuccessfulAt should be preserved")
	}
	if state.LastAttempt.IsZero() {
		t.Error("LastAttempt should be preserved")
	}
}

// TestFreshenLoadedDockerRecoveryState_NilSafe verifies that the helper is a
// no-op on nil input — the production code path uses it inside an `if state != nil`
// guard, but defensive coverage prevents future regressions.
func TestFreshenLoadedDockerRecoveryState_NilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("freshenLoadedDockerRecoveryState panicked on nil: %v", r)
		}
	}()
	freshenLoadedDockerRecoveryState(nil)
}

// TestShouldEnableDockerRecovery_PerServerTriState pins the behaviour of the
// per-server isolation clause in shouldEnableDockerRecovery across the legacy
// `enabled` *bool tri-state, now that the clause routes through
// config.ServerDependsOnDocker instead of reading the bool by hand (GH #1142).
// With global isolation off, NO value of the legacy bool containerises the
// server: nil is "inherit" (which the global answer already covers), false is
// an opt-out, and true is an opt-in the resolver deliberately IGNORES under a
// none global mode. Isolation MODES are covered by
// TestShouldEnableDockerRecovery_HonorsIsolationModes.
func TestShouldEnableDockerRecovery_PerServerTriState(t *testing.T) {
	newManager := func(servers ...*config.ServerConfig) *Manager {
		m := &Manager{logger: zap.NewNop()}
		m.globalConfig.Store(&config.Config{
			DockerIsolation: &config.DockerIsolationConfig{Enabled: false},
			Servers:         servers,
		})
		return m
	}

	t.Run("global off and per-server nil stays off", func(t *testing.T) {
		m := newManager(&config.ServerConfig{
			Name:      "npx-server",
			Command:   "npx",
			Isolation: &config.IsolationConfig{Image: "node:22"},
		})
		if m.shouldEnableDockerRecovery() {
			t.Error("an inheriting (nil) per-server override must not enable Docker recovery when global isolation is off")
		}
	})

	t.Run("global off and per-server explicit false stays off", func(t *testing.T) {
		m := newManager(&config.ServerConfig{
			Name:      "npx-server",
			Command:   "npx",
			Isolation: &config.IsolationConfig{Enabled: config.BoolPtr(false)},
		})
		if m.shouldEnableDockerRecovery() {
			t.Error("an explicit opt-out must not enable Docker recovery")
		}
	})

	t.Run("global off and per-server explicit true stays off", func(t *testing.T) {
		// The resolver reports IsolationSourceServerOptInIgnored here: a legacy
		// bool opt-in cannot revive isolation while the global mode is none, so
		// the server is NOT containerised and there are no containers to
		// monitor or clean up. The old hand-rolled clause started the monitor
		// anyway.
		m := newManager(&config.ServerConfig{
			Name:      "npx-server",
			Command:   "npx",
			Isolation: &config.IsolationConfig{Enabled: config.BoolPtr(true)},
		})
		if m.shouldEnableDockerRecovery() {
			t.Error("a bool opt-in the resolver ignores must not enable Docker recovery")
		}
	})

	t.Run("a server whose own command is docker enables recovery", func(t *testing.T) {
		m := newManager(&config.ServerConfig{
			Name:    "already-dockerised",
			Command: "docker",
			Args:    []string{"run", "-i", "--rm", "mcp/foo"},
		})
		if !m.shouldEnableDockerRecovery() {
			t.Error("a server that shells out to docker itself needs the daemon, and its containers need cleanup")
		}
	})

	t.Run("global isolation on enables recovery regardless", func(t *testing.T) {
		m := &Manager{logger: zap.NewNop()}
		m.globalConfig.Store(&config.Config{
			DockerIsolation: &config.DockerIsolationConfig{Enabled: true},
		})
		if !m.shouldEnableDockerRecovery() {
			t.Error("global isolation on must enable Docker recovery")
		}
	})
}
