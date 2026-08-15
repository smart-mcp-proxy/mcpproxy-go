package preflight

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSuggest_PrefixAndNearMisses(t *testing.T) {
	candidates := []string{
		"gh:create_issue",
		"gh:create_issues",
		"gh:close_issue",
		"slack:post_message",
		"fs:read_file",
	}

	tests := []struct {
		name string
		want string
		out  []string
	}{
		{
			name: "one edit away, then two",
			want: "gh:create_isue",
			out:  []string{"gh:create_issue", "gh:create_issues"},
		},
		{
			name: "prefix of a longer id ranks first",
			want: "gh:create_issue",
			// exact matches are never suggested; the plural is a prefix extension
			out: []string{"gh:create_issues"},
		},
		{
			name: "no candidate within budget",
			want: "gh:totally_different_tool",
			out:  nil,
		},
		{
			name: "empty query",
			want: "",
			out:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.out, Suggest(tt.want, candidates))
		})
	}
}

func TestSuggest_CapsAtThreeAndIsDeterministic(t *testing.T) {
	candidates := []string{"gh:aa", "gh:ab", "gh:ac", "gh:ad", "gh:ae"}
	got := Suggest("gh:a", candidates)
	assert.Len(t, got, MaxSuggestions)
	assert.Equal(t, []string{"gh:aa", "gh:ab", "gh:ac"}, got, "ties break lexicographically for a stable response")

	// Repeated calls must not reorder.
	assert.Equal(t, got, Suggest("gh:a", candidates))
}

func TestSuggest_NeverSuggestsTheExactID(t *testing.T) {
	assert.Empty(t, Suggest("gh:sync", []string{"gh:sync"}))
}

func TestSuggest_DeduplicatesCandidates(t *testing.T) {
	got := Suggest("gh:syn", []string{"gh:sync", "gh:sync", "gh:sync"})
	assert.Equal(t, []string{"gh:sync"}, got)
}

func TestLevenshtein_BudgetedDistance(t *testing.T) {
	assert.Equal(t, 0, levenshtein("abc", "abc", 2))
	assert.Equal(t, 1, levenshtein("abc", "abd", 2))
	assert.Equal(t, 2, levenshtein("abc", "add", 2))
	assert.Greater(t, levenshtein("abc", "xyz", 2), 2, "over-budget distances give up early")
	assert.Equal(t, 3, levenshtein("", "abc", 2), "an empty side is just the other length")
	// Runes, not bytes: "café" -> "cafe" is ONE edit, though it is two bytes.
	assert.Equal(t, 1, levenshtein("café", "cafe", 2))
}

// The evaluator wires suggestions into not_found from the caller-visible corpus.
func TestEvaluate_NotFoundCarriesSuggestions(t *testing.T) {
	w := healthyWorld()
	res := evalOne(t, w, ToolRef{ID: "gh:sunc"})
	assert.Equal(t, ReasonNotFound, res.Reason)
	assert.Equal(t, []string{"gh:sync"}, res.DidYouMean)
}

// A miss on a server with no near neighbours simply carries none.
func TestEvaluate_NotFoundWithoutSuggestions(t *testing.T) {
	res := evalOne(t, healthyWorld(), ToolRef{ID: "gh:completely_unrelated"})
	assert.Equal(t, ReasonNotFound, res.Reason)
	assert.Empty(t, res.DidYouMean)
}

// Indexed names may carry the "server:tool" prefix or be bare; the corpus
// normalizes both to canonical ids.
func TestEvaluate_SuggestionCorpusNormalizesBareNames(t *testing.T) {
	w := healthyWorld()
	w.index.tools[srv] = []IndexedTool{{Name: tool}} // bare name in the index
	res := evalOne(t, w, ToolRef{ID: "gh:sunc"})
	assert.Equal(t, []string{"gh:sync"}, res.DidYouMean)
}
