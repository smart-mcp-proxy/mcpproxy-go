package arms

import (
	"encoding/json"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// TestArms_TrailingSchemaDataIsExplicitError verifies contract rule 2 at the
// top level of every arm schema parser: a schema with ANY trailing bytes after
// the first JSON value ('{} {}', '{}garbage', and the dec.More() blind spots
// '{}}' / '{}]') must be an explicit error, never a silently half-parsed
// schema.
func TestArms_TrailingSchemaDataIsExplicitError(t *testing.T) {
	badSchemas := []string{
		`{"type":"object"} {}`,
		`{"type":"object"}garbage`,
		`{"type":"object"}}`,
		`{"type":"object"}]`,
	}
	encoders := []struct {
		name string
		arm  interface {
			EncodeTool(bench.Tool) (string, error)
		}
	}{
		{name: "baseline_json", arm: NewBaseline()},
		{name: "compact_sig", arm: NewCompact()},
		{name: "toon_listing", arm: NewToonListing()},
	}
	for _, enc := range encoders {
		for _, bad := range badSchemas {
			tl := bench.Tool{ToolID: "s:bad", Server: "s", Name: "bad", Description: "d",
				Schema: json.RawMessage(bad)}
			if got, err := enc.arm.EncodeTool(tl); err == nil {
				t.Errorf("%s: EncodeTool with trailing schema data %q must fail, got %q", enc.name, bad, got)
			}
		}
	}
}
