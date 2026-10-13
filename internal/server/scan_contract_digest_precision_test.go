package server

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/hash"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// TestScanExport_LargeIntegerSchemas_DigestMatchesStoredContract (Sol r8 P2
// assessment): the scan export decodes schemas into interface{} (float64
// numbers), but the stored-contract digest goes through hash.NormalizeJSON,
// which decodes the same way. Both sides therefore canonicalise a large
// integer identically, so an unchanged contract with integers beyond 2^53 is
// covered by a fresh scan rather than misclassified as uncovered.
func TestScanExport_LargeIntegerSchemas_DigestMatchesStoredContract(t *testing.T) {
	for _, tc := range []struct{ name, params, output string }{
		{"beyond 2^53", `{"type":"object","properties":{"n":{"type":"integer","maximum":9007199254740993}}}`,
			`{"type":"object","properties":{"id":{"type":"integer","minimum":12345678901234567891,"const":9223372036854775807}}}`},
		{"exponent range", `{"type":"object","properties":{"n":{"type":"number","maximum":100000000000000000000000}}}`,
			`{"type":"object","properties":{"v":{"type":"number","maximum":1e21,"minimum":-18446744073709551615}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := &config.ToolMetadata{Name: "big", Description: "returns big ids", ParamsJSON: tc.params, OutputSchemaJSON: tc.output}
			exported := toolMetadataToScanMap("srv", tool)
			digests := scanner.ToolContractDigests([]map[string]interface{}{exported})
			stored := hash.ToolContractDigest(tool.Description, tc.params, tc.output)
			require.Contains(t, digests["big"], stored,
				"the exported contract digest must equal the stored record's digest")
		})
	}
}
