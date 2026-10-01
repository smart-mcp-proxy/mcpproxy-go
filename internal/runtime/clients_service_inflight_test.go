package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

func rotateOutcomes(h *svcHarness) map[string]int {
	out := map[string]int{}
	for _, c := range h.changes() {
		if c["change"] == "rotate" {
			out[c["diff"].(map[string]interface{})["outcome"].(string)]++
		}
	}
	return out
}

// FR-021a: a reconcile that runs between Issue and Commit sees the file still
// holding the old secret; it must NOT roll the staged secret back.
func TestConnectMinter_ReconcileBetweenIssueAndCommitKeepsPending(t *testing.T) {
	h := newSvcHarness(t)
	ctx := context.Background()
	m := h.svc.ConnectMinter()
	old := h.mint("cursor", "ro", nil)
	intent := connect.CredentialIntent{ActorKind: "api_key", Surface: "api"}

	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)
	h.reader.secrets["cursor"] = old // the file still holds A
	require.NoError(t, h.svc.ReconcileClient(ctx, "cursor"))
	require.NoError(t, h.svc.Reconcile(ctx))
	require.NoError(t, h.svc.ReconcileTimeOnly(ctx))

	h.reader.secrets["cursor"] = issued.Secret
	require.NoError(t, m.Commit("cursor", intent, issued))
	require.True(t, h.authenticates(issued.Secret))
	require.False(t, h.authenticates(old))
	require.Equal(t, map[string]int{"finalized": 1}, rotateOutcomes(h))
}

func TestConnectMinter_SecondIssueWhileInFlightIsRefused(t *testing.T) {
	h := newSvcHarness(t)
	m := h.svc.ConnectMinter()
	h.mint("cursor", "ro", nil)
	intent := connect.CredentialIntent{ActorKind: "api_key", Surface: "api"}

	first, err := m.Issue("cursor", intent)
	require.NoError(t, err)
	_, err = m.Issue("cursor", intent)
	var busy *ConnectInProgressError
	require.ErrorAs(t, err, &busy)
	require.Equal(t, "connect_in_progress", busy.Code())
	require.True(t, h.authenticates(first.Secret), "the first pending secret survives the refused second issue")

	require.NoError(t, m.Commit("cursor", intent, first))
	third, err := m.Issue("cursor", intent)
	require.NoError(t, err, "the claim ends at commit")
	require.NoError(t, m.Abort("cursor", intent, third))
	_, err = m.Issue("cursor", intent)
	require.NoError(t, err, "the claim ends at abort")
}

func TestConnectMinter_CommitFailsWhenPendingWasDropped(t *testing.T) {
	h := newSvcHarness(t)
	m := h.svc.ConnectMinter()
	old := h.mint("cursor", "ro", nil)
	intent := connect.CredentialIntent{ActorKind: "api_key", Surface: "api"}

	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)
	_, err = h.sm.RollbackClientCredentialRotation("cursor") // a lost race
	require.NoError(t, err)

	err = m.Commit("cursor", intent, issued)
	var sup *CredentialSupersededError
	require.ErrorAs(t, err, &sup)
	require.Equal(t, "credential_superseded", sup.Code())
	require.True(t, h.authenticates(old))
	require.False(t, h.authenticates(issued.Secret))
}

func TestConnectMinter_FreshMintCommitFailsWhenRevoked(t *testing.T) {
	h := newSvcHarness(t)
	m := h.svc.ConnectMinter()
	intent := connect.CredentialIntent{Profile: strp("ro"), ActorKind: "api_key", Surface: "api"}
	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)
	_, err = h.sm.ForgetClientCredential("cursor")
	require.NoError(t, err)
	var sup *CredentialSupersededError
	require.ErrorAs(t, m.Commit("cursor", intent, issued), &sup)
	require.False(t, h.authenticates(issued.Secret))
}

