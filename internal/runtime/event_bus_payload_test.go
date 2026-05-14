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
	assert.NotEqual(t, "Bearer secret-token-value", authVal, "Authorization header plaintext must not survive")
	assert.NotContains(t, authVal, "Bearer", "Bearer keyword leaks the secret prefix")
	assert.NotContains(t, authVal, "secret-token-value", "the secret token must not appear")
	assert.Contains(t, authVal, "••••", "value should use the masked-display format on the wire")
	assert.Contains(t, authVal, "chars)", "the masked-display format includes the length suffix")
	assert.Equal(t, "application/json", contentVal, "Content-Type is not sensitive; must not be redacted")
}

// ctxAwareLister is a serversLister that blocks ListServers until the
// caller-supplied ctx fires Done. Used to verify the parent ctx is threaded
// through buildServersChangedPayload — without that threading the call sits
// on a detached 2-second timer regardless of app-shutdown cancellation.
type ctxAwareLister struct {
	servers []*contracts.Server
	stats   *contracts.ServerStats
}

func (l *ctxAwareLister) ListServers(ctx context.Context) ([]*contracts.Server, *contracts.ServerStats, error) {
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-time.After(5 * time.Second):
		// Safety net: tests should never reach this.
		return l.servers, l.stats, nil
	}
}

// TestEmitServersChanged_PayloadPreservesSecurityScan is the SSE-parity
// regression test for PR #463: the servers.changed embed must carry every
// field the REST /api/v1/servers response carries. The Web UI's mergeServers
// treats incoming server data as authoritative and deletes absent keys
// (it's how a count dropping to zero clears its badge), so any field that
// only one path populates is silently wiped from the store on every SSE
// delivery. SecurityScan is plumbed through management.ListServers so REST
// and SSE share one enrichment site — this asserts the SSE path carries it
// through to subscribers unchanged.
func TestEmitServersChanged_PayloadPreservesSecurityScan(t *testing.T) {
	lastScan := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	servers := []*contracts.Server{
		{
			Name: "alpha",
			SecurityScan: &contracts.SecurityScanSummary{
				LastScanAt: &lastScan,
				RiskScore:  42,
				Status:     "warnings",
				FindingCounts: &contracts.FindingCounts{
					Dangerous: 1,
					Warning:   3,
					Info:      7,
					Total:     11,
				},
			},
		},
		{Name: "beta"}, // No scan summary — must not gain a stray one.
	}
	stats := &contracts.ServerStats{TotalServers: 2}

	rt := newPayloadTestRuntime(t, &fakeServersLister{servers: servers, stats: stats})
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	rt.emitServersChanged("scan-parity", nil)

	evt := receiveServersChanged(t, ch)
	gotServers, ok := evt.Payload["servers"].([]contracts.Server)
	require.True(t, ok)
	require.Len(t, gotServers, 2)

	require.NotNil(t, gotServers[0].SecurityScan, "alpha must keep its SecurityScan on the SSE path")
	assert.Equal(t, "warnings", gotServers[0].SecurityScan.Status)
	assert.Equal(t, 42, gotServers[0].SecurityScan.RiskScore)
	require.NotNil(t, gotServers[0].SecurityScan.FindingCounts)
	assert.Equal(t, 1, gotServers[0].SecurityScan.FindingCounts.Dangerous)
	assert.Equal(t, 11, gotServers[0].SecurityScan.FindingCounts.Total)

	assert.Nil(t, gotServers[1].SecurityScan, "beta must not gain a stray scan summary")
}

// TestBuildServersChangedPayload_HonoursCancelledParentCtx is the regression
// test for the shutdown-drain path: if the parent ctx is already cancelled
// (because Runtime.Close() fired appCancel) the ListServers call must abort
// promptly rather than wait up to 2 seconds on the (otherwise detached)
// WithTimeout.
func TestBuildServersChangedPayload_HonoursCancelledParentCtx(t *testing.T) {
	rt := newPayloadTestRuntime(t, nil)
	rt.managementService = &ctxAwareLister{
		servers: []*contracts.Server{{Name: "s1"}},
		stats:   &contracts.ServerStats{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	evt := rt.buildServersChangedPayload(ctx, "shutdown", nil)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 200*time.Millisecond,
		"buildServersChangedPayload must abort immediately when parent ctx is already cancelled (took %s — Background-rooted timeout would have been ~2s)",
		elapsed)

	// On ctx.Err() the build still publishes a notify-only event — neither
	// `servers` nor `stats` is set.
	assert.Equal(t, "shutdown", evt.Payload["reason"])
	_, hasServers := evt.Payload["servers"]
	assert.False(t, hasServers, "no servers key when ListServers errors")
}
