package preflight

import (
	"sort"
	"strings"
)

// MaxSuggestions caps `did_you_mean` at three entries (FR-013): enough to fix a
// typo, too few to enumerate a corpus.
const MaxSuggestions = 3

// maxEditDistance is the Levenshtein budget for a near-miss. Two edits catches
// realistic typos (transposition, a dropped or doubled character, a wrong
// suffix) without turning the list into a fuzzy search.
const maxEditDistance = 2

// Suggest returns up to MaxSuggestions candidate ids nearest to want.
//
// Candidates MUST already be filtered to what the caller may see: in-scope,
// non-quarantined servers only (a quarantined server's tools are never
// suggested — FR-013 — and they are not indexed in the first place). The
// function itself performs no filtering and no I/O; it is pure string work, so
// the visibility decision stays in one place (the evaluator's corpus builder).
//
// Ranking: prefix relationships first (a truncated or over-typed id is the most
// common miss), then smaller edit distance, then lexicographic order for a
// deterministic response. Exact matches are never suggested — if the id existed
// the caller would not be seeing not_found.
func Suggest(want string, candidates []string) []string {
	want = strings.TrimSpace(want)
	if want == "" || len(candidates) == 0 {
		return nil
	}

	type scored struct {
		id     string
		prefix bool
		dist   int
	}
	var hits []scored
	seen := make(map[string]struct{}, len(candidates))
	for _, cand := range candidates {
		if cand == "" || cand == want {
			continue
		}
		if _, dup := seen[cand]; dup {
			continue
		}
		seen[cand] = struct{}{}

		prefix := strings.HasPrefix(cand, want) || strings.HasPrefix(want, cand)
		dist := levenshtein(want, cand, maxEditDistance)
		if !prefix && dist > maxEditDistance {
			continue
		}
		hits = append(hits, scored{id: cand, prefix: prefix, dist: dist})
	}
	if len(hits) == 0 {
		return nil
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].prefix != hits[j].prefix {
			return hits[i].prefix
		}
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].id < hits[j].id
	})

	if len(hits) > MaxSuggestions {
		hits = hits[:MaxSuggestions]
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.id)
	}
	return out
}

// levenshtein computes the edit distance between a and b, giving up (returning
// maxDist+1) once every cell of the current row exceeds maxDist. Operates on
// runes so multi-byte names are not scored by their UTF-8 length.
func levenshtein(a, b string, maxDist int) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	// Length difference alone already exceeds the budget.
	if diff := len(ra) - len(rb); diff > maxDist || -diff > maxDist {
		return maxDist + 1
	}

	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		rowMin := curr[0]
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
			if curr[j] < rowMin {
				rowMin = curr[j]
			}
		}
		if rowMin > maxDist {
			return maxDist + 1
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}
