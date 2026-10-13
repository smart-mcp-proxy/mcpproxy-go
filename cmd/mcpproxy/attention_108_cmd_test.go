package main

import (
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// TestAttentionCmd_Spec108Kinds pins Spec 109-l: the generic table renders the
// Spec 108 kinds (the binding guard with a setting subject, an admin-key client)
// with no format change, and `status` counts them.
func TestAttentionCmd_Spec108Kinds(t *testing.T) {
	resp := &cliclient.AttentionResponse{
		Count:       2,
		GeneratedAt: "2026-10-01T09:00:00Z",
		Items: []contracts.AttentionItem{
			{
				ID:      "anonymous_denied_by_binding_guard:setting:require_mcp_auth",
				Kind:    "anonymous_denied_by_binding_guard",
				Rank:    4,
				Subject: contracts.AttentionSubject{Type: "setting", ID: "require_mcp_auth", Name: "Anonymous callers"},
				Summary: "Anonymous callers are denied: client bindings could be bypassed without authentication",
				Detail:  "Bound: Codex. Turn on authentication, or set anonymous callers to a profile no wider than the bindings.",
				Fix:     contracts.AttentionFix{Verb: "change_setting", Label: "Require authentication…", Target: "/settings?tab=security&focus=require_mcp_auth"},
			},
			{
				ID:      "client_holds_admin_key:client:cursor",
				Kind:    "client_holds_admin_key",
				Rank:    5,
				Subject: contracts.AttentionSubject{Type: "client", ID: "cursor", Name: "Cursor"},
				Summary: "Cursor: holds the admin API key",
				Fix:     contracts.AttentionFix{Verb: "upgrade_admin_key_holders", Label: "Upgrade…", Target: "/clients?focus=cursor"},
			},
		},
	}
	setOutputGlobals(t, "table", false)
	output := captureOutput(func() {
		if err := printAttentionOutput(resp); err != nil {
			t.Fatalf("printAttentionOutput: %v", err)
		}
	})
	assertClientGolden(t, "attention-108-kinds.golden", output)

	count := resp.Count
	status := captureOutput(func() {
		printStatusTable(&StatusInfo{State: "Running", Edition: "personal", NeedsAttention: &count})
	})
	if !strings.Contains(status, "Needs attention: 2 (run 'mcpproxy attention')") {
		t.Errorf("status first line must count the Spec 108 items, got:\n%s", status)
	}
}
