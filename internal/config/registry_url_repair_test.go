package config

import "testing"

// GH #783 round 2 (Codex review): fixing the add path is not enough. A user who
// already added a static registry BEFORE the fix carries the mangled servers_url
// in their config forever — the merge copies servers_url/protocol through
// verbatim, so they stay broken after upgrading. The reporter of #783 is exactly
// such a user. Repair those entries on load.

func TestRepairMangledRegistryURLs_FixesStaticDocumentSource(t *testing.T) {
	const doc = "https://raw.githubusercontent.com/fleuristes/app-registry/refs/heads/main/apps.json"
	cfg := &Config{Registries: []RegistryEntry{{
		ID:         "raw-githubusercontent-com",
		URL:        doc,
		ServersURL: doc + "/v0.1/servers", // what the old derivation produced
		Protocol:   "modelcontextprotocol/registry",
		Provenance: RegistryProvenanceCustom,
	}}}

	if n := RepairMangledRegistryURLs(cfg); n != 1 {
		t.Fatalf("repaired %d entries, want 1", n)
	}
	got := cfg.Registries[0]
	if got.ServersURL != doc {
		t.Errorf("ServersURL = %q, want the URL the user actually pasted (%q)", got.ServersURL, doc)
	}
	if got.Protocol != RegistryProtocolGenericJSON {
		t.Errorf("Protocol = %q, want %q — a static document does not speak the official protocol",
			got.Protocol, RegistryProtocolGenericJSON)
	}

	// Idempotent: a second pass changes nothing.
	if n := RepairMangledRegistryURLs(cfg); n != 0 {
		t.Errorf("second pass repaired %d entries, want 0 (must be idempotent)", n)
	}
}

// The repair must be surgical. A base URL carrying a path (https://acme.example/api)
// may legitimately serve its collection at /api/v0.1/servers — that registry
// WORKS today, and rewriting its servers_url would break it. Only a URL whose
// final path segment names a document (apps.json) is definitionally unable to
// host a /v0.1/servers route, and that is the only case we touch.
func TestRepairMangledRegistryURLs_LeavesWorkingRegistriesAlone(t *testing.T) {
	cases := []struct {
		name  string
		entry RegistryEntry
	}{
		{"path-based registry that may really serve /v0.1/servers", RegistryEntry{
			ID: "acme", URL: "https://acme.example/api",
			ServersURL: "https://acme.example/api/v0.1/servers",
			Protocol:   "modelcontextprotocol/registry",
		}},
		{"bare base URL — the derivation was correct", RegistryEntry{
			ID: "acme2", URL: "https://acme.example",
			ServersURL: "https://acme.example/v0.1/servers",
			Protocol:   "modelcontextprotocol/registry",
		}},
		{"servers_url the user set explicitly, not our derivation", RegistryEntry{
			ID: "acme3", URL: "https://acme.example/apps.json",
			ServersURL: "https://acme.example/custom/endpoint",
			Protocol:   "modelcontextprotocol/registry",
		}},
		{"already repaired", RegistryEntry{
			ID: "acme4", URL: "https://acme.example/apps.json",
			ServersURL: "https://acme.example/apps.json",
			Protocol:   RegistryProtocolGenericJSON,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Registries: []RegistryEntry{tc.entry}}
			before := cfg.Registries[0]

			if n := RepairMangledRegistryURLs(cfg); n != 0 {
				t.Errorf("repaired %d entries, want 0 — this registry must not be touched", n)
			}
			if cfg.Registries[0].ServersURL != before.ServersURL || cfg.Registries[0].Protocol != before.Protocol {
				t.Errorf("entry was modified: %+v -> %+v", before, cfg.Registries[0])
			}
		})
	}
}

// A built-in default is never rewritten, whatever its shape.
func TestRepairMangledRegistryURLs_SkipsBuiltins(t *testing.T) {
	defaults := DefaultRegistries()
	cfg := &Config{Registries: append([]RegistryEntry(nil), defaults...)}

	if n := RepairMangledRegistryURLs(cfg); n != 0 {
		t.Errorf("repaired %d shipped defaults, want 0", n)
	}
}
