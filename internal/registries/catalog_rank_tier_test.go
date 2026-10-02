package registries

import (
	"sort"
	"testing"
)

// Spec 109 fix-catalog-rank T176 (D37.1): the relevance tier ranks first.

func tierHit(id, title, desc string) CatalogHit {
	pub := derivePublisher(id, "Reg")
	return CatalogHit{Source: "s", Title: title, Publisher: pub, Entry: ServerEntry{ID: id, Name: id, Description: desc}}
}

func TestMatchTier_Table(t *testing.T) {
	cases := []struct {
		name string
		hit  CatalogHit
		q    string
		want int
	}{
		{"owner equals q", tierHit("io.github.github/github-mcp-server", "GitHub", ""), "github", 5},
		{"owner equals q, case", tierHit("io.github.github/x", "X", ""), "GitHub", 5},
		{"segment equals q", tierHit("com.mcparmory/github", "github", ""), "github", 4},
		{"title equals q", tierHit("io.github.rog0x/other", "GitHub", ""), "github", 4},
		{"segment starts with q at a token", tierHit("io.x.y/github-mcp-server", "github-mcp-server", ""), "github", 3},
		{"title starts with q", tierHit("a.b/zzz", "GitHub Actions helper", ""), "github", 3},
		{"token equals q", tierHit("io.github.x/obsidian-github-mcp", "obsidian-github-mcp", ""), "github", 2},
		{"prefix without a token boundary is only a substring", tierHit("a.b/githubx", "githubx", ""), "github", 1},
		{"substring of segment", tierHit("a.b/mygithubthing", "mygithubthing", ""), "github", 1},
		{"description only", tierHit("a.b/zzz", "zzz", "talks about GitHub issues"), "github", 1},
		{"namespace only", tierHit("io.github.06ketan/slideshot", "slideshot", ""), "github", 0},
		{"no match", tierHit("a.b/zzz", "zzz", ""), "github", 0},
		{"multi-word matches hyphenated names", tierHit("a.b/github-actions", "github-actions", ""), "github actions", 4},
		{"multi-word token window", tierHit("a.b/my-github-actions-tool", "my-github-actions-tool", ""), "github actions", 2},
		{"empty q", tierHit("a.b/github", "github", ""), "", 0},
		{"a subdomain label is not the owner", tierHit("com.quranmajeed.time/prayer-times", "prayer-times", ""), "time", 1},
		{"flat id owner is never set", tierHit("github/thing", "Thing", ""), "github", 0},
		{"registry-name publisher is never tier 5", CatalogHit{Publisher: "GitHub", Entry: ServerEntry{ID: "tool", Name: "tool"}}, "github", 0},
	}
	for _, c := range cases {
		if got := matchTier(c.hit, c.q); got != c.want {
			t.Errorf("%s: matchTier(%q) = %d, want %d", c.name, c.q, got, c.want)
		}
	}
}

func TestRank_RelevanceTierBeatsPopularity(t *testing.T) {
	popular := CatalogHit{Popularity: intPop(100), Entry: ServerEntry{ID: "zzz", Name: "Unrelated"}}
	relevant := CatalogHit{Popularity: intPop(1), Entry: ServerEntry{ID: "aaa", Name: "github tool"}}
	if !Rank(relevant, popular, "github") {
		t.Error("expected the name match to outrank a more popular non-match")
	}
	if Rank(popular, relevant, "github") {
		t.Error("expected the name match to outrank (reverse check)")
	}
}

func TestRank_OfficialBeatsVerifiedWithinATier(t *testing.T) {
	official := CatalogHit{Source: "official", Official: true, Entry: ServerEntry{ID: "z", Name: "Z"}}
	verifiedPopular := CatalogHit{Source: "smithery", Verified: true, Popularity: intPop(9999), Entry: ServerEntry{ID: "a", Name: "A"}}
	if !Rank(official, verifiedPopular, "") {
		t.Error("official must rank first within a tier")
	}
	// Across tiers the tier wins, even against an official source.
	exact := CatalogHit{Source: "custom", Entry: ServerEntry{ID: "c/github", Name: "github"}}
	prefixOfficial := CatalogHit{Source: "official", Official: true, Verified: true, Entry: ServerEntry{ID: "c/github-thing", Name: "github-thing"}}
	if !Rank(exact, prefixOfficial, "github") {
		t.Error("an exact name from any source must outrank an official prefix match")
	}
}

func TestRank_ExactNameBeatsTokenMatchAcrossSources(t *testing.T) {
	hits := []CatalogHit{
		{Source: "official", Official: true, Verified: true, Popularity: intPop(900), Title: "obsidian-github-mcp", Entry: ServerEntry{ID: "io.x.y/obsidian-github-mcp"}},
		{Source: "docker", Official: true, Verified: true, Popularity: &Popularity{Installs: intPtr(5)}, Title: "time", Entry: ServerEntry{ID: "time"}},
		{Source: "reference", Official: true, Verified: true, Curated: true, Title: "time", Entry: ServerEntry{ID: "time"}},
		{Source: "official", Official: true, Verified: true, Title: "mcp-time-server", Entry: ServerEntry{ID: "io.x.y/mcp-time-server"}},
	}
	sort.SliceStable(hits, func(i, j int) bool { return Rank(hits[i], hits[j], "time") })
	if hits[0].Title != "time" || hits[1].Title != "time" {
		t.Fatalf("both exact `time` hits must lead, got %s %s %s %s", hits[0].Title, hits[1].Title, hits[2].Title, hits[3].Title)
	}
	if hits[0].Source != "docker" {
		t.Errorf("within the tier popularity breaks the tie, got %s first", hits[0].Source)
	}
}

func TestRank_EmptyQueryUnchanged(t *testing.T) {
	hits := []CatalogHit{
		{Source: "c", Title: "Zed", Entry: ServerEntry{ID: "z"}},
		{Source: "o", Official: true, Title: "Beta", Entry: ServerEntry{ID: "b"}},
		{Source: "o", Official: true, Verified: true, Title: "Gamma", Entry: ServerEntry{ID: "g"}},
		{Source: "o", Official: true, Verified: true, Popularity: intPop(5), Title: "Delta", Entry: ServerEntry{ID: "d"}},
		{Source: "o", Official: true, Verified: true, Title: "Alpha", Entry: ServerEntry{ID: "a"}},
	}
	sort.SliceStable(hits, func(i, j int) bool { return Rank(hits[i], hits[j], "") })
	var got []string
	for _, h := range hits {
		got = append(got, h.Title)
	}
	want := []string{"Delta", "Alpha", "Gamma", "Beta", "Zed"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("empty-query order = %v, want %v (official, verified, popularity, title)", got, want)
		}
	}
}
