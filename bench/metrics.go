package bench

import (
	"fmt"
	"math"
)

// Label is a graded relevance judgement for one tool against one query, taken
// from the Spec 065 retrieval golden set (relevance 2 = primary, 1 = related,
// 0 / absent = irrelevant).
type Label struct {
	ToolID    string `json:"tool_id"`
	Relevance int    `json:"relevance"`
}

// GoldenQuery is one labelled query -> relevant-tool(s) judgement.
type GoldenQuery struct {
	ID     string  `json:"id"`
	Query  string  `json:"query"`
	Labels []Label `json:"labels"`
}

// GoldenSet is the frozen Spec 065 retrieval golden set
// (retrieval_golden_v1.json).
type GoldenSet struct {
	CorpusVersion string        `json:"corpus_version"`
	Queries       []GoldenQuery `json:"queries"`
}

// relevanceOf returns the graded relevance of toolID for the given labels (0 if
// the tool is not a labelled relevant result).
func relevanceOf(toolID string, labels []Label) int {
	for _, l := range labels {
		if l.ToolID == toolID {
			return l.Relevance
		}
	}
	return 0
}

// relevantCount is the number of tools with relevance >= 1 for a query.
func relevantCount(labels []Label) int {
	n := 0
	for _, l := range labels {
		if l.Relevance >= 1 {
			n++
		}
	}
	return n
}

// RecallAtK is the fraction of the query's relevant tools (relevance >= 1) that
// appear in the top-k of the ranking. Returns 0 when there are no relevant
// tools (a degenerate query that should not be scored).
func RecallAtK(ranked []string, labels []Label, k int) float64 {
	total := relevantCount(labels)
	if total == 0 {
		return 0
	}
	hits := 0
	for i, id := range ranked {
		if i >= k {
			break
		}
		if relevanceOf(id, labels) >= 1 {
			hits++
		}
	}
	return float64(hits) / float64(total)
}

