// Package corpusio loads public tool-retrieval corpora into the bench types
// (spec 083, US3). Loaders are strict and deterministic: every record is
// validated, output slices are sorted by stable IDs, and seeded subsetting is
// reproducible across runs and machines (FR-011/012/013/014, FR-010).
package corpusio

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// Cache file names written by scripts/fetch-toolret.sh into
// bench/results/cache/toolret/<revision>/ (research D5). The cache is
// runtime-only and never committed: the ToolRet dataset's license is unstated
// upstream (FR-013).
const (
	toolRetToolsFile   = "tools.json"
	toolRetQueriesFile = "queries.json"
)

// toolRetTool is one record of the cache tools.json (converted from the
// mangopy/ToolRet-Tools parquet schema: `id: string`, `documentation: string`;
// `config` is the HF config name — code|customized|web — added by the fetch
// script for provenance).
type toolRetTool struct {
	ID            string `json:"id"`
	Config        string `json:"config"`
	Documentation string `json:"documentation"`
}

type toolRetToolsEnvelope struct {
	Dataset  string        `json:"dataset"`
	Revision string        `json:"revision"`
	Tools    []toolRetTool `json:"tools"`
}

// toolRetLabel is one flattened relevance judgement (the upstream `labels`
// column is a JSON string of {id, doc, relevance}; the fetch script drops the
// bulky redundant `doc` and keeps {id, relevance}).
type toolRetLabel struct {
	ID        string `json:"id"`
	Relevance int    `json:"relevance"`
}

// toolRetQuery is one record of the cache queries.json (converted from the
// mangopy/ToolRet-Queries parquet schema: id/query/instruction/labels/category
// — all strings upstream).
type toolRetQuery struct {
	ID          string         `json:"id"`
	Config      string         `json:"config"`
	Category    string         `json:"category"`
	Query       string         `json:"query"`
	Instruction string         `json:"instruction"`
	Labels      []toolRetLabel `json:"labels"`
}

type toolRetQueriesEnvelope struct {
	Dataset string `json:"dataset"`
	// Revision is the pinned HF revision of mangopy/ToolRet-Queries.
	Revision string `json:"revision"`
	// DroppedEmptyQueries counts upstream rows the fetch script excluded for
	// failing per-record validation (one such row exists at the pinned
	// revision: mnms_query_17 has empty query text).
	DroppedEmptyQueries int            `json:"dropped_empty_queries"`
	Queries             []toolRetQuery `json:"queries"`
}

// ToolRet is the loaded ToolRet benchmark: corpus + golden set plus the
// provenance every report row needs (FR-011/012).
type ToolRet struct {
	// Corpus maps every ToolRet tool to a bench.Tool: ToolID/Name = upstream
	// id, Server = HF config (code|customized|web), Description = the full
	// `documentation` text (opaque — ToolRet docs are heterogeneous JSON/text,
	// NOT MCP input schemas, so Schema stays nil). Version =
	// "toolret-tools@<revision>".
	Corpus *bench.Corpus
	// Golden maps every scoreable query to a bench.GoldenQuery. Query is the
	// upstream `query` text only; the ToolRet `instruction` (its
	// instruction-following retrieval facet) is intentionally NOT concatenated
	// — the BM25 funnel under test receives what an agent would send, and the
	// limitation is documented here rather than hidden in the text.
	//
	// Relevance-grade mapping (FR-012): ToolRet label relevance is an integer
	// (value 1 = relevant at the pinned revision — the dataset is binary);
	// grades are mapped by IDENTITY onto the bench graded scale, so any
	// upstream grade >= 1 feeds NDCG gain unchanged, and labels with
	// relevance < 1 are dropped (bench semantics: 0/absent = irrelevant).
	Golden *bench.GoldenSet
	// ToolsRevision / QueriesRevision are the pinned HF revisions stamped by
	// the fetch script.
	ToolsRevision   string
	QueriesRevision string
	// DroppedUpstream echoes the fetch script's own per-record drop count
	// (dropped_empty_queries in the cache envelope).
	DroppedUpstream int
	// DroppedUnscoreable counts queries dropped at load because no label
	// survived the relevance >= 1 filter (unscoreable by every bench metric).
	DroppedUnscoreable int
}

