package supervisor

import (
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
)

// Spec 116 PR2 review round 6 (Sol): reconcile retained a server's tools by
// NAME, so a same-name replacement reconciled from a newer configuration
// snapshot inherited the removed server's tool definitions in the StateView,
// and the REST listings stamped them with the replacement's incarnation.
// Reconcile must carry the retained tools only within one incarnation; the
// control reconcile (same incarnation) proves the assertion is not vacuous.
func TestReconcile_R6_ToolsNotCarriedAcrossIncarnation(t *testing.T) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{{Name: "acme", Enabled: true}},
	}
	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()
	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()
	_ = mockUpstream.AddServer("acme", cfg.Servers[0])
	mockUpstream.setConnected("acme", true)
	sup := New(configSvc, mockUpstream, zap.NewNop())

	snapAt := func(inc int64) *configsvc.Snapshot {
		return &configsvc.Snapshot{Config: cfg, Version: inc,
			ServerGenerations: map[string]configsvc.ServerGen{"acme": {Gen: inc, Incarnation: inc}}}
	}

	_ = sup.reconcile(snapAt(1))
	toolsA := []*config.ToolMetadata{{Name: "a_tool", ServerName: "acme", Description: "A's definition"}}
	if _, err := sup.RefreshToolsFromDiscovery(toolsA, sup.DiscoveryGenerations()); err != nil {
		t.Fatalf("RefreshToolsFromDiscovery: %v", err)
	}
	status := sup.StateView().Snapshot().Servers["acme"]
	if status == nil || len(status.Tools) != 1 || status.Incarnation != 1 {
		t.Fatalf("setup: want A's tool at incarnation 1, got %+v", status)
	}

	// Control: a reconcile of the SAME incarnation keeps the retained tools.
	_ = sup.reconcile(snapAt(1))
	if got := sup.StateView().Snapshot().Servers["acme"]; len(got.Tools) != 1 {
		t.Fatalf("control: same-incarnation reconcile must keep the tools, got %d", len(got.Tools))
	}

	// A same-name replacement (incarnation 2) must not inherit A's tools.
	_ = sup.reconcile(snapAt(2))
	got := sup.StateView().Snapshot().Servers["acme"]
	if got.Incarnation != 2 {
		t.Fatalf("StateView incarnation: want 2, got %d", got.Incarnation)
	}
	if len(got.Tools) != 0 || got.ToolsDiscovered {
		t.Fatalf("replacement inherited the removed server's tools: %+v", got.Tools)
	}
	if st := sup.CurrentSnapshot().Servers["acme"]; st == nil || len(st.Tools) != 0 || st.Incarnation != 2 {
		t.Fatalf("retained supervisor state carried A's tools into incarnation 2: %+v", st)
	}
}