// ReciprocalRank is 1/rank of the first relevant tool in the ranking, or 0 if
// none of the ranked tools are relevant.
func ReciprocalRank(ranked []string, labels []Label) float64 {
	for i, id := range ranked {
		if relevanceOf(id, labels) >= 1 {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

// NDCGAtK is the normalized discounted cumulative gain at k using the graded
// relevance as the gain (linear gain, log2 position discount). 1.0 means the
// ranking is in ideal (relevance-descending) order; 0 means no gain in top-k.
func NDCGAtK(ranked []string, labels []Label, k int) float64 {
	dcg := 0.0
	for i, id := range ranked {
		if i >= k {
			break
		}
		rel := relevanceOf(id, labels)
		if rel == 0 {
			continue
		}
		dcg += float64(rel) / math.Log2(float64(i+2)) // position i (0-based) -> log2(i+2)
	}
	idcg := idealDCG(labels, k)
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// idealDCG is the DCG of the best possible ordering (relevances sorted
// descending) capped at k.
func idealDCG(labels []Label, k int) float64 {
	rels := make([]int, 0, len(labels))
	for _, l := range labels {
		if l.Relevance >= 1 {
			rels = append(rels, l.Relevance)
		}
	}
	// descending sort (small slice; insertion sort keeps deps minimal)
	for i := 1; i < len(rels); i++ {
		for j := i; j > 0 && rels[j] > rels[j-1]; j-- {
			rels[j], rels[j-1] = rels[j-1], rels[j]
		}
	}
	idcg := 0.0
	for i, rel := range rels {
		if i >= k {
			break
		}
		idcg += float64(rel) / math.Log2(float64(i+2))
	}
	return idcg
}

// AveragePrecision is the mean of the precision values computed at each rank
// where a relevant tool is retrieved, divided by the total number of relevant
// tools (so unretrieved relevant tools lower the score). Binary relevance
// (relevance >= 1) is used, matching the standard MAP definition.
func AveragePrecision(ranked []string, labels []Label) float64 {
	total := relevantCount(labels)
	if total == 0 {
		return 0
	}
	hits := 0
	sumPrec := 0.0
	for i, id := range ranked {
		if relevanceOf(id, labels) >= 1 {
			hits++
			sumPrec += float64(hits) / float64(i+1)
		}
	}
	return sumPrec / float64(total)
}

// SearchFunc replays one query through the retrieval system under test and
// returns the ranked tool IDs (most relevant first), limited to `limit`.
type SearchFunc func(query string, limit int) (ranked []string, err error)

// RetrievalMetricValues holds the aggregated metric numbers. It is the
// `retrieval.metrics` object of the Spec 065 score-report.schema.json contract.
type RetrievalMetricValues struct {
	RecallAt map[int]float64 `json:"recall_at"`
	MRR      float64         `json:"mrr"`
	NDCGAt10 float64         `json:"ndcg_at_10"`
	MAP      float64         `json:"map"`
}

// RetrievalGate is the `retrieval.gate` object of the score-report contract.
//
// A standalone live run has no stored baseline to regress against, so the gate
// cannot fail by construction: Passed is true and Metric/Tolerance are empty.
// Regression gating against a committed baseline is the CI lane's job (MCP-3133)
// — that run fills Metric/Tolerance and can set Passed=false.
type RetrievalGate struct {
	Passed    bool    `json:"passed"`
	Metric    string  `json:"metric,omitempty"`
	Tolerance float64 `json:"tolerance,omitempty"`
}

// RetrievalMetrics is the aggregated retrieval-quality report over a golden set.
// Its JSON shape IS the Spec 065 score-report.schema.json `retrieval` block
// (nested `metrics` + `gate`), so a live report's retrieval payload validates
// against that contract directly.
type RetrievalMetrics struct {
	CorpusVersion string                `json:"corpus_version"`
	GoldenVersion string                `json:"golden_version,omitempty"`
	RunsAveraged  int                   `json:"runs_averaged"`
	QueryCount    int                   `json:"query_count,omitempty"`
	Metrics       RetrievalMetricValues `json:"metrics"`
	Gate          RetrievalGate         `json:"gate"`
}

// ScoreRetrieval replays every golden query through search and aggregates
// Recall@k (for each k in ks), MRR, nDCG@10 and MAP as the mean over all
// queries. The search is deterministic (BM25), so a single run is averaged.
func ScoreRetrieval(golden *GoldenSet, search SearchFunc, ks []int) (*RetrievalMetrics, error) {
	if golden == nil || len(golden.Queries) == 0 {
		return nil, fmt.Errorf("golden set is empty")
	}
	// The largest k we must retrieve to score every requested cutoff and nDCG@10.
	maxK := 10
	for _, k := range ks {
		if k > maxK {
			maxK = k
		}
	}

	recallSum := make(map[int]float64, len(ks))
	var mrrSum, ndcgSum, mapSum float64
	for _, q := range golden.Queries {
		ranked, err := search(q.Query, maxK)
		if err != nil {
			return nil, fmt.Errorf("search %q: %w", q.ID, err)
		}
		for _, k := range ks {
			recallSum[k] += RecallAtK(ranked, q.Labels, k)
		}
		mrrSum += ReciprocalRank(ranked, q.Labels)
		ndcgSum += NDCGAtK(ranked, q.Labels, 10)
		mapSum += AveragePrecision(ranked, q.Labels)
	}

	n := float64(len(golden.Queries))
	recallAt := make(map[int]float64, len(ks))
	for _, k := range ks {
		recallAt[k] = recallSum[k] / n
	}
	return &RetrievalMetrics{
		CorpusVersion: golden.CorpusVersion,
		RunsAveraged:  1,
		QueryCount:    len(golden.Queries),
		Metrics: RetrievalMetricValues{
			RecallAt: recallAt,
			MRR:      mrrSum / n,
			NDCGAt10: ndcgSum / n,
			MAP:      mapSum / n,
		},
		// No baseline compared in a standalone live run, so the regression gate
		// cannot fail (see RetrievalGate). CI fills this in against a baseline.
		Gate: RetrievalGate{Passed: true},
	}, nil
}
