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

	notQuarantined := registryAddMessage("github-server", false)
	if !strings.Contains(notQuarantined, "Added github-server to MCPProxy") {
		t.Fatalf("expected message to contain %q, got %q", "Added github-server to MCPProxy", notQuarantined)
	}
	if strings.Contains(notQuarantined, "quarantined") {
		t.Fatalf("an enabled (non-quarantined) add must not claim quarantine, got %q", notQuarantined)
	}
}
