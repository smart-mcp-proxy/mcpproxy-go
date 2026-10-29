package httpapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// Issue #1699: a token can carry its own call budget, set when it is created.

func TestCreateToken_RateLimit(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", createTokenRequest{
		Name:        "loop-guard",
		Permissions: []string{"read"},
		RateLimit:   &auth.TokenRateLimit{PerMinute: 60, Burst: 10},
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var created createTokenResponse
	decodeSuccess(t, w, &created)
	require.NotNil(t, created.RateLimit)
	assert.Equal(t, auth.TokenRateLimit{PerMinute: 60, Burst: 10}, *created.RateLimit)

	stored, err := store.GetAgentTokenByName("loop-guard")
	require.NoError(t, err)
	require.NotNil(t, stored.RateLimit, "the budget is stored with the token")
	assert.Equal(t, 60, stored.RateLimit.PerMinute)
	assert.Equal(t, 10, stored.RateLimit.Burst)

	// The authenticated request carries it, which is what the admission seam reads.
	ac := stored.AuthContext()
	require.NotNil(t, ac.RateLimit)
	assert.Equal(t, 60, ac.RateLimit.PerMinute)
	assert.NotSame(t, stored.RateLimit, ac.RateLimit, "the request context must not alias the stored record")

	// get and list show it.
	w = doRequest(t, srv, http.MethodGet, "/api/v1/tokens/loop-guard", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var info tokenInfoResponse
	decodeSuccess(t, w, &info)
	require.NotNil(t, info.RateLimit)
	assert.Equal(t, 60, info.RateLimit.PerMinute)
	assert.Equal(t, 10, info.RateLimit.Burst)

	w = doRequest(t, srv, http.MethodGet, "/api/v1/tokens", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var list struct {
		Tokens []tokenInfoResponse `json:"tokens"`
	}
	decodeSuccess(t, w, &list)
	require.Len(t, list.Tokens, 1)
	require.NotNil(t, list.Tokens[0].RateLimit)
}

func TestCreateToken_NoRateLimitByDefault(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", createTokenRequest{Name: "plain", Permissions: []string{"read"}})
	require.Equal(t, http.StatusCreated, w.Code)

	stored, err := store.GetAgentTokenByName("plain")
	require.NoError(t, err)
	assert.Nil(t, stored.RateLimit, "an omitted rate_limit leaves the token on the config default")
	assert.NotContains(t, w.Body.String(), "rate_limit")
}

func TestCreateToken_RateLimitZeroIsAnExplicitExemption(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", createTokenRequest{
		Name: "exempt", Permissions: []string{"read"}, RateLimit: &auth.TokenRateLimit{PerMinute: 0},
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	stored, err := store.GetAgentTokenByName("exempt")
	require.NoError(t, err)
	require.NotNil(t, stored.RateLimit, "per_minute 0 is kept: it opts out of agent_token_rate_limit")
	assert.Equal(t, 0, stored.RateLimit.PerMinute)
}

func TestCreateToken_RateLimitInvalid(t *testing.T) {
	cases := map[string]auth.TokenRateLimit{
		"negative per_minute":    {PerMinute: -1},
		"negative burst":         {PerMinute: 10, Burst: -1},
		"burst without a rate":   {PerMinute: 0, Burst: 5},
		"per_minute above bound": {PerMinute: auth.MaxRateLimitPerMinute + 1},
		"burst above bound":      {PerMinute: 10, Burst: auth.MaxRateLimitPerMinute + 1},
	}
	for name, rl := range cases {
		t.Run(name, func(t *testing.T) {
			store := newMockTokenStore()
			srv := newTestTokenServer(t, store, nil)
			rl := rl
			w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", createTokenRequest{
				Name: "bad", Permissions: []string{"read"}, RateLimit: &rl,
			})
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), "rate_limit")
			assert.Empty(t, store.tokens, "nothing is stored for a refused request")
		})
	}
}

// TestHandleCallTool_RateLimitedReturns429WithRetryAfter: the REST tool-call
// route answers a token that is over its budget with 429 and a Retry-After
// hint, exactly as it answers a concurrency shed.
func TestHandleCallTool_RateLimitedReturns429WithRetryAfter(t *testing.T) {
	t.Setenv("CI", "")
	apiKey := "test-rate-limit-api-key"

	cases := []struct {
		name       string
		retryAfter time.Duration
		wantHeader string
		wantText   string
	}{
		{"fractional wait rounds up", 1500 * time.Millisecond, "2", "Retry in 2 seconds."},
		{"sub-second wait is one second", 10 * time.Millisecond, "1", "Retry in 1 second."},
		{"a slow rate", 20 * time.Second, "20", "Retry in 20 seconds."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limitErr := &limiter.LimitError{
				Scope: limiter.ScopeToken, Reason: limiter.ReasonRateLimited,
				Limit: 3, Burst: 5, RetryAfter: tc.retryAfter,
			}
			ctrl := &shedController{apiKey: apiKey, err: fmt.Errorf("tool call failed: %w", limitErr)}
			srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

			w := postToolCall(t, srv, apiKey)

			require.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())
			assert.Equal(t, tc.wantHeader, w.Header().Get("Retry-After"))
			assert.Contains(t, w.Body.String(), "Rate limit reached")
			assert.Contains(t, w.Body.String(), "3 tool calls per minute")
			assert.Contains(t, w.Body.String(), "bursts of up to 5")
			assert.Contains(t, w.Body.String(), tc.wantText)
		})
	}
}
