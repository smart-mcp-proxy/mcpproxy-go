package httpapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The 409 from POST /api/v1/connect/{client} carries a top-level `action`
// discriminator, and spec 091 makes it THE machine-readable signal telling
// "an entry is there, pass force" (already_exists) apart from "your view is
// stale, re-preview" (precondition_failed). A client generated from the OpenAPI
// document must therefore SEE that field: documenting the body as a plain
// error response leaves the discriminator visible only in prose, and a
// generated client that cannot read it either loops or forces a write over
// state the user never saw (research D9).
func TestOAS_ConnectConflictDocumentsTheActionDiscriminator(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "oas", "swagger.yaml"))
	require.NoError(t, err, "oas/swagger.yaml must be committed")

	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema map[string]interface{} `yaml:"schema"`
				} `yaml:"content"`
			} `yaml:"responses"`
		} `yaml:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]interface{} `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	conflict, ok := doc.Paths["/api/v1/connect/{client}"]["post"].Responses["409"]
	require.True(t, ok, "POST /api/v1/connect/{client} must document a 409")
	ref, _ := conflict.Content["application/json"].Schema["$ref"].(string)
	require.NotEmpty(t, ref, "the 409 body must reference a schema")

	name := filepath.Base(ref)
	schema, ok := doc.Components.Schemas[name]
	require.True(t, ok, "referenced schema %q must be defined", name)

	assert.Contains(t, schema.Properties, "action",
		"the 409 schema (%s) must expose the action discriminator the contract depends on", name)
	assert.Contains(t, schema.Properties, "data",
		"the 409 schema (%s) must expose the ConnectResult payload the handler writes", name)
	assert.Contains(t, schema.Properties, "error", "the 409 schema (%s) must expose the message", name)
	assert.Contains(t, schema.Properties, "success", "the 409 schema (%s) must expose success", name)
}
