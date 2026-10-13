package storage

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Invariant: SaveUpstreamServer never lowers a recorded Quarantined=true unless
// the incoming config carries an explicit operator decision. Only
// QuarantineUpstreamServer (the review/approve door) may clear it otherwise.

func newGuardManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir(), zaptest.NewLogger(t).Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func guardServer(name string, quarantined bool) *config.ServerConfig {
	return &config.ServerConfig{
		Name:        name,
		Command:     "true",
		Protocol:    "stdio",
		Enabled:     true,
		Quarantined: quarantined,
		Created:     time.Now(),
	}
}

func storedQuarantined(t *testing.T, m *Manager, name string) bool {
	t.Helper()
	sc, err := m.GetUpstreamServer(name)
	require.NoError(t, err)
	return sc.Quarantined
}

func TestSaveUpstreamServer_DoesNotLowerRecordedQuarantine(t *testing.T) {
	m := newGuardManager(t)
	require.NoError(t, m.SaveUpstreamServer(guardServer("s", true)))

	update := guardServer("s", false)
	update.Args = []string{"--changed"}
	update.Env = map[string]string{"K": "V"}
	require.NoError(t, m.SaveUpstreamServer(update))

	got, err := m.GetUpstreamServer("s")
	require.NoError(t, err)
	assert.True(t, got.Quarantined, "an unstated false must not lower the recorded quarantine")
	assert.Equal(t, []string{"--changed"}, got.Args, "other fields still update")
	assert.Equal(t, "V", got.Env["K"])
}

func TestSaveUpstreamServer_ExplicitFalseLowersQuarantine(t *testing.T) {
	m := newGuardManager(t)
	require.NoError(t, m.SaveUpstreamServer(guardServer("s", true)))

	update := guardServer("s", false)
	update.MarkQuarantineExplicitlySet(true)
	require.NoError(t, m.SaveUpstreamServer(update))

	assert.False(t, storedQuarantined(t, m, "s"))
}

func TestSaveUpstreamServer_ExplicitFalseFromJSON(t *testing.T) {
	m := newGuardManager(t)
	require.NoError(t, m.SaveUpstreamServer(guardServer("x", true)))

	var sc config.ServerConfig
	require.NoError(t, json.Unmarshal([]byte(`{"name":"x","command":"true","protocol":"stdio","enabled":true,"quarantined":false}`), &sc))
	require.True(t, sc.QuarantineExplicitlySet())
	require.NoError(t, m.SaveUpstreamServer(&sc))

	assert.False(t, storedQuarantined(t, m, "x"), "an operator-written quarantined:false is obeyed")
}

func TestSaveUpstreamServer_NewServerUnquarantinedIsSaved(t *testing.T) {
	m := newGuardManager(t)
	require.NoError(t, m.SaveUpstreamServer(guardServer("fresh", false)))
	assert.False(t, storedQuarantined(t, m, "fresh"))
}

func TestSaveUpstreamServer_RaiseAlwaysAllowed(t *testing.T) {
	m := newGuardManager(t)
	require.NoError(t, m.SaveUpstreamServer(guardServer("s", false)))
	require.NoError(t, m.SaveUpstreamServer(guardServer("s", true)))
	assert.True(t, storedQuarantined(t, m, "s"))
}

func TestSaveUpstreamServer_DoesNotMutateCaller(t *testing.T) {
	m := newGuardManager(t)
	require.NoError(t, m.SaveUpstreamServer(guardServer("s", true)))

	update := guardServer("s", false)
	require.NoError(t, m.SaveUpstreamServer(update))

	assert.False(t, update.Quarantined, "the guard changes the persisted record, never the caller's struct")
	assert.False(t, update.QuarantineExplicitlySet())
}

func TestQuarantineUpstreamServer_StillUnquarantines(t *testing.T) {
	m := newGuardManager(t)
	require.NoError(t, m.SaveUpstreamServer(guardServer("s", true)))

	require.NoError(t, m.QuarantineUpstreamServer("s", false))

	assert.False(t, storedQuarantined(t, m, "s"), "the sanctioned approve door must still work")
}

func TestSaveUpstreamServer_ConcurrentQuarantineNotLost(t *testing.T) {
	m := newGuardManager(t)
	require.NoError(t, m.SaveUpstreamServer(guardServer("s", false)))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = m.SaveUpstreamServer(guardServer("s", false))
			}
		}
	}()

	require.NoError(t, m.QuarantineUpstreamServer("s", true))
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	assert.True(t, storedQuarantined(t, m, "s"), "a concurrent quarantine must not be lost to a stale save")
}
