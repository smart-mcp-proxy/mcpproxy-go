package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// rateLimitedServer answers every request with the given status and, when
// retryAfter is non-empty, a Retry-After header. It counts the requests it saw
// so a test can prove the reconnect gate actually stopped the redials.
func rateLimitedServer(t *testing.T, status int, retryAfter string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func connectToRateLimited(t *testing.T, url, protocol string, headers ...map[string]string) *managed.Client {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	cfg := &config.ServerConfig{
		Name:     "rate-limited-upstream",
		URL:      url,
		Protocol: protocol,
		Enabled:  true,
		Created:  time.Now(),
	}
	if len(headers) > 0 {
		cfg.Headers = headers[0]
	}

	client, err := managed.NewClient("retry-after-test", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err = client.Connect(ctx)
	require.Error(t, err, "a 429 upstream must not produce a successful connection")
	return client
}

// TestConnect_429WithRetryAfter_ParksReconnects is the acceptance case for
// #1040: mcp-go flattens the 429 response into an error string, so without the
// transport RoundTripper the Retry-After header is gone before any state code
// runs and the supervisor keeps redialing on its own ladder.
func TestConnect_429WithRetryAfter_ParksReconnects(t *testing.T) {
	var hits atomic.Int32
	srv := rateLimitedServer(t, http.StatusTooManyRequests, "3600", &hits)

	client := connectToRateLimited(t, srv.URL+"/mcp", "http")

	info := client.GetConnectionInfo()
	require.False(t, info.RetryAfter.IsZero(), "the Retry-After hint must reach ConnectionInfo")
	assert.WithinDuration(t, time.Now().Add(time.Hour), info.RetryAfter, 30*time.Second,
		"Retry-After: 3600 must park the server for an hour")

	// The gate the supervisor's reconcile and Manager.ConnectAll both consult.
	assert.False(t, info.ShouldAutoReconnect(time.Now()),
		"no ActionConnect may be planned inside the Retry-After window")
	assert.False(t, info.ShouldAutoReconnect(time.Now().Add(59*time.Minute)),
		"the window must still hold nearly an hour later")
	assert.True(t, info.ShouldAutoReconnect(time.Now().Add(61*time.Minute)),
		"once the window elapses the normal ladder takes over again")
}

// TestConnect_429WithHTTPDateRetryAfter covers the second RFC 7231 form.
func TestConnect_429WithHTTPDateRetryAfter(t *testing.T) {
	var hits atomic.Int32
	when := time.Now().Add(20 * time.Minute).UTC().Format(http.TimeFormat)
	srv := rateLimitedServer(t, http.StatusTooManyRequests, when, &hits)

	client := connectToRateLimited(t, srv.URL+"/mcp", "http")

	info := client.GetConnectionInfo()
	require.False(t, info.RetryAfter.IsZero(), "an HTTP-date Retry-After must parse")
	assert.WithinDuration(t, time.Now().Add(20*time.Minute), info.RetryAfter, time.Minute)
	assert.False(t, info.ShouldAutoReconnect(time.Now()))
}

// TestConnect_429MalformedRetryAfter_FallsBackToBackoff pins the fallback: a
// value we cannot parse must leave the existing exponential ladder in charge
// rather than parking the server on a guess.
func TestConnect_429MalformedRetryAfter_FallsBackToBackoff(t *testing.T) {
	var hits atomic.Int32
	srv := rateLimitedServer(t, http.StatusTooManyRequests, "when we feel like it", &hits)

	client := connectToRateLimited(t, srv.URL+"/mcp", "http")

	info := client.GetConnectionInfo()
	assert.True(t, info.RetryAfter.IsZero(), "a malformed hint must not park the server")
	assert.Equal(t, types.StateError, info.State)
	assert.GreaterOrEqual(t, info.RetryCount, 1, "the normal retry ladder must have advanced instead")
}

// TestConnect_429OversizedRetryAfter_IsCapped keeps a hostile or misconfigured
// upstream from parking a server for days.
func TestConnect_429OversizedRetryAfter_IsCapped(t *testing.T) {
	var hits atomic.Int32
	srv := rateLimitedServer(t, http.StatusTooManyRequests, "604800", &hits) // one week

	client := connectToRateLimited(t, srv.URL+"/mcp", "http")

	info := client.GetConnectionInfo()
	require.False(t, info.RetryAfter.IsZero())
	assert.WithinDuration(t, time.Now().Add(time.Hour), info.RetryAfter, time.Minute,
		"an oversized hint must clamp to the one-hour cap")
}

// TestConnect_429RetryAfter_HeadersAndSSETransports walks the other two
// client-construction branches touched by #1040 — the headers variant of the
// streamable-HTTP client and the SSE client — because each builds its
// *http.Client differently and could silently drop the RoundTripper.
func TestConnect_429RetryAfter_HeadersAndSSETransports(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		path     string
		headers  map[string]string
	}{
		{"streamable-http with headers", "http", "/mcp", map[string]string{"Authorization": "Bearer test-token"}},
		{"sse", "sse", "/sse", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := rateLimitedServer(t, http.StatusTooManyRequests, "900", &hits)

			var client *managed.Client
			if tt.headers != nil {
				client = connectToRateLimited(t, srv.URL+tt.path, tt.protocol, tt.headers)
			} else {
				client = connectToRateLimited(t, srv.URL+tt.path, tt.protocol)
			}

			info := client.GetConnectionInfo()
			require.False(t, info.RetryAfter.IsZero(), "the %s branch must also record the hint", tt.name)
			assert.WithinDuration(t, time.Now().Add(15*time.Minute), info.RetryAfter, time.Minute)
			assert.False(t, info.ShouldAutoReconnect(time.Now()))
		})
	}
}

// TestConnect_503WithoutRetryAfter_LeavesLadderAlone guards the deliberately
// narrow 503 handling: a bare 503 is an outage, which the ladder already paces.
func TestConnect_503WithoutRetryAfter_LeavesLadderAlone(t *testing.T) {
	var hits atomic.Int32
	srv := rateLimitedServer(t, http.StatusServiceUnavailable, "", &hits)

	client := connectToRateLimited(t, srv.URL+"/mcp", "http")

	info := client.GetConnectionInfo()
	assert.True(t, info.RetryAfter.IsZero(), "a bare 503 carries no hint to honour")
}