func TestConnectMinter_ForgetDuringConnectFailsCommitClosed(t *testing.T) {
	h := newSvcHarness(t)
	m := h.svc.ConnectMinter()
	old := h.mint("cursor", "ro", nil)
	intent := connect.CredentialIntent{ActorKind: "api_key", Surface: "api"}
	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)

	_, err = h.svc.Forget(context.Background(), h.actor(), "cursor", false)
	require.NoError(t, err, "a deliberate revoke is never blocked by a connect")
	var sup *CredentialSupersededError
	require.ErrorAs(t, m.Commit("cursor", intent, issued), &sup)
	require.False(t, h.authenticates(old))
	require.False(t, h.authenticates(issued.Secret))
}

func TestConnectMinter_RotateFinalizeSetBindingRefusedWhileInFlight(t *testing.T) {
	h := newSvcHarness(t)
	ctx := context.Background()
	m := h.svc.ConnectMinter()
	h.mint("cursor", "ro", nil)
	intent := connect.CredentialIntent{ActorKind: "api_key", Surface: "api"}
	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)

	var busy *ConnectInProgressError
	_, _, err = h.svc.Rotate(ctx, h.actor(), "cursor")
	require.ErrorAs(t, err, &busy)
	_, err = h.svc.FinalizeRotation(ctx, h.actor(), "cursor")
	require.ErrorAs(t, err, &busy)
	_, err = h.svc.SetBinding(ctx, h.actor(), "cursor", "full", nil)
	require.ErrorAs(t, err, &busy)
	_, skipped, err := h.svc.BulkAssign(ctx, h.actor(), "ro", "full", nil)
	require.NoError(t, err)
	require.Len(t, skipped, 1)
	require.Equal(t, "connect_in_progress", skipped[0].Code)

	// other clients are unaffected
	h.mint("windsurf", "ro", nil)
	_, err = h.svc.SetBinding(ctx, h.actor(), "windsurf", "full", nil)
	require.NoError(t, err)

	// Commit's own binding change is not refused by its own claim.
	require.NoError(t, m.Abort("cursor", intent, issued))
	intent2 := connect.CredentialIntent{Profile: strp("full"), ActorKind: "api_key", Surface: "api"}
	issued2, err := m.Issue("cursor", intent2)
	require.NoError(t, err)
	require.NoError(t, m.Commit("cursor", intent2, issued2))
	v, err := h.svc.Get("cursor")
	require.NoError(t, err)
	require.Equal(t, "full", v.Profile)
}

func TestConnectMinter_ReleaseClearsClaimAndLetsReconcilerResolve(t *testing.T) {
	h := newSvcHarness(t)
	ctx := context.Background()
	m := h.svc.ConnectMinter()
	old := h.mint("cursor", "ro", nil)
	intent := connect.CredentialIntent{ActorKind: "api_key", Surface: "api"}
	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)

	m.Release("cursor", issued)
	h.reader.secrets["cursor"] = issued.Secret // the ambiguous write did land
	require.NoError(t, h.svc.ReconcileClient(ctx, "cursor"))
	require.True(t, h.authenticates(issued.Secret))
	require.False(t, h.authenticates(old))
	require.Equal(t, map[string]int{"finalized": 1}, rotateOutcomes(h))
}

func TestConnectMinter_StaleClaimIsIgnoredAfterTTL(t *testing.T) {
	h := newSvcHarness(t)
	ctx := context.Background()
	m := h.svc.ConnectMinter()
	old := h.mint("cursor", "ro", nil)
	intent := connect.CredentialIntent{ActorKind: "api_key", Surface: "api"}
	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)

	h.clock = h.clock.Add(3 * time.Minute) // the connect was abandoned (a panic)
	h.reader.secrets["cursor"] = old
	require.NoError(t, h.svc.ReconcileClient(ctx, "cursor"))
	require.True(t, h.authenticates(old))
	require.False(t, h.authenticates(issued.Secret), "an abandoned claim no longer shields the pending secret")
}
