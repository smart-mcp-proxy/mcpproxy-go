package config

import (
	"fmt"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/qapilot"
)

// Quarantine/approval testing pilot — add-time admission table (QP-01).
//
// QuarantineDefaultForServer is the decision every add door (REST, MCP,
// CLI, registry, config-file admission gate) consults. The table covers the
// global switch (on, off, unset) × every trust_mode value the policy names,
// plus empty and unrecognised values, plus the legacy fields the mode is
// migrated from.
func TestQAPilot_AdmissionTable(t *testing.T) {
	qapilot.Track(t, "admission/table", "QP-01")
	globals := []struct {
		name  string
		value *bool
		on    bool
	}{{"unset", nil, true}, {"true", qpBoolPtr(true), true}, {"false", qpBoolPtr(false), false}}
	modes := []string{"auto", "scan", "manual", "", "bogus", "AUTO"}
	for _, g := range globals {
		for _, mode := range modes {
			cfg := &Config{QuarantineEnabled: g.value}
			sc := &ServerConfig{Name: "s", TrustMode: mode}
			want := qapilot.AdmissionQuarantined(g.on, qapilot.TrustMode(mode))
			if got := cfg.QuarantineDefaultForServer(sc); got != want {
				t.Errorf("quarantine_enabled=%s trust_mode=%q: policy quarantines=%v, implementation=%v", g.name, mode, want, got)
			}
		}
		// A nil server config fails closed to the global switch's answer.
		cfg := &Config{QuarantineEnabled: g.value}
		if got := cfg.QuarantineDefaultForServer(nil); got != g.on {
			t.Errorf("quarantine_enabled=%s nil server: want %v, got %v", g.name, g.on, got)
		}
	}
	// Legacy fields: auto_approve_tool_changes:true and skip_quarantine:true
	// resolve to auto when trust_mode is empty (spec 086 FR-015/016), and an
	// explicit trust_mode always wins over them.
	legacy := []struct {
		name string
		sc   *ServerConfig
		want qapilot.TrustMode
	}{
		{"auto_approve_tool_changes=true", &ServerConfig{AutoApproveToolChanges: qpBoolPtr(true)}, qapilot.TrustAuto},
		{"auto_approve_tool_changes=false", &ServerConfig{AutoApproveToolChanges: qpBoolPtr(false)}, qapilot.TrustManual},
		{"skip_quarantine=true", &ServerConfig{SkipQuarantine: true}, qapilot.TrustAuto},
		{"trust_mode=manual wins over skip_quarantine", &ServerConfig{TrustMode: "manual", SkipQuarantine: true}, qapilot.TrustManual},
		{"trust_mode=scan wins over auto_approve", &ServerConfig{TrustMode: "scan", AutoApproveToolChanges: qpBoolPtr(true)}, qapilot.TrustScan},
	}
	for _, l := range legacy {
		cfg := &Config{}
		want := qapilot.AdmissionQuarantined(true, l.want)
		if got := cfg.QuarantineDefaultForServer(l.sc); got != want {
			t.Errorf("%s: policy quarantines=%v, implementation=%v (effective %s)", l.name, want, got, l.sc.EffectiveTrustMode())
		}
	}
	_ = fmt.Sprintf
}

func qpBoolPtr(b bool) *bool { return &b }
