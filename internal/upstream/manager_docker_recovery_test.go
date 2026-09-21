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

// TestRunDockerInfo_ExecutesResolvedBinary exercises runDockerInfo's real
// exec.CommandContext(...).Run() invocation shape end-to-end against a real
// subprocess, so a regression in the argument/flag shape (wrong dockerBin
// position, broken --format string) is caught even though the tests below
// inject dockerInfoRunnerFn and never call this function.
func TestRunDockerInfo_ExecutesResolvedBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/bin/sh-style fake docker is POSIX only")
	}

	dockerPath := filepath.Join(t.TempDir(), "docker")
	script := `#!/bin/sh
case "$1" in
  info) printf '"24.0.0"\n'; exit 0 ;;
  *)    exit 99 ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := runDockerInfo(ctx, dockerPath); err != nil {
		t.Fatalf("expected fake docker info to succeed, got: %v", err)
	}
}

// TestRunDockerInfo_PropagatesNonZeroExit ensures a real non-zero process exit
// (not just a synthetic Go error) surfaces as an error from runDockerInfo.
func TestRunDockerInfo_PropagatesNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/bin/sh-style fake docker is POSIX only")
	}

	dockerPath := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(dockerPath, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := runDockerInfo(ctx, dockerPath); err == nil {
		t.Fatal("expected non-zero exit to surface as an error")
	}
}

// TestCheckDockerAvailability_UsesShellwrapResolver verifies that a launchd-style
// minimal PATH (the situation when mcpproxy is launched from /Applications/...app
// or a LoginItem) does not break docker lookup, because checkDockerAvailability
// now goes through dockerResolverFn (shellwrap-backed) instead of relying on
// $PATH for the bare "docker" binary.
//
// dockerInfoRunnerFn is faked so the test asserts the resolved executable
// directly without depending on process startup or a wall-clock deadline; the
// real exec.CommandContext(...).Run() shape is covered separately by
// TestRunDockerInfo_ExecutesResolvedBinary above.
func TestCheckDockerAvailability_UsesShellwrapResolver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("launchd PATH scenario is macOS/Linux only")
	}

	dockerPath := filepath.Join(t.TempDir(), "docker")

	original := dockerResolverFn
	t.Cleanup(func() { dockerResolverFn = original })
	dockerResolverFn = func(*zap.Logger) (string, error) {
		return dockerPath, nil
	}

	var resolvedBin string
	originalRunner := dockerInfoRunnerFn
	t.Cleanup(func() { dockerInfoRunnerFn = originalRunner })
	dockerInfoRunnerFn = func(_ context.Context, dockerBin string) error {
		resolvedBin = dockerBin
		return nil
	}

	m := &Manager{logger: zap.NewNop()}

	if err := m.checkDockerAvailability(context.Background()); err != nil {
		t.Fatalf("expected docker to be reachable via resolver, got: %v", err)
	}
	if resolvedBin != dockerPath {
		t.Fatalf("docker executable = %q, want resolved path %q", resolvedBin, dockerPath)
	}
}

// TestCheckDockerAvailability_FallbackOnResolverFailure ensures that even when
// the resolver itself errors out we still select the bare executable name — preserving
// the original behaviour for hosts where the shellwrap probes legitimately
// cannot find docker but it IS on the parent's PATH.
func TestCheckDockerAvailability_FallbackOnResolverFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/bin/sh-style fake docker is POSIX only")
	}

	original := dockerResolverFn
	t.Cleanup(func() { dockerResolverFn = original })
	dockerResolverFn = func(*zap.Logger) (string, error) {
		return "", errors.New("simulated resolver failure")
	}

	var resolvedBin string
	originalRunner := dockerInfoRunnerFn
	t.Cleanup(func() { dockerInfoRunnerFn = originalRunner })
	dockerInfoRunnerFn = func(_ context.Context, dockerBin string) error {
		resolvedBin = dockerBin
		return nil
	}

	m := &Manager{logger: zap.NewNop()}

	if err := m.checkDockerAvailability(context.Background()); err != nil {
		t.Fatalf("expected fallback bare-name exec to succeed, got: %v", err)
	}
	if resolvedBin != "docker" {
		t.Fatalf("docker executable = %q, want bare-name fallback", resolvedBin)
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
