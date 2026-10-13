package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// newReviewCaptureTestServer builds a Server over a real runtime holding one
// quarantined server whose baseline scan completed after exporting tools.
func newReviewCaptureTestServer(t *testing.T, fn func(ctx context.Context, name string) error) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Servers = []*config.ServerConfig{{Name: "srv", Enabled: true, Quarantined: true}}
	rt, err := runtime.New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	require.NoError(t, rt.StorageManager().SaveScanJob(&scanner.ScanJob{
		ID: "job-1", ServerName: "srv", Status: scanner.ScanJobStatusCompleted,
		ScanPass: scanner.ScanPassSecurityScan, StartedAt: time.Now().Add(-time.Minute),
		ScanContext: &scanner.ScanContext{SourceMethod: "tool_definitions_only", ToolsExported: 4},
	}))
	return &Server{logger: zap.NewNop(), runtime: rt, reviewCaptureFn: fn}
}

// runSettledEvent feeds one security.scan_settled event through the real
// server event loop and waits for the loop to return.
func runSettledEvent(t *testing.T, s *Server, payload map[string]any, repeat int) {
	t.Helper()
	ch := make(chan runtime.Event, repeat)
	for i := 0; i < repeat; i++ {
		ch <- runtime.Event{Type: runtime.EventTypeSecurityScanSettled, Payload: payload}
	}
	close(ch)
	s.listenForRoutingModeRefresh(ch)
}

func TestReviewCaptureAfterScanSettled(t *testing.T) {
	t.Run("completed scan captures the eligible server once", func(t *testing.T) {
		var calls atomic.Int32
		called := make(chan string, 4)
		s := newReviewCaptureTestServer(t, func(_ context.Context, name string) error {
			calls.Add(1)
			called <- name
			return nil
		})
		runSettledEvent(t, s, map[string]any{"server_name": "srv", "status": "completed"}, 1)
		select {
		case name := <-called:
			assert.Equal(t, "srv", name)
		case <-time.After(3 * time.Second):
			t.Fatal("capture was not triggered")
		}
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("duplicate settle events while a capture is in flight capture once", func(t *testing.T) {
		var calls atomic.Int32
		started := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		s := newReviewCaptureTestServer(t, func(_ context.Context, _ string) error {
			calls.Add(1)
			once.Do(func() { close(started) })
			<-release
			return nil
		})
		runSettledEvent(t, s, map[string]any{"server_name": "srv", "status": "completed"}, 3)
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("capture was not triggered")
		}
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, int32(1), calls.Load(), "single-flight must collapse duplicates")
		close(release)

		// Once the capture finished the guard is released, so a later settle
		// can capture again.
		require.Eventually(t, func() bool {
			_, busy := s.reviewCaptureInFlight.Load("srv")
			return !busy
		}, 3*time.Second, 5*time.Millisecond)
	})

	t.Run("failed scan does not capture", func(t *testing.T) {
		var calls atomic.Int32
		s := newReviewCaptureTestServer(t, func(context.Context, string) error {
			calls.Add(1)
			return nil
		})
		runSettledEvent(t, s, map[string]any{"server_name": "srv", "status": "failed"}, 1)
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, int32(0), calls.Load())
	})

	t.Run("ineligible server does not capture", func(t *testing.T) {
		var calls atomic.Int32
		s := newReviewCaptureTestServer(t, func(context.Context, string) error {
			calls.Add(1)
			return nil
		})
		runSettledEvent(t, s, map[string]any{"server_name": "ghost", "status": "completed"}, 1)
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, int32(0), calls.Load())
	})
}
