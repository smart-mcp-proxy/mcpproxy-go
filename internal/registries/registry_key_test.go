package registries

import (
	"context"
	"errors"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestRegistryKeyEnvVar(t *testing.T) {
	cases := map[string]string{
		"pulse":          "MCPPROXY_REGISTRY_PULSE_API_KEY",
		"my-corp":        "MCPPROXY_REGISTRY_MY_CORP_API_KEY",
		"azure-mcp-demo": "MCPPROXY_REGISTRY_AZURE_MCP_DEMO_API_KEY",
	}
	for id, want := range cases {
		if got := RegistryKeyEnvVar(id); got != want {
			t.Errorf("RegistryKeyEnvVar(%q) = %q, want %q", id, got, want)
		}
	}
}

// FR-008: a registry that requires a key with none configured is skipped via
// ErrRegistryKeyMissing rather than performing a network fetch or erroring
// opaquely.
func TestSearchServers_KeyAbsentSkipped(t *testing.T) {
	cfg := &config.Config{
		Registries: []config.RegistryEntry{
			{ID: "needs-key", Name: "Key Required", ServersURL: "https://example.invalid/servers", RequiresKey: true},
		},
	}
	SetRegistriesFromConfig(cfg)
	t.Setenv("MCPPROXY_REGISTRY_NEEDS_KEY_API_KEY", "") // ensure absent

	_, err := SearchServers(context.Background(), "needs-key", "", "", 10, nil)
	if !errors.Is(err, ErrRegistryKeyMissing) {
		t.Fatalf("expected ErrRegistryKeyMissing, got %v", err)
	}
}

// When the key IS configured, the registry is not skipped — the key check is
// bypassed and a different (non-sentinel) path runs.
func TestSearchServers_KeyPresentNotSkipped(t *testing.T) {
	cfg := &config.Config{
		Registries: []config.RegistryEntry{
			{ID: "needs-key", Name: "Key Required", ServersURL: "", RequiresKey: true},
		},
	}
	SetRegistriesFromConfig(cfg)
	t.Setenv("MCPPROXY_REGISTRY_NEEDS_KEY_API_KEY", "sk-test-123")

	_, err := SearchServers(context.Background(), "needs-key", "", "", 10, nil)
	if errors.Is(err, ErrRegistryKeyMissing) {
		t.Fatalf("key is present; should not be skipped as key-missing, got %v", err)
	}
}
