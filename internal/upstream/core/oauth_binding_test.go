package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// Spec 116 PR2b (#1607) through core: a REAL authorization-code exchange of
// an instance bound to (acme, I1) against the local OAuth server, whose
// token save is parked after the token response arrived. The server is
// removed (and re-added with the same URL) while it is parked. Releasing it
// stores nothing: no token and no client registration exist for the name
// afterwards, and the re-added server starts unauthenticated.

type bindingCoreFixture struct {
	*staleDCRFixture
	m *storage.Manager
	g int64
}

func newBindingCoreFixture(t *testing.T, name string) *bindingCoreFixture {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "")
	t.Setenv("HEADLESS", "1")
	srv := oauthserver.Start(t, oauthserver.Options{})
	t.Cleanup(func() { _ = srv.Shutdown() })
	m, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	stopCallbackServerAfterTest(t, name)
	return &bindingCoreFixture{
		staleDCRFixture: &staleDCRFixture{srv: srv, db: m.GetBoltDB(), name: name, key: oauth.GenerateServerKey(name, srv.MCPURL)},
		m:               m,
	}
}

func (f *bindingCoreFixture) create(t *testing.T) commitview.CredentialBinding {
	t.Helper()
	f.g++
	sg := commitview.ServerGen{Gen: f.g, Incarnation: f.g, OAuthEpoch: f.g}
	require.NoError(t, f.m.ApplyServerCommit(&storage.ServerCommitBatch{Generation: f.g, Saves: []storage.ServerSave{{
		Config: &config.ServerConfig{Name: f.name, URL: f.srv.MCPURL, Protocol: "http", Enabled: true}, Stamp: sg,
	}}}))
	return sg.Binding()
}

func (f *bindingCoreFixture) remove(t *testing.T) {
	t.Helper()
	f.g++
	require.NoError(t, f.m.ApplyServerCommit(&storage.ServerCommitBatch{Generation: f.g, Removes: []string{f.name}}))
}

func (f *bindingCoreFixture) record(t *testing.T) *storage.OAuthTokenRecord {
	t.Helper()
	rec, err := f.db.GetOAuthTokenBound(f.key, commitview.CredentialBinding{})
	if err != nil {
		return nil
	}
	return rec
}

func TestCoreLoginCompletion_ParkedAcrossRemoval_T2_9(t *testing.T) {
	for _, readd := range []bool{false, true} {
		name := "acme-remove"
		if readd {
			name = "acme-readd"
		}
		t.Run(name, func(t *testing.T) {
			f := newBindingCoreFixture(t, name)
			old := f.create(t)
			c := f.client(t, nil)
			c.SetCredentialBinding(old)
			store := oauth.NewBoundPersistentTokenStore(f.name, f.srv.MCPURL, f.db, old)
			h, code, state, verifier := loginPairHandler(t, f.staleDCRFixture, store)

			parked, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			restore := oauth.SetBindingTestHooks(oauth.BindingTestHooks{BeforePersist: func(server string, b commitview.CredentialBinding) {
				if server != f.name {
					return
				}
				once.Do(func() {
					close(parked)
					<-release
				})
			}})
			defer restore()

			done := make(chan error, 1)
			go func() {
				_, err := c.exchangeAuthorizationCode(context.Background(), h, nil, code, state, verifier)
				done <- err
			}()
			select {
			case <-parked:
			case <-time.After(15 * time.Second):
				t.Fatal("the code exchange never reached its token save")
			}

			f.remove(t)
			var cur commitview.CredentialBinding
			if readd {
				cur = f.create(t)
			}
			close(release)
			err := <-done
			require.Error(t, err, "the exchange of a removed instance reports that its token was discarded")
			assert.True(t, errors.Is(err, oauth.ErrCredentialBindingStale), "%v", err)
			assert.Nil(t, f.record(t), "no token or client registration is written for the name after the removal")

			// The login's completion would also persist its client
			// registration; bound to the old binding it writes nothing.
			c.persistCompletedDCRCredentials(h.GetClientID(), h.GetClientSecret())
			assert.Nil(t, f.record(t), "the completion's DCR save is refused too")

			if readd {
				has, _, _ := oauth.HasPersistedTokenBound(f.name, f.srv.MCPURL, f.db, cur)
				assert.False(t, has, "the re-added server starts unauthenticated")
			}
		})
	}
}

// The live counterpart keeps working: the same exchange bound to the
// current binding stores the token and its client together (WithLoginClient,
// the #1559 pairing), and the completion's DCR save is accepted.
func TestCoreLoginCompletion_CurrentBindingStores(t *testing.T) {
	f := newBindingCoreFixture(t, "acme-live")
	cur := f.create(t)
	c := f.client(t, nil)
	c.SetCredentialBinding(cur)
	store := oauth.NewBoundPersistentTokenStore(f.name, f.srv.MCPURL, f.db, cur)
	h, code, state, verifier := loginPairHandler(t, f.staleDCRFixture, store)
	stored, err := c.exchangeAuthorizationCode(context.Background(), h, nil, code, state, verifier)
	require.NoError(t, err)
	assert.True(t, stored)
	rec := f.record(t)
	require.NotNil(t, rec)
	assert.NotEmpty(t, rec.AccessToken)
	assert.Equal(t, h.GetClientID(), rec.ClientID, "token and client are stored together")
	assert.Equal(t, cur.OAuthEpoch, rec.OAuthEpoch, "the record carries the instance's binding")
}

// The failure of a login whose result storage refused (the server was
// removed, replaced or reconfigured meanwhile) is not recorded on the server
// status by name: it would mark the replacement as failed (live check: the
// re-added server flipped from "pending auth" to an error state).
func TestReportOAuthFailure_SupersededInstanceNotRecorded(t *testing.T) {
	f := newBindingCoreFixture(t, "acme-report")
	c := f.client(t, nil)
	var recorded []error
	tm := oauth.GetTokenStoreManager()
	tm.SetOAuthFailureCallback(func(_ string, err error) { recorded = append(recorded, err) })
	defer tm.SetOAuthFailureCallback(nil)

	c.reportOAuthFailure(fmt.Errorf("token exchange failed: %w", oauth.ErrCredentialBindingStale))
	assert.Empty(t, recorded, "a binding-stale failure is not recorded")

	c.reportOAuthFailure(errors.New("access_denied"))
	require.Len(t, recorded, 1, "an ordinary failure of a live instance still is")

	c.Retire()
	c.reportOAuthFailure(errors.New("access_denied"))
	assert.Len(t, recorded, 1, "nor is any failure of a retired instance")
}
