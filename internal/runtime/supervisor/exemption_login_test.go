package supervisor

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
)

// GH #1722 cause 1: a quarantined server's inspection exemption expired while
// the operator was still completing its OAuth login, and the expiry-driven
// disconnect retired the in-flight flow. While a login is in flight for the
// server the exemption is held (extended by a grace) instead of expiring.

func newExemptionTestSupervisor(t *testing.T) *Supervisor {
	t.Helper()
	cfg := &config.Config{Listen: "127.0.0.1:8080", Servers: []*config.ServerConfig{}}
	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	t.Cleanup(configSvc.Close)
	mockUpstream := NewMockUpstreamAdapter()
	t.Cleanup(mockUpstream.Close)
	return New(configSvc, mockUpstream, zap.NewNop())
}

func (s *Supervisor) exemptionExpiry(name string) (time.Time, bool) {
	s.inspectionExemptionsMu.Lock()
	defer s.inspectionExemptionsMu.Unlock()
	exp, ok := s.inspectionExemptions[name]
	return exp, ok
}

func TestInspectionExemption_HeldWhileLoginInFlight(t *testing.T) {
	s := newExemptionTestSupervisor(t)
	var loginActive atomic.Bool
	s.SetInspectionExemptionHold(func(name string) bool {
		return name == "srv" && loginActive.Load()
	})
	s.exemptionHoldGrace = 40 * time.Millisecond

	require.NoError(t, s.RequestInspectionExemption("srv", 20*time.Millisecond))
	loginActive.Store(true)
	time.Sleep(50 * time.Millisecond)

	assert.True(t, s.IsInspectionExempted("srv"),
		"an expired exemption must be held while the server's OAuth login is in flight")
	exp, ok := s.exemptionExpiry("srv")
	require.True(t, ok, "the held exemption must stay recorded")
	assert.True(t, exp.After(time.Now()), "the hold must extend the expiry into the future")

	// Once the login is over the exemption runs out after the grace.
	loginActive.Store(false)
	assert.Eventually(t, func() bool { return !s.IsInspectionExempted("srv") }, 2*time.Second, 5*time.Millisecond,
		"without a login in flight the exemption must expire again")
}

func TestInspectionExemption_CleanupLoopHoldsLoginInFlight(t *testing.T) {
	s := newExemptionTestSupervisor(t)
	var loginActive atomic.Bool
	s.SetInspectionExemptionHold(func(string) bool { return loginActive.Load() })
	s.exemptionHoldGrace = time.Minute

	s.inspectionExemptionsMu.Lock()
	s.inspectionExemptions["srv"] = time.Now().Add(-time.Second) // already past
	s.inspectionExemptionsMu.Unlock()

	loginActive.Store(true)
	assert.Empty(t, s.collectExpiredExemptions(time.Now()),
		"the cleanup loop must not disconnect a server whose login is in flight")
	exp, ok := s.exemptionExpiry("srv")
	require.True(t, ok)
	assert.True(t, exp.After(time.Now().Add(30*time.Second)), "held exemption extended by the grace")

	loginActive.Store(false)
	s.inspectionExemptionsMu.Lock()
	s.inspectionExemptions["srv"] = time.Now().Add(-time.Second)
	s.inspectionExemptionsMu.Unlock()
	assert.Equal(t, []string{"srv"}, s.collectExpiredExemptions(time.Now()),
		"with no login in flight the expired exemption is collected as before")
	_, ok = s.exemptionExpiry("srv")
	assert.False(t, ok, "a collected exemption is removed")
}

func TestInspectionExemption_NoHoldWithoutPredicate(t *testing.T) {
	s := newExemptionTestSupervisor(t)
	require.NoError(t, s.RequestInspectionExemption("srv", 20*time.Millisecond))
	time.Sleep(40 * time.Millisecond)
	assert.False(t, s.IsInspectionExempted("srv"), "no predicate: the historical expiry applies")
}

// A later, shorter grant (the scan's 15 minutes on top of the add's hour) must
// not cut an exemption short: the add's review window is what the operator
// relies on while logging in.
func TestRequestInspectionExemption_NeverShortens(t *testing.T) {
	s := newExemptionTestSupervisor(t)
	require.NoError(t, s.RequestInspectionExemption("srv", time.Hour))
	long, ok := s.exemptionExpiry("srv")
	require.True(t, ok)

	require.NoError(t, s.RequestInspectionExemption("srv", 15*time.Minute))
	after, ok := s.exemptionExpiry("srv")
	require.True(t, ok)
	assert.Equal(t, long, after, "a shorter re-grant must keep the longer expiry")

	require.NoError(t, s.RequestInspectionExemption("srv", 2*time.Hour))
	longer, ok := s.exemptionExpiry("srv")
	require.True(t, ok)
	assert.True(t, longer.After(long), "a longer re-grant extends the exemption")
}
