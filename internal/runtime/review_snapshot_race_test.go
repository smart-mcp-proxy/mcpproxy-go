package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// A capture that lands between the review's storage read and its live-state
// read must not leave the payload mixing the old records with the new capture.
func TestGetServerReview_ConsistentWhenCaptureLandsAfterRecordRead(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "srv", Enabled: true, Quarantined: true}})
	tool := func(name string) *config.ToolMetadata {
		return &config.ToolMetadata{ServerName: "srv", Name: "srv:" + name, Description: name}
	}
	require.NoError(t, rt.persistQuarantinedToolDefinitions("srv", []*config.ToolMetadata{tool("a")}))
	first := *reviewOf(t, rt, "srv").Server.LastCaptureAt

	fired := 0
	rt.reviewAfterRecords = func() {
		fired++
		if fired == 1 {
			require.NoError(t, rt.persistQuarantinedToolDefinitions("srv", []*config.ToolMetadata{tool("b")}))
		}
	}
	got, err := rt.GetServerReview(context.Background(), "srv")
	require.NoError(t, err)
	require.Len(t, got.Tools, 1)
	require.Equal(t, "b", got.Tools[0].Name)
	require.Equal(t, 1, got.Server.LiveToolCount)
	require.NotNil(t, got.Server.LastCaptureAt)
	require.True(t, got.Server.LastCaptureAt.After(first))
}
