package main

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
)

func TestResolveFixerKey_PicksFirstButton(t *testing.T) {
	entry, ok := diagnostics.Get(diagnostics.OAuthRefreshExpired)
	if !ok {
		t.Fatalf("expected catalog to contain OAuthRefreshExpired")
	}
	var key string
	step := resolveFixerKey(entry, &key)
	if step == nil {
		t.Fatalf("expected a Button fix step to be resolved")
	}
	if step.Type != diagnostics.FixStepButton {
		t.Fatalf("expected Button type, got %s", step.Type)
	}
	if key == "" {
		t.Fatalf("expected fixer_key to be populated, got empty")
	}
}

func TestResolveFixerKey_ExactMatch(t *testing.T) {
	entry, ok := diagnostics.Get(diagnostics.OAuthRefreshExpired)
	if !ok {
		t.Fatalf("expected catalog to contain OAuthRefreshExpired")
	}
	key := "oauth_reauth"
	step := resolveFixerKey(entry, &key)
	if step == nil {
		t.Fatalf("expected oauth_reauth to resolve on OAuth code")
	}
	if step.FixerKey != "oauth_reauth" {
		t.Fatalf("unexpected fixer_key %q", step.FixerKey)
	}
}

func TestResolveFixerKey_MissingMatch(t *testing.T) {
	entry, ok := diagnostics.Get(diagnostics.OAuthRefreshExpired)
	if !ok {
		t.Fatalf("expected catalog to contain OAuthRefreshExpired")
	}
	key := "nonexistent_key"
	step := resolveFixerKey(entry, &key)
	if step != nil {
		t.Fatalf("expected nil for unknown fixer_key, got %+v", step)
	}
}

func TestFilterDiagnosticsByServer_KeepsMatchingEntries(t *testing.T) {
	diag := map[string]interface{}{
		"upstream_errors": []interface{}{
			map[string]interface{}{"server_name": "a", "error_message": "boom"},
			map[string]interface{}{"server_name": "b", "error_message": "boom2"},
		},
		"oauth_required": []interface{}{
			map[string]interface{}{"server_name": "a"},
		},
		"missing_secrets": []interface{}{
			map[string]interface{}{"secret_name": "TOKEN", "used_by": []interface{}{"a", "c"}},
			map[string]interface{}{"secret_name": "OTHER", "used_by": []interface{}{"b"}},
		},
		"total_issues":   4,
		"some_other_key": "passthrough",
	}
	out := filterDiagnosticsByServer(diag, "a")

	upstream, _ := out["upstream_errors"].([]interface{})
	if len(upstream) != 1 {
		t.Fatalf("expected 1 upstream error for server a, got %d", len(upstream))
	}
	oauth, _ := out["oauth_required"].([]interface{})
	if len(oauth) != 1 {
		t.Fatalf("expected 1 oauth entry for server a, got %d", len(oauth))
	}
	secrets, _ := out["missing_secrets"].([]interface{})
	if len(secrets) != 1 {
		t.Fatalf("expected 1 missing-secret referring to a, got %d", len(secrets))
	}
	if out["some_other_key"] != "passthrough" {
		t.Fatalf("expected unknown fields to pass through untouched")
	}
	total, _ := out["total_issues"].(int)
	if total != 3 {
		t.Fatalf("expected total_issues=3 (sum of per-server arrays), got %d", total)
	}
}

func TestFilterQuarantineStatsByServer(t *testing.T) {
	stats := []quarantineServerStats{
		{ServerName: "a", PendingCount: 2},
		{ServerName: "b", ChangedCount: 1},
	}
	out := filterQuarantineStatsByServer(stats, "a")
	if len(out) != 1 {
		t.Fatalf("expected 1 stat for server a, got %d", len(out))
	}
	if out[0].ServerName != "a" {
		t.Fatalf("unexpected server %s", out[0].ServerName)
	}
}
