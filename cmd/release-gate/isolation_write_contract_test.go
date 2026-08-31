package main

import (
	"encoding/json"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
)

// TestQuarantineFixtureAddRequestUsesWritableIsolationKey pins the gate's
// add-server body to the WRITABLE isolation key.
//
// GH #1142 made `isolation.enabled` read-only on POST /api/v1/servers: it is
// the EFFECTIVE state on reads, so the handler now answers 400 rather than
// letting a read-modify-write turn "inherits the global setting" into a
// permanent override. The gate's fixture opt-out is load-bearing — it runs a
// HOST binary and must stay native when the docker matrix cell turns global
// isolation on — so it has to keep opting out, via `enabled_override`.
//
// The assertion decodes the gate's own body with the SERVER's request type, so
// it fails exactly when the real handler would reject the request, instead of
// re-stating the gate's JSON tags back at itself.
func TestQuarantineFixtureAddRequestUsesWritableIsolationKey(t *testing.T) {
	body, err := json.Marshal(quarantineFixtureAddRequest("gate-fresh-abc123", "/tmp/bin/mcpfixture-gate-fresh-abc123"))
	if err != nil {
		t.Fatalf("marshal add-server body: %v", err)
	}

	var decoded httpapi.AddServerRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("server-side decode of gate body %s: %v", body, err)
	}
	if decoded.Isolation == nil {
		t.Fatalf("the per-server isolation opt-out was dropped from the gate body: %s", body)
	}
	if decoded.Isolation.Enabled.Set {
		t.Fatalf("gate sends the read-only `isolation.enabled`; the handler answers 400 and the whole release QA gate fails: %s", body)
	}
	if !decoded.Isolation.EnabledOverride.Set {
		t.Fatalf("gate must still opt the host fixture OUT of isolation via `enabled_override`: %s", body)
	}
	if decoded.Isolation.EnabledOverride.Value == nil || *decoded.Isolation.EnabledOverride.Value {
		t.Fatalf("the fixture opt-out must be enabled_override=false, got %+v: %s", decoded.Isolation.EnabledOverride, body)
	}
}
