package registries

import (
	"testing"
)

// Spec 109 fix-catalog-rank T169 (D36.5, D36.7, D36.9, D36.10): what the
// catalog hit says about itself. Verified is no longer "any built-in source".

func officialReg() *RegistryEntry {
	return &RegistryEntry{ID: "official", Name: "Official", Protocol: protocolOfficial, Provenance: "official"}
}

func TestNamespaceOwner(t *testing.T) {
	cases := []struct {
		id    string
		owner string
		ok    bool
	}{
		{"io.github.github/github-mcp-server", "github", true},
		{"com.notion/mcp", "notion", true},
		{"ai.smithery/Hint-Services-x", "smithery", true},
		{"com.quranmajeed.time/prayer-times", "quranmajeed", true}, // a subdomain label is not the publisher
		{"uk.co.acme/x", "acme", true},
		{"com.example.api.v2/x", "example", true},
		{"io.github/x", "", false},
		{"acme/github-fork", "", false}, // no dotted namespace: the registry-name fallback
		{"fetch", "", false},
		{"/x", "", false},
		{"io.github./x", "", false},
	}
	for _, c := range cases {
		owner, ok := namespaceOwner(c.id)
		if owner != c.owner || ok != c.ok {
			t.Errorf("namespaceOwner(%q) = %q,%v want %q,%v", c.id, owner, ok, c.owner, c.ok)
		}
	}
}

func TestBuildCatalogHit_VerifiedMeansPublisherOwnsRepo(t *testing.T) {
	custom := &RegistryEntry{ID: "c", Name: "C", Protocol: protocolOfficial, Provenance: "custom"}
	docker := &RegistryEntry{ID: "docker", Name: "Docker", Provenance: "official"}
	ref := &RegistryEntry{ID: "reference", Name: "Reference", Protocol: protocolReference, Provenance: "official"}
	cases := []struct {
		name  string
		reg   *RegistryEntry
		id    string
		repo  string
		wants bool
	}{
		{"github owns github", officialReg(), "io.github.github/github-mcp-server", "https://github.com/github/github-mcp-server", true},
		{"io.github owner case-insensitive", officialReg(), "io.github.Dave-London/github", "https://github.com/dave-london/mcp", true},
		{"io.github other owner", officialReg(), "io.github.rog0x/github", "https://github.com/someone-else/github", false},
		{"domain label inside owner", officialReg(), "com.notion/mcp", "https://github.com/makenotion/notion-mcp-server", true},
		{"re-publisher", officialReg(), "ai.smithery/Hint-Services-x", "https://github.com/Hint-Services/x", false},
		{"borrowed repo", officialReg(), "agency.ottobot/foo", "https://github.com/modelcontextprotocol/registry", false},
		{"short label never matches", officialReg(), "io.ab/foo", "https://github.com/cabinet/foo", false},
		{"no repository", officialReg(), "io.github.github/github-mcp-server", "", false},
		{"non-github repository", officialReg(), "com.notion/mcp", "https://gitlab.com/makenotion/x", false},
		{"untrusted source", custom, "io.github.github/github-mcp-server", "https://github.com/github/github-mcp-server", false},
		{"docker keeps IsTrusted", docker, "fetch", "", true},
		{"reference keeps IsTrusted", ref, "fetch", "", true},
	}
	for _, c := range cases {
		hit := BuildCatalogHit(c.reg, ServerEntry{ID: c.id, Name: c.id, SourceCodeURL: c.repo})
		if hit.Verified != c.wants {
			t.Errorf("%s: Verified = %v, want %v", c.name, hit.Verified, c.wants)
		}
		if hit.Official != c.reg.IsTrusted() {
			t.Errorf("%s: Official = %v must stay IsTrusted (D36.6)", c.name, hit.Official)
		}
	}
}

