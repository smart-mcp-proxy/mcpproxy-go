package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Spec 116 PR2b (#1607, quickstart §T2): the runtime half of the OAuth
// credential binding. The commit stamps {Incarnation, OAuthEpoch} on the
// snapshot, the row and the manager's desired view in one pass, instances
// are built bound to it, and a construction for a view the row moved past is
// refused (review R6.1).

func pr2bBinding(t *testing.T, rt *Runtime, name string) commitview.ServerGen {
	t.Helper()
	sg, ok := rt.configSvc.Current().ServerGeneration(name)
	require.True(t, ok)
	return sg
}

// The OAuth epoch moves exactly with an OAuth clear: a non-OAuth edit keeps
// the binding, an OAuth config change moves the epoch (keeping the
// incarnation), a remove and re-add moves both. Snapshot, row and the
// instance's binding always agree.
func TestSpec116_PR2b_CommitStampsCredentialBinding(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	up := pr3Upstream(t, "acme", "acme_tool")
	rt, _ := newSpec116Runtime(t, nil)
	pr3Create(t, rt, pr3Server("acme", up.URL))
	created := pr2bBinding(t, rt, "acme")
	assert.Equal(t, created.Gen, created.OAuthEpoch)
	assert.Equal(t, created, storedStamp(t, rt, "acme").Stamp, "row and snapshot agree")
	c := pr3WaitConnected(t, rt, "acme", up.URL)
	assert.Equal(t, created.Binding(), c.CredentialBinding(), "the instance is bound to the committed binding")

	pr3Update(t, rt, "acme", func(cur *config.ServerConfig) { cur.DisabledTools = []string{"x"} })
	edited := pr2bBinding(t, rt, "acme")
	assert.Greater(t, edited.Gen, created.Gen)
	assert.Equal(t, created.Binding(), edited.Binding(), "a non-OAuth edit keeps the binding")

	pr3Update(t, rt, "acme", func(cur *config.ServerConfig) { cur.OAuth = &config.OAuthConfig{Scopes: []string{"read"}} })
	re := pr2bBinding(t, rt, "acme")
	assert.Equal(t, re.Gen, re.OAuthEpoch, "an OAuth config change moves the epoch to G")
	assert.Equal(t, created.Incarnation, re.Incarnation, "and keeps the incarnation")
	assert.Equal(t, re, storedStamp(t, rt, "acme").Stamp)
	require.Eventually(t, func() bool {
		b, ok := rt.upstreamManager.CurrentCredentialBinding("acme")
		return ok && b == re.Binding()
	}, 15*time.Second, 5*time.Millisecond, "the reconnected instance is bound to the new epoch")
	db, _ := rt.upstreamManager.DesiredBinding("acme")
	assert.Equal(t, re.Binding(), db)

	pr3Remove(t, rt, "acme")
	pr3Create(t, rt, pr3Server("acme", up.URL))
	readd := pr2bBinding(t, rt, "acme")
	assert.Greater(t, readd.Incarnation, re.Incarnation)
	assert.Equal(t, readd.Incarnation, readd.OAuthEpoch)
}

// T2.14 (review R6.1): a commit that changes acme's OAuth config (and,
// variant, a one-commit remove and re-add with the same URL) is parked at
// afterStorage: the row already holds the new binding, step 7 has not run, so
// the manager still holds the old generation. An old-generation worker
// released there passes the desired-generation check, and the committed-view
// check refuses it: no instance and no OAuth config are built for the old
// view. After the commit is released, the new worker connects bound to the
// new binding.
func TestSpec116_T2_14_StaleWorkerRefusedByCommittedView(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	for _, variant := range []string{"oauth_change", "replace"} {
		t.Run(variant, func(t *testing.T) {
			up := pr3Upstream(t, "acme", "acme_tool")
			rt, _ := newSpec116Runtime(t, nil)
			pr3Create(t, rt, pr3Server("acme", up.URL))
			old := pr2bBinding(t, rt, "acme")
			oldCfg := *spec116Live(rt, "acme")
			pr3WaitConnected(t, rt, "acme", up.URL)

			parked, release := make(chan struct{}), make(chan struct{})
			rt.testHooks.afterStorage = func(reason string) {
				if reason != "pr2b_parked" {
					return
				}
				close(parked)
				<-release
			}
			var checked []commitview.ServerGen
			restore := upstream.SetBeforeBindingCheckHookForTest(func(name string, sg commitview.ServerGen) {
				if name == "acme" {
					checked = append(checked, sg)
				}
			})
			defer restore()

			done := make(chan error, 1)
			go func() {
				_, err := rt.Commit("pr2b_parked", func(tx *CommitTx) error {
					if variant == "replace" {
						if err := tx.RemoveServer("acme"); err != nil {
							return err
						}
						return tx.CreateServer(pr3Server("acme", up.URL))
					}
					return tx.UpdateServer("acme", func(cur *config.ServerConfig) error {
						cur.OAuth = &config.OAuthConfig{Scopes: []string{"write"}}
						return nil
					})
				})
				done <- err
			}()
			spec116Await(t, parked, "the commit to park after its storage step")

			// The old generation's worker acts now (a stale ConnectAll /
			// supervisor connect / post-commit worker would): its generation
			// is still the manager's, but the row moved on.
			gen, present := rt.upstreamManager.DesiredGeneration("acme")
			require.True(t, present)
			require.Equal(t, old.Gen, gen, "precondition: step 7 has not run")
			_, err := rt.upstreamManager.ReplaceInstanceAt("acme", old.Gen, mustClient(t, rt, "acme"), &oldCfg)
			assert.ErrorIs(t, err, upstream.ErrStaleGeneration, "the stale construction is refused")
			require.NotEmpty(t, checked)
			assert.Equal(t, old, checked[len(checked)-1], "the check used the manager's committed view, not the row")
			c := mustClient(t, rt, "acme")
			assert.Equal(t, old.Binding(), c.CredentialBinding(), "the registered instance was not replaced by a stale construction")

			close(release)
			require.NoError(t, <-done)
			cur := pr2bBinding(t, rt, "acme")
			assert.NotEqual(t, old.Binding(), cur.Binding())
			require.Eventually(t, func() bool {
				b, ok := rt.upstreamManager.CurrentCredentialBinding("acme")
				return ok && b == cur.Binding()
			}, 15*time.Second, 5*time.Millisecond, "the commit's own worker builds the instance for the new binding")
		})
	}
}

func mustClient(t *testing.T, rt *Runtime, name string) *managed.Client {
	t.Helper()
	c, ok := rt.upstreamManager.GetClient(name)
	require.True(t, ok)
	return c
}

// T2.17 (review R8.2), the OAuth half: an old-binding token save parked
// across a one-commit remove and re-create (equal entry) stores nothing; the
// replacement row carries Incarnation == OAuthEpoch == G and no token.
func TestSpec116_T2_17_ReplaceRefusesParkedOAuthSave(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	up := pr3Upstream(t, "acme", "acme_tool")
	rt, _ := newSpec116Runtime(t, nil)
	pr3Create(t, rt, pr3Server("acme", up.URL))
	old := pr2bBinding(t, rt, "acme")
	db := rt.storageManager.GetBoltDB()
	store := oauth.NewBoundPersistentTokenStore("acme", up.URL, db, old.Binding())
	require.NoError(t, store.SaveToken(context.Background(), &client.Token{AccessToken: "old", RefreshToken: "old-rt", ExpiresAt: time.Now().Add(time.Hour)}))

	parked, release := make(chan struct{}), make(chan struct{})
	restore := oauth.SetBindingTestHooks(oauth.BindingTestHooks{BeforePersist: func(server string, b commitview.CredentialBinding) {
		if server == "acme" && b == old.Binding() {
			close(parked)
			<-release
		}
	}})
	defer restore()
	done := make(chan error, 1)
	go func() {
		done <- store.SaveToken(context.Background(), &client.Token{AccessToken: "late", RefreshToken: "late-rt", ExpiresAt: time.Now().Add(time.Hour)})
	}()
	spec116Await(t, parked, "the old binding's save to park")

	res, err := rt.Commit("pr2b_replace", func(tx *CommitTx) error {
		if err := tx.RemoveServer("acme"); err != nil {
			return err
		}
		return tx.CreateServer(pr3Server("acme", up.URL))
	})
	require.NoError(t, err)
	require.Contains(t, res.Purged, "acme", "the replacement purged the old server's state")
	row := storedStamp(t, rt, "acme").Stamp
	assert.Equal(t, res.Generation, row.Incarnation)
	assert.Equal(t, res.Generation, row.OAuthEpoch)

	close(release)
	err = <-done
	assert.True(t, errors.Is(err, oauth.ErrCredentialBindingStale), "%v", err)
	_, err = db.GetOAuthTokenBound(oauth.GenerateServerKey("acme", up.URL), commitview.CredentialBinding{})
	assert.Error(t, err, "the replacement has no token")
}
