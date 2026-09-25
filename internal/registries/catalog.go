// Catalog (Spec 109 FR-060-067, data-model §9): a source-agnostic,
// ranked view over every enabled registry ("catalog source"), fanned out in
// parallel and merged into one ordered list. This is the shared backend for
// GET /catalog/search, the CLI `catalog` command group and MCP
// `search_servers` with `registry` omitted.
package registries

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secretlike"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwords"
)

// Popularity carries a source's own popularity signal — stars for a
// GitHub-hosted server, install/download counts otherwise. Either or both may
// be nil; a source that reports neither leaves Popularity itself nil.
type Popularity struct {
	Stars    *int `json:"stars,omitempty"`
	Installs *int `json:"installs,omitempty"`
}

// CatalogHit is the internal ranked catalog result (Go only, never
// marshalled): it keeps the existing registries.ServerEntry untouched so
// GET /registries/{id}/servers and MCP search_servers keep serving its JSON
// unchanged (contracts/rest-api.md#catalog).
type CatalogHit struct {
	Entry      ServerEntry
	Source     string
	Title      string
	Publisher  string
	Verified   bool
	Official   bool
	Popularity *Popularity
}

// CatalogInstall is the REST/MCP install target: either a remote URL or a
// local command + args, never both (data-model §9).
type CatalogInstall struct {
	URL     string   `json:"url,omitempty"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

// CatalogInput is the REST DTO for one required input (FR-061), folding the
// D13 name heuristic into SecretLike so a registry that omits or falsifies its
// own isSecret flag still defaults the field to Secret (FR-065).
type CatalogInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SecretLike  bool   `json:"secret_like"`
}

// CatalogResult is the GET /catalog/search response DTO (data-model §9),
// built from a CatalogHit by ToCatalogResult. It is a distinct type from
// ServerEntry so that type's JSON shape (url, installCmd, registry,
// required_inputs[].secret) is never renamed.
type CatalogResult struct {
	Source         string         `json:"source"`
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	Publisher      string         `json:"publisher,omitempty"`
	Verified       bool           `json:"verified"`
	Official       bool           `json:"official"`
	Popularity     *Popularity    `json:"popularity,omitempty"`
	Description    string         `json:"description"`
	Transport      string         `json:"transport"`
	Install        CatalogInstall `json:"install"`
	RequiredInputs []CatalogInput `json:"required_inputs,omitempty"`
	SourceCodeURL  string         `json:"source_code_url,omitempty"`
	Added          bool           `json:"added"`
}

// CatalogSections groups the empty-query landing results (FR-060): the
// official-source hits and the top-popularity hits across every source, each
// capped at catalogSectionCap. Internal (CatalogHit, not the REST DTO) —
// callers convert with ToCatalogResult once they know each entry's Added
// state (FR-007).
type CatalogSections struct {
	Official []CatalogHit
	Popular  []CatalogHit
}

// SourceError records a catalog source that failed or timed out, surfaced as
// GET /catalog/search's unavailable[] (FR-060).
type SourceError struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

// SearchOptions configures a catalog search. SourceTimeout defaults to 5s;
// only tests set another value (T109a, SC-011).
type SearchOptions struct {
	SourceTimeout time.Duration
}

const (
	defaultCatalogSourceTimeout = 5 * time.Second
	catalogSectionCap           = 12
	defaultCatalogLimit         = 10
	maxCatalogLimit             = 50
)

// SearchAll fans SearchServers out to every enabled registry in parallel
// (FR-060), each bounded by opts.SourceTimeout (default 5s), merges the
// results, de-duplicates by (source, id), ranks them with Rank, and returns
// unavailable[] for sources that failed or timed out. An empty q additionally
// populates sections (official + popular, ≤ 12 each); a non-empty q leaves
// sections nil.
func SearchAll(ctx context.Context, q, tag string, limit int, opts SearchOptions) ([]CatalogHit, *CatalogSections, []SourceError) {
	timeout := opts.SourceTimeout
	if timeout <= 0 {
		timeout = defaultCatalogSourceTimeout
	}
	if limit <= 0 {
		limit = defaultCatalogLimit
	}
	if limit > maxCatalogLimit {
		limit = maxCatalogLimit
	}

	sources := ListRegistries()

	type sourceOutcome struct {
		hits        []CatalogHit
		unavailable *SourceError
	}
	outcomes := make([]sourceOutcome, len(sources))

	var wg sync.WaitGroup
	for i := range sources {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reg := sources[i]
			sctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			entries, err := SearchServers(sctx, reg.ID, tag, q, limit, nil)
			if err != nil {
				reason := err.Error()
				if sctx.Err() != nil {
					reason = fmt.Sprintf("timeout after %s", timeout)
				}
				outcomes[i] = sourceOutcome{unavailable: &SourceError{Source: reg.ID, Reason: reason}}
				return
			}

			hits := make([]CatalogHit, 0, len(entries))
			for _, e := range entries {
				hits = append(hits, BuildCatalogHit(&reg, e))
			}
			outcomes[i] = sourceOutcome{hits: hits}
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool)
	var all []CatalogHit
	var unavailable []SourceError
	for _, outcome := range outcomes {
		if outcome.unavailable != nil {
			unavailable = append(unavailable, *outcome.unavailable)
			continue
		}
		for _, h := range outcome.hits {
			key := h.Source + "\x00" + h.Entry.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, h)
		}
	}

	sort.SliceStable(all, func(i, j int) bool { return Rank(all[i], all[j], q) })
	sort.SliceStable(unavailable, func(i, j int) bool { return unavailable[i].Source < unavailable[j].Source })

	if len(all) > limit {
		all = all[:limit]
	}

	var sections *CatalogSections
	if strings.TrimSpace(q) == "" {
		sections = buildSections(all)
	}

	return all, sections, unavailable
}

// BuildCatalogHit derives the catalog-only fields (Title, Publisher,
// Verified, Official, Popularity) from a registry entry. Verified currently
// tracks Official — no registry in this spec supplies an independent
// publisher-verification signal yet, so a trusted (built-in) source's
// namespace is the only verification evidence available (data-model §9,
// research D12). Exported so a single-entry lookup (CLI `catalog show`) can
// build the same CatalogHit shape SearchAll uses internally.
func BuildCatalogHit(reg *RegistryEntry, entry ServerEntry) CatalogHit {
	official := reg.IsTrusted()
	title := entry.Name
	if title == "" {
		title = entry.ID
	}
	return CatalogHit{
		Entry:     entry,
		Source:    reg.ID,
		Title:     title,
		Publisher: derivePublisher(entry.ID, reg.Name),
		Verified:  official,
		Official:  official,
	}
}

// derivePublisher extracts a display publisher from an official-protocol
// reverse-DNS id such as "io.github.github/github-mcp-server" (→ "github").
// Falls back to the registry's own name when the id carries no such
// namespace.
func derivePublisher(id, registryName string) string {
	slash := strings.IndexByte(id, '/')
	if slash <= 0 {
		return registryName
	}
	namespace := id[:slash]
	if dot := strings.LastIndexByte(namespace, '.'); dot >= 0 && dot+1 < len(namespace) {
		return namespace[dot+1:]
	}
	return registryName
}

// Rank is the pure, deterministic catalog ordering (data-model §9,
// contracts/rest-api.md#catalog): official desc, verified desc, popularity
// desc (missing = 0), text relevance desc, title asc, id asc. It reports
// whether a sorts strictly before b.
func Rank(a, b CatalogHit, q string) bool {
	if a.Official != b.Official {
		return a.Official
	}
	if a.Verified != b.Verified {
		return a.Verified
	}
	if ap, bp := popularityScore(a.Popularity), popularityScore(b.Popularity); ap != bp {
		return ap > bp
	}
	if ar, br := relevanceScore(a, q), relevanceScore(b, q); ar != br {
		return ar > br
	}
	at, bt := strings.ToLower(catalogTitle(a)), strings.ToLower(catalogTitle(b))
	if at != bt {
		return at < bt
	}
	return a.Entry.ID < b.Entry.ID
}

func catalogTitle(h CatalogHit) string {
	if h.Title != "" {
		return h.Title
	}
	if h.Entry.Name != "" {
		return h.Entry.Name
	}
	return h.Entry.ID
}

func popularityScore(p *Popularity) int {
	if p == nil {
		return 0
	}
	score := 0
	if p.Stars != nil {
		score += *p.Stars
	}
	if p.Installs != nil {
		score += *p.Installs
	}
	return score
}

// relevanceScore is a simple, deterministic token-match count of q against
// title/id/description — good enough to break ties below popularity, never
// used as the primary key.
func relevanceScore(h CatalogHit, q string) int {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return 0
	}
	score := 0
	title := strings.ToLower(catalogTitle(h))
	id := strings.ToLower(h.Entry.ID)
	desc := strings.ToLower(h.Entry.Description)
	if title == q {
		score += 100
	}
	if strings.Contains(title, q) {
		score += 10
	}
	if strings.Contains(id, q) {
		score += 5
	}
	if strings.Contains(desc, q) {
		score++
	}
	return score
}

// buildSections splits the already-ranked merged hits into the empty-query
// landing sections (FR-060): official-source hits (in rank order) and the
// top-popularity hits across every source, each capped at 12.
func buildSections(ranked []CatalogHit) *CatalogSections {
	sections := &CatalogSections{Official: []CatalogHit{}, Popular: []CatalogHit{}}
	for _, h := range ranked {
		if h.Official && len(sections.Official) < catalogSectionCap {
			sections.Official = append(sections.Official, h)
		}
	}

	byPopularity := append([]CatalogHit(nil), ranked...)
	sort.SliceStable(byPopularity, func(i, j int) bool {
		return popularityScore(byPopularity[i].Popularity) > popularityScore(byPopularity[j].Popularity)
	})
	for i := 0; i < len(byPopularity) && i < catalogSectionCap; i++ {
		sections.Popular = append(sections.Popular, byPopularity[i])
	}
	return sections
}

// ToCatalogResult builds the REST DTO from an internal hit. added is computed
// by the caller (httpapi layer, FR-007 scoped join) — SearchAll itself has no
// notion of the calling caller's visibility.
func ToCatalogResult(h CatalogHit, added bool) CatalogResult {
	install, transport := toCatalogInstall(h.Entry)

	var inputs []CatalogInput
	for _, in := range DetectRequiredInputs(&h.Entry) {
		inputs = append(inputs, CatalogInput{
			Name:        in.Name,
			Description: in.Description,
			SecretLike:  in.Secret || secretlike.LooksSecret(in.Name),
		})
	}

	return CatalogResult{
		Source:         h.Source,
		ID:             h.Entry.ID,
		Title:          catalogTitle(h),
		Publisher:      h.Publisher,
		Verified:       h.Verified,
		Official:       h.Official,
		Popularity:     h.Popularity,
		Description:    h.Entry.Description,
		Transport:      transport,
		Install:        install,
		RequiredInputs: inputs,
		SourceCodeURL:  h.Entry.SourceCodeURL,
		Added:          added,
	}
}

// toCatalogInstall derives the transport + install target from a
// ServerEntry: a URL (or ConnectURL) is remote/http; otherwise InstallCmd is
// split into command + args for a local/stdio install (data-model §9).
func toCatalogInstall(entry ServerEntry) (CatalogInstall, string) {
	url := entry.URL
	if url == "" {
		url = entry.ConnectURL
	}
	if url != "" {
		return CatalogInstall{URL: url}, "http"
	}
	if entry.InstallCmd != "" {
		parts, err := shellwords.Split(entry.InstallCmd)
		if err == nil && len(parts) > 0 {
			return CatalogInstall{Command: parts[0], Args: parts[1:]}, "stdio"
		}
	}
	return CatalogInstall{}, "stdio"
}

// CatalogInstallTarget returns a stable string identifying an install's
// target (a URL, or a command + args), used to join a catalog entry against a
// configured server (contracts/rest-api.md#catalog "added"): a manually added
// server matches on this alone, a catalog-added one also needs a matching
// source_registry_id.
func CatalogInstallTarget(install CatalogInstall) string {
	if install.URL != "" {
		return "url:" + install.URL
	}
	return "cmd:" + install.Command + " " + strings.Join(install.Args, " ")
}
