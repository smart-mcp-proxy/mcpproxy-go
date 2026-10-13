package httpapi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/smart-mcp-proxy/mcpproxy-go/oas"
)

// Spec 117 PR-2 (#1566): both approval request bodies accept expected_hashes
// absent, explicitly null (an unbound request), {} (bound to the empty
// snapshot) or populated. The published contract — swagger.yaml and the
// served docs.go template — must accept exactly what the handlers accept, so
// a schema-validating or generated client does not reject {"expected_hashes":
// null}, while strings, arrays and non-string hashes still fail.
func TestOAS_ExpectedHashesIsObjectOrNull(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "oas", "swagger.yaml"))
	require.NoError(t, err, "oas/swagger.yaml must be committed")
	var yamlDoc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(raw, &yamlDoc))

	var jsonDoc map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(oas.SwaggerInfo.ReadDoc()), &jsonDoc), "docs.go template must render valid JSON")

	sources := map[string]map[string]interface{}{
		"swagger.yaml": yamlDoc,
		"docs.go":      jsonDoc,
	}

	valid := []string{
		`{}`,
		`{"expected_hashes":null}`,
		`{"expected_hashes":{}}`,
		`{"expected_hashes":{"read_file":"rv2:abc"}}`,
	}
	invalid := []string{
		`{"expected_hashes":"rv2:abc"}`,
		`{"expected_hashes":["rv2:abc"]}`,
		`{"expected_hashes":{"read_file":1}}`,
		`{"expected_hashes":{"read_file":null}}`,
	}

	for srcName, doc := range sources {
		for _, schemaName := range []string{"httpapi.securityApproveRequest", "httpapi.approveToolsRequest"} {
			t.Run(srcName+"/"+schemaName, func(t *testing.T) {
				schema := compileComponentSchema(t, doc, schemaName)
				for _, body := range valid {
					inst, err := jsonschema.UnmarshalJSON(strings.NewReader(body))
					require.NoError(t, err)
					require.NoError(t, schema.Validate(inst), "%s must accept %s", schemaName, body)
				}
				for _, body := range invalid {
					inst, err := jsonschema.UnmarshalJSON(strings.NewReader(body))
					require.NoError(t, err)
					require.Error(t, schema.Validate(inst), "%s must reject %s", schemaName, body)
				}
			})
		}
	}
}

// compileComponentSchema compiles components.schemas[name] as a standalone
// JSON Schema 2020-12 document (OAS 3.1 schemas are JSON Schema). The two
// request bodies carry no $ref, so no resolution is needed.
func compileComponentSchema(t *testing.T, doc map[string]interface{}, name string) *jsonschema.Schema {
	t.Helper()
	components, _ := doc["components"].(map[string]interface{})
	schemas, _ := components["schemas"].(map[string]interface{})
	s, ok := schemas[name]
	require.True(t, ok, "schema %q must be documented — run 'make swagger'", name)

	// Round-trip through JSON so YAML-decoded values become JSON Schema values.
	buf, err := json.Marshal(s)
	require.NoError(t, err)
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(buf)))
	require.NoError(t, err)

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	url := fmt.Sprintf("mem://%s.schema.json", name)
	require.NoError(t, c.AddResource(url, inst))
	compiled, err := c.Compile(url)
	require.NoError(t, err)
	return compiled
}
