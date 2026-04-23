package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func boolPtr(b bool) *bool { return &b }

// TestShouldIsolate_WarnsOnIgnoredPerServerOptIn covers the silent
// short-circuit bug: when a user sets `isolation.enabled: true` on a server
// but forgets to enable `docker_isolation.enabled` globally, the server
// falls back to host execution. We want that to be loud in the log (once).
func TestShouldIsolate_WarnsOnIgnoredPerServerOptIn(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	im := NewIsolationManagerWithLogger(&config.DockerIsolationConfig{
		Enabled: false, // global off
	}, logger)

	srv := &config.ServerConfig{
		Name:    "my-server",
		Command: "python",
		Isolation: &config.IsolationConfig{
			Enabled: boolPtr(true), // per-server opt-in
		},
	}

	got := im.ShouldIsolate(srv)
	assert.False(t, got, "global off should short-circuit to false")

	entries := recorded.All()
	assert.Len(t, entries, 1, "expected exactly one warning")
	assert.Contains(t, entries[0].Message, "per-server docker isolation opt-in ignored")

	// Dedup: a second call for the same server must not emit another warning.
	_ = im.ShouldIsolate(srv)
	entries = recorded.All()
	assert.Len(t, entries, 1, "warning must be deduped per server name")
}

// TestShouldIsolate_NoWarningWhenGlobalOn verifies we don't emit the
// warning when the global flag is on — only the silent-fallback case is
// interesting.
func TestShouldIsolate_NoWarningWhenGlobalOn(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	im := NewIsolationManagerWithLogger(&config.DockerIsolationConfig{
		Enabled: true,
	}, logger)

	srv := &config.ServerConfig{
		Name:    "my-server",
		Command: "python",
		Isolation: &config.IsolationConfig{
			Enabled: boolPtr(true),
		},
	}

	_ = im.ShouldIsolate(srv)
	assert.Empty(t, recorded.All(), "no warning expected when global flag is on")
}

// TestShouldIsolate_NoWarningWhenPerServerNil verifies that a nil
// per-server isolation config (i.e. "inherit global") does NOT trigger the
// warning. The warning is only for explicit per-server opt-ins that get
// ignored.
func TestShouldIsolate_NoWarningWhenPerServerNil(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	im := NewIsolationManagerWithLogger(&config.DockerIsolationConfig{
		Enabled: false,
	}, logger)

	srv := &config.ServerConfig{
		Name:    "my-server",
		Command: "python",
		// No Isolation config at all.
	}

	_ = im.ShouldIsolate(srv)
	assert.Empty(t, recorded.All(), "no warning expected when per-server isolation is not configured")
}

// TestShouldIsolate_DifferentServersWarnedSeparately checks that the dedup
// is per-server, not global — two different servers each get their own
// warning.
func TestShouldIsolate_DifferentServersWarnedSeparately(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	im := NewIsolationManagerWithLogger(&config.DockerIsolationConfig{
		Enabled: false,
	}, logger)

	_ = im.ShouldIsolate(&config.ServerConfig{
		Name:      "server-a",
		Command:   "python",
		Isolation: &config.IsolationConfig{Enabled: boolPtr(true)},
	})
	_ = im.ShouldIsolate(&config.ServerConfig{
		Name:      "server-b",
		Command:   "python",
		Isolation: &config.IsolationConfig{Enabled: boolPtr(true)},
	})

	assert.Len(t, recorded.All(), 2, "two distinct servers should each warn once")
}
