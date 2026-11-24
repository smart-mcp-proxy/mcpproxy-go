package runtime

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// brokenOAuthStore is a real store whose browser-login bindings cannot be read.
type brokenOAuthStore struct{ *storage.Manager }

func (brokenOAuthStore) ListOAuthClientBindings() (map[string]storage.OAuthClientBinding, error) {
	return nil, errors.New("disk on fire")
}

// A storage read error REFUSES the change instead of being read as "no
// bindings" (research R5): the producers of a guard state fail closed.
func TestGuardProducersFailClosedOnAnOAuthStoreReadError(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	broken := brokenOAuthStore{fx.sm}
	var shared = fx.clients.mu
	clients := NewClientsService(ClientsServiceDeps{
		Store: broken, HMACKey: func() ([]byte, error) { return oauthTestKey, nil },
		Config: func() *config.Config { return fx.cfg }, Guard: func() BindingGuard { return stubGuard{} },
		Now: fx.clock.Now, Mu: shared,
	})

	_, err := clients.SetBinding(fx.ctx(), fx.actor(), "codex", "rw", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "browser-login")
	pin, _, _, _ := fx.sm.CurrentClientBinding("codex")
	assert.Equal(t, "ro", pin, "nothing was written")

	// The views refuse too, rather than silently dropping the logins.
	_, err = clients.List()
	require.Error(t, err)
}

func TestOAuthNameMismatch(t *testing.T) {
	cases := []struct {
		observed, reported, client string
		want                       bool
	}{
		{"", "anything", "cursor", false},
		{"cursor", "other", "cursor", false},            // a registry id
		{"Cursor", "other", "cursor", false},            // the registry display name
		{"totally-other", "cursor ide", "cursor", true}, // neither
		{"cursor ide", "cursor ide", "acme", false},     // the registered name
		{"cursor ide", "something else", "acme", true},  // a custom client knows only its registered name
		{"claude-code (via mcp-remote 0.1.2)", "x", "claude-code", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, OAuthNameMismatch(c.observed, c.reported, c.client), "%+v", c)
	}
}

func TestTouch_StampsLastUseAndObservedNameOffTheRequestPath(t *testing.T) {
	fx := newOAuthFx(t)
	fx.loginNew("cursor", "ro")
	gid := fx.grants()[0].GrantID
	fx.svc.Touch(gid, "Cursor-VSCode")
	require.Eventually(t, func() bool {
		g, _ := fx.sm.GetOAuthGrant(gid)
		return g != nil && g.LastUsedAt != nil && g.ObservedClientName != ""
	}, 2*time.Second, 10*time.Millisecond)
	g, _ := fx.sm.GetOAuthGrant(gid)
	assert.Equal(t, "cursor-vscode", g.ObservedClientName, "sanitised before it is stored")
	fx.svc.Touch("", "x") // no grant: nothing happens
}
