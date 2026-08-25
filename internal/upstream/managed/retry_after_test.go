package managed

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// coreClientThatWasRateLimited returns a core client whose transport has just
// observed a 429 with the given Retry-After — the state the recorder is in when
// any later failure path asks it for a hint (#1040).
func coreClientThatWasRateLimited(t *testing.T, retryAfter string) *core.Client {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", retryAfter)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
	}))
	t.Cleanup(srv.Close)

	cc, err := core.NewClient("retry-after-health-test", &config.ServerConfig{
		Name:     "rate-limited-upstream",
		URL:      srv.URL + "/mcp",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now(),
	}, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.Error(t, cc.Connect(ctx), "a 429 upstream must not connect")
	require.False(t, cc.RetryAfterDeadline().IsZero(), "precondition: the transport recorded the hint")
	return cc
}

// TestPerformHealthCheck_SyncsRetryAfter pins reachability, which is the whole
// point of this path: a 429 is NOT a connection error (isConnectionError matches
// transport failures, not HTTP statuses), so a sync gated behind that
// classification would never run for the case it exists for. The window must
// reach the state machine even though the health verdict itself is unchanged.
func TestPerformHealthCheck_SyncsRetryAfter(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.coreClient = coreClientThatWasRateLimited(t, "1800")
	mc.healthProbe = &fakeProber{
		pingErr: errors.New("request failed with status 429: rate limit exceeded"),
	}

	mc.performHealthCheck()

	info := mc.StateManager.GetConnectionInfo()
	require.False(t, info.RetryAfter.IsZero(),
		"a rate-limited ping must hand its window to the state machine")
	assert.WithinDuration(t, time.Now().Add(30*time.Minute), info.RetryAfter, time.Minute)
	assert.False(t, info.ShouldAutoReconnect(time.Now()),
		"no automatic reconnect may happen inside the window")

	// The health verdict is unchanged: a 429 does not mean the transport died.
	assert.Equal(t, types.StateReady, mc.StateManager.GetState(),
		"recording a rate-limit window must not itself mark the server unhealthy")
}

// TestCallTool_SyncsRetryAfter covers the other request surface that can be
// rate-limited without any connect attempt being involved.
func TestCallTool_SyncsRetryAfter(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.coreClient = coreClientThatWasRateLimited(t, "900")
	mc.toolInvoker = &fakeToolCaller{
		err: errors.New("request failed with status 429: rate limit exceeded"),
	}

	_, err := mc.CallTool(context.Background(), "some_tool", map[string]interface{}{})
	require.Error(t, err)

	info := mc.StateManager.GetConnectionInfo()
	require.False(t, info.RetryAfter.IsZero(),
		"a rate-limited tools/call must hand its window to the state machine")
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), info.RetryAfter, time.Minute)
}
