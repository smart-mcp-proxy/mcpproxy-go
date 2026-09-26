package registries

import "testing"

// TestRank_OfficialBeatsEverything pins the primary sort key: official source
// wins regardless of everything else (data-model §9, contracts/rest-api.md#catalog).
func TestRank_OfficialBeatsEverything(t *testing.T) {
	official := CatalogHit{Source: "official", Official: true, Entry: ServerEntry{ID: "z", Name: "Z"}}
	verifiedPopular := CatalogHit{Source: "smithery", Verified: true, Popularity: intPop(9999), Entry: ServerEntry{ID: "a", Name: "A"}}

	if !Rank(official, verifiedPopular, "") {
		t.Error("expected official source to rank first")
	}
	if Rank(verifiedPopular, official, "") {
		t.Error("expected official source to rank first (reverse check)")
	}
}

// TestRank_VerifiedBeatsPopularity pins the secondary sort key.
func TestRank_VerifiedBeatsPopularity(t *testing.T) {
	verified := CatalogHit{Verified: true, Popularity: intPop(1), Entry: ServerEntry{ID: "b"}}
	popular := CatalogHit{Verified: false, Popularity: intPop(99999), Entry: ServerEntry{ID: "a"}}
	if !Rank(verified, popular, "") {
		t.Error("expected verified to outrank raw popularity")
	}
}

// TestRank_PopularityBeatsRelevance pins the third sort key.
func TestRank_PopularityBeatsRelevance(t *testing.T) {
	popular := CatalogHit{Popularity: intPop(100), Entry: ServerEntry{ID: "zzz", Name: "Unrelated"}}
	relevant := CatalogHit{Popularity: intPop(1), Entry: ServerEntry{ID: "aaa", Name: "github tool"}}
	if !Rank(popular, relevant, "github") {
		t.Error("expected popularity to outrank text relevance")
	}
}

// TestRank_RelevanceBeatsTitle pins the fourth key: a query match outranks
// alphabetical order.
func TestRank_RelevanceBeatsTitle(t *testing.T) {
	matches := CatalogHit{Entry: ServerEntry{ID: "z", Name: "github tool"}}
	noMatch := CatalogHit{Entry: ServerEntry{ID: "a", Name: "aardvark"}}
	if !Rank(matches, noMatch, "github") {
		t.Error("expected the query-matching title to outrank alphabetical order")
	}
}

// TestRank_TitleThenID pins the final tiebreakers for an otherwise-equal pair,
// including for the empty-query case (T004 order).
func TestRank_TitleThenID(t *testing.T) {
	a := CatalogHit{Title: "Alpha", Entry: ServerEntry{ID: "b"}}
	b := CatalogHit{Title: "Beta", Entry: ServerEntry{ID: "a"}}
	if !Rank(a, b, "") {
		t.Error("expected 'Alpha' to sort before 'Beta'")
	}

	c := CatalogHit{Title: "Same", Entry: ServerEntry{ID: "a"}}
	d := CatalogHit{Title: "Same", Entry: ServerEntry{ID: "b"}}
	if !Rank(c, d, "") {
		t.Error("expected id 'a' to sort before id 'b' when titles tie")
	}
}

// TestRank_MissingPopularityIsZero pins that an entry with no Popularity
// object never beats one with an explicit positive value, and never panics.
func TestRank_MissingPopularityIsZero(t *testing.T) {
	withPop := CatalogHit{Popularity: intPop(1), Entry: ServerEntry{ID: "b"}}
	noPop := CatalogHit{Entry: ServerEntry{ID: "a"}}
	if !Rank(withPop, noPop, "") {
		t.Error("expected the entry with popularity to outrank the one without")
	}
}

func intPop(stars int) *Popularity {
	return &Popularity{Stars: &stars}
}
