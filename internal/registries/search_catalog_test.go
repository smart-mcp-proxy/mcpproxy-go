package registries

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Spec 109 fix-catalog-rank T174 (D37.3): a typed catalog query fetches the
// whole filtered result for ranking instead of truncating to `limit` in the
// registry's own order. The exported per-registry SearchServers contract is
// unchanged.

func flatGithubServers(n int) string {
	items := make([]string, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, fmt.Sprintf(`{"id":"acme/s%03d","name":"github tool %03d","description":"d"}`, i, i))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestSearchCatalogSource_NoTruncationBeforeRank(t *testing.T) {
	for _, tc := range []struct{ matches, want int }{{80, 80}, {400, typedFetchCap}} {
		srv := jsonServer(t, flatGithubServers(tc.matches))
		reg := &RegistryEntry{ID: "flat", Name: "Flat", ServersURL: srv.URL}
		got, err := searchCatalogSource(context.Background(), reg, "github")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != tc.want {
			t.Errorf("%d matches -> %d entries, want %d", tc.matches, len(got), tc.want)
		}
		for _, e := range got {
			if e.Registry != "Flat" {
				t.Fatalf("Registry = %q, want the source name", e.Registry)
			}
		}
	}
	if typedFetchCap != 300 {
		t.Errorf("typedFetchCap = %d, want 300 (D37.3)", typedFetchCap)
	}
}

func TestSearchCatalogSource_OfficialUsesExpansion(t *testing.T) {
	reg, counter := newOfficialFixtureRegistry(t, nil)
	got, err := searchCatalogSource(context.Background(), reg, "github")
	if err != nil {
		t.Fatal(err)
	}
	if len(counter.searches) != 3 {
		t.Fatalf("requests = %v, want main + 2 expansions", counter.searches)
	}
	if len(got) == 0 || got[0].ID != "io.github.github/github-mcp-server" {
		t.Fatalf("the owner hit must be in the fetched set and lead it, got %v", entryIDs(got)[:3])
	}
}

func TestSearchCatalogSource_PhraseQueryMatchesHyphenatedNames(t *testing.T) {
	reg, _ := newOfficialFixtureRegistry(t, nil)
	got, err := searchCatalogSource(context.Background(), reg, "github actions")
	if err != nil {
		t.Fatal(err)
	}
	ids := strings.Join(entryIDs(got), ",")
	if !strings.Contains(ids, "io.github.ofershap/github-actions") {
		t.Fatalf("a multi-word q must match names whose words are hyphenated, got %s", ids)
	}
}

func TestSearchCatalogSource_MissingKeyAndNoEndpoint(t *testing.T) {
	if _, err := searchCatalogSource(context.Background(), &RegistryEntry{ID: "x", Name: "X"}, "q"); err == nil {
		t.Fatal("a source with no servers endpoint must error")
	}
}

func TestFilterServers_MatchesTitle(t *testing.T) {
	servers := []ServerEntry{
		{ID: "a/one", Name: "a/one", Title: "GitHub", Description: "x"},
		{ID: "a/two", Name: "a/two", Description: "y"},
	}
	got := filterServers(servers, "", "github")
	if len(got) != 1 || got[0].ID != "a/one" {
		t.Fatalf("filterServers must match the title, got %+v", got)
	}
}

func TestMatchCachedEntry_MatchesTitle(t *testing.T) {
	e := ServerEntry{ID: "a/one", Name: "a/one", Title: "GitHub"}
	if !matchCachedEntry(&e, "github") {
		t.Fatal("matchCachedEntry must match the title")
	}
}

func TestSearchServers_PerRegistryContractUnchanged(t *testing.T) {
	srv := jsonServer(t, flatGithubServers(80))
	withTestRegistries(t, []RegistryEntry{{ID: "flat", Name: "Flat", ServersURL: srv.URL}})

	got, err := SearchServers(context.Background(), "flat", "", "github", 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 || got[0].ID != "acme/s000" || got[9].ID != "acme/s009" {
		t.Fatalf("per-registry search keeps limit and registry order, got %d (%s..)", len(got), got[0].ID)
	}
	got, err = SearchServers(context.Background(), "flat", "", "github", 500, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 {
		t.Fatalf("limit is still capped at 50, got %d", len(got))
	}
}

func TestMatchCachedEntry_TokensAndSeparators(t *testing.T) {
	e := &ServerEntry{ID: "io.github.acme/github-actions", Name: "github-actions", Description: "Run CI_jobs"}
	cases := []struct {
		q    string
		want bool
	}{
		{"github actions", true},
		{"github-actions", true},
		{"Actions GITHUB", true},
		{"ci jobs", true},
		{"github gitlab", false},
		{"  ", true},
	}
	for _, c := range cases {
		if got := matchCachedEntry(e, c.q); got != c.want {
			t.Errorf("matchCachedEntry(%q) = %v want %v", c.q, got, c.want)
		}
	}
}
