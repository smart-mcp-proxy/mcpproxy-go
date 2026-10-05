package upstream

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/callstats"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type notifyRecorder struct {
	mu    sync.Mutex
	names []string
}

func (n *notifyRecorder) observe(server string) {
	n.mu.Lock()
	n.names = append(n.names, server)
	n.mu.Unlock()
}

func (n *notifyRecorder) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.names)
}

func newNotifyManager(t *testing.T) (*Manager, *notifyRecorder, *fakeClock) {
	t.Helper()
	m := NewManager(zap.NewNop(), &config.Config{}, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel(); m.stopCallHealthTimers() })
	now := &fakeClock{t: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	m.callStats.SetClock(now.Now)
	rec := &notifyRecorder{}
	m.SetCallHealthObserver(rec.observe)
	return m, rec, now
}

// FR-068: a failure-rate change that flips the computed health level pushes a
// servers.changed nudge; calls that do not flip it stay quiet.
func TestManager_CallHealthObserver_FiresOnFlipOnly(t *testing.T) {
	m, rec, _ := newNotifyManager(t)
	fail := errors.New("dial tcp: connection refused")

	for i := 0; i < 4; i++ {
		m.RecordCallOutcome("s", nil, fail) // 4/4: under the sample floor
	}
	assert.Equal(t, 0, rec.count(), "below the sample floor nothing flips")

	m.RecordCallOutcome("s", nil, fail) // 5/5: degraded
	require.Equal(t, 1, rec.count())
	assert.Equal(t, "s", rec.names[0])

	m.RecordCallOutcome("s", nil, fail) // still degraded: no new event
	assert.Equal(t, 1, rec.count())
}

// Recovery by window expiry has no triggering call, so a timer re-evaluates it.
func TestManager_CallHealthObserver_FiresWhenWindowExpires(t *testing.T) {
	m, rec, now := newNotifyManager(t)
	m.callHealth.recheckInterval = 20 * time.Millisecond
	m.callHealth.notifyInterval = -1 // never debounce

	for i := 0; i < 5; i++ {
		m.RecordCallOutcome("s", nil, errors.New("connection reset"))
	}
	require.Equal(t, 1, rec.count())

	now.Advance(callstats.WindowSize + callstats.BucketSize)
	require.Eventually(t, func() bool { return rec.count() == 2 },
		2*time.Second, 10*time.Millisecond, "expiry of the window must notify")
}

func TestManager_CallHealthObserver_DebouncesPerServer(t *testing.T) {
	m, rec, now := newNotifyManager(t)
	m.callHealth.notifyInterval = 150 * time.Millisecond
	m.callHealth.recheckInterval = time.Hour

	for i := 0; i < 5; i++ {
		m.RecordCallOutcome("s", nil, errors.New("connection reset"))
	}
	require.Equal(t, 1, rec.count())

	// Flip back and forth inside the debounce window.
	now.Advance(callstats.WindowSize + callstats.BucketSize)
	m.RecordCallOutcome("s", nil, nil) // 1 call: healthy again -> flip, debounced
	assert.Equal(t, 1, rec.count(), "second flip inside the interval is deferred")
	require.Eventually(t, func() bool { return rec.count() == 2 },
		2*time.Second, 10*time.Millisecond, "the deferred notification must still fire")
}

// Reviewer finding: six old successes + five newer failures is healthy (5/11);
// when the successes expire the remaining 5/5 degrades with no triggering call.
func TestManager_CallHealthObserver_FiresWhenSuccessesExpire(t *testing.T) {
	m, rec, now := newNotifyManager(t)
	m.callHealth.recheckInterval = 20 * time.Millisecond
	m.callHealth.notifyInterval = -1

	for i := 0; i < 6; i++ {
		m.RecordCallOutcome("s", nil, nil)
	}
	now.Advance(4 * time.Minute)
	for i := 0; i < 5; i++ {
		m.RecordCallOutcome("s", nil, errors.New("connection reset"))
	}
	require.Equal(t, 0, rec.count(), "5 of 11 is healthy")

	now.Advance(callstats.WindowSize - 4*time.Minute + callstats.BucketSize) // successes age out
	require.Eventually(t, func() bool { return rec.count() == 1 },
		2*time.Second, 10*time.Millisecond, "5 of 5 after expiry must notify")
}

// Reviewer finding: a call still in flight when its server is removed fails
// (the transport closes) and must not recreate the dropped window.
func TestManager_RecordClientCallOutcome_IgnoresRemovedClient(t *testing.T) {
	serverCfg := limitedServerConfig("gone-server")
	cfg := &config.Config{Servers: []*config.ServerConfig{serverCfg}}
	m := newConcurrencyManager(t, cfg, serverCfg)
	client, ok := m.GetClient("gone-server")
	require.True(t, ok)

	m.RecordClientCallOutcome(client, nil, errors.New("connection reset"))
	calls, _, _ := m.CallStats("gone-server")
	require.Equal(t, 1, calls)

	m.RemoveServer("gone-server")
	m.RecordClientCallOutcome(client, nil, errors.New("transport closed"))
	calls, _, _ = m.CallStats("gone-server")
	assert.Zero(t, calls, "an in-flight failure after removal must not resurrect the window")
}
