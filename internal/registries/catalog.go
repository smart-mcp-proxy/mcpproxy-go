// Catalog (Spec 109 FR-060-067, data-model §9): a source-agnostic,
// ranked view over every enabled registry ("catalog source"), fanned out in
// parallel and merged into one ordered list. This is the shared backend for
// GET /catalog/search, the CLI `catalog` command group and MCP
// `search_servers` with `registry` omitted.
package registries

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
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
	Entry     ServerEntry
	Source    string
	Title     string
	Publisher string
	Verified  bool
	Official  bool
	// Curated marks a hit from the built-in reference source (Spec 109 D35):
	// the shipped, hand-picked basics. buildSections lists them first in the
	// Official section. Pure data, set by BuildCatalogHit.
	Curated bool
	// FromCache marks a hit served from the per-source listing cache because
	// the source's live fetch failed (Spec 109 D35); see listing_cache.go.
	FromCache  bool
	Popularity *Popularity

	// starsBorrowed marks a hit whose GitHub stars must not be attributed to it
	// (Spec 109 D37.7: "stars eligible" is its negation, so the zero value stays
	// eligible for hand-built hits). An official-protocol entry may name ANY
	// GitHub repo as its source, so stars only count when the publisher owns
	// that repo (Verified); a hit that borrows another project's repo keeps only
	// its source-native signal. Set by BuildCatalogHit; never marshalled.
	starsBorrowed bool
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
	// AddedServerName identifies the one visible installed server that made
	// Added true. It is intentionally omitted when no unique match exists.
	// This lets clients open a credential-bearing install without attempting to
	// join against redacted GET /servers fields.
	AddedServerName string `json:"added_server_name,omitempty"`
	// FromCache is true when this hit came from the source's cached listing
	// because its live search failed. The source is also in unavailable[] with
	// fallback="cached_listing" (Spec 109 D35).
	FromCache bool `json:"from_cache,omitempty"`
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
	// Fallback is "cached_listing" when SearchAll answered for this source from
	// its cached listing instead (the hits carry FromCache); CachedAt is when
	// that listing was last refreshed. Both are empty when there was nothing to
	// fall back on (Spec 109 D35).
	Fallback string     `json:"fallback,omitempty"`
	CachedAt *time.Time `json:"cached_at,omitempty"`
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

