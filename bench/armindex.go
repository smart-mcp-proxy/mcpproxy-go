// armindex.go — temp-dir retrieval index for arm-aware quality scoring
// (Spec 083 US2, FR-008, research D7).
//
// For index-altering arms the profiler must answer "does this encoding move
// recall?", which is only meaningful if the index under test IS the production
// index: same field mappings, same analyzers, same six-branch SearchTools
// query shape. So the builder funnels through the production
// internal/index.Manager (BatchIndexTools → BleveIndex.BatchIndex) and the
// SearchFunc calls the production search path (Manager.SearchTools →
// BleveIndex.SearchTools) — nothing here re-implements the mapping or the
// query. The SC-003 parity test (bench/armindex_test.go) proves the baseline
// arm through this funnel reproduces the recorded golden-set baseline
// (recall@5 = 0.68 ± 0.05) before any other arm's score is trusted.
package bench

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
)

// IndexMetadataFunc maps one corpus tool to the exact metadata the production
// retrieval index ingests for an arm. It is the bench-side shape of
// arms.Arm.EncodeIndexMetadata (bench cannot import bench/arms — arms imports
// bench — so the mapping crosses the boundary as a function value).
type IndexMetadataFunc func(t Tool) (config.ToolMetadata, error)

// ArmIndex is a scratch-directory retrieval index built through the
// production index code for one arm's encoding of one corpus. Close releases
// the underlying Bleve index; the scratch directory itself is owned by the
// caller (tests pass t.TempDir()).
type ArmIndex struct {
	mgr *index.Manager
}

// BuildArmIndex creates a fresh production index.Manager rooted at dir (the
// index lands in dir/index.bleve) and batch-indexes the given per-tool
// metadata — the output of an arm's EncodeIndexMetadata over a corpus. dir
// must be an empty scratch directory: an existing index there would be
// silently reused and contaminate the measurement.
func BuildArmIndex(dir string, metas []*config.ToolMetadata) (*ArmIndex, error) {
	if len(metas) == 0 {
		return nil, fmt.Errorf("build arm index: no tool metadata to index")
	}
	mgr, err := index.NewManager(dir, zap.NewNop())
	if err != nil {
		return nil, fmt.Errorf("build arm index at %q: %w", dir, err)
	}
	if err := mgr.BatchIndexTools(metas); err != nil {
		_ = mgr.Close()
		return nil, fmt.Errorf("batch-index %d tools at %q: %w", len(metas), dir, err)
	}
	return &ArmIndex{mgr: mgr}, nil
}

// SearchFunc returns the ScoreRetrieval adapter over the production search
// path: each query runs through Manager.SearchTools (the exact BM25 query
// shape retrieve_tools uses) and the hits are returned as ranked
// "server:tool" IDs — the golden set's tool_id format, matching how the live
// REST path assembles IDs (LiveClient.Search).
func (ai *ArmIndex) SearchFunc() SearchFunc {
	return func(query string, limit int) ([]string, error) {
		results, err := ai.mgr.SearchTools(query, limit)
		if err != nil {
			return nil, fmt.Errorf("arm index search %q: %w", query, err)
		}
		ranked := make([]string, 0, len(results))
		for _, r := range results {
			ranked = append(ranked, r.Tool.ServerName+":"+r.Tool.Name)
		}
		return ranked, nil
	}
}

// Close releases the underlying index.
func (ai *ArmIndex) Close() error {
	return ai.mgr.Close()
}
