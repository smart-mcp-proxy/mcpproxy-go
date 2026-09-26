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

	// Source narrows results to one catalog source id (GET
	// /catalog/search?source=). Applied BEFORE ranking/truncation to
	// `limit`, not after: an official/verified source's hits sort first by
	// design, so a post-hoc filter on an already-truncated top-`limit` list
	// could silently drop a narrower source's real matches that simply
	// didn't survive the pre-filter truncation.
	Source string

	// PopularityWait bounds how long SearchAll waits (Spec 110 FR-007) for a
	// background popularity fetch to land before returning. Zero — the Go
	// zero value, i.e. left unset — means the default (800ms), matching
	// every other *Timeout-shaped field in this struct (see SourceTimeout).
	// A NEGATIVE value disables the wait entirely: SearchAll still enqueues
	// the misses for background fetching, it just never blocks for them.
	// The wait never outlives ctx.
	PopularityWait time.Duration
}

const (
	defaultCatalogSourceTimeout = 5 * time.Second
	catalogSectionCap           = 12
	defaultCatalogLimit         = 10
	maxCatalogLimit             = 50

	// defaultPopularityWait is SearchOptions.PopularityWait's zero-value
	// default (Spec 110 FR-007): long enough for a warm cached/in-flight
	// GitHub round trip, short enough to never meaningfully slow a search.
	defaultPopularityWait = 800 * time.Millisecond
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

	empty := strings.TrimSpace(q) == ""

	// Spec 110 FR-005 (zcode review round 1, finding 2): an empty q fans out
	// each source at the per-source MAXIMUM, not the caller's `limit`, so the
	// section pool (Official/Popular) is as wide as each source will give —
	// otherwise a `limit` of 10 would starve Popular of anything beyond the
	// first 10 official hits before popularity ever gets a say. The final
	// `results` list is still truncated to `limit` below. A non-empty q keeps
	// fetching exactly `limit` per source (unchanged), since it has no
	// sections to populate.
	fetchLimit := limit
	if empty {
		fetchLimit = maxCatalogLimit
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

			entries, err := SearchServers(sctx, reg.ID, tag, q, fetchLimit, nil)
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
	// all is built and kept in MERGE order (registry list order, then each
	// source's native order) until buildSections has taken its snapshot
	// below — Spec 110 FR-005 (zcode review finding 1): Official must come
	// from a copy taken BEFORE any Rank sort, or it silently degrades back
	// into popularity order on an all-official default install (the exact
	// bug this spec fixes).
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

	if opts.Source != "" {
		all = filterHitsBySource(all, opts.Source)
	}

	// Spec 110 FR-002/007: resolve popularity (cache hits immediately, misses
	// queued for a bounded background wait) BEFORE ranking, so both the
	// Rank tiebreak (US2) and the Popular section see up-to-date signal. This
	// mutates each hit's Popularity in place but does not reorder `all`.
	resolvePopularity(ctx, all, q, popularityWait(opts.PopularityWait))

	var sections *CatalogSections
	if empty {
		// Built from `all` in its current MERGE order, before the Rank sort
		// below (FR-005).
		sections = buildSections(all, q)
	}

	sort.SliceStable(all, func(i, j int) bool { return Rank(all[i], all[j], q) })
	sort.SliceStable(unavailable, func(i, j int) bool { return unavailable[i].Source < unavailable[j].Source })

	if len(all) > limit {
		all = all[:limit]
	}

	return all, sections, unavailable
}

// popularityWait resolves SearchOptions.PopularityWait's documented
// convention: zero (unset) means the 800ms default; negative disables the
// wait entirely (fetches are still enqueued, SearchAll just never blocks on
// them).
func popularityWait(configured time.Duration) time.Duration {
	switch {
	case configured < 0:
		return 0
	case configured == 0:
		return defaultPopularityWait
	default:
		return configured
	}
}

// resolvePopularity is Spec 110's FR-007 hook: it asks the installed
// PopularityProvider (if any) to resolve every hit's GitHub repo key, waits
// up to `wait` for the background fetch to land, and re-applies whatever is
// now cached. A nil provider (no popularity wiring — e.g. most tests) is a
// fast no-op.
func resolvePopularity(ctx context.Context, hits []CatalogHit, q string, wait time.Duration) {
	provider := getPopularityProvider()
	if provider == nil {
		return
	}

	// FR-009(e): enqueue misses in the order SearchAll ranks them, so the
	// most relevant ones are fetched first. This priority copy is throwaway —
	// it never replaces `hits`' own order, which Official's merge-order
	// requirement (FR-005) depends on.
	priority := append([]CatalogHit(nil), hits...)
	sort.SliceStable(priority, func(i, j int) bool { return Rank(priority[i], priority[j], q) })

	seen := make(map[string]bool, len(priority))
	keys := make([]string, 0, len(priority))
	for _, h := range priority {
		key, ok := GitHubRepoKey(h.Entry.SourceCodeURL)
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		// Only Stale/Absent need a fetch (FR-008): a Fresh positive or a
		// still-fresh Negative (confirmed 404/451, or an error entry backing
		// off within its own shorter TTL) is left alone.
		if _, state := provider.Lookup(key); state == LookupStale || state == LookupAbsent {
			keys = append(keys, key)
		}
	}

	provider.Resolve(ctx, keys, wait)

	// Re-apply lookups: any key that landed during the bounded wait now shows
	// its stars; everything else is unchanged.
	for i := range hits {
		applyCachedStars(&hits[i])
	}
}

// filterHitsBySource keeps only the hits from one catalog source, applied
// before ranking/truncation (see SearchOptions.Source).
func filterHitsBySource(hits []CatalogHit, source string) []CatalogHit {
	out := make([]CatalogHit, 0, len(hits))
	for _, h := range hits {
		if h.Source == source {
			out = append(out, h)
		}
	}
	return out
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
	hit := CatalogHit{
		Entry:     entry,
		Source:    reg.ID,
		Title:     title,
		Publisher: derivePublisher(entry.ID, reg.Name),
		Verified:  official,
		Official:  official,
	}
	// FR-001/FR-002: copy the source-native signal (e.g. Docker pull_count)
	// first, then layer in GitHub stars from the provider's cache only — no
	// network call, so this stays synchronous.
	if entry.Popularity != nil {
		p := *entry.Popularity
		hit.Popularity = &p
	}
	applyCachedStars(&hit)
	return hit
}

// applyCachedStars fills in hit.Popularity.Stars from the installed
// PopularityProvider's cache ONLY (Spec 110 FR-002): no I/O, so both
// BuildCatalogHit and SearchAll's post-Resolve re-apply can call this freely.
// A nil provider, an entry with no GitHub-shaped SourceCodeURL, or a
// non-displayable lookup state (Absent/Negative) leave the hit unchanged.
func applyCachedStars(hit *CatalogHit) {
	key, ok := GitHubRepoKey(hit.Entry.SourceCodeURL)
	if !ok {
		return
	}
	provider := getPopularityProvider()
	if provider == nil {
		return
	}
	stars, state := provider.Lookup(key)
	if state != LookupFresh && state != LookupStale {
		// No displayable stars now. The hit may still carry stars from an
		// earlier apply (BuildCatalogHit saw them Stale, then the refresh
		// during Resolve's wait came back 404/451), so fall back to the
		// source-native value instead of leaving the old count in place.
		resetToSourceNativeStars(hit)
		return
	}
	if hit.Popularity == nil {
		hit.Popularity = &Popularity{}
	}
	s := stars
	hit.Popularity.Stars = &s
}

// resetToSourceNativeStars drops provider-supplied stars from hit, keeping
// only what the source itself reported (entry.Popularity).
func resetToSourceNativeStars(hit *CatalogHit) {
	if hit.Popularity == nil {
		return
	}
	var native *int
	if hit.Entry.Popularity != nil && hit.Entry.Popularity.Stars != nil {
		v := *hit.Entry.Popularity.Stars
		native = &v
	}
	hit.Popularity.Stars = native
	if hit.Popularity.Stars == nil && hit.Popularity.Installs == nil {
		hit.Popularity = nil
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
	if !popularityEqual(a.Popularity, b.Popularity) {
		return morePopular(a.Popularity, b.Popularity)
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

// popularityKey extracts the (stars, installs) tuple a Popularity compares
// on, with a nil pointer or nil field reading as 0 (Spec 110 FR-004).
func popularityKey(p *Popularity) (stars, installs int) {
	if p == nil {
		return 0, 0
	}
	if p.Stars != nil {
		stars = *p.Stars
	}
	if p.Installs != nil {
		installs = *p.Installs
	}
	return stars, installs
}

// morePopular reports whether a ranks strictly ABOVE b: stars desc, then
// installs desc (FR-004). Stars and installs are never summed or converted
// into one another — a lexicographic tuple compare, not a score.
func morePopular(a, b *Popularity) bool {
	as, ai := popularityKey(a)
	bs, bi := popularityKey(b)
	if as != bs {
		return as > bs
	}
	return ai > bi
}

// popularityEqual reports whether a and b compare equal under morePopular's
// ordering (both keys tie), the signal Rank and buildSections use to decide
// whether to fall through to the next sort key.
func popularityEqual(a, b *Popularity) bool {
	as, ai := popularityKey(a)
	bs, bi := popularityKey(b)
	return as == bs && ai == bi
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

// buildSections splits the merged, de-duplicated, source-filtered pool
// (BEFORE limit truncation and BEFORE any Rank sort) into the empty-query
// landing sections (Spec 110 FR-005, amending Spec 109 FR-060):
//
//   - Official: official-source hits in `pool`'s own MERGE (source-native)
//     order — registry-list order, then each source's native order. Pool
//     MUST NOT have been Rank-sorted yet: on an all-official default install
//     every hit ties on Official/Verified, so Rank order IS popularity order,
//     and building Official from a ranked pool silently reintroduces the bug
//     this spec fixes. Capped at 12.
//   - Popular: hits with a known signal (stars>0 ∨ installs>0), sorted by
//     popularity (FR-004) then Rank as a tiebreak, at most one per GitHub
//     repo key (a monorepo's shared star count keeps only the first by Rank —
//     spec.md edge cases), capped at 12.
func buildSections(pool []CatalogHit, q string) *CatalogSections {
	sections := &CatalogSections{Official: []CatalogHit{}, Popular: []CatalogHit{}}

	for _, h := range pool {
		if h.Official && len(sections.Official) < catalogSectionCap {
			sections.Official = append(sections.Official, h)
		}
	}

	candidates := make([]CatalogHit, 0, len(pool))
	for _, h := range pool {
		if stars, installs := popularityKey(h.Popularity); stars > 0 || installs > 0 {
			candidates = append(candidates, h)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if !popularityEqual(candidates[i].Popularity, candidates[j].Popularity) {
			return morePopular(candidates[i].Popularity, candidates[j].Popularity)
		}
		return Rank(candidates[i], candidates[j], q)
	})

	seenRepo := make(map[string]bool, len(candidates))
	for _, h := range candidates {
		if len(sections.Popular) >= catalogSectionCap {
			break
		}
		dedupKey, hasRepo := GitHubRepoKey(h.Entry.SourceCodeURL)
		if !hasRepo {
			// No GitHub repo to dedup against (e.g. a Docker-only pull-count
			// hit) — (source, id) already made this unique within `pool`.
			dedupKey = "no-repo:" + h.Source + "\x00" + h.Entry.ID
		}
		if seenRepo[dedupKey] {
			continue
		}
		seenRepo[dedupKey] = true
		sections.Popular = append(sections.Popular, h)
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