// SearchAll fans the catalog fetch out to every enabled registry in parallel
// (FR-060), each bounded by opts.SourceTimeout (default 5s), merges the
// results, de-duplicates by (source, id), ranks them with Rank, and returns
// unavailable[] for sources that failed or timed out. An empty q additionally
// populates sections (official + popular, ≤ 12 each); a non-empty q leaves
// sections nil.
//
// A typed q is fetched in full (searchCatalogSource, ≤ typedFetchCap per
// source) and ranked BEFORE it is truncated to `limit` (Spec 109 D37.3): the
// official registry returns names in byte order, so truncating first would let
// its alphabet decide what the user can see. A fetch that outlives the budget
// finishes in the background and refreshes the listing cache (D37.11, see
// catalog_warm_behind.go).
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
	// `results` list is still truncated to `limit` below. A typed q has no
	// sections: it fetches every match (capped) and ranks before truncating.
	fetchLimit := maxCatalogLimit
	cachedCap := fetchLimit
	if !empty {
		cachedCap = typedFetchCap
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

			fetch := func(c context.Context, onPartial func([]ServerEntry, bool)) ([]ServerEntry, error) {
				if empty {
					return searchRegistry(c, &reg, tag, "", fetchLimit, nil)
				}
				return searchCatalogSourceProgress(c, &reg, q, onPartial)
			}
			res := fetchSourceWithinBudget(ctx, reg, timeout, fetch)
			if res.err != nil {
				reason := res.err.Error()
				if res.timedOut {
					reason = fmt.Sprintf("timeout after %s", timeout)
				}
				failure := &SourceError{Source: reg.ID, Reason: reason}
				// Live hits that arrived before the failure (the official
				// protocol's owner and name-prefix queries) still count, ahead
				// of the cached listing's matches.
				matches := make([]CatalogHit, 0, len(res.entries))
				live := make(map[string]bool, len(res.entries))
				for _, e := range res.entries {
					live[e.ID] = true
					matches = append(matches, BuildCatalogHit(&reg, e))
				}
				// Answer from the source's cached listing when the live fetch
				// failed for any reason except a missing API key (that source
				// never fetched, so nothing is cached for it). The source stays
				// in unavailable[]; the cached hits are marked FromCache.
				if !errors.Is(res.err, ErrRegistryKeyMissing) {
					if cached, at, ok := cachedListing(&reg); ok {
						cachedMatches := 0
						for i := range cached {
							if cachedMatches >= cachedCap {
								break
							}
							if live[cached[i].ID] || !matchCachedEntry(&cached[i], q) {
								continue
							}
							cached[i].Registry = reg.Name
							hit := BuildCatalogHit(&reg, cached[i])
							hit.FromCache = true
							matches = append(matches, hit)
							cachedMatches++
						}
						failure.Fallback = FallbackCachedListing
						failure.CachedAt = &at
					}
				}
				outcomes[i] = sourceOutcome{hits: matches, unavailable: failure}
				return
			}

			hits := make([]CatalogHit, 0, len(res.entries))
			for _, e := range res.entries {
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
	// mutates each hit's Popularity in place but does not reorder `all`. A
	// typed q enqueues at most `limit` keys (D37.7): the fetch is wide now, and
	// GitHub's unauthenticated budget is 50 requests an hour.
	maxKeys := 0
	if !empty {
		maxKeys = limit
	}
	resolvePopularity(ctx, all, q, popularityWait(opts.PopularityWait), maxKeys)

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
// fast no-op. maxKeys > 0 caps how many distinct repos are considered: only
// the top maxKeys eligible hits in Rank order are enqueued (Spec 109 D37.7);
// 0 means no cap (the empty-query landing, which needs stars across its pool).
func resolvePopularity(ctx context.Context, hits []CatalogHit, q string, wait time.Duration, maxKeys int) {
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
		if h.starsBorrowed {
			continue // borrowed repo: its stars are not this server's (D37.7)
		}
		key, ok := GitHubRepoKey(h.Entry.SourceCodeURL)
		if !ok || seen[key] {
			continue
		}
		if maxKeys > 0 && len(seen) >= maxKeys {
			break
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
// Verified, Official, Popularity) from a registry entry. Official means the
// hit came from a built-in (trusted) source and feeds Rank and the Official
// section (Spec 109 D37.6). Verified is narrower (D37.5): for a trusted
// official-protocol entry it means the publisher's namespace owns the source
// repository; for a trusted reference or Docker entry it stays "trusted
// source"; an untrusted source never verifies. Exported so a single-entry
// lookup (CLI `catalog show`) can build the same CatalogHit shape SearchAll
// uses internally.
func BuildCatalogHit(reg *RegistryEntry, entry ServerEntry) CatalogHit {
	official := reg.IsTrusted()
	verified := official
	starsBorrowed := false
	if reg.Protocol == protocolOfficial {
		verified = official && publisherOwnsRepo(entry.ID, entry.SourceCodeURL)
		starsBorrowed = !verified
	}
	// A parser's "No description available" is a placeholder, not a
	// description: surfaces print nothing for an empty one (D37.9).
	if entry.Description == noDescAvailable {
		entry.Description = ""
	}
	hit := CatalogHit{
		Entry:         entry,
		Source:        reg.ID,
		Title:         catalogHitTitle(reg, entry),
		Publisher:     derivePublisher(entry.ID, reg.Name),
		Verified:      verified,
		Official:      official,
		Curated:       reg.Protocol == protocolReference,
		starsBorrowed: starsBorrowed,
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

// catalogHitTitle picks the display title (Spec 109 D37.10): the source's own
// title, then for an official-protocol entry the name segment after the
// namespace ("github", not "io.github.github/github-mcp-server"), then the
// entry's name, then its id.
func catalogHitTitle(reg *RegistryEntry, entry ServerEntry) string {
	if entry.Title != "" {
		return entry.Title
	}
	if reg.Protocol == protocolOfficial {
		if i := strings.IndexByte(entry.ID, '/'); i >= 0 && i+1 < len(entry.ID) {
			return entry.ID[i+1:]
		}
	}
	if entry.Name != "" {
		return entry.Name
	}
	return entry.ID
}

// publisherOwnsRepo reports whether the namespace of an official-protocol id
// owns the GitHub repository it names as its source (Spec 109 D37.5): an
// `io.github.<x>` namespace needs repo owner x; a domain namespace needs its
// owner label (≥ 3 characters, e.g. "notion" of com.notion) to equal the repo
// owner, be a whole token of it, or be a ≥ 5 character brand with a ≤ 4
// character prefix/suffix on it (e.g. "makenotion"). A re-publisher of someone else's server, a
// borrowed repo URL or a missing repository never verifies.
func publisherOwnsRepo(id, sourceCodeURL string) bool {
	key, ok := GitHubRepoKey(sourceCodeURL)
	if !ok {
		return false
	}
	repoOwner := key[:strings.IndexByte(key, '/')]
	slash := strings.IndexByte(id, '/')
	if slash <= 0 {
		return false
	}
	namespace := strings.ToLower(id[:slash])
	if x, isGitHub := strings.CutPrefix(namespace, "io.github."); isGitHub {
		return x != "" && x == repoOwner
	}
	label, ok := namespaceOwner(id)
	label = strings.ToLower(label)
	return ok && domainLabelMatchesOwner(label, repoOwner)
}

// domainLabelMatchesOwner is the domain-namespace half of publisherOwnsRepo.
// A bare substring test let any short label verify against an unrelated owner
// ("hub" inside "github"), so the label must be the owner itself, a whole
// "-"/"_"-separated token of it ("acme" of acme-corp), or a brand of at least
// 5 characters with a short (at most 4 characters) prefix or suffix on the
// owner ("notion" of makenotion).
func domainLabelMatchesOwner(label, repoOwner string) bool {
	if len(label) < 3 {
		return false
	}
	if label == repoOwner {
		return true
	}
	for _, tok := range strings.FieldsFunc(repoOwner, func(r rune) bool { return r == '-' || r == '_' }) {
		if tok == label {
			return true
		}
	}
	if len(label) < 5 {
		return false
	}
	if rest, ok := strings.CutPrefix(repoOwner, label); ok && len(rest) <= 4 {
		return true
	}
	if rest, ok := strings.CutSuffix(repoOwner, label); ok && len(rest) <= 4 {
		return true
	}
	return false
}

// applyCachedStars fills in hit.Popularity.Stars from the installed
// PopularityProvider's cache ONLY (Spec 110 FR-002): no I/O, so both
// BuildCatalogHit and SearchAll's post-Resolve re-apply can call this freely.
// A nil provider, an entry with no GitHub-shaped SourceCodeURL, or a
// non-displayable lookup state (Absent/Negative) leave the hit unchanged.
func applyCachedStars(hit *CatalogHit) {
	if hit.starsBorrowed {
		// The publisher does not own the named repo: its stars are not this
		// server's. Keep only what the source itself reported (D37.7).
		resetToSourceNativeStars(hit)
		return
	}
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
// reverse-DNS id such as "io.github.github/github-mcp-server" (→ "github"; see
// namespaceOwner). Falls back to the registry's own name when the id carries
// no such namespace.
func derivePublisher(id, registryName string) string {
	if owner, ok := namespaceOwner(id); ok {
		return owner
	}
	return registryName
}

// secondLevelSuffixes are the second-level public suffixes that sit between a
// country code and the organisation in a reverse-DNS namespace (uk.co.acme).
var secondLevelSuffixes = map[string]bool{"co": true, "com": true, "org": true, "net": true, "ac": true, "gov": true, "edu": true}

// namespaceOwner returns the publisher label of an id's reverse-DNS namespace
// (Spec 109 D37.1 tier 5): the user of `io.github.<user>`, otherwise the
// registrable-domain label, the second one ("com.notion/x" -> "notion",
// "com.quranmajeed.time/x" -> "quranmajeed", "uk.co.acme/x" -> "acme"). It
// reports false when the id has no dotted namespace, so the registry-name
// fallback of derivePublisher never counts as an owner.
func namespaceOwner(id string) (string, bool) {
	slash := strings.IndexByte(id, '/')
	if slash <= 0 {
		return "", false
	}
	labels := strings.Split(id[:slash], ".")
	if len(labels) < 2 {
		return "", false
	}
	i := 1
	switch {
	case labels[0] == "io" && labels[1] == "github":
		i = 2
	case len(labels) >= 3 && isCountryCode(labels[0]) && secondLevelSuffixes[labels[1]]:
		i = 2
	}
	if i >= len(labels) || labels[i] == "" {
		return "", false
	}
	return labels[i], true
}

// isCountryCode reports whether a label is a two-letter ASCII ccTLD (uk, au).
func isCountryCode(l string) bool {
	if len(l) != 2 {
		return false
	}
	for i := 0; i < 2; i++ {
		if c := l[i] | 0x20; c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

// Rank is the pure, deterministic catalog ordering (Spec 109 D37.1, data-model
// §9, contracts/rest-api.md#catalog): match tier desc (how well the NAME
// matches q: publisher equals q > exact name > name prefix > name token >
// substring or description > namespace-only), official source desc, verified
// desc, popularity desc (missing = 0, Spec 110 FR-004), title asc, id asc. It
// reports whether a sorts strictly before b. An empty q puts every hit in
// tier 0, so browse-time order is official, verified, popularity, title, id.
func Rank(a, b CatalogHit, q string) bool {
	if at, bt := matchTier(a, q), matchTier(b, q); at != bt {
		return at > bt
	}
	if a.Official != b.Official {
		return a.Official
	}
	if a.Verified != b.Verified {
		return a.Verified
	}
	if !popularityEqual(a.Popularity, b.Popularity) {
		return morePopular(a.Popularity, b.Popularity)
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

// normalizeMatchText lower-cases s and collapses every run of the separators
// `- _ . /` and whitespace to one space, so "GitHub-MCP_server" and "github mcp
// server" compare equal. Surrounding separators are trimmed.
func normalizeMatchText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := true // swallow leading separators
	for _, r := range strings.ToLower(s) {
		switch r {
		case '-', '_', '.', '/', ' ', '\t', '\n', '\r':
			if !space {
				b.WriteByte(' ')
				space = true
			}
		default:
			b.WriteRune(r)
			space = false
		}
	}
	return strings.TrimSuffix(b.String(), " ")
}

// matchTier scores how well a hit's NAME matches q (Spec 109 D37.1), 0-5. Rank
// puts it first so the real server beats a merely popular or official one:
//
//	5  the publisher (namespace owner) equals q   io.github.github/… for "github"
//	4  the name segment or the title equals q     com.mcparmory/github
//	3  the segment or title starts with q          github-mcp-server
//	2  a whole word of the segment/title equals q  obsidian-github-mcp
//	1  q is a substring of the segment/title/description
//	0  no match, or the only match is the namespace (io.github.*)
//
// q and every field are normalized by normalizeMatchText. An empty q is 0.
func matchTier(h CatalogHit, q string) int {
	nq := normalizeMatchText(q)
	if nq == "" {
		return 0
	}
	id := h.Entry.ID
	if owner, ok := namespaceOwner(id); ok && normalizeMatchText(owner) == nq {
		return 5
	}
	seg := id
	if i := strings.IndexByte(id, '/'); i >= 0 {
		seg = id[i+1:]
	}
	fields := [2]string{normalizeMatchText(seg), normalizeMatchText(catalogTitle(h))}
	for _, f := range fields {
		if f == nq {
			return 4
		}
	}
	for _, f := range fields {
		if strings.HasPrefix(f, nq+" ") {
			return 3
		}
	}
	for _, f := range fields {
		if strings.Contains(" "+f+" ", " "+nq+" ") {
			return 2
		}
	}
	for _, f := range fields {
		if strings.Contains(f, nq) {
			return 1
		}
	}
	if strings.Contains(normalizeMatchText(h.Entry.Description), nq) {
		return 1
	}
	return 0
}

// buildSections splits the merged, de-duplicated, source-filtered pool
// (BEFORE limit truncation and BEFORE any Rank sort) into the empty-query
// landing sections (Spec 110 FR-005, amending Spec 109 FR-060):
//
//   - Official: the official-source hits of `pool`, curated first, never
//     popularity-ordered (Spec 109 D35 amends Spec 110 FR-005; see
//     officialBrowseOrder). Pool MUST NOT have been Rank-sorted yet: on an
//     all-official default install every hit ties on Official/Verified, so Rank
//     order IS popularity order, and building Official from a ranked pool
//     silently reintroduces the bug Spec 110 fixed. Capped at 12.
//   - Popular: hits with a known signal (stars>0 ∨ installs>0), sorted by
//     popularity (FR-004) then Rank as a tiebreak, at most one per GitHub
//     repo key (a monorepo's shared star count keeps only the first by Rank —
//     spec.md edge cases) and one per normalized title (D37.8), capped at 12.
func buildSections(pool []CatalogHit, q string) *CatalogSections {
	sections := &CatalogSections{Official: []CatalogHit{}, Popular: []CatalogHit{}}

	sections.Official = officialBrowseOrder(pool)
	if len(sections.Official) > catalogSectionCap {
		sections.Official = sections.Official[:catalogSectionCap]
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
	seenTitle := make(map[string]bool, len(candidates))
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
		// The same server listed by two sources (the reference `fetch` and
		// Docker's mcp/fetch) shares a normalized title, not a repo key
		// (Spec 109 D37.8).
		titleKey := popularTitleKey(h)
		if seenRepo[dedupKey] || (titleKey != "" && seenTitle[titleKey]) {
			continue
		}
		seenRepo[dedupKey] = true
		if titleKey != "" {
			seenTitle[titleKey] = true
		}
		sections.Popular = append(sections.Popular, h)
	}
	return sections
}

// popularTitleKey is Popular's second de-dup key (Spec 109 D37.8): the title
// lower-cased with a Docker `mcp/` prefix stripped, nothing else, so `fetch`
// and `mcp/fetch` collapse while `fetch-mcp` stays separate.
func popularTitleKey(h CatalogHit) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(catalogTitle(h))), "mcp/")
}

// officialBrowseOrder orders the official-source hits of the merged pool (in
// MERGE order: registry-list order, then each source's native order) for the
// empty-query Official section. The curated reference servers come first, in
// curated order; the rest are interleaved round-robin across their sources in
// registry-list order, each source keeping its native order. The official
// source paginates alphabetically by reverse-DNS id, so taking its first twelve
// would list obscure namespaces and hide the curated basics (Spec 109 D35, demo
// finding #6). Popularity plays no part: Popular must still differ from Official.
func officialBrowseOrder(pool []CatalogHit) []CatalogHit {
	var out []CatalogHit
	var sources []string
	bySource := make(map[string][]CatalogHit)
	for _, h := range pool {
		if !h.Official {
			continue
		}
		if h.Curated {
			out = append(out, h)
			continue
		}
		if _, ok := bySource[h.Source]; !ok {
			sources = append(sources, h.Source)
		}
		bySource[h.Source] = append(bySource[h.Source], h)
	}
	for round := 0; ; round++ {
		progressed := false
		for _, src := range sources {
			if round < len(bySource[src]) {
				out = append(out, bySource[src][round])
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return out
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
		FromCache:      h.FromCache,
	}
}

// toCatalogInstall derives the transport + install target from a
// ServerEntry: InstallCmd is split into command + args for a local/stdio
// install (data-model §9); a URL is used only when there is no InstallCmd.
//
// This must match official.go's officialServerToEntry precedence for a
// hybrid entry (both a package AND a remote): "package wins for stdio; keep
// the remote as a fallback" — ConnectURL is populated there specifically as
// a fallback, never as the primary transport, so InstallCmd is checked
// FIRST here. Getting this backwards (checking URL/ConnectURL before
// InstallCmd) both misreports the transport for every such hybrid entry and
// breaks the "added" join: CatalogInstallTarget would key off the URL while
// the actually-configured server (added via InstallCmd, stdio) is keyed by
// command+args, so a subsequent search never marks it added (review round 4
// F-B).
func toCatalogInstall(entry ServerEntry) (CatalogInstall, string) {
	if entry.InstallCmd != "" {
		parts, err := shellwords.Split(entry.InstallCmd)
		if err == nil && len(parts) > 0 {
			return CatalogInstall{Command: parts[0], Args: parts[1:]}, "stdio"
		}
	}
	url := entry.URL
	if url == "" {
		url = entry.ConnectURL
	}
	if url != "" {
		return CatalogInstall{URL: url}, "http"
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

	// Command arguments are structured values: joining them with spaces makes
	// ["a b", "c"] indistinguishable from ["a", "b c"]. Length-prefix every
	// value so catalog entries only join the exact configured argv.
	var target strings.Builder
	target.WriteString("cmd:")
	target.WriteString(strconv.Itoa(len(install.Command)))
	target.WriteByte(':')
	target.WriteString(install.Command)
	for _, arg := range install.Args {
		target.WriteString(strconv.Itoa(len(arg)))
		target.WriteByte(':')
		target.WriteString(arg)
	}
	return target.String()
}
