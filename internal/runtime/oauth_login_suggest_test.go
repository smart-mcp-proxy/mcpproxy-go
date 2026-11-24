package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
)

// suggestFor registers a client reporting `name`, starts an authorization on the
// given address ("" = generic) and returns the described view.
func (fx *oauthFx) suggestFor(name, client string) *AuthorizationView {
	fx.t.Helper()
	resp, rerr, err := fx.svc.Register(mcplogin.RegistrationRequest{RedirectURIs: []string{oauthTestRedirect}, ClientName: name})
	require.NoError(fx.t, err)
	require.Nil(fx.t, rerr)
	id := fx.authorize(resp["client_id"].(string), client)
	v, err := fx.svc.Describe(fx.ctx(), id)
	require.NoError(fx.t, err)
	return v
}

func TestOAuthDescribe_SuggestedClient(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("cursor", "ro", auth.ProfileModeLocked)
	fx.mintClient("codex", "rw", auth.ProfileModeSwitchable)

	t.Run("no match", func(t *testing.T) {
		assert.Nil(t, fx.suggestFor("Totally Different", "").SuggestedClient)
	})
	t.Run("id match, case and whitespace", func(t *testing.T) {
		s := fx.suggestFor("  CODEX ", "").SuggestedClient
		require.NotNil(t, s)
		assert.Equal(t, "codex", s.ID)
		assert.Equal(t, "id", s.Matched)
	})
	t.Run("one client on several fields is one match, id wins", func(t *testing.T) {
		// "cursor" is the id, the registry display name "Cursor" and a known name.
		s := fx.suggestFor("cursor", "").SuggestedClient
		require.NotNil(t, s)
		assert.Equal(t, "cursor", s.ID)
		assert.Equal(t, "id", s.Matched)
	})
	t.Run("display name of a registry client", func(t *testing.T) {
		// "Cursor" registry display name differs from the id only by case; use the
		// alias path below for a distinct spelling.
		s := fx.suggestFor("Cursor", "").SuggestedClient
		require.NotNil(t, s)
		assert.Equal(t, "cursor", s.ID)
	})
	t.Run("registry alias", func(t *testing.T) {
		s := fx.suggestFor("Cursor-VSCode", "").SuggestedClient
		require.NotNil(t, s)
		assert.Equal(t, "cursor", s.ID)
		assert.Equal(t, "known_name", s.Matched)
	})
	t.Run("parenthesised suffix", func(t *testing.T) {
		s := fx.suggestFor("Codex (my-server)", "").SuggestedClient
		require.NotNil(t, s)
		assert.Equal(t, "codex", s.ID)
		assert.Equal(t, "id", s.Matched)
	})
	t.Run("name equal to one client's id and another's display name is ambiguous", func(t *testing.T) {
		fx.mintClient("zed", "ro", auth.ProfileModeLocked)
		_, _, err := fx.clients.Add(fx.ctx(), fx.actor(), AddRequest{ID: "acme", DisplayName: "Zed", Profile: "ro"})
		require.NoError(t, err)
		assert.Nil(t, fx.suggestFor("zed", "").SuggestedClient)
		// Either one alone is fine.
		s := fx.suggestFor("Acme (x)", "").SuggestedClient
		require.NotNil(t, s)
		assert.Equal(t, "acme", s.ID)
	})
	t.Run("never on a per-client address", func(t *testing.T) {
		v := fx.suggestFor("codex", "cursor")
		assert.Equal(t, "client", v.AddressKind)
		assert.Nil(t, v.SuggestedClient)
	})
	t.Run("the server never applies the suggestion", func(t *testing.T) {
		v := fx.suggestFor("codex", "")
		require.NotNil(t, v.SuggestedClient)
		pv, err := fx.svc.Preview(fx.ctx(), v.ID, ApprovalChoice{})
		require.NoError(t, err)
		assert.False(t, pv.Complete)
		assert.Nil(t, pv.Access)
		assert.Nil(t, pv.Guard)
		_, err = fx.svc.Approve(fx.ctx(), v.ID, ApprovalChoice{Profile: oauthStrp("ro")})
		assert.ErrorIs(t, err, ErrOAuthClientRequired)
		code, status, ok := OAuthErrorStatus(ErrOAuthClientRequired)
		assert.True(t, ok)
		assert.Equal(t, "client_required", code)
		assert.Equal(t, 422, status)
	})
}
