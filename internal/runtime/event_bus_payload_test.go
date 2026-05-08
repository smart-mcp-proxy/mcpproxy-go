package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// fakeServersLister is a minimal implementation of the serversLister interface
// used by emitServersChanged. It's stored on Runtime.managementService so the
// emit path can type-assert and call ListServers.
type fakeServersLister struct {
	servers []*contracts.Server
	stats   *contracts.ServerStats
	err     error
	calls   atomic.Int64
}

func (f *fakeServersLister) ListServers(_ context.Context) ([]*contracts.Server, *contracts.ServerStats, error) {
	f.calls.Add(1)
	return f.servers, f.stats, f.err
}

func newPayloadTestRuntime(t *testing.T, lister *fakeServersLister) *Runtime {
	t.Helper()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}
	if lister != nil {
		rt.managementService = lister
	}
	return rt
}

func receiveServersChanged(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type == EventTypeServersChanged {
				return evt
			}
		case <-deadline:
			t.Fatalf("did not receive servers.changed event within timeout")
		}
	}
}

// Spec 047 — Phase 4 (US2): SSE servers.changed payload includes server list and stats.

func TestEmitServersChanged_PayloadIncludesServers(t *testing.T) {
	servers := []*contracts.Server{
		{Name: "alpha"},
		{Name: "beta"},
	}
	stats := &contracts.ServerStats{TotalServers: 2, ConnectedServers: 1}

	rt := newPayloadTestRuntime(t, &fakeServersLister{servers: servers, stats: stats})
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	rt.emitServersChanged("test", nil)

	evt := receiveServersChanged(t, ch)
	assert.Equal(t, "test", evt.Payload["reason"])

	gotServers, ok := evt.Payload["servers"].([]contracts.Server)
	require.True(t, ok, "payload[servers] should be []contracts.Server, got %T", evt.Payload["servers"])
	require.Len(t, gotServers, 2)
	assert.Equal(t, "alpha", gotServers[0].Name)
	assert.Equal(t, "beta", gotServers[1].Name)

	gotStats, ok := evt.Payload["stats"].(*contracts.ServerStats)
	require.True(t, ok, "payload[stats] should be *contracts.ServerStats, got %T", evt.Payload["stats"])
	assert.Equal(t, 2, gotStats.TotalServers)
	assert.Equal(t, 1, gotStats.ConnectedServers)
}

func TestEmitServersChanged_FallsBackToNotifyOnlyWhenListServersFails(t *testing.T) {
	rt := newPayloadTestRuntime(t, &fakeServersLister{err: errors.New("transient I/O")})
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	rt.emitServersChanged("io-failure", nil)

	evt := receiveServersChanged(t, ch)
	assert.Equal(t, "io-failure", evt.Payload["reason"])
	_, hasServers := evt.Payload["servers"]
	_, hasStats := evt.Payload["stats"]
	assert.False(t, hasServers, "servers key should be absent when ListServers errors")
	assert.False(t, hasStats, "stats key should be absent when ListServers errors")
}

func TestEmitServersChanged_NoListerStillPublishesNotifyOnly(t *testing.T) {
	// Older deployments may not have wired the management service yet (or it's
	// nil during early startup). The emit path must still publish a usable
	// notify-only event.
	rt := newPayloadTestRuntime(t, nil)
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	rt.emitServersChanged("startup", nil)

	evt := receiveServersChanged(t, ch)
	assert.Equal(t, "startup", evt.Payload["reason"])
	_, hasServers := evt.Payload["servers"]
	assert.False(t, hasServers, "servers key should be absent without a configured lister")
}

func TestEmitServersChanged_RedactsSensitiveHeaders(t *testing.T) {
	servers := []*contracts.Server{
		{
			Name: "alpha",
			Headers: map[string]string{
				"Authorization": "Bearer secret-token-value",
				"Content-Type":  "application/json",
			},
		},
	}
	stats := &contracts.ServerStats{TotalServers: 1}

	rt := newPayloadTestRuntime(t, &fakeServersLister{servers: servers, stats: stats})
	// Default config (RevealSecretHeaders=false). Wire a minimal config service
	// snapshot so r.Config() returns non-nil.
	rt.cfg = &config.Config{}
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	rt.emitServersChanged("redact-check", nil)

	evt := receiveServersChanged(t, ch)
	gotServers, ok := evt.Payload["servers"].([]contracts.Server)
	require.True(t, ok)
	require.Len(t, gotServers, 1)

	authVal := gotServers[0].Headers["Authorization"]
	contentVal := gotServers[0].Headers["Content-Type"]
	assert.NotEqual(t, "Bearer secret-token-value", authVal, "Authorization header must be redacted")
	assert.Contains(t, authVal, "REDACTED")
	assert.Equal(t, "application/json", contentVal, "Content-Type is not sensitive; must not be redacted")
}
