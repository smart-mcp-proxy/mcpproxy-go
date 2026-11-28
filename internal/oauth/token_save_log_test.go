package oauth

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
)

// GH #1827: whether a refresh flight's new token pair was stored is the
// question the retained logs could not answer. The token store logged it
// through zap.L(), the no-op logger in this binary, so the outcome of every
// save on the mcp-go refresh path was written nowhere. It now writes one line
// per save to the server's OAuth logger, with the flight that produced it.
func TestPersistentTokenStore_LogsSaveOutcomeWithFlight(t *testing.T) {
	db := newTestBolt(t)
	name, u := uniqueServerName(t), "https://save-log.example.com/mcp"
	seedToken(t, db, name, u, "rt-0", -time.Minute)
	key := GenerateServerKey(name, u)
	obs, logs := observer.New(zapcore.DebugLevel)
	store := NewBoundPersistentTokenStoreWithLogger(name, u, db, commitview.CredentialBinding{}, zap.New(obs))

	rec, err := db.GetOAuthToken(key)
	require.NoError(t, err)
	flightCtx := withRefreshFlightInfo(withFlightGeneration(context.Background(), generationOf(rec)),
		RefreshFlightInfo{Flight: 7, Trigger: RefreshTriggerReactive})
	require.NoError(t, store.SaveToken(flightCtx, &client.Token{AccessToken: "at-1", RefreshToken: "rt-1", ExpiresAt: time.Now().Add(time.Hour)}))

	// The same flight's result again: the grant it started from is gone.
	require.NoError(t, store.SaveToken(flightCtx, &client.Token{AccessToken: "at-2", RefreshToken: "rt-2", ExpiresAt: time.Now().Add(time.Hour)}))

	// A sign-in's code exchange is not part of a flight.
	require.NoError(t, store.SaveToken(context.Background(), &client.Token{AccessToken: "at-3", RefreshToken: "rt-3", ExpiresAt: time.Now().Add(time.Hour)}))

	// A response that arrives after the flight's deadline is not stored.
	expired, cancel := context.WithCancel(flightCtx)
	cancel()
	require.Error(t, store.SaveToken(expired, &client.Token{AccessToken: "at-4", RefreshToken: "rt-4", ExpiresAt: time.Now().Add(time.Hour)}))

	entries := logs.FilterMessage("OAuth token save finished").All()
	require.Len(t, entries, 4)
	first := entries[0].ContextMap()
	assert.Equal(t, "saved", first["result"])
	assert.Equal(t, uint64(7), first["flight"])
	assert.Equal(t, "reactive", first["trigger"])
	assert.Equal(t, true, first["refresh_token_changed"])

	second := entries[1].ContextMap()
	assert.Equal(t, "superseded", second["result"])
	assert.Equal(t, uint64(7), second["flight"])

	third := entries[2].ContextMap()
	assert.Equal(t, "saved", third["result"])
	assert.Equal(t, uint64(0), third["flight"])
	assert.Equal(t, "none", third["trigger"])

	fourth := entries[3].ContextMap()
	assert.Equal(t, "cancelled", fourth["result"])
	assert.Equal(t, uint64(7), fourth["flight"])

	for _, e := range logs.All() {
		for k, v := range e.ContextMap() {
			s := toString(v)
			assert.NotContains(t, s, "rt-", "field %s carries no refresh token", k)
			assert.NotContains(t, s, "at-", "field %s carries no access token", k)
		}
	}
}
