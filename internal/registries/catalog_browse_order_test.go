package registries

import (
	"fmt"
	"reflect"
	"testing"
)

// Spec 109 D35 (T164, demo finding #6): the empty-query Official section used
// to be the merged pool's first twelve official hits. The official source
// paginates alphabetically by reverse-DNS id, so it filled with obscure
// "ac.inference.sh/..." entries and the curated reference servers (filesystem,
// memory, fetch, ...) never appeared. Official is now curated-first, then the
// remaining official hits round-robin across sources in registry-list order.
// It is still never popularity-ordered (Spec 110: Popular must differ).

func hitsOf(source string, curated bool, ids ...string) []CatalogHit {
	out := make([]CatalogHit, 0, len(ids))
	for _, id := range ids {
		out = append(out, CatalogHit{
			Entry:    ServerEntry{ID: id, Name: id},
			Source:   source,
			Title:    id,
			Official: true,
			Verified: true,
			Curated:  curated,
		})
	}
	return out
}

func TestBuildSections_OfficialCuratedFirstThenRoundRobin(t *testing.T) {
	var official []string
	for _, c := range "abcdefghijklmn" {
		official = append(official, fmt.Sprintf("ac.%c", c))
	}
	// Merge order = registry-list order: official, reference, docker.
	var pool []CatalogHit
	pool = append(pool, hitsOf("official", false, official...)...)
	pool = append(pool, hitsOf("reference", true, "ref-1", "ref-2", "ref-3", "ref-4", "ref-5", "ref-6", "ref-7")...)
	pool = append(pool, hitsOf("docker", false, "dock-1", "dock-2", "dock-3")...)

	sections := buildSections(pool, "")
	got := idsOf(sections.Official)
	want := []string{
		"ref-1", "ref-2", "ref-3", "ref-4", "ref-5", "ref-6", "ref-7",
		"ac.a", "dock-1", "ac.b", "dock-2", "ac.c",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Official order\n got  %v\n want %v", got, want)
	}
}

func TestBuildSections_OfficialRoundRobinDrainsShortSourcesAndHonoursCap(t *testing.T) {
	var pool []CatalogHit
	pool = append(pool, hitsOf("a", false, "a1", "a2")...)
	pool = append(pool, hitsOf("b", false, "b1", "b2", "b3", "b4", "b5", "b6", "b7", "b8", "b9", "b10", "b11", "b12")...)
	got := idsOf(buildSections(pool, "").Official)
	want := []string{"a1", "b1", "a2", "b2", "b3", "b4", "b5", "b6", "b7", "b8", "b9", "b10"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestBuildSections_OfficialSkipsNonOfficialHits(t *testing.T) {
	pool := hitsOf("official", false, "o1")
	pool = append(pool, CatalogHit{Entry: ServerEntry{ID: "community"}, Source: "community", Official: false})
	got := idsOf(buildSections(pool, "").Official)
	if !reflect.DeepEqual(got, []string{"o1"}) {
		t.Fatalf("got %v", got)
	}
}

// Popular must stay different from Official even though Official is reordered:
// Official ignores popularity entirely.
func TestBuildSections_OfficialNeverPopularityOrdered(t *testing.T) {
	stars := func(n int) *Popularity { return &Popularity{Stars: &n} }
	pool := hitsOf("official", false, "low", "mid", "high")
	pool[0].Popularity = stars(1)
	pool[1].Popularity = stars(50)
	pool[2].Popularity = stars(9000)

	sections := buildSections(pool, "")
	if got := idsOf(sections.Official); !reflect.DeepEqual(got, []string{"low", "mid", "high"}) {
		t.Fatalf("Official must keep the source's native order, got %v", got)
	}
	if got := idsOf(sections.Popular); !reflect.DeepEqual(got, []string{"high", "mid", "low"}) {
		t.Fatalf("Popular must be popularity-ordered, got %v", got)
	}
}

func TestBuildCatalogHit_MarksTheCuratedReferenceSource(t *testing.T) {
	ref := &RegistryEntry{ID: "reference", Protocol: protocolReference, Provenance: "official"}
	other := &RegistryEntry{ID: "official", Provenance: "official"}
	if h := BuildCatalogHit(ref, ServerEntry{ID: "reference/filesystem"}); !h.Curated {
		t.Fatal("a hit from the built-in reference source must be Curated")
	}
	if h := BuildCatalogHit(other, ServerEntry{ID: "x"}); h.Curated {
		t.Fatal("a hit from any other source must not be Curated")
	}
}
