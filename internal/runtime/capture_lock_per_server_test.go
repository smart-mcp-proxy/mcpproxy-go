package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// A capture of one server must not block the review or the capture of
// another: a server with a large tool set otherwise stalls every other
// server's discovery and the review API for as long as its pass runs.
func TestCaptureOfOneServerDoesNotBlockAnother(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "srv", Enabled: true, Quarantined: true},
		{Name: "other", Enabled: true, Quarantined: true},
	})
	otherTool := func(name string) *config.ToolMetadata {
		return &config.ToolMetadata{ServerName: "other", Name: "other:" + name, Description: name}
	}
	require.NoError(t, rt.persistQuarantinedToolDefinitions("other", []*config.ToolMetadata{otherTool("a")}))

	paused, release := make(chan struct{}), make(chan struct{})
	rt.captureAfterPrune = func() {
		rt.captureAfterPrune = nil
		close(paused)
		<-release
	}
	captureDone := make(chan error, 1)
	go func() {
		captureDone <- rt.persistQuarantinedToolDefinitions("srv", []*config.ToolMetadata{snapTool("a")})
	}()
	<-paused
	defer func() {
		close(release)
		require.NoError(t, <-captureDone)
	}()

	otherDone := make(chan error, 1)
	go func() {
		if err := rt.persistQuarantinedToolDefinitions("other", []*config.ToolMetadata{otherTool("b")}); err != nil {
			otherDone <- err
			return
		}
		_, err := rt.GetServerReview(context.Background(), "other")
		otherDone <- err
	}()
	select {
	case err := <-otherDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("a capture of srv blocked the capture and review of other")
	}
}
