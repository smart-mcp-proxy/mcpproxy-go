package runtime

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

type failFinalizeStore struct{ ClientCredentialStore }

func (failFinalizeStore) FinalizeClientCredentialRotation(string) (*auth.AgentToken, error) {
	return nil, errors.New("disk full")
}

// A rotating connect that moves the binding and then fails to finalize puts
// the previous binding back. The restore must be as visible as the forward
// move: a compensating profile_change record and a binding-changed
// announcement, otherwise the audit trail and live sessions keep showing the
// new (possibly wider) binding.
func TestConnectMinter_FinalizeFailureRestoreIsAuditedAndAnnounced(t *testing.T) {
	h := newSvcHarness(t)
	m := h.svc.ConnectMinter()
	h.mint("cursor", "ro", nil)
	intent := connect.CredentialIntent{Profile: strp("full"), ActorKind: "api_key", Surface: "api"}
	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)

	h.svc.store = failFinalizeStore{h.svc.store}
	err = m.Commit("cursor", intent, issued)
	require.ErrorContains(t, err, "disk full", "Commit still returns the finalize error")

	view, err := h.svc.Get("cursor")
	require.NoError(t, err)
	require.Equal(t, "ro", view.Profile, "the previous binding is back in the store")

	var assigns []map[string]interface{}
	for _, c := range h.changes() {
		if c["change"] == "assign" {
			assigns = append(assigns, c)
		}
	}
	require.Len(t, assigns, 3, "mint, forward move and compensating restore")
	fwd, restore := assigns[1], assigns[2]
	require.Equal(t, "full", fwd["profile"])
	require.Equal(t, "ro", restore["profile"])
	require.Equal(t, "full", restore["previous_profile"])
	diff, _ := restore["diff"].(map[string]interface{})
	require.Equal(t, true, diff["restored"])
	require.Equal(t, "finalize_failed", diff["reason"])

	h.mu.Lock()
	defer h.mu.Unlock()
	require.Len(t, h.notified, 2, "sessions are told about both the move and the restore")
}
