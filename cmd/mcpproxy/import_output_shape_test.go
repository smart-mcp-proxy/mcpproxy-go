package main

import (
	"encoding/json"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/configimport"
)

// TestBuildImportedServersOutput_OmitsEmptyEnvAndHeaders pins CLI/REST shape
// parity for the FR-040 env/headers classification: the REST DTO
// (internal/httpapi/import.go ImportedServerResponse) tags Env/Headers
// `json:"...,omitempty"`, so a server with no env vars or headers omits both
// keys entirely rather than emitting them as JSON null. The CLI's `-o json`
// output must match — a schema-sensitive consumer of either surface should
// not see the key present-but-null on one and absent on the other for the
// same import.
func TestBuildImportedServersOutput_OmitsEmptyEnvAndHeaders(t *testing.T) {
	imported := []*configimport.ImportedServer{
		{
			Server: &config.ServerConfig{Name: "no-secrets", Command: "npx"},
			// EnvFields / HeaderFields left nil: nothing to classify.
		},
	}

	out := buildImportedServersOutput(imported)
	b, err := json.Marshal(out[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := decoded["env"]; ok {
		t.Errorf(`"env" key must be omitted when there are no env fields, got %s`, decoded["env"])
	}
	if _, ok := decoded["headers"]; ok {
		t.Errorf(`"headers" key must be omitted when there are no header fields, got %s`, decoded["headers"])
	}
}

// TestBuildImportedServersOutput_IncludesPopulatedEnvAndHeaders is the
// counterpart: when classification produced entries, both keys must still
// carry them.
func TestBuildImportedServersOutput_IncludesPopulatedEnvAndHeaders(t *testing.T) {
	imported := []*configimport.ImportedServer{
		{
			Server: &config.ServerConfig{Name: "with-secrets", Command: "npx"},
			EnvFields: []configimport.ImportedField{
				{Name: "API_KEY", SecretLike: true},
			},
			HeaderFields: []configimport.ImportedField{
				{Name: "Authorization", SecretLike: true},
			},
		},
	}

	out := buildImportedServersOutput(imported)
	envField, ok := out[0]["env"].([]configimport.ImportedField)
	if !ok || len(envField) != 1 {
		t.Errorf("expected env to carry 1 classified field, got %#v", out[0]["env"])
	}
	headerField, ok := out[0]["headers"].([]configimport.ImportedField)
	if !ok || len(headerField) != 1 {
		t.Errorf("expected headers to carry 1 classified field, got %#v", out[0]["headers"])
	}
}
