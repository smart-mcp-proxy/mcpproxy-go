package supervisor

import (
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
)

// Spec 117 FR-034 (#1610): reconcile used to copy a server's retained tool
// list forward whatever its state, so a server that was disabled or
// quarantined (and whose index entries the runtime purged under the approval
// lock) got its old tools republished into the StateView on the next
// reconcile. Reconcile must keep no tools for a disabled or quarantined
// server; re-enabling restores them only through a fresh capture.
func TestReconcile_DropsRetainedToolsOfIneligibleServer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*config.ServerConfig)
	}{
		{"disabled", func(s *config.ServerConfig) { s.Enabled = false }},
		{"quarantined", func(s *config.ServerConfig) { s.Quarantined = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &config.ServerConfig{Name: "acme", Enabled: true}
			cfg := &config.Config{Listen: "127.0.0.1:8080", Servers: []*config.ServerConfig{srv}}
			configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
			defer configSvc.Close()
			mockUpstream := NewMockUpstreamAdapter()
			defer mockUpstream.Close()
			_ = mockUpstream.AddServer("acme", srv)
			mockUpstream.setConnected("acme", true)
			sup := New(configSvc, mockUpstream, zap.NewNop())

			snap := func(c *config.Config) *configsvc.Snapshot {
				return &configsvc.Snapshot{Config: c, Version: 1,
					ServerGenerations: map[string]configsvc.ServerGen{"acme": {Gen: 1, Incarnation: 1}}}
			}
			_ = sup.reconcile(snap(cfg))
			tools := []*config.ToolMetadata{{Name: "a_tool", ServerName: "acme", Description: "A"}}
			if _, err := sup.RefreshToolsFromDiscovery(tools, sup.DiscoveryGenerations()); err != nil {
				t.Fatalf("RefreshToolsFromDiscovery: %v", err)
			}
			_ = sup.reconcile(snap(cfg))
			if got := sup.StateView().Snapshot().Servers["acme"]; got == nil || len(got.Tools) != 1 {
				t.Fatalf("control: an eligible server keeps its retained tools across reconcile, got %+v", got)
			}

			changed := *srv
			tc.apply(&changed)
			ineligible := &config.Config{Listen: cfg.Listen, Servers: []*config.ServerConfig{&changed}}
			_ = sup.reconcile(snap(ineligible))

			if got := sup.StateView().Snapshot().Servers["acme"]; got == nil || len(got.Tools) != 0 {
				t.Fatalf("StateView republished tools of a %s server: %+v", tc.name, got)
			}
			if st := sup.CurrentSnapshot().Servers["acme"]; st == nil || len(st.Tools) != 0 {
				t.Fatalf("supervisor retained tools of a %s server: %+v", tc.name, st)
			}

			// Re-enabling does not resurrect the dropped list: only a fresh
			// capture publishes tools again.
			_ = sup.reconcile(snap(cfg))
			if got := sup.StateView().Snapshot().Servers["acme"]; len(got.Tools) != 0 {
				t.Fatalf("re-enabled server got its old tools back without a capture: %+v", got.Tools)
			}
		})
	}
}
