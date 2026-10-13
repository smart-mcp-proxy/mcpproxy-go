package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

// hookMinter runs afterIssue between the real Issue (staged secret B) and the
// config write, the window the FR-021a race lives in.
type hookMinter struct {
	connect.CredentialMinter
	afterIssue func()
}

func (m hookMinter) Issue(clientID string, intent connect.CredentialIntent) (*connect.IssuedCredential, error) {
	issued, err := m.CredentialMinter.Issue(clientID, intent)
	if err == nil && m.afterIssue != nil {
		m.afterIssue()
	}
	return issued, err
}

// TestConnect_ReconcileRaceThroughConnectService drives a real connect.Service:
// a reconcile that fires after the credential is staged but before the config
// file is rewritten must not roll the new secret back, so the secret the file
// ends up holding still authenticates.
func TestConnect_ReconcileRaceThroughConnectService(t *testing.T) {
	h := newSvcHarness(t)
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", home+"/AppData/Local")
	t.Setenv("APPDATA", home+"/AppData/Roaming")
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "admin-key", home).WithRequireMCPAuth(true)
	svc.WithCredentialMinter(h.svc.ConnectMinter())
	h.svc.SetConfigReader(svc)

	cfgPath := connect.ConfigPath("cursor", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte("{}\n"), 0o644))

	prof := "ro"
	res, err := svc.ConnectWithOptions("cursor", "", connect.ConnectOptions{Intent: connect.CredentialIntent{Profile: &prof, ActorKind: "api_key", Surface: "api"}})
	require.NoError(t, err)
	require.True(t, res.Success)
	first, found, err := svc.ClientSecret("cursor")
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, h.authenticates(first))

	svc.WithCredentialMinter(hookMinter{
		CredentialMinter: h.svc.ConnectMinter(),
		afterIssue: func() {
			require.NoError(t, h.svc.ReconcileClient(context.Background(), "cursor"))
		},
	})
	res, err = svc.ConnectWithOptions("cursor", "", connect.ConnectOptions{Force: true, Intent: connect.CredentialIntent{ActorKind: "api_key", Surface: "api"}})
	require.NoError(t, err)
	require.True(t, res.Success)

	second, found, err := svc.ClientSecret("cursor")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEqual(t, first, second)
	require.True(t, h.authenticates(second), "the secret the file holds must authenticate")
	require.False(t, h.authenticates(first))
	require.Equal(t, map[string]int{"finalized": 1}, rotateOutcomes(h))
}

// toggleGuard refuses every candidate once armed, so a test can let Issue pass
// the guard and then make Commit's binding change fail.
type toggleGuard struct{ armed *bool }

func (g toggleGuard) BindingGuardDelta(_, _ GuardState) []BindingRef {
	if *g.armed {
		return []BindingRef{{ClientID: "cursor", Profile: "full"}}
	}
	return nil
}
func (g toggleGuard) BindingGuardFixes(GuardState, []BindingRef) []GuardFix { return nil }
func (g toggleGuard) BindingGuardActiveBindings() []BindingRef              { return nil }

// 1451-1: the binding is applied before the new secret is finalized, so a
// binding failure leaves the old secret live under the old binding and the new
// secret not finalized.
func TestConnectMinter_CommitBindingFailureKeepsOldSecretAndBinding(t *testing.T) {
	h := newSvcHarness(t)
	armed := false
	h.guard = toggleGuard{armed: &armed}
	m := h.svc.ConnectMinter()
	old := h.mint("cursor", "ro", nil)
	intent := connect.CredentialIntent{Profile: strp("full"), Mode: strp("locked"), ActorKind: "api_key", Surface: "api"}

	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)
	armed = true
	err = m.Commit("cursor", intent, issued)
	var guardErr *BindingGuardError
	require.ErrorAs(t, err, &guardErr)

	view, err := h.svc.Get("cursor")
	require.NoError(t, err)
	require.Equal(t, "ro", view.Profile, "the old binding is untouched")
	require.True(t, h.authenticates(old))
	require.Empty(t, rotateOutcomes(h)["finalized"], "the new secret was not finalized")
	all, err := h.svc.Records()
	require.NoError(t, err)
	require.Equal(t, 1, len(all))
	require.NotEmpty(t, all[0].PendingHash, "the rotation is still staged for the reconciler")
}

// 1451-16: connect A's claim expires, connect B claims and issues; A's late
// Abort and Release must not touch B's pending secret or claim.
func TestConnectMinter_StaleAbortCannotRollBackNewerConnect(t *testing.T) {
	h := newSvcHarness(t)
	m := h.svc.ConnectMinter()
	h.mint("cursor", "ro", nil)
	intent := connect.CredentialIntent{ActorKind: "api_key", Surface: "api"}

	a, err := m.Issue("cursor", intent)
	require.NoError(t, err)
	h.clock = h.clock.Add(connectInFlightTTL + time.Second)
	b, err := m.Issue("cursor", intent)
	require.NoError(t, err)

	m.Release("cursor", a)
	var busy *ConnectInProgressError
	require.ErrorAs(t, m.CheckIdle("cursor"), &busy, "A's release must not drop B's claim")

	err = m.Abort("cursor", intent, a)
	var superseded *CredentialSupersededError
	require.ErrorAs(t, err, &superseded)
	require.True(t, h.authenticates(b.Secret), "B's pending secret survives A's abort")
	require.NoError(t, m.Commit("cursor", intent, b))
	require.True(t, h.authenticates(b.Secret))
}
