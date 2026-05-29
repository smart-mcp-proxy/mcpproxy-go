package server

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// helper: a sanitisation config with a given action.
func sanCfg(action string, spotlight, strip bool) *config.OutputSanitisationConfig {
	c := config.DefaultOutputSanitisationConfig()
	c.ResponseAction = action
	c.SpotlightUntrusted = spotlight
	c.StripControlChars = strip
	return c
}

func TestEvaluateOutputSanitisation_DefaultTrusted_NoOp(t *testing.T) {
	d := evaluateOutputSanitisation(config.DefaultOutputSanitisationConfig(), contracts.ContentTrustTrusted, false)
	if d.block || d.redact || d.strip || d.spotlight {
		t.Fatalf("trusted + default must be a no-op, got %+v", d)
	}
}

func TestEvaluateOutputSanitisation_DefaultUntrusted_NoOp(t *testing.T) {
	// Track B is fully opt-in: default config does nothing, even for untrusted.
	d := evaluateOutputSanitisation(config.DefaultOutputSanitisationConfig(), contracts.ContentTrustUntrusted, false)
	if d.block || d.redact || d.strip || d.spotlight {
		t.Fatalf("untrusted + default must be a no-op (fully opt-in), got %+v", d)
	}
}

func TestEvaluateOutputSanitisation_SpotlightOptIn(t *testing.T) {
	d := evaluateOutputSanitisation(sanCfg("spotlight", true, false), contracts.ContentTrustUntrusted, false)
	if !d.spotlight {
		t.Fatalf("untrusted + spotlight-on must spotlight, got %+v", d)
	}
	if d.redact || d.strip || d.block {
		t.Fatalf("spotlight-only must not mutate further, got %+v", d)
	}
	// trusted is never spotlighted
	dt := evaluateOutputSanitisation(sanCfg("spotlight", true, false), contracts.ContentTrustTrusted, false)
	if dt.spotlight {
		t.Fatalf("trusted must never be spotlighted, got %+v", dt)
	}
}

func TestEvaluateOutputSanitisation_Redact_RegardlessOfTrust(t *testing.T) {
	for _, trust := range []string{contracts.ContentTrustTrusted, contracts.ContentTrustUntrusted} {
		d := evaluateOutputSanitisation(sanCfg("redact", true, false), trust, false)
		if !d.redact {
			t.Fatalf("redact action must redact for trust=%q, got %+v", trust, d)
		}
	}
}

func TestEvaluateOutputSanitisation_Strip_OnlyUntrusted(t *testing.T) {
	cfg := sanCfg("spotlight", true, true)
	if d := evaluateOutputSanitisation(cfg, contracts.ContentTrustTrusted, false); d.strip {
		t.Fatalf("strip must not apply to trusted output, got %+v", d)
	}
	if d := evaluateOutputSanitisation(cfg, contracts.ContentTrustUntrusted, false); !d.strip {
		t.Fatalf("strip must apply to untrusted output, got %+v", d)
	}
}

func TestEvaluateOutputSanitisation_Block_CriticalBlocks(t *testing.T) {
	cfg := sanCfg("block", true, false)
	d := evaluateOutputSanitisation(cfg, contracts.ContentTrustUntrusted, true)
	if !d.block {
		t.Fatalf("block action + critical detection must block, got %+v", d)
	}
	if d.reason == "" {
		t.Fatalf("block decision must carry a reason")
	}
}

func TestEvaluateOutputSanitisation_Block_NonCriticalPasses(t *testing.T) {
	cfg := sanCfg("block", true, false)
	d := evaluateOutputSanitisation(cfg, contracts.ContentTrustUntrusted, false)
	if d.block {
		t.Fatalf("block action without a critical detection must not block, got %+v", d)
	}
	if !d.spotlight {
		t.Fatalf("block mode on untrusted should still spotlight, got %+v", d)
	}
}

func TestEvaluateOutputSanitisation_NilConfig_NoOp(t *testing.T) {
	// nil config == default == fully opt-in == no-op.
	d := evaluateOutputSanitisation(nil, contracts.ContentTrustUntrusted, false)
	if d.block || d.redact || d.strip || d.spotlight {
		t.Fatalf("nil config must be a no-op (fully opt-in), got %+v", d)
	}
}
