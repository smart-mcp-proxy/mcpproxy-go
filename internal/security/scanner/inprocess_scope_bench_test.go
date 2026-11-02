package scanner

import (
	"fmt"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// benchPeerFleet builds a fleet shaped like the install behind the "high CPU
// while idle" report: 11 servers, ~471 tools, realistic descriptions and
// schemas. The first server is the one whose changed tools are scanned.
func benchPeerFleet(servers, toolsPerServer int) (string, []*config.ToolMetadata, map[string][]*config.ToolMetadata) {
	mk := func(server string, i int) *config.ToolMetadata {
		return &config.ToolMetadata{
			ServerName: server,
			Name:       fmt.Sprintf("%s_tool_%d", server, i),
			RawName:    fmt.Sprintf("%s_tool_%d", server, i),
			Description: fmt.Sprintf("Retrieve, list or update %s records number %d. Returns a paginated list of items with their identifiers, titles, "+
				"owners, timestamps and status. Use the cursor parameter to fetch the next page. Supports filtering by workspace, "+
				"project and label; sorting by created or updated time.", server, i),
			ParamsJSON: fmt.Sprintf(`{"type":"object","properties":{"query":{"type":"string","description":"Free-text search for %s %d"},`+
				`"cursor":{"type":"string","description":"Pagination cursor from the previous response"},`+
				`"limit":{"type":"integer","minimum":1,"maximum":100,"default":25},`+
				`"filter":{"type":"object","properties":{"workspace":{"type":"string"},"project":{"type":"string"},"labels":{"type":"array","items":{"type":"string"}}}},`+
				`"sort":{"type":"string","enum":["created_asc","created_desc","updated_asc","updated_desc"]}},"required":["query"]}`, server, i),
			OutputSchemaJSON: `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},"status":{"type":"string"}}}},"next_cursor":{"type":"string"}}}`,
		}
	}
	target := "notion"
	var targetTools []*config.ToolMetadata
	for i := 0; i < toolsPerServer; i++ {
		targetTools = append(targetTools, mk(target, i))
	}
	peers := make(map[string][]*config.ToolMetadata)
	for s := 1; s < servers; s++ {
		name := fmt.Sprintf("peer%d", s)
		for i := 0; i < toolsPerServer; i++ {
			peers[name] = append(peers[name], mk(name, i))
		}
	}
	return target, targetTools, peers
}

// BenchmarkScanToolMetadataVerdict_PerToolWithPeers is one discovery-pass scan
// of a single held tool with the full peer fleet as context, which the runtime
// repeats once per held tool on every pass.
func BenchmarkScanToolMetadataVerdict_PerToolWithPeers(b *testing.B) {
	target, tools, peers := benchPeerFleet(11, 43)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		verdict, _, ok := ScanToolMetadataVerdict(target, tools[i%len(tools):i%len(tools)+1], peers)
		if verdict != "clean" || !ok {
			b.Fatalf("verdict=%s ok=%v", verdict, ok)
		}
	}
}
