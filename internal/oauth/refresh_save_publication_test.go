package oauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func recordTransientFailure(t *testing.T, c *RefreshCoordinator, key string, b commitview.CredentialBinding) {
	t.Helper()
	rec := &storage.OAuthTokenRecord{ServerName: key, AccessToken: "expired", RefreshToken: "rt", ExpiresAt: time.Now().Add(-time.Hour)}
	_, _, err := c.Do(context.Background(), RefreshRequest{
		Key: key, Binding: b, ServerName: key, ObservedRefreshToken: "rt", Trigger: RefreshTriggerReactive,
		Load: func() (*storage.OAuthTokenRecord, error) { cp := *rec; return &cp, nil },
		Refresh: func(context.Context, *storage.OAuthTokenRecord) (*client.Token, error) {
			return nil, errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
		},
	})
	require.Error(t, err)
	require.True(t, c.TransientlyFailing(key, b), "precondition: a transient failure is recorded")
}

// GH #1929 (Windows CI, TestNetworkRecovery_ConnectedServerWithStalledRefreshRecovers):
// a successful refresh commits the new token before NoteTokenSavedFor clears
// the transient state, and other goroutines can use the token in between (a
// slow fsync on Windows held that window open long enough for a tool call to
// succeed while TransientlyFailing still said true). From the moment a token
// response is being stored, the credential is not reported as failing.
func TestPersistentTokenStore_SaveInProgressIsNotReportedAsTransientlyFailing(t *testing.T) {
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const name, url = "save-publication", "https://save-publication.example/mcp"
	key := GenerateServerKey(name, url)
	b := commitview.CredentialBinding{}
	c := DefaultRefreshCoordinator()
	recordTransientFailure(t, c, key, b)

	var during, statusDuring bool
	prev := oauthBeforePersistHook
	oauthBeforePersistHook = func(server string, _ commitview.CredentialBinding) {
		if server == name {
			during = c.TransientlyFailing(key, b)
			_, statusDuring = c.TransientRefreshStatus(key, b)
		}
	}
	t.Cleanup(func() { oauthBeforePersistHook = prev })

	store := NewPersistentTokenStore(name, url, db)
	require.NoError(t, store.SaveToken(context.Background(), &client.Token{
		AccessToken: "fresh", RefreshToken: "rt2", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour),
	}))
	assert.False(t, during, "while the new token is being stored, the refresh is not reported as failing")
	assert.False(t, statusDuring, "nor as Token refresh pending")
	assert.False(t, c.TransientlyFailing(key, b), "after the save the state is settled")
}

// A save that does not complete leaves the earlier transient failure in place.
func TestRefreshCoordinator_EndedSaveWithoutSuccessKeepsTheTransientFailure(t *testing.T) {
	c := NewRefreshCoordinator()
	b := commitview.CredentialBinding{}
	recordTransientFailure(t, c, "k", b)

	end := c.BeginTokenSaveFor("k", b)
	assert.False(t, c.TransientlyFailing("k", b))
	end()
	end() // idempotent
	assert.True(t, c.TransientlyFailing("k", b), "a save that failed or was discarded changes nothing")

	end = c.BeginTokenSaveFor("k", b)
	c.NoteTokenSavedFor("k", b)
	end()
	assert.False(t, c.TransientlyFailing("k", b), "a completed save settles it")
}