func TestBuildCatalogHit_TitleOrder(t *testing.T) {
	reg := officialReg()
	cases := []struct {
		name  string
		reg   *RegistryEntry
		entry ServerEntry
		want  string
	}{
		{"server.json title first", reg, ServerEntry{ID: "io.github.github/github-mcp-server", Name: "io.github.github/github-mcp-server", Title: "GitHub"}, "GitHub"},
		{"then the name segment", reg, ServerEntry{ID: "ai.smithery/smithery-ai-github", Name: "ai.smithery/smithery-ai-github"}, "smithery-ai-github"},
		{"flat source keeps its name", &RegistryEntry{ID: "flat", Name: "Flat"}, ServerEntry{ID: "acme/github-fork", Name: "GitHub (community fork)"}, "GitHub (community fork)"},
		{"then the id", &RegistryEntry{ID: "flat", Name: "Flat"}, ServerEntry{ID: "acme/x"}, "acme/x"},
		{"official entry without a slash keeps its name", reg, ServerEntry{ID: "plain", Name: "plain"}, "plain"},
	}
	for _, c := range cases {
		if got := BuildCatalogHit(c.reg, c.entry).Title; got != c.want {
			t.Errorf("%s: Title = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBuildCatalogHit_PlaceholderDescriptionBecomesEmpty(t *testing.T) {
	hit := BuildCatalogHit(officialReg(), ServerEntry{ID: "a.b/c", Name: "a.b/c", Description: noDescAvailable})
	if hit.Entry.Description != "" {
		t.Fatalf("Entry.Description = %q, want empty (D36.9)", hit.Entry.Description)
	}
	real := BuildCatalogHit(officialReg(), ServerEntry{ID: "a.b/c", Name: "a.b/c", Description: "real"})
	if real.Entry.Description != "real" {
		t.Fatalf("a real description must stay, got %q", real.Entry.Description)
	}
}

func TestToCatalogResult_DescriptionEmptyForPlaceholder(t *testing.T) {
	hit := BuildCatalogHit(officialReg(), ServerEntry{ID: "a.b/c", Name: "a.b/c", Description: noDescAvailable})
	if got := ToCatalogResult(hit, false).Description; got != "" {
		t.Fatalf("CatalogResult.Description = %q, want empty", got)
	}
}

func TestBuildCatalogHit_StarsIgnoredWhenPublisherDoesNotOwnRepo(t *testing.T) {
	stub := &stubPopularityProvider{stars: map[string]int{"modelcontextprotocol/registry": 5000, "github/github-mcp-server": 21000}}
	defer SetPopularityProviderForTest(stub)()

	borrowed := BuildCatalogHit(officialReg(), ServerEntry{
		ID: "agency.ottobot/foo", Name: "agency.ottobot/foo",
		SourceCodeURL: "https://github.com/modelcontextprotocol/registry",
	})
	if borrowed.Popularity != nil {
		t.Fatalf("borrowed stars must not count, got %+v", borrowed.Popularity)
	}
	// Re-applying after a Resolve must not leak them either.
	applyCachedStars(&borrowed)
	if borrowed.Popularity != nil {
		t.Fatalf("applyCachedStars leaked borrowed stars: %+v", borrowed.Popularity)
	}

	own := BuildCatalogHit(officialReg(), ServerEntry{
		ID: "io.github.github/github-mcp-server", Name: "io.github.github/github-mcp-server",
		SourceCodeURL: "https://github.com/github/github-mcp-server",
	})
	if own.Popularity == nil || own.Popularity.Stars == nil || *own.Popularity.Stars != 21000 {
		t.Fatalf("the publisher's own repo keeps its stars, got %+v", own.Popularity)
	}

	// An ineligible hit still keeps its source-native signal.
	installs := 7
	native := BuildCatalogHit(officialReg(), ServerEntry{
		ID: "agency.ottobot/bar", Name: "agency.ottobot/bar",
		SourceCodeURL: "https://github.com/modelcontextprotocol/registry",
		Popularity:    &Popularity{Installs: &installs},
	})
	if native.Popularity == nil || native.Popularity.Installs == nil || native.Popularity.Stars != nil {
		t.Fatalf("source-native installs stay, stars do not, got %+v", native.Popularity)
	}
}
