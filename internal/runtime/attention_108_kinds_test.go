package runtime

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

func TestAttention108KindsEqualProfileWarningCodes(t *testing.T) {
	pairs := map[string]profile.WarningCode{
		AttentionKindAnonymousDeniedByBindingGuard: profile.WarningAnonymousDeniedByBindingGuard,
		AttentionKindClientHoldsAdminKey:           profile.WarningClientHoldsAdminKey,
		AttentionKindClientTokenNameConflict:       profile.WarningClientTokenNameConflict,
		AttentionKindProfileMissing:                profile.WarningProfileMissing,
		AttentionKindClientRotationPending:         profile.WarningClientRotationPending,
		AttentionKindClientCredentialExpiring:      profile.WarningClientCredentialExpiring,
	}
	for kind, code := range pairs {
		assert.Equal(t, kind, string(code))
	}
	assert.Len(t, AttentionKinds(), 13)
}

func TestAttention108FixVerbsEqualWarningActionKinds(t *testing.T) {
	assert.Equal(t, string(profile.FixChangeSetting), AttentionFixChangeSetting)
	assert.Equal(t, profile.WarningActionUpgradeAdminKeyHolders, AttentionFixUpgradeAdminKeyHolders)
	assert.Equal(t, string(profile.FixEditToken), AttentionFixEditToken)
	assert.Equal(t, string(profile.FixMoveClient), AttentionFixMoveClient)
	assert.Equal(t, string(profile.FixReconnectClient), AttentionFixReconnectClient)
}

func attn108ByKind(items []contracts.AttentionItem) map[string]contracts.AttentionItem {
	out := map[string]contracts.AttentionItem{}
	for _, it := range items {
		out[it.Kind] = it
	}
	return out
}

func attn108Time(t time.Time) *time.Time { return &t }

func TestAttention108ComputeTable(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	exp := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	seen := now.Add(-time.Minute)
	in := AttentionInput{
		Now: now,
		ClientWarnings: []AttentionClientWarning{
			{Code: string(profile.WarningAnonymousDeniedByBindingGuard), BindingNames: []string{"Cursor", "Codex"}, BindingCount: 2, Since: seen},
			{Code: string(profile.WarningClientHoldsAdminKey), ClientID: "cursor", DisplayName: "Cursor", Since: seen},
			{Code: string(profile.WarningClientTokenNameConflict), ClientID: "codex", DisplayName: "Codex", Since: seen},
			{Code: string(profile.WarningProfileMissing), ClientID: "codex", DisplayName: "Codex", Profile: "work-readonly", Since: seen},
			{Code: string(profile.WarningClientRotationPending), ClientID: "my client", DisplayName: "My Client", Since: seen},
			{Code: string(profile.WarningClientCredentialExpiring), ClientID: "codex", DisplayName: "Codex", ExpiresAt: &exp},
		},
	}
	items := Compute(in)
	require.Len(t, items, 6)
	got := attn108ByKind(items)

	g := got[AttentionKindAnonymousDeniedByBindingGuard]
	assert.Equal(t, 4, g.Rank)
	assert.Equal(t, "anonymous_denied_by_binding_guard:setting:require_mcp_auth", g.ID)
	assert.Equal(t, contracts.AttentionSubject{Type: "setting", ID: "require_mcp_auth", Name: "Anonymous callers"}, g.Subject)
	assert.Equal(t, "Anonymous callers are denied: client bindings could be bypassed without authentication", g.Summary)
	assert.Equal(t, "Bound: Cursor, Codex. Turn on authentication, or set anonymous callers to a profile no wider than the bindings.", g.Detail)
	assert.Equal(t, contracts.AttentionFix{Verb: "change_setting", Label: "Require authentication…", Target: "/settings?tab=security&focus=require_mcp_auth"}, g.Fix)
	assert.Equal(t, seen, g.Since)

	a := got[AttentionKindClientHoldsAdminKey]
	assert.Equal(t, 5, a.Rank)
	assert.Equal(t, "client_holds_admin_key:client:cursor", a.ID)
	assert.Equal(t, contracts.AttentionSubject{Type: "client", ID: "cursor", Name: "Cursor"}, a.Subject)
	assert.Equal(t, "Cursor: holds the admin API key", a.Summary)
	assert.Equal(t, "Upgrade it to a client credential, then rotate the admin API key.", a.Detail)
	assert.Equal(t, contracts.AttentionFix{Verb: "upgrade_admin_key_holders", Label: "Upgrade…", Target: "/clients?focus=cursor"}, a.Fix)

	c := got[AttentionKindClientTokenNameConflict]
	assert.Equal(t, 6, c.Rank)
	assert.Equal(t, "client_token_name_conflict:client:codex", c.ID)
	assert.Equal(t, "Codex: token name client-codex is held by an agent token", c.Summary)
	assert.Equal(t, "Revoke or delete that token, then connect Codex again.", c.Detail)
	assert.Equal(t, contracts.AttentionFix{Verb: "edit_token", Label: "Open token", Target: "/clients?tab=tokens&token=client-codex"}, c.Fix)

	m := got[AttentionKindProfileMissing]
	assert.Equal(t, 7, m.Rank)
	assert.Equal(t, "Codex: bound to missing profile work-readonly — denied everything", m.Summary)
	assert.Equal(t, "Move it to an existing profile.", m.Detail)
	assert.Equal(t, contracts.AttentionFix{Verb: "move_client", Label: "Move client…", Target: "/clients?focus=codex"}, m.Fix)

	r := got[AttentionKindClientRotationPending]
	assert.Equal(t, 8, r.Rank)
	assert.Equal(t, "client_rotation_pending:client:my client", r.ID)
	assert.Equal(t, "My Client: credential rotation not finished", r.Summary)
	assert.Equal(t, "Reconnect My Client or finalize the rotation.", r.Detail)
	assert.Equal(t, contracts.AttentionFix{Verb: "reconnect_client", Label: "Reconnect…", Target: "/clients?focus=my+client"}, r.Fix)

	e := got[AttentionKindClientCredentialExpiring]
	assert.Equal(t, 9, e.Rank)
	assert.Equal(t, "Codex: client credential expires 2026-10-08", e.Summary)
	assert.Equal(t, "Reconnect to issue a new credential.", e.Detail)
	assert.Equal(t, contracts.AttentionFix{Verb: "reconnect_client", Label: "Reconnect…", Target: "/clients?focus=codex"}, e.Fix)
	assert.Equal(t, exp.Add(-14*24*time.Hour), e.Since)

	// Rank order 4..9.
	for i, it := range items {
		assert.Equal(t, 4+i, it.Rank)
	}
}

