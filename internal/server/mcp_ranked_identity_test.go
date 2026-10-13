package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 085 US1 T019 (⟲#6) — FR-007 / SC-002, RELEASE-BLOCKING.
//
// tool_response_mode is serialization-only: for EVERY query in the frozen
// 47-query golden set (specs/065-evaluation-foundation/datasets/
// retrieval_golden_v1.json) the ordered result-ID list must be identical
// between full and compact mode. Any mismatch means compaction leaked into
// the query/rank/top-k path and blocks the release. This is a US1 test, NOT
// deferred to the US5 profiler gate (which re-asserts it live).

const (
	goldenSetPath = "../../specs/065-evaluation-foundation/datasets/retrieval_golden_v1.json"
	corpusPath    = "../../specs/065-evaluation-foundation/datasets/corpus_v1.tools.json"
)

type goldenSet struct {
	CorpusVersion string        `json:"corpus_version"`
	Queries       []goldenQuery `json:"queries"`
}

type goldenQuery struct {
	ID    string `json:"id"`
	Query string `json:"query"`
}

type frozenCorpus struct {
	Tools []frozenTool `json:"tools"`
}

type frozenTool struct {
	Server      string `json:"server"`
	Tool        string `json:"tool"`
	ToolID      string `json:"tool_id"`
	Description string `json:"description"`
}

// seedFrozenCorpus indexes the spec-065 frozen 45-tool corpus (the corpus the
// golden set's labels reference) into the proxy's index. The corpus fixture
// carries no input schemas (schemas live in the 083 corpus_v2, absent from
// this tree until the rebase), which is irrelevant here: ranked identity is
// about result IDs and order, not schema payloads.
func seedFrozenCorpus(t *testing.T, proxy *MCPProxyServer) int {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(corpusPath))
	require.NoError(t, err, "frozen corpus fixture must exist")
	var corpus frozenCorpus
	require.NoError(t, json.Unmarshal(raw, &corpus))
	require.Len(t, corpus.Tools, 45, "corpus_v1 is frozen at 45 tools")

	servers := map[string]bool{}
	for _, tool := range corpus.Tools {
		if !servers[tool.Server] {
			servers[tool.Server] = true
			require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
				Name: tool.Server, Enabled: true,
			}))
		}
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name:        tool.ToolID,
			ServerName:  tool.Server,
			Description: tool.Description,
			ParamsJSON:  `{"type":"object","properties":{}}`,
			Hash:        "hash-" + tool.ToolID,
		}))
	}
	return len(corpus.Tools)
}

// rankedIDs runs one retrieve_tools call with the given detail override and
// returns the ordered result-ID list ("name" in full mode, "id" in compact).
func rankedIDs(t *testing.T, proxy *MCPProxyServer, query, detail string) []string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query":  query,
		"limit":  float64(15),
		"detail": detail,
	}
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	resp := decodeRetrieve(t, result)

	key := "name"
	if detail == config.ToolResponseModeCompact {
		key = "id"
	}
	ids := make([]string, 0, len(resp.Tools))
	for _, entry := range resp.Tools {
		id, ok := entry[key].(string)
		require.True(t, ok, "entry must carry a string %q field", key)
		ids = append(ids, id)
	}
	return ids
}

func TestRankedIDIdentity_FullGoldenSet(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedFrozenCorpus(t, proxy)

	raw, err := os.ReadFile(filepath.Clean(goldenSetPath))
	require.NoError(t, err, "golden set fixture must exist")
	var golden goldenSet
	require.NoError(t, json.Unmarshal(raw, &golden))
	require.Len(t, golden.Queries, 47, "retrieval_golden_v1 is frozen at 47 queries")
	require.Equal(t, "corpus_v1", golden.CorpusVersion)

	mismatches := 0
	nonEmpty := 0
	for _, q := range golden.Queries {
		q := q
		t.Run(q.ID, func(t *testing.T) {
			fullIDs := rankedIDs(t, proxy, q.Query, config.ToolResponseModeFull)
			compactIDs := rankedIDs(t, proxy, q.Query, config.ToolResponseModeCompact)
			if len(fullIDs) > 0 {
				nonEmpty++
			}
			if !assert.Equal(t, fullIDs, compactIDs,
				"query %q (%s): ranked result IDs must be identical between modes (SC-002 — release blocker)",
				q.Query, q.ID) {
				mismatches++
			}
		})
	}

	require.Zero(t, mismatches, "SC-002 gate: 100%% ranked-ID identity across the golden set — %d/%d queries mismatched", mismatches, len(golden.Queries))
	// Guard against a vacuous pass: the corpus must actually match a healthy
	// majority of golden queries (each golden query targets corpus tools).
	require.Greater(t, nonEmpty, 40, fmt.Sprintf("only %d/47 queries returned results — fixture indexing is broken", nonEmpty))
}
