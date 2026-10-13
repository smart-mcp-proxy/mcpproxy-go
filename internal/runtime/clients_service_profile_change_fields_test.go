package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// Spec 108 FR-030/T063: a profile_change record carries the NEW profile, the
// client id and the token name as first-class fields, so /activity?client=
// finds it, beside the metadata keys 108-c2 already writes.
func TestWriteChange_ProfileChangeHasFirstClassFields(t *testing.T) {
	h := newSvcHarness(t)
	ctx := context.Background()
	a := Actor{Kind: "agent_token", Name: "admin-bot", Surface: profile.SurfaceCLI}

	secret := h.mint("cursor", "ro", nil) // assign (mint)
	_, err := h.svc.SetBinding(ctx, a, "cursor", "full", nil)
	require.NoError(t, err) // assign
	_, err = h.svc.SetBinding(ctx, a, "cursor", "full", strp("switchable"))
	require.NoError(t, err) // unlock
	_, err = h.svc.SetBinding(ctx, a, "cursor", "full", strp("locked"))
	require.NoError(t, err) // lock
	_, _, err = h.svc.Rotate(ctx, a, "cursor")
	require.NoError(t, err)
	_, err = h.svc.FinalizeRotation(ctx, a, "cursor") // rotate
	require.NoError(t, err)
	_, err = h.svc.Forget(ctx, a, "cursor", true) // forget
	require.NoError(t, err)
	require.NotEmpty(t, secret)

	h.mu.Lock()
	defer h.mu.Unlock()
	require.GreaterOrEqual(t, len(h.records), 6)
	seen := map[string]bool{}
	for _, rec := range h.records {
		change, _ := rec.Metadata["change"].(string)
		seen[change] = true
		require.Equal(t, "cursor", rec.ClientID, change)
		require.Equal(t, "client-cursor", rec.TokenName, change)
		require.Equal(t, rec.Metadata["profile"], rec.Profile, "profile is the NEW profile (%s)", change)
		require.Empty(t, rec.ProfileSource, "a profile_change is not a resolution: no source (%s)", change)
		// metadata keys are kept for one release
		require.Equal(t, "cursor", rec.Metadata["client_id"])
		require.Equal(t, "client-cursor", rec.Metadata["token_name"])
	}
	for _, want := range []string{"assign", "unlock", "lock", "rotate", "forget"} {
		require.True(t, seen[want], "expected a %s record", want)
	}
	// The assign to "full" carries the new profile.
	var sawFull bool
	for _, rec := range h.records {
		if rec.Metadata["change"] == "assign" && rec.Profile == "full" {
			sawFull = true
		}
	}
	require.True(t, sawFull)
}
