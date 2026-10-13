// LiveMCPTool loader: converts the committed Apache-2.0 frozen snapshot
// (specs/083-discovery-profiler/datasets/livemcptool_snapshot/) into the
// bench-native Corpus for token/scale measurement (Spec 083 FR-011b/013,
// research D6). Package doc lives in toolret.go.

package corpusio

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// LiveMCPTool corpus provenance, surfaced in the report's CorpusDescriptor
// rows (FR-012/013). Full provenance in the snapshot's ATTRIBUTION.md.
const (
	// LiveMCPToolLicense is the license of the LiveMCPTool corpus.
	LiveMCPToolLicense = "Apache-2.0"
	// LiveMCPToolAttribution credits the corpus source (paper arXiv:2508.01780).
	LiveMCPToolAttribution = "LiveMCPTool corpus from LiveMCPBench (ICIP/LiveMCPBench, icip-cas), arXiv:2508.01780"
	// LiveMCPToolSourceURL is the canonical dataset location.
	LiveMCPToolSourceURL = "https://huggingface.co/datasets/ICIP/LiveMCPBench"
)

// liveMCPToolGoldenAbsence explains why the loader returns no golden set
// (FR-011 explicit absence). The 95 LiveMCPBench task annotations list the
// tools their reference solutions used only as unqualified free-text names
// inside "Annotator Metadata" — of the 150 distinct names, 5 resolve to no
// tool in the corpus and 13 resolve to multiple servers (e.g. read_file,
// search_files), so per-tool relevance labels cannot be derived without
// guessing. Analysis details: ATTRIBUTION.md next to the snapshot.
const liveMCPToolGoldenAbsence = "relevance labels not derived: LiveMCPBench task annotations name tools as unqualified free text (5/150 names resolve to no corpus tool, 13/150 to multiple servers); deriving a golden set would require guessing server attribution (FR-011 SHOULD not met)"

// liveMCPToolSnapshot mirrors the normalized committed snapshot file
// (specs/083-discovery-profiler/datasets/livemcptool_snapshot/tools.json).
type liveMCPToolSnapshot struct {
	Version     string              `json:"version"`
	Source      json.RawMessage     `json:"source,omitempty"` // provenance block, not consumed by the loader
	ServerCount int                 `json:"server_count"`
	ToolCount   int                 `json:"tool_count"`
	Tools       []liveMCPToolRecord `json:"tools"`
}

// liveMCPToolRecord is one normalized tool row: server, tool, description,
// inputSchema when present.
type liveMCPToolRecord struct {
	Server      string          `json:"server"`
	Tool        string          `json:"tool"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// LoadLiveMCPTool reads the committed LiveMCPTool frozen snapshot and converts
// it into the bench-native Corpus for token/scale measurement (FR-011b).
//
// Return shape: (corpus, goldenSet, goldenAbsenceReason, err). The golden set
// is always nil for this corpus — relevance labels are not derivable from the
// LiveMCPBench task annotations — and the reason string records that absence
// explicitly, per FR-011. Callers surfacing retrieval quality must skip this
// corpus and may print the reason.
//
// Validation is strict and per-record: empty server/tool names, duplicate
// server:tool pairs, invalid schema JSON, and count drift against the
// snapshot's own server_count/tool_count header all fail with the offending
// record index (edge case: silent tool-count drift must warn loudly — here it
// is an error because the snapshot is frozen and self-describing).
//
// Determinism (FR-010/021): tool order is the snapshot's committed order
// (canonically sorted by server:tool at capture) and schemas are compacted to
// canonical bytes at load, mirroring bench.LoadCorpusV2.
func LoadLiveMCPTool(path string) (*bench.Corpus, *bench.GoldenSet, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, "", fmt.Errorf("read livemcptool snapshot %q: %w", path, err)
	}
	var snap liveMCPToolSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, nil, "", fmt.Errorf("parse livemcptool snapshot %q: %w", path, err)
	}
	if snap.Version == "" {
		return nil, nil, "", fmt.Errorf("livemcptool snapshot %q: missing version stamp", path)
	}
	if len(snap.Tools) == 0 {
		return nil, nil, "", fmt.Errorf("livemcptool snapshot %q contains no tools", path)
	}

	corpus := &bench.Corpus{
		Version: snap.Version,
		Tools:   make([]bench.Tool, 0, len(snap.Tools)),
	}
	servers := make(map[string]bool, snap.ServerCount)
	seen := make(map[string]bool, len(snap.Tools))
	for i, rec := range snap.Tools {
		if rec.Server == "" {
			return nil, nil, "", fmt.Errorf("livemcptool snapshot %q: record %d: empty server name", path, i)
		}
		if rec.Tool == "" {
			return nil, nil, "", fmt.Errorf("livemcptool snapshot %q: record %d: empty tool name", path, i)
		}
		toolID := rec.Server + ":" + rec.Tool
		if seen[toolID] {
			return nil, nil, "", fmt.Errorf("livemcptool snapshot %q: record %d: duplicate tool %s", path, i, toolID)
		}
		seen[toolID] = true
		servers[rec.Server] = true

		schema := rec.InputSchema
		if len(schema) > 0 {
			// Canonicalize at the ingestion boundary (sorted keys, compact,
			// verbatim numbers) so CountToolWithSchema and the arm renderers
			// count identical bytes (contract parity, research D7b).
			canon, err := bench.CanonicalJSON(schema)
			if err != nil {
				return nil, nil, "", fmt.Errorf("livemcptool snapshot %q: record %d (%s): invalid inputSchema JSON: %w", path, i, toolID, err)
			}
			schema = json.RawMessage(canon)
		}
		corpus.Tools = append(corpus.Tools, bench.Tool{
			ToolID:      toolID,
			Server:      rec.Server,
			Name:        rec.Tool,
			Description: rec.Description,
			Schema:      schema,
		})
	}

	if snap.ToolCount != len(corpus.Tools) {
		return nil, nil, "", fmt.Errorf("livemcptool snapshot %q: tool_count header says %d but %d records present — snapshot drifted; regenerate and update ATTRIBUTION.md", path, snap.ToolCount, len(corpus.Tools))
	}
	if snap.ServerCount != len(servers) {
		return nil, nil, "", fmt.Errorf("livemcptool snapshot %q: server_count header says %d but %d distinct servers present — snapshot drifted; regenerate and update ATTRIBUTION.md", path, snap.ServerCount, len(servers))
	}

	return corpus, nil, liveMCPToolGoldenAbsence, nil
}
