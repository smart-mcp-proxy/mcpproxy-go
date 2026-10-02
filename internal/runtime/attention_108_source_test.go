package runtime

import (
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// activeBindingGuard reports one bypassable binding so the guard warning fires.
type activeBindingGuard struct{ stubGuard }

func (activeBindingGuard) BindingGuardActiveBindings() []BindingRef {
	return []BindingRef{{ClientID: "cursor", TokenName: "client-cursor", Profile: "ro", Mode: "locked"}}
}

func (activeBindingGuard) BindingGuardFixes(GuardState, []BindingRef) []GuardFix { return nil }

func attn108Pairs(items []AttentionClientWarning) []string {
	out := make([]string, 0, len(items))
	for _, w := range items {
		out = append(out, w.Code+"|"+w.ClientID)
	}
	sort.Strings(out)
	return out
}

// TestAttention108SourceEqualsClientsWarnings pins P2/R2: the attention list's
// Spec 108 items are exactly the warnings GET /clients serves.
func TestAttention108SourceEqualsClientsWarnings(t *testing.T) {
	rt := newFunnelRuntime(t)
	rt.SetBindingGuard(activeBindingGuard{})
	sm := rt.StorageManager()
	cfgReads := atomic.Int32{}
	rt.clientsService.SetConfigReader(readerFunc(func(string) (string, bool, error) {
		cfgReads.Add(1)
		return "", false, nil
	}))

	// A locked client bound to a profile that is not in the config (profile_missing)
	// and expiring within the window.
	_, err := sm.MintClientCredential("codex", "mcp_cli_attn108_missing_000000000", funnelKey, auth.ProfileModeLocked, "gone", time.Now().Add(48*time.Hour))
	require.NoError(t, err)
	// A client with a staged rotation (client_rotation_pending), not expiring.
	_, err = sm.MintClientCredential("cursor", "mcp_cli_attn108_rotating_00000000", funnelKey, auth.ProfileModeLocked, "ro", time.Now().Add(200*24*time.Hour))
	require.NoError(t, err)
	_, err = sm.StageClientCredentialRotation("cursor", "mcp_cli_attn108_staged_0000000000", funnelKey)
	require.NoError(t, err)
	// Observed admin-key state for a registry client with no credential record.
	require.NoError(t, sm.UpdateOnboardingState(func(st *storage.OnboardingState) error {
		st.ClientCredentialObserved = map[string]storage.ClientCredentialObservation{
			"claude-code": {State: string(profile.CredentialStateAdminKey), At: time.Now()},
		}
		return nil
	}))

	got := rt.AttentionClientWarnings()
	require.NotEmpty(t, got)

	state, err := rt.GetOnboardingState()
	require.NoError(t, err)
	states, err := rt.clientsService.ObservedCredentialStates(state.ClientCredentialObserved)
	require.NoError(t, err)
	var want []string
	for _, w := range rt.clientsService.Warnings(states) {
		want = append(want, string(w.Code)+"|"+w.ClientID)
	}
	sort.Strings(want)
	assert.Equal(t, want, attn108Pairs(got))
	assert.Contains(t, want, "client_holds_admin_key|claude-code")
	assert.Contains(t, want, "profile_missing|codex")
	assert.Contains(t, want, "client_credential_expiring|codex")
	assert.Contains(t, want, "client_rotation_pending|cursor")
	assert.Contains(t, want, "anonymous_denied_by_binding_guard|")

	// Through Compute: the same set with the guard's subject id swapped in.
	items := Compute(AttentionInput{Now: time.Now(), ClientWarnings: got})
	assert.Len(t, items, len(want))

	// Display names come from the connect registry; the profile slug and expiry
	// from the credential record.
	byCode := map[string]AttentionClientWarning{}
	for _, w := range got {
		byCode[w.Code+"|"+w.ClientID] = w
	}
	assert.Equal(t, "gone", byCode["profile_missing|codex"].Profile)
	require.NotNil(t, byCode["client_credential_expiring|codex"].ExpiresAt)
	assert.NotEmpty(t, byCode["client_holds_admin_key|claude-code"].DisplayName)
	assert.Equal(t, 1, byCode["anonymous_denied_by_binding_guard|"].BindingCount)

	assert.Zero(t, cfgReads.Load(), "attention never reads a client config file (Spec 075)")
}

func TestAttention108NoClientsServiceMeansNoWarnings(t *testing.T) {
	rt := &Runtime{}
	assert.Empty(t, rt.AttentionClientWarnings())
}

func TestAttention108ObservedStatesSkipClientsWithOwnRecord(t *testing.T) {
	rt := newFunnelRuntime(t)
	_, err := rt.StorageManager().MintClientCredential("cursor", "mcp_cli_attn108_owned_0000000000", funnelKey, auth.ProfileModeLocked, "ro", time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	states, err := rt.clientsService.ObservedCredentialStates(map[string]storage.ClientCredentialObservation{
		"cursor":      {State: string(profile.CredentialStateAdminKey)},
		"unknown-app": {State: string(profile.CredentialStateAdminKey)},
		"codex":       {State: string(profile.CredentialStateClient)},
	})
	require.NoError(t, err)
	assert.Empty(t, states, "a client with its own record, an unregistered id and a non-admin state contribute nothing")
}
