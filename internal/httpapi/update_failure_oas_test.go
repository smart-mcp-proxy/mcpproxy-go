package httpapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The recording endpoint is the whole tray→core seam of spec 095, and its
// contract is the promise that the ONLY thing crossing it is a stage from a
// closed set (FR-009). A generated client must therefore see that enum, and
// the 204/400/500 trio must stay documented: the tray treats any non-2xx
// identically (FR-016), but the 204's durability promise (FR-011) is only
// meaningful if the 500 that protects it is part of the published contract.
func TestOAS_UpdateFailureDocumentsTheClosedStageEnum(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "oas", "swagger.yaml"))
	require.NoError(t, err, "oas/swagger.yaml must be committed")

	var doc struct {
		Paths map[string]map[string]struct {
			RequestBody struct {
				Content map[string]struct {
					Schema map[string]interface{} `yaml:"schema"`
				} `yaml:"content"`
			} `yaml:"requestBody"`
			Responses map[string]interface{} `yaml:"responses"`
		} `yaml:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Type string   `yaml:"type"`
					Enum []string `yaml:"enum"`
				} `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	op, ok := doc.Paths["/api/v1/telemetry/update-failure"]["post"]
	require.True(t, ok, "POST /api/v1/telemetry/update-failure must be documented — run 'make swagger'")

	for _, status := range []string{"204", "400", "500"} {
		assert.Contains(t, op.Responses, status, "the endpoint must document a %s response", status)
	}

	ref, _ := op.RequestBody.Content["application/json"].Schema["$ref"].(string)
	require.NotEmpty(t, ref, "the request body must reference a schema")

	schema, ok := doc.Components.Schemas[filepath.Base(ref)]
	require.True(t, ok, "referenced schema %q must be defined", filepath.Base(ref))

	require.Len(t, schema.Properties, 1, "the request body carries exactly one field (FR-009)")
	stage, ok := schema.Properties["stage"]
	require.True(t, ok, "the request body must expose 'stage'")
	assert.Equal(t, "string", stage.Type)
	assert.ElementsMatch(t, []string{"appcast", "download", "install", "other"}, stage.Enum,
		"the published enum must match the closed set the handler accepts")
}