func TestAttention108SortFirstBeforeServerItems(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	in := AttentionInput{
		Now: now,
		Servers: []AttentionServer{
			attnServer("github", func(s *AttentionServer) {
				s.Health.Status = health.StatusSignInRequired
				s.StateSince = now
			}),
			attnServer("review", func(s *AttentionServer) { s.Quarantined = true }),
		},
		ClientWarnings: []AttentionClientWarning{
			{Code: string(profile.WarningClientCredentialExpiring), ClientID: "codex", DisplayName: "Codex", ExpiresAt: attn108Time(now.Add(24 * time.Hour))},
			{Code: string(profile.WarningClientHoldsAdminKey), ClientID: "cursor", DisplayName: "Cursor"},
		},
	}
	items := Compute(in)
	require.GreaterOrEqual(t, len(items), 4)
	assert.Equal(t, AttentionKindClientHoldsAdminKey, items[0].Kind)
	assert.Equal(t, AttentionKindClientCredentialExpiring, items[1].Kind)
	assert.Equal(t, AttentionKindSignInRequired, items[2].Kind)
}

func TestAttention108GuardDetailTruncatesAfterThree(t *testing.T) {
	in := AttentionInput{
		Now: time.Now(),
		ClientWarnings: []AttentionClientWarning{{
			Code:         string(profile.WarningAnonymousDeniedByBindingGuard),
			BindingNames: []string{"A", "B", "C", "D", "E"},
			BindingCount: 5,
		}},
	}
	items := Compute(in)
	require.Len(t, items, 1)
	assert.Equal(t, "Bound: A, B, C (+2 more). Turn on authentication, or set anonymous callers to a profile no wider than the bindings.", items[0].Detail)
}

func TestAttention108UnknownCodeIgnored(t *testing.T) {
	items := Compute(AttentionInput{Now: time.Now(), ClientWarnings: []AttentionClientWarning{{Code: "future_code", ClientID: "x"}}})
	assert.Empty(t, items)
}

func TestAttention108NoSecretsInItems(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	items := Compute(AttentionInput{
		Now: time.Now(),
		ClientWarnings: []AttentionClientWarning{
			{Code: string(profile.WarningAnonymousDeniedByBindingGuard), BindingNames: []string{"Cursor"}, BindingCount: 1},
			{Code: string(profile.WarningClientHoldsAdminKey), ClientID: "cursor", DisplayName: "Cursor"},
			{Code: string(profile.WarningClientTokenNameConflict), ClientID: "codex", DisplayName: "Codex"},
			{Code: string(profile.WarningProfileMissing), ClientID: "codex", DisplayName: "Codex", Profile: "p"},
			{Code: string(profile.WarningClientRotationPending), ClientID: "codex", DisplayName: "Codex"},
			{Code: string(profile.WarningClientCredentialExpiring), ClientID: "codex", DisplayName: "Codex", ExpiresAt: &exp},
		},
	})
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	assert.False(t, regexp.MustCompile(`mcp_(cli|agt)_|apikey|api_key=`).Match(raw), string(raw))
}
