package runtime

import (
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()

	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:           tempDir,
		Listen:            "127.0.0.1:9000",
		ToolResponseLimit: 0,
		Servers:           []*config.ServerConfig{},
	}

	rt, err := New(cfg, filepath.Join(tempDir, "config.yaml"), zap.NewNop())
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	t.Cleanup(func() {
		_ = rt.Close()
	})

	return rt
}

func TestRuntimeUpdateStatusBroadcasts(t *testing.T) {
	rt := newTestRuntime(t)

	phase := "Ready"
	message := "All systems go"
	stats := map[string]interface{}{"total_tools": 3}
	toolsIndexed := 3

	rt.UpdateStatus(phase, message, stats, toolsIndexed)

	status := rt.CurrentStatus()
	if status.Phase != phase {
		t.Fatalf("expected phase %q, got %q", phase, status.Phase)
	}
	if status.Message != message {
		t.Fatalf("expected message %q, got %q", message, status.Message)
	}
	if status.ToolsIndexed != toolsIndexed {
		t.Fatalf("expected toolsIndexed %d, got %d", toolsIndexed, status.ToolsIndexed)
	}
	if time.Since(status.LastUpdated) > time.Second {
		t.Fatalf("expected recent last updated, got %v", status.LastUpdated)
	}

	select {
	case snapshot := <-rt.StatusChannel():
		if snapshot.Phase != phase || snapshot.Message != message {
			t.Fatalf("status channel delivered unexpected snapshot: %#v", snapshot)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("did not receive status update on channel")
	}

	rendered := rt.StatusSnapshot(false)
	if rendered["phase"] != phase {
		t.Fatalf("expected snapshot phase %q, got %v", phase, rendered["phase"])
	}
	if rendered["tools_indexed"] != toolsIndexed {
		t.Fatalf("expected snapshot tools_indexed %d, got %v", toolsIndexed, rendered["tools_indexed"])
	}
	if rendered["running"] != false {
		t.Fatalf("expected running false, got %v", rendered["running"])
	}
}

func TestRuntimeStatusSnapshotReflectsRunningAndListen(t *testing.T) {
	rt := newTestRuntime(t)

	rt.SetRunning(true)
	rt.UpdateStatus("Ready", "listening", nil, 0)

	snapshot := rt.StatusSnapshot(true)

	if snapshot["running"] != true {
		t.Fatalf("expected running true, got %v", snapshot["running"])
	}
	if snapshot["listen_addr"] != "127.0.0.1:9000" {
		t.Fatalf("expected listen address 127.0.0.1:9000, got %v", snapshot["listen_addr"])
	}
}

func TestRuntimeUpdatePhaseWithoutUpstreamManager(t *testing.T) {
	rt := newTestRuntime(t)

	rt.upstreamManager = nil

	rt.UpdatePhase("Idle", "No upstream manager")

	status := rt.CurrentStatus()
	if status.Phase != "Idle" {
		t.Fatalf("expected phase Idle, got %q", status.Phase)
	}
	if status.UpstreamStats != nil {
		t.Fatalf("expected nil upstream stats, got %#v", status.UpstreamStats)
	}
	if status.ToolsIndexed != 0 {
		t.Fatalf("expected tools indexed 0, got %d", status.ToolsIndexed)
	}
}

func TestExtractToolCount(t *testing.T) {
	cases := []struct {
		name  string
		stats map[string]interface{}
		want  int
	}{
		{
			name:  "nil stats",
			stats: nil,
			want:  0,
		},
		{
			name: "total tools field",
			stats: map[string]interface{}{
				"total_tools": 5,
			},
			want: 5,
		},
		{
			name: "nested server counts",
			stats: map[string]interface{}{
				"servers": map[string]interface{}{
					"srv1": map[string]interface{}{"tool_count": 2},
					"srv2": map[string]interface{}{"tool_count": 3},
				},
			},
			want: 5,
		},
		{
			name: "mixed types ignored",
			stats: map[string]interface{}{
				"servers": map[string]interface{}{
					"srv1": []int{1, 2},
					"srv2": map[string]interface{}{"tool_count": 4},
				},
			},
			want: 4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractToolCount(tc.stats); got != tc.want {
				t.Fatalf("expected %d tools, got %d", tc.want, got)
			}
		})
	}
}
