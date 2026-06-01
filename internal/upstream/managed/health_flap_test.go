package managed

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

func newTestClientForHealth(t *testing.T) *Client {
	t.Helper()
	mc := &Client{
		logger: zap.NewNop(),
	}
	mc.SetConfig(&config.ServerConfig{Name: "flap-server"})
	mc.StateManager = types.NewStateManager()
	mc.StateManager.TransitionTo(types.StateConnecting)
	mc.StateManager.TransitionTo(types.StateReady)
	return mc
}

// TestHealthCheck_TransientTimeoutToleratedBelowThreshold verifies that a
// single transient timeout (the slow-upstream case that caused
// MCPX_UNKNOWN_UNCLASSIFIED to surface in the UI on every disable-tool toggle)
// does NOT flip the server to Error state. The state must remain Ready until
// healthCheckFailureThreshold consecutive transient failures have accumulated.
func TestHealthCheck_TransientTimeoutToleratedBelowThreshold(t *testing.T) {
	mc := newTestClientForHealth(t)
	timeoutErr := errors.New(`failed to list tools: transport error: failed to send request: failed to send request: Post "https://hf.co/mcp": context deadline exceeded`)

	// First (threshold-1) failures must be tolerated — counter increments
	// but state stays Ready.
	for i := 1; i < healthCheckFailureThreshold; i++ {
		shouldError := mc.recordHealthCheckFailure(timeoutErr)
		assert.False(t, shouldError, "transient failure #%d should be tolerated", i)
		assert.Equal(t, types.StateReady, mc.StateManager.GetState(),
			"state should stay Ready after transient failure #%d", i)
	}

	// The Nth consecutive failure tips us over.
	shouldError := mc.recordHealthCheckFailure(timeoutErr)
	assert.True(t, shouldError, "Nth transient failure should trigger Error transition")
}

// TestHealthCheck_HardErrorTriggersImmediateError verifies that hard
// connection failures (connection refused, host unreachable) bypass the
// flap-resistance threshold — the server is genuinely down and the user
// should see that immediately, not wait 90 seconds.
func TestHealthCheck_HardErrorTriggersImmediateError(t *testing.T) {
	mc := newTestClientForHealth(t)
	hardErr := errors.New("dial tcp 127.0.0.1:65535: connect: connection refused")

	shouldError := mc.recordHealthCheckFailure(hardErr)
	assert.True(t, shouldError, "hard error must trigger Error on first occurrence")
}

// TestHealthCheck_SuccessResetsCounter verifies that after a transient
// failure, a successful health check resets the consecutive-failure counter
// — so two failures spaced by a recovery don't add up to the threshold.
func TestHealthCheck_SuccessResetsCounter(t *testing.T) {
	mc := newTestClientForHealth(t)
	timeoutErr := errors.New("transport error: context deadline exceeded")

	// Accumulate threshold-1 failures.
	for i := 1; i < healthCheckFailureThreshold; i++ {
		mc.recordHealthCheckFailure(timeoutErr)
	}
	require.Equal(t, healthCheckFailureThreshold-1, mc.consecutiveHealthFailures)

	// One success wipes the slate.
	mc.recordHealthCheckSuccess()
	assert.Equal(t, 0, mc.consecutiveHealthFailures)

	// Now the next transient failure must be back to "tolerated".
	shouldError := mc.recordHealthCheckFailure(timeoutErr)
	assert.False(t, shouldError, "first failure after recovery must be tolerated again")
}

// TestIsTransientHealthCheckError covers the categorisation that gates the
// flap-resistance behavior.
func TestIsTransientHealthCheckError(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		transient bool
	}{
		{"context deadline exceeded", errors.New("Post: context deadline exceeded"), true},
		{"explicit timeout word", errors.New("net/http: request timeout"), true},
		{"context canceled", errors.New("operation: context canceled"), true},
		{"connection refused", errors.New("dial: connection refused"), false},
		{"no such host", errors.New("dial tcp: lookup nope.invalid: no such host"), false},
		{"network unreachable", errors.New("network is unreachable"), false},
		{"connection reset", errors.New("read: connection reset by peer"), false},
		{"nil error", nil, false},
	}
	for _, tc := range cases {
		got := isTransientHealthCheckError(tc.err)
		if got != tc.transient {
			t.Errorf("%s: isTransientHealthCheckError = %v, want %v", tc.name, got, tc.transient)
		}
	}
}

// TestHealthCheck_ResetOnConnect verifies that a fresh Connect() clears the
// consecutive-failure counter even if a previous session accumulated some.
// Important so reconnect cycles don't carry stale failure debt.
func TestHealthCheck_ResetOnConnect(t *testing.T) {
	mc := newTestClientForHealth(t)
	timeoutErr := errors.New("context deadline exceeded")

	mc.recordHealthCheckFailure(timeoutErr)
	mc.recordHealthCheckFailure(timeoutErr)
	require.Equal(t, 2, mc.consecutiveHealthFailures)

	mc.resetHealthCheckFailures()
	assert.Equal(t, 0, mc.consecutiveHealthFailures)

	// Use ctx to silence unused import warnings on stripped builds.
	_ = context.Background()
}