// LoadToolRet reads a fetch-script cache directory
// (bench/results/cache/toolret/<revision>/) and maps it to bench types.
// Validation is per-record and fail-fast: missing IDs, empty text, duplicate
// IDs, dangling label references, and missing revision stamps are all errors
// with the offending record named (FR-011).
func LoadToolRet(cacheDir string) (*ToolRet, error) {
	var toolsEnv toolRetToolsEnvelope
	if err := readJSON(filepath.Join(cacheDir, toolRetToolsFile), &toolsEnv); err != nil {
		return nil, err
	}
	var queriesEnv toolRetQueriesEnvelope
	if err := readJSON(filepath.Join(cacheDir, toolRetQueriesFile), &queriesEnv); err != nil {
		return nil, err
	}
	if toolsEnv.Revision == "" {
		return nil, fmt.Errorf("toolret cache %s: missing revision stamp — re-run scripts/fetch-toolret.sh", toolRetToolsFile)
	}
	if queriesEnv.Revision == "" {
		return nil, fmt.Errorf("toolret cache %s: missing revision stamp — re-run scripts/fetch-toolret.sh", toolRetQueriesFile)
	}
	if len(toolsEnv.Tools) == 0 {
		return nil, fmt.Errorf("toolret cache %s: contains no tools", toolRetToolsFile)
	}
	if len(queriesEnv.Queries) == 0 {
		return nil, fmt.Errorf("toolret cache %s: contains no queries", toolRetQueriesFile)
	}

	version := "toolret-tools@" + toolsEnv.Revision

	// Tools → Corpus (validated, then sorted by stable tool ID).
	corpus := &bench.Corpus{Version: version, Tools: make([]bench.Tool, 0, len(toolsEnv.Tools))}
	seenTools := make(map[string]bool, len(toolsEnv.Tools))
	for i, tl := range toolsEnv.Tools {
		if tl.ID == "" {
			return nil, fmt.Errorf("toolret tools[%d]: missing id", i)
		}
		if strings.TrimSpace(tl.Documentation) == "" {
			return nil, fmt.Errorf("toolret tool %q: empty documentation", tl.ID)
		}
		if seenTools[tl.ID] {
			return nil, fmt.Errorf("toolret tools: duplicate tool id %q", tl.ID)
		}
		seenTools[tl.ID] = true
		server := tl.Config
		if server == "" {
			server = "toolret"
		}
		corpus.Tools = append(corpus.Tools, bench.Tool{
			ToolID:      tl.ID,
			Server:      server,
			Name:        tl.ID,
			Description: tl.Documentation,
		})
	}
	sort.Slice(corpus.Tools, func(i, j int) bool { return corpus.Tools[i].ToolID < corpus.Tools[j].ToolID })

	// Queries → GoldenSet (validated, relevance-filtered, sorted by query ID;
	// labels sorted by tool ID within each query).
	golden := &bench.GoldenSet{CorpusVersion: version, Queries: make([]bench.GoldenQuery, 0, len(queriesEnv.Queries))}
	seenQueries := make(map[string]bool, len(queriesEnv.Queries))
	droppedUnscoreable := 0
	for i, q := range queriesEnv.Queries {
		if q.ID == "" {
			return nil, fmt.Errorf("toolret queries[%d]: missing id", i)
		}
		if strings.TrimSpace(q.Query) == "" {
			return nil, fmt.Errorf("toolret query %q: empty query text", q.ID)
		}
		if seenQueries[q.ID] {
			return nil, fmt.Errorf("toolret queries: duplicate query id %q", q.ID)
		}
		seenQueries[q.ID] = true

		labels := make([]bench.Label, 0, len(q.Labels))
		for j, lab := range q.Labels {
			if lab.ID == "" {
				return nil, fmt.Errorf("toolret query %q labels[%d]: missing id", q.ID, j)
			}
			if !seenTools[lab.ID] {
				return nil, fmt.Errorf("toolret query %q: label references unknown tool %q (not in tools.json — tools/queries revisions out of sync?)", q.ID, lab.ID)
			}
			if lab.Relevance < 1 {
				continue // bench semantics: relevance 0/absent = irrelevant
			}
			labels = append(labels, bench.Label{ToolID: lab.ID, Relevance: lab.Relevance})
		}
		if len(labels) == 0 {
			droppedUnscoreable++
			continue
		}
		sort.Slice(labels, func(a, b int) bool { return labels[a].ToolID < labels[b].ToolID })
		golden.Queries = append(golden.Queries, bench.GoldenQuery{ID: q.ID, Query: q.Query, Labels: labels})
	}
	sort.Slice(golden.Queries, func(i, j int) bool { return golden.Queries[i].ID < golden.Queries[j].ID })
	if len(golden.Queries) == 0 {
		return nil, fmt.Errorf("toolret cache %s: no scoreable queries (every query lost all labels to the relevance >= 1 filter)", toolRetQueriesFile)
	}

	return &ToolRet{
		Corpus:             corpus,
		Golden:             golden,
		ToolsRevision:      toolsEnv.Revision,
		QueriesRevision:    queriesEnv.Revision,
		DroppedUpstream:    queriesEnv.DroppedEmptyQueries,
		DroppedUnscoreable: droppedUnscoreable,
	}, nil
}

// SubsetQueries returns a deterministic seeded subset of size queries
// (FR-014): the input queries are first sorted by ID (so the caller's order
// cannot leak into the selection), a math/rand permutation with the fixed
// seed picks the subset, and the result is re-sorted by ID. Identical
// revision + seed + size therefore always yields the identical subset. When
// size >= len(queries) the full sorted set is returned.
func SubsetQueries(golden *bench.GoldenSet, seed int64, size int) (*bench.GoldenSet, error) {
	if golden == nil || len(golden.Queries) == 0 {
		return nil, fmt.Errorf("subset: golden set is empty")
	}
	if size <= 0 {
		return nil, fmt.Errorf("subset: size must be positive, got %d", size)
	}

	sorted := make([]bench.GoldenQuery, len(golden.Queries))
	copy(sorted, golden.Queries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	out := &bench.GoldenSet{CorpusVersion: golden.CorpusVersion}
	if size >= len(sorted) {
		out.Queries = sorted
		return out, nil
	}

	// math/rand with a fixed seed (not crypto): reproducible sampling over the
	// sorted slice is the whole point (FR-014).
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic benchmark subsetting, not security
	picked := rng.Perm(len(sorted))[:size]
	sort.Ints(picked)
	out.Queries = make([]bench.GoldenQuery, 0, size)
	for _, idx := range picked {
		out.Queries = append(out.Queries, sorted[idx])
	}
	return out, nil
}

// readJSON reads and unmarshals one cache file with actionable errors.
func readJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("toolret cache file %s not found — run scripts/fetch-toolret.sh first: %w", filepath.Base(path), err)
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
