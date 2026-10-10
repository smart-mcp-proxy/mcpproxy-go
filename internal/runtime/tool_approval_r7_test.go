package runtime

import (
	"errors"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/stretchr/testify/require"
)

// UX-02 cross-review r7 finding 2: the CLI now sends expected_hashes {} for an
// empty review, exactly as Web and tray do. On the real approval path that
// binding refuses a tool captured between the review read and the approval
// write — both for a server approval and for approve_all — and nothing is
// written: the server stays quarantined and the tool stays pending.
func TestEmptySnapshotApprovalRefusesToolCapturedAfterTheReview(t *testing.T) {
	rt := newUX02R5ApprovalRuntime(t, t.TempDir(), true)
	// Discovery captures a destructive tool after the (empty) review was read.
	_, err := rt.checkToolApprovals("srv", []*config.ToolMetadata{ux02Destructive("srv")})
	require.NoError(t, err)
	require.Equal(t, []string{"drop_all=pending"}, ux02NotApproved(t, rt, "srv"))

	committed := false
	n, err := rt.CommitServerApprovalDecision("srv", nil, map[string]string{}, "api", func() error { committed = true; return nil })
	var stale *storage.StaleToolReviewError
	require.True(t, errors.As(err, &stale), "server approval bound to {} must be out of date, got %v", err)
	require.Contains(t, stale.Unreviewed, "drop_all")
	require.Zero(t, n)
	require.False(t, committed, "the unquarantine must not run")

	res, err := rt.ApproveToolsReviewed("srv", nil, true, map[string]string{}, "api")
	require.True(t, errors.As(err, &stale), "approve_all bound to {} must be out of date, got %v", err)
	if res != nil {
		require.Empty(t, res.Approved)
	}

	require.Equal(t, []string{"drop_all=pending"}, ux02NotApproved(t, rt, "srv"))
	srv, err := rt.storageManager.GetUpstreamServer("srv")
	require.NoError(t, err)
	require.True(t, srv.Quarantined)
}
