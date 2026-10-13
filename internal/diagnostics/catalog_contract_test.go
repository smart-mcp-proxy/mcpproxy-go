package diagnostics

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

// contractPath is the PUBLISHED output contract for `mcpproxy doctor
// list-codes -o json`. It sets additionalProperties:false, so any field added
// to CatalogEntry without a matching contract update makes the command emit a
// document that fails its own schema.
const contractPath = "../../specs/044-diagnostics-taxonomy/contracts/catalog-schema.json"

// TestCatalog_MatchesPublishedContract validates the real serialized catalog
// against the shipped JSON schema. Nothing in CI did this before, which is how
// the `retry` field (GH #1145) reached the published output without reaching
// the contract.
func TestCatalog_MatchesPublishedContract(t *testing.T) {
	abs, err := filepath.Abs(contractPath)
	require.NoError(t, err)
	raw, err := os.ReadFile(abs)
	require.NoError(t, err, "the published catalog contract must exist")

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	require.NoError(t, err)

	c := jsonschema.NewCompiler()
	require.NoError(t, c.AddResource("catalog-schema.json", doc))
	schema, err := c.Compile("catalog-schema.json")
	require.NoError(t, err)

	entries := All()
	require.NotEmpty(t, entries)

	for _, entry := range entries {
		t.Run(string(entry.Code), func(t *testing.T) {
			encoded, err := json.Marshal(entry)
			require.NoError(t, err)
			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
			require.NoError(t, err)
			require.NoError(t, schema.Validate(instance),
				"catalog entry does not satisfy the published contract:\n%s", encoded)
		})
	}
}
