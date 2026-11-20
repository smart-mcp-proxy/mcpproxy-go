package main

import (
	"strings"
	"testing"
)

// TestRegistryAddMessage (Spec 109 FR-063 / T025): the CLI prints "Added
// <name> to MCPProxy (quarantined for review)" — the same wording the
// Web/macOS "Add to MCPProxy" action uses after a successful add.
func TestRegistryAddMessage(t *testing.T) {
	quarantined := registryAddMessage("github-server", true)
	if !strings.Contains(quarantined, "Added github-server to MCPProxy") {
		t.Fatalf("expected message to contain %q, got %q", "Added github-server to MCPProxy", quarantined)
	}
	if !strings.Contains(quarantined, "quarantined for review") {
		t.Fatalf("expected quarantined message to say %q, got %q", "quarantined for review", quarantined)
	}

	// The quarantined hint must name the step that actually admits the
	// server. `mcpproxy upstream approve` only approves pending tools: on a
	// quarantined server it prints "Approved 0 tools" and the server stays
	// quarantined, which left first-run users stuck.
	if !strings.Contains(quarantined, "mcpproxy review show github-server") ||
		!strings.Contains(quarantined, "mcpproxy review approve github-server --yes") {
		t.Fatalf("quarantined message must name review show / review approve --yes, got %q", quarantined)
	}
	if strings.Contains(quarantined, "upstream approve") {
		t.Fatalf("quarantined message must not point at upstream approve, got %q", quarantined)
	}

	notQuarantined := registryAddMessage("github-server", false)
	if !strings.Contains(notQuarantined, "Added github-server to MCPProxy") {
		t.Fatalf("expected message to contain %q, got %q", "Added github-server to MCPProxy", notQuarantined)
	}
	if strings.Contains(notQuarantined, "quarantined") {
		t.Fatalf("an enabled (non-quarantined) add must not claim quarantine, got %q", notQuarantined)
	}
}
