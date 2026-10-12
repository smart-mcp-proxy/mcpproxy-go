package runtime

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 116 x Spec 117 PR-2: a tool or server approval carries BOTH the
// incarnation of the server reviewed and the fingerprints of the definitions
// reviewed. The two refusals stay distinct: ErrServerReplaced
// (server_replaced) and *toolreview.StaleToolReviewError (review_out_of_date).
func TestReviewBound_ToolApprovalsCarryBothBindings(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, manualStdio("acme", false))
	old, ok := rt.ConfigSnapshot().ServerGeneration("acme")
	require.True(t, ok)
	seedPendingTool(t, rt, "acme", "echo", "v1")
	rec, err := rt.storageManager.GetToolApproval("acme", "echo")
	require.NoError(t, err)
	good := map[string]string{"echo": reviewFingerprint(rec)}

	// Current incarnation + matching fingerprints: approved.
	n, err := rt.ApproveToolsReviewedAt("acme", []string{"echo"}, good, "api", old.Incarnation)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, storage.ToolApprovalStatusApproved, toolStatus(t, rt, "acme", "echo"))

	// Wrong hash, current incarnation: review_out_of_date, not server_replaced.
	seedPendingTool(t, rt, "acme", "ping", "v1")
	_, err = rt.ApproveToolsReviewedAt("acme", []string{"ping"}, map[string]string{"ping": "rv1-stale"}, "api", old.Incarnation)
	var stale *toolreview.StaleToolReviewError
	require.True(t, errors.As(err, &stale))
	assert.NotErrorIs(t, err, ErrServerReplaced)
	_, err = rt.ApproveAllToolsReviewedAt("acme", map[string]string{"ping": "rv1-stale"}, "api", old.Incarnation)
	require.True(t, errors.As(err, &stale))
	assert.Equal(t, storage.ToolApprovalStatusPending, toolStatus(t, rt, "acme", "ping"))

	// The server is removed and re-added: the old incarnation wins the
	// error even when the hashes would also mismatch, and nothing is written.
	removeAndReAdd(t, rt, manualStdio("acme", false))
	seedPendingTool(t, rt, "acme", "ping", "v2")
	_, err = rt.ApproveToolsReviewedAt("acme", []string{"ping"}, map[string]string{"ping": "rv1-stale"}, "api", old.Incarnation)
	require.ErrorIs(t, err, ErrServerReplaced)
	assert.False(t, errors.As(err, &stale), "server_replaced stays distinct from review_out_of_date")
	_, err = rt.ApproveAllToolsReviewedAt("acme", map[string]string{}, "api", old.Incarnation)
	require.ErrorIs(t, err, ErrServerReplaced)
	assert.Equal(t, storage.ToolApprovalStatusPending, toolStatus(t, rt, "acme", "ping"))

	// An unbound review (nil hashes) still honours the incarnation.
	_, err = rt.ApproveToolsReviewedAt("acme", []string{"ping"}, nil, "api", old.Incarnation)
	require.ErrorIs(t, err, ErrServerReplaced)
}

func TestReviewBound_ServerApprovalChecksIncarnationThenFingerprints(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, manualStdio("acme", true))
	old, ok := rt.ConfigSnapshot().ServerGeneration("acme")
	require.True(t, ok)
	removeAndReAdd(t, rt, manualStdio("acme", true))

	saved := false
	_, err := rt.CommitServerApprovalAt("acme", old.Incarnation, nil, map[string]string{"x": "rv1-stale"}, "api", func() error { saved = true; return nil })
	require.ErrorIs(t, err, ErrServerReplaced)
	assert.False(t, saved, "nothing is committed for a replaced server")
	assert.True(t, spec116Live(rt, "acme").Quarantined, "the replacement stays quarantined")
}

// Review resync R1: a server removed (not re-added) after the reviewer
// captured its incarnation is a replaced server (409 server_replaced), not a
// missing one (404). Nothing is recorded and no server reappears.
func TestReviewBound_ServerApprovalRemovedOnlyIsReplaced(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, manualStdio("acme", true))
	old, ok := rt.ConfigSnapshot().ServerGeneration("acme")
	require.True(t, ok)
	_, err := rt.Commit("remove_server", func(tx *CommitTx) error { return tx.RemoveServer("acme") })
	require.NoError(t, err)

	saved := false
	_, err = rt.CommitServerApprovalAt("acme", old.Incarnation, nil, nil, "api", func() error { saved = true; return nil })
	require.ErrorIs(t, err, ErrServerReplaced)
	var nf *ServerNotFoundError
	assert.False(t, errors.As(err, &nf), "a bound approval of a removed server is server_replaced, not not-found")
	assert.False(t, saved, "nothing is committed")
	_, present := rt.ConfigSnapshot().ServerGeneration("acme")
	assert.False(t, present, "the approval must not recreate the server")
	recs, lerr := rt.storageManager.ListToolApprovals("acme")
	require.NoError(t, lerr)
	assert.Empty(t, recs, "no approval or baseline records for a removed server")
}
