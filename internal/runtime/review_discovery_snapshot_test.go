package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func descTool(server, desc string) *config.ToolMetadata {
	return &config.ToolMetadata{ServerName: server, Name: server + ":t", Description: desc, ParamsJSON: `{"type":"object"}`}
}

// Normal (trusted) discovery publishes live state and updates approval records.
// A review issued between those two steps must wait and then return one
// complete generation, never the old definitions with the new capture stamp.
// commitDiscoveredTools is the single publisher behind both the single-server
// path and the sweep.
func TestGetServerReview_WaitsForNormalDiscoveryBetweenPublishAndRecords(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "srv", Enabled: true}})
	ctx := context.Background()
	_, err := rt.commitDiscoveredTools(ctx, "srv", []*config.ToolMetadata{descTool("srv", "v1")})
	require.NoError(t, err)
	before := reviewOf(t, rt, "srv")
	require.Equal(t, "v1", before.Tools[0].Description)
	oldStamp := *before.Server.LastCaptureAt

	paused, release := make(chan struct{}), make(chan struct{})
	rt.discoveryAfterPublish = func() {
		close(paused)
		<-release
	}
	done := make(chan error, 1)
	go func() {
		_, err := rt.commitDiscoveredTools(ctx, "srv", []*config.ToolMetadata{descTool("srv", "v2")})
		done <- err
	}()
	<-paused

	type res struct {
		r   *ServerReview
		err error
	}
	reviewDone := make(chan res, 1)
	go func() {
		r, err := rt.GetServerReview(ctx, "srv")
		reviewDone <- res{r, err}
	}()
	select {
	case got := <-reviewDone:
		t.Fatalf("review returned mid-discovery: stamp=%v tool=%q err=%v", got.r.Server.LastCaptureAt, got.r.Tools[0].Description, got.err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-done)
	got := <-reviewDone
	require.NoError(t, got.err)
	require.True(t, got.r.Server.LastCaptureAt.After(oldStamp))
	require.Equal(t, "v2", got.r.Tools[0].Description, "new stamp must come with new definitions")
	require.Equal(t, 1, got.r.Server.LiveToolCount)
}

// A server quarantined while tools/list was in flight: discovery must not index
// it, and its review must still be one generation (records + snapshot + stamp
// captured together).
func TestCommitDiscoveredTools_QuarantinedDuringListCapturesConsistentlyWithoutIndexing(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "srv", Enabled: true, Quarantined: true}})
	ctx := context.Background()
	eligible, err := rt.commitDiscoveredTools(ctx, "srv", []*config.ToolMetadata{descTool("srv", "v1")})
	require.NoError(t, err)
	require.False(t, eligible)
	indexed, err := rt.indexManager.GetToolsByServer("srv")
	require.NoError(t, err)
	require.Empty(t, indexed, "a quarantined server must never be indexed by discovery")

	review := reviewOf(t, rt, "srv")
	require.NotNil(t, review.Server.LastCaptureAt)
	require.Len(t, review.Tools, 1)
	require.Equal(t, "v1", review.Tools[0].Description)
	require.Equal(t, 1, review.Server.LiveToolCount)
	require.Equal(t, "pending", review.Tools[0].ApprovalStatus)
}
