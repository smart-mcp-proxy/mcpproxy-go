package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func snapTool(name string) *config.ToolMetadata {
	return &config.ToolMetadata{ServerName: "srv", Name: "srv:" + name, Description: name}
}

// A review that runs after a capture persisted and pruned its records but
// before it published the stamp must wait for the capture, never return the
// new records with the previous capture's stamp and live count.
func TestGetServerReview_WaitsForCaptureBetweenPruneAndStamp(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "srv", Enabled: true, Quarantined: true}})
	require.NoError(t, rt.persistQuarantinedToolDefinitions("srv", []*config.ToolMetadata{snapTool("a")}))
	first := *reviewOf(t, rt, "srv").Server.LastCaptureAt

	paused, release := make(chan struct{}), make(chan struct{})
	rt.captureAfterPrune = func() {
		close(paused)
		<-release
	}
	captureDone := make(chan error, 1)
	go func() {
		captureDone <- rt.persistQuarantinedToolDefinitions("srv", []*config.ToolMetadata{snapTool("b")})
	}()
	<-paused

	type res struct {
		r   *ServerReview
		err error
	}
	reviewDone := make(chan res, 1)
	go func() {
		r, err := rt.GetServerReview(context.Background(), "srv")
		reviewDone <- res{r, err}
	}()
	select {
	case got := <-reviewDone:
		t.Fatalf("review returned mid-capture: %+v err=%v", got.r, got.err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-captureDone)
	got := <-reviewDone
	require.NoError(t, got.err)
	require.Len(t, got.r.Tools, 1)
	require.Equal(t, "b", got.r.Tools[0].Name)
	require.Equal(t, 1, got.r.Server.LiveToolCount)
	require.True(t, got.r.Server.LastCaptureAt.After(first))
}

// A capture that completes after each of the record reads must never produce
// a payload that mixes generations: the review returns the generation it
// started in, intact.
func TestGetServerReview_ConsistentWhenCaptureCompletesAfterEachRecordRead(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "srv", Enabled: true, Quarantined: true}})
	require.NoError(t, rt.persistQuarantinedToolDefinitions("srv", []*config.ToolMetadata{snapTool("a")}))

	names := []string{"a", "b", "c", "d", "e", "f"}
	for i := 0; i < 5; i++ {
		oldStamp := *reviewOf(t, rt, "srv").Server.LastCaptureAt
		prev, next := names[i], names[i+1]
		started := make(chan struct{})
		done := make(chan error, 1)
		rt.reviewAfterRecords = func() {
			go func() {
				close(started)
				done <- rt.persistQuarantinedToolDefinitions("srv", []*config.ToolMetadata{snapTool(next)})
			}()
			<-started
			time.Sleep(20 * time.Millisecond)
		}
		got, err := rt.GetServerReview(context.Background(), "srv")
		rt.reviewAfterRecords = nil
		require.NoError(t, err)
		require.NoError(t, <-done)
		require.Len(t, got.Tools, 1)
		require.Equal(t, prev, got.Tools[0].Name, "round %d", i)
		require.Equal(t, 1, got.Server.LiveToolCount, "round %d", i)
		require.True(t, got.Server.LastCaptureAt.Equal(oldStamp), "round %d", i)
	}
}
