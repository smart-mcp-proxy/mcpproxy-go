package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// GH #1172: the server-list projection needs to know whether the LIVE
// connection was established with OAuth, so a stored OAuth token is only
// treated as "in play" when it actually authenticated the connection. These
// tests pin AuthStrategy against the shared strategy loop with fake strategies
// — no network, no transport.

func newStrategyTestClient() *Client {
	return &Client{
		config: &config.ServerConfig{Name: "s", URL: "https://upstream.example/mcp"},
		logger: zap.NewNop(),
	}
}

func TestRunAuthStrategies_RecordsWinningStrategy(t *testing.T) {
	ok := func(context.Context) error { return nil }
	unauthorized := func(context.Context) error { return errors.New("401 Unauthorized") }
	noHeaders := func(context.Context) error { return errors.New("no headers configured") }

	cases := []struct {
		name       string
		strategies []authStrategy
		want       string
	}{
		{"static headers work", []authStrategy{{"headers", ok}, {"no-auth", unauthorized}, {"OAuth", unauthorized}}, "headers"},
		{"headers rejected, OAuth rescues", []authStrategy{{"headers", unauthorized}, {"no-auth", unauthorized}, {"OAuth", ok}}, AuthStrategyOAuth},
		{"no headers configured, anonymous works", []authStrategy{{"headers", noHeaders}, {"no-auth", ok}, {"OAuth", ok}}, "no-auth"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newStrategyTestClient()
			require.Equal(t, "", c.AuthStrategy(), "nothing is known before any attempt")

			require.NoError(t, c.runAuthStrategies(context.Background(), tc.strategies, ""))
			assert.Equal(t, tc.want, c.AuthStrategy())
		})
	}
}

func TestRunAuthStrategies_NoWinnerLeavesStrategyEmpty(t *testing.T) {
	unauthorized := func(context.Context) error { return errors.New("401 Unauthorized") }
	c := newStrategyTestClient()

	err := c.runAuthStrategies(context.Background(), []authStrategy{{"headers", unauthorized}, {"OAuth", unauthorized}}, "SSE ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all SSE authentication strategies failed")
	assert.Equal(t, "", c.AuthStrategy(), "a failed attempt must not claim any strategy")
}

// A non-auth failure aborts the chain without a winner — and the error is
// returned as-is (no "all strategies failed" wrapper), exactly as before.
func TestRunAuthStrategies_TransportErrorAbortsWithoutWinner(t *testing.T) {
	boom := errors.New("dial tcp: connection refused")
	c := newStrategyTestClient()

	err := c.runAuthStrategies(context.Background(), []authStrategy{
		{"headers", func(context.Context) error { return boom }},
		{"OAuth", func(context.Context) error { t.Fatal("must not reach OAuth after a transport error"); return nil }},
	}, "")
	require.ErrorIs(t, err, boom)
	assert.Equal(t, "", c.AuthStrategy())
}
