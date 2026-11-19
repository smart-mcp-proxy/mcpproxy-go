package oauth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// GH #1929: a refresh that failed while the laptop had no network leaves a
// transient cooldown (10s doubling to 5 min) during which a reactive caller is
// answered with the previous error and no request is sent. When the network
// demonstrably came back, the connectivity watcher lifts that cooldown so the
// reconnect's refresh goes out at once. It never lifts a terminal latch.
func TestRefreshCoordinator_ClearTransientCooldown(t *testing.T) {
	c := NewRefreshCoordinator()
	now := time.Date(2026, 11, 18, 7, 28, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	b := commitview.CredentialBinding{}
	rec := &storage.OAuthTokenRecord{ServerName: "k", AccessToken: "expired", RefreshToken: "rt", ExpiresAt: now.Add(-time.Hour)}
	load := func() (*storage.OAuthTokenRecord, error) { cp := *rec; return &cp, nil }

	var calls atomic.Int32
	offline := func(context.Context, *storage.OAuthTokenRecord) (*client.Token, error) {
		calls.Add(1)
		return nil, errors.New("dial tcp: lookup auth.example: no such host")
	}
	req := RefreshRequest{Key: "k", Binding: b, ServerName: "s", ObservedRefreshToken: "rt", Trigger: RefreshTriggerReactive, Load: load, Refresh: offline}

	// A night of failed refreshes builds the cooldown up.
	for i := 0; i < 8; i++ {
		_, _, _ = c.Do(context.Background(), req)
		now = now.Add(6 * time.Minute)
	}
	n := calls.Load()
	now = now.Add(-6 * time.Minute) // still inside the last cooldown
	_, _, err := c.Do(context.Background(), req)
	require.ErrorIs(t, err, ErrTokenRefreshTransient)
	require.Equal(t, n, calls.Load(), "inside the cooldown no request is sent")

	assert.True(t, c.ClearTransientCooldown("k", b))
	online := func(context.Context, *storage.OAuthTokenRecord) (*client.Token, error) {
		calls.Add(1)
		rec.AccessToken = "fresh" // the real refresher persists the new token
		return &client.Token{AccessToken: "fresh", RefreshToken: "rt"}, nil
	}
	req.Refresh = online
	tok, _, err := c.Do(context.Background(), req)
	require.NoError(t, err, "with the cooldown lifted the refresh goes out")
	assert.Equal(t, "fresh", tok.AccessToken)
	assert.Equal(t, n+1, calls.Load())

	assert.False(t, c.ClearTransientCooldown("k", b), "nothing left to clear")
	assert.False(t, c.ClearTransientCooldown("other", b), "an unknown key has no cooldown")
}

func TestRefreshCoordinator_ClearTransientCooldownKeepsTheTerminalLatch(t *testing.T) {
	c := NewRefreshCoordinator()
	b := commitview.CredentialBinding{}
	rec := &storage.OAuthTokenRecord{ServerName: "k", AccessToken: "expired", RefreshToken: "rt"}
	load := func() (*storage.OAuthTokenRecord, error) { cp := *rec; return &cp, nil }
	var calls atomic.Int32
	req := RefreshRequest{Key: "k", Binding: b, ServerName: "s", ObservedRefreshToken: "rt", Trigger: RefreshTriggerReactive, Load: load,
		Refresh: func(context.Context, *storage.OAuthTokenRecord) (*client.Token, error) {
			calls.Add(1)
			return nil, &RefreshFailure{Class: RefreshClassInvalidGrant, Message: "revoked", Err: ErrRefreshFailed}
		}}

	_, _, err := c.Do(context.Background(), req)
	require.Error(t, err)
	c.ClearTransientCooldown("k", b)
	_, _, err = c.Do(context.Background(), req)
	require.Error(t, err, "a revoked grant stays refused")
	assert.Equal(t, int32(1), calls.Load(), "the latch still answers without a request: a rejected credential is not retried")
}
