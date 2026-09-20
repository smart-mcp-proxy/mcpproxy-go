package server

import (
	"sort"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// directCatalog is the immutable snapshot of the direct enumeration surface
// (Spec 102 FR-017).
//
// It exists to remove a split source of truth. Today the direct surface answers
// four different questions from three different places: the listing renders from
// a live DiscoverTools projection, the discovery filters read a separate
// directToolPermissions map, and describe_tool would resolve through the search
// index — which is a DIFFERENT population, since the index is filtered at the
// tool level while the listing is filtered only at the server level. The catalog
// makes one snapshot answer all four: listing rendering, signature lookup,
// describe_tool resolution, and pre-dispatch validation.
//
// It is immutable after publication and swapped atomically. Nothing mutates an
// entry in place; a rebuild produces a whole new catalog.
type directCatalog struct {
	// generation increments once per published rebuild. It is logged on publish
	// and asserted by the skew tests, which need to distinguish "a rebuild
	// happened between these two reads" from "the signature cache changed under
	// us" — the latter must NOT look like a generation change (D13).
	generation uint64

	byDisplayName map[string]*directCatalogEntry
	displayNames  []string // sorted; the deterministic listing order
	withheld      []directCatalogCollision

	// byCanonical maps "<server>:<tool>" onto the SAME entry pointers as
	// byDisplayName. It exists because describe_tool accepts both id forms
	// (Spec 102 FR-011) and neither may be resolved by re-parsing the other:
	// "we__ird__do_thing" splits on the first "__" into ("we", "ird__do_thing"),
	// a different tool. Two maps over one entry set is what makes the two forms
	// answer identically by construction rather than by agreement.
	//
	// An entry is admitted here only if its canonical form is unambiguous —
	// see ambiguousCanonical.
	byCanonical map[string]*directCatalogEntry

	// mode is the direct serialization this generation was RENDERED with
	// ("full" or "deferred"). It lives on the snapshot rather than being
	// re-read from config at compare time because that is the only place the
	// question "what is currently published?" has an honest answer: config says
	// what the operator wants, the catalog says what connected clients were
	// actually served (Spec 102 FR-014 / T068).
	mode string

	// ambiguousCanonical holds canonical ids that two admitted DISPLAY names
	// flatten onto: server "a" + tool "b:c" and server "a:b" + tool "c" are
	// distinct, unambiguous display names ("a__b:c", "a:b__c") whose canonical
	// forms are both "a:b:c". Those entries stay listed and describable by
	// display name; only the canonical form is withheld, because it cannot name
	// one of them.
	ambiguousCanonical map[string]struct{}

	// canonicalShadow records, for every canonical string withdrawn from
	// byCanonical (an entry in ambiguousCanonical), every entry that string
	// could name — Spec 105 FR010-G3. Resolution over this list is deferred
	// to REQUEST time (LookupCanonicalForAuth), never decided once here: this
	// build is scope-blind by construction (one snapshot serves every
	// caller), so deciding a winner here would either pick one caller's
	// authorized entry over another's, or withhold an id from EVERY caller
	// merely because it collides with something a given caller cannot even
	// see. Administrator resolution is unaffected: LookupCanonicalForAuth's
	// fast path is exactly LookupCanonical's answer for an unambiguous id,
	// and for an ambiguous one an administrator's authorization predicate
	// admits every shadow candidate — so it still lands on "more than one
	// authorized, therefore absent", byte-identical to what LookupCanonical
	// itself answers (see TestDescribeDirect_DisplayAndCanonicalNamespaceOverlap).
	canonicalShadow map[string][]*directCatalogEntry
}

// directCatalogEntry is one tool as the direct surface sees it.
//
// The handler closes over its OWN entry at registration time, so a dispatch can
// never validate against a definition other than the one its own registration
// advertised (research.md R9). That is the half of FR-017's atomicity claim that
// is literally satisfied rather than delivered as a safety property.
type directCatalogEntry struct {
	DisplayName string
	ServerName  string
	ToolName    string

	// Description is the RAW upstream description. RenderedDescription is what
	// was actually registered — "[server] desc", plus the compact signature
	// suffix in deferred mode.
	//
	// Both are stored because they answer different questions, and the rendered
	// one is captured at render time and never recomputed: the signature cache
	// mutates independently of rebuilds, so re-rendering to compare would report
	// a difference that is a cache warm/evict rather than a catalog change
	// (D13 rule 5).
	Description         string
	RenderedDescription string

	ParamsJSON       string
	OutputSchemaJSON string
	Hash             string

	Annotations *config.ToolAnnotations

	// RequiredPermission is derived from the UPSTREAM annotations above, exactly
	// as dispatch derives it — never from the registered mcp.Tool's annotations,
	// which carry mcp-go's NewTool defaults (destructiveHint=true unless
	// overridden) and would classify nearly every tool destructive, hiding the
	// catalog from read- and write-scoped tokens and disagreeing with dispatch
	// (D10 / D13 rule 3).
	RequiredPermission string
}

// toolMetadata projects an entry back onto the shape the shared entry builder
// consumes, so a direct definition and a retrieve_tools definition render
// through ONE builder and cannot drift on the fields they share.
//
// Name carries the canonical "<server>:<tool>" prefix because that is what
// buildFullToolEntry treats as the id (#871), and what describe_tool has always
// returned as `name` on every other surface.
func (e *directCatalogEntry) toolMetadata() *config.ToolMetadata {
	if e == nil {
		return nil
	}
	return &config.ToolMetadata{
		ServerName:       e.ServerName,
		Name:             e.ServerName + ":" + e.ToolName,
		Description:      e.Description,
		ParamsJSON:       e.ParamsJSON,
		OutputSchemaJSON: e.OutputSchemaJSON,
		Hash:             e.Hash,
		Annotations:      e.Annotations,
	}
}

// directCatalogCollision records a display name that two distinct upstream
// pairs flatten to. Both are withheld; this is what lets an operator find out
// why a tool they expect is missing.
type directCatalogCollision struct {
	DisplayName string
	Origins     []directCatalogOrigin
}

type directCatalogOrigin struct {
	ServerName string
	ToolName   string
}

// buildDirectCatalog builds the snapshot from a tool projection. Pure: it never
// publishes, never touches the server, and never logs through a global.
//
// D13 rule 1 forbids the builder from publishing — the publisher must call
// SetTools first and swap the catalog immediately after, so it needs the handle
// rather than having it installed behind its back.
//
// It NEVER returns nil, on any input. A nil catalog and an empty catalog mean
// different things to the discovery filters: an empty catalog denies every
// display name (rule 2), while a nil one means "not built yet" and must not
// deny. Returning nil from the DiscoverTools error path — which is what the
// pre-102 builder did — would therefore silently switch the filters from
// deny-on-miss to allow-everything at exactly the moment upstream discovery is
// failing.
func buildDirectCatalog(tools []*config.ToolMetadata, logger *zap.Logger) *directCatalog {
	cat := &directCatalog{
		byDisplayName:      make(map[string]*directCatalogEntry, len(tools)),
		displayNames:       make([]string, 0, len(tools)),
		byCanonical:        make(map[string]*directCatalogEntry, len(tools)),
		ambiguousCanonical: make(map[string]struct{}),
		canonicalShadow:    make(map[string][]*directCatalogEntry),
	}

	// First pass: group by display name so a collision is detected before any
	// entry is admitted. Detecting it while inserting would admit the first
	// writer and then have to retract it, which is how "first writer wins"
	// creeps back in.
	grouped := make(map[string][]*config.ToolMetadata, len(tools))
	order := make([]string, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		if t.Name == "" {
			// Spec 105 FR-008 (FR008-G7): an upstream tool with an empty raw
			// name renders as "server__" and has no registration identity to
			// authorize it against — the direct-surface analogue of the
			// FR-006 empty-prompt-name rule. Refused admission here, at the
			// source, rather than admitted and relied on to be caught by a
			// downstream filter: withheld from every caller, administrators
			// included (SC-005 exception).
			if logger != nil {
				logger.Warn("Withholding direct tool with an empty raw name: no registration identity to authorize it against",
					zap.String("server_name", t.ServerName))
			}
			continue
		}
		name := FormatDirectToolName(t.ServerName, t.Name)
		if _, seen := grouped[name]; !seen {
			order = append(order, name)
		}
		grouped[name] = append(grouped[name], t)
	}

	for _, name := range order {
		group := grouped[name]

		// A collision is two DISTINCT origins flattening to one display name, not
		// merely two entries under one name. The same (server, tool) appearing
		// twice in the projection — a duplicate, e.g. from a reindex race — is
		// not ambiguous: both entries denote the same tool, so withholding it
		// would delete a legitimate tool over a bookkeeping artefact.
		origins := make([]directCatalogOrigin, 0, len(group))
		seenOrigin := make(map[directCatalogOrigin]struct{}, len(group))
		for _, t := range group {
			o := directCatalogOrigin{ServerName: t.ServerName, ToolName: t.Name}
			if _, dup := seenOrigin[o]; dup {
				continue
			}
			seenOrigin[o] = struct{}{}
			origins = append(origins, o)
		}

		if len(origins) > 1 {
			// A display name must never denote two origins. Withhold ALL of
			// them: picking a winner would hand one caller the other tool's
			// schema, and the loser is invisible either way.
			cat.withheld = append(cat.withheld, directCatalogCollision{
				DisplayName: name,
				Origins:     origins,
			})

			if logger != nil {
				logger.Warn("Withholding colliding direct display name: two upstream tools flatten to one name, so neither is listed or describable",
					zap.String("display_name", name),
					zap.Any("origins", origins))
			}
			continue
		}

		t := group[0]
		entry := &directCatalogEntry{
			DisplayName:        name,
			ServerName:         t.ServerName,
			ToolName:           t.Name,
			Description:        t.Description,
			ParamsJSON:         t.ParamsJSON,
			OutputSchemaJSON:   t.OutputSchemaJSON,
			Hash:               t.Hash,
			Annotations:        t.Annotations,
			RequiredPermission: requiredPermissionForDirectTool(t.Annotations),
		}
		cat.byDisplayName[name] = entry
		cat.displayNames = append(cat.displayNames, name)

		// Canonical index, with the same "never pick a winner" rule the display
		// map applies: if two admitted entries flatten onto one canonical id,
		// BOTH lose the canonical form rather than one silently shadowing the
		// other.
		canonical := entry.ServerName + ":" + entry.ToolName
		if prior, dup := cat.byCanonical[canonical]; dup {
			delete(cat.byCanonical, canonical)
			cat.ambiguousCanonical[canonical] = struct{}{}
			// Spec 105 FR010-G3: both candidates go on the shadow list — the
			// entry that occupied byCanonical first, and this one — so a
			// per-request, per-caller resolution can still pick whichever one
			// (if either) the requester is authorized to see.
			cat.canonicalShadow[canonical] = append(cat.canonicalShadow[canonical], prior, entry)
			if logger != nil {
				logger.Warn("Withholding ambiguous canonical direct id: two distinct display names flatten to it, so it resolves in neither form",
					zap.String("canonical_id", canonical))
			}
			continue
		}
		if _, ambiguous := cat.ambiguousCanonical[canonical]; ambiguous {
			cat.canonicalShadow[canonical] = append(cat.canonicalShadow[canonical], entry)
			continue
		}
		cat.byCanonical[canonical] = entry
	}

	// Cross-namespace ambiguity: one string can be entry A's DISPLAY name and
	// entry B's CANONICAL id at the same time. Server "x" with tool "y:z"
	// displays as "x__y:z"; server "x__y" with tool "z" canonicalizes to
	// "x__y:z". Resolution tries the display map first, so without this the
	// canonical id would silently answer with the other tool's definition.
	//
	// Only the CANONICAL form is withdrawn, not the display one. The display
	// map is the listing's own key space and is unambiguous within itself, so
	// withdrawing that instead would unlist two working tools to resolve a
	// question about a third id form. Both tools stay listed and describable by
	// display name; the ambiguous canonical id resolves to nothing.
	for canonical, entry := range cat.byCanonical {
		other, clash := cat.byDisplayName[canonical]
		if !clash || other == entry {
			continue
		}
		delete(cat.byCanonical, canonical)
		cat.ambiguousCanonical[canonical] = struct{}{}
		// Spec 105 FR010-G3: the entry whose OWN canonical form this string
		// is, plus the entry it clashes with (the one whose DISPLAY name
		// happens to equal that string), both go on the shadow list.
		cat.canonicalShadow[canonical] = append(cat.canonicalShadow[canonical], entry, other)
		if logger != nil {
			logger.Warn("Withholding ambiguous direct id: it is one tool's display name and another's canonical id, so it resolves only as the display name",
				zap.String("id", canonical))
		}
	}

	// Sorted so the listing order — and therefore the FR-010 built-ins golden —
	// is stable across runs. Go randomizes map iteration, so without this the
	// golden would be flaky by construction.
	sort.Strings(cat.displayNames)

	return cat
}

// Lookup resolves a display name to its entry. A withheld collision resolves to
// nothing, which is the point.
func (c *directCatalog) Lookup(displayName string) (*directCatalogEntry, bool) {
	if c == nil {
		return nil, false
	}
	e, ok := c.byDisplayName[displayName]
	return e, ok
}

// LookupCanonical resolves a "<server>:<tool>" id to its entry. A withheld
// display-name collision is absent from this map too (it was never admitted),
// and so is an ambiguous canonical id.
func (c *directCatalog) LookupCanonical(canonicalID string) (*directCatalogEntry, bool) {
	if c == nil {
		return nil, false
	}
	e, ok := c.byCanonical[canonicalID]
	return e, ok
}

// LookupCanonicalForAuth resolves a canonical "<server>:<tool>" id the way
// LookupCanonical does when the id is unambiguous — byte-identical for every
// caller, administrators included (Spec 105 FR-010: administrator resolution
// is unchanged). When the id was withdrawn from byCanonical for colliding
// with another entry, it instead resolves against the shadow candidate list,
// filtered to the ones `authorized` admits: exactly one authorized candidate
// resolves as if the others never existed, so a HIDDEN colliding entry can
// never suppress an id an AUTHORIZED entry would otherwise answer to
// (FR010-G3). Zero or more than one authorized candidate is a genuine
// ambiguity from this caller's own point of view too (an administrator who
// can see every candidate always lands here) and resolves to nothing, same
// as an absent id.
func (c *directCatalog) LookupCanonicalForAuth(canonicalID string, authorized func(*directCatalogEntry) bool) (*directCatalogEntry, bool) {
	if c == nil {
		return nil, false
	}
	if e, ok := c.byCanonical[canonicalID]; ok {
		return e, true
	}
	candidates := c.canonicalShadow[canonicalID]
	if len(candidates) == 0 {
		return nil, false
	}
	var match *directCatalogEntry
	for _, candidate := range candidates {
		if !authorized(candidate) {
			continue
		}
		if match != nil {
			// More than one candidate is authorized for this caller: a real
			// ambiguity, not a disclosure artefact.
			return nil, false
		}
		match = candidate
	}
	if match == nil {
		return nil, false
	}
	return match, true
}

// DisplayNames returns the sorted display names this catalog admits.
func (c *directCatalog) DisplayNames() []string {
	if c == nil {
		return nil
	}
	out := make([]string, len(c.displayNames))
	copy(out, c.displayNames)
	return out
}

// Len is the number of admitted entries — withheld collisions are not counted,
// because they are not served.
func (c *directCatalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.byDisplayName)
}

// Withheld returns the collisions this build refused to serve.
func (c *directCatalog) Withheld() []directCatalogCollision {
	if c == nil {
		return nil
	}
	out := make([]directCatalogCollision, len(c.withheld))
	copy(out, c.withheld)
	return out
}

// Mode is the direct serialization this snapshot was rendered with.
func (c *directCatalog) Mode() string {
	if c == nil {
		return ""
	}
	return c.mode
}

// Generation is the publish counter for this snapshot.
func (c *directCatalog) Generation() uint64 {
	if c == nil {
		return 0
	}
	return c.generation
}

// directResolveDecision is the three-way outcome of resolving a direct display
// name, which is deliberately NOT a bool.
//
// "Not found" and "no catalog" must not collapse into one answer: an empty
// catalog means the build ran and admitted nothing, so every name should be
// denied; a nil catalog means the build has not run yet, and denying there would
// blank the surface during startup. The old directToolPermissions map could not
// express the difference — a nil map and an empty map both missed — which is why
// the distinction gets its own type here (D13 rule 2).
type directResolveDecision int

const (
	// directResolveFound: the catalog admits this name; the entry is returned.
	directResolveFound directResolveDecision = iota
	// directResolveDenied: a catalog exists and does not admit this name.
	directResolveDenied
	// directResolveBuiltin: a tool this proxy serves itself, not an upstream
	// projection. Built-ins carry no server__tool form, so without an explicit
	// set they would be denied off their own surface.
	directResolveBuiltin
	// directResolveNoCatalog: nothing published yet — callers must fall back to
	// their pre-catalog behaviour rather than deny.
	directResolveNoCatalog
)

// resolveDirectTool maps a direct display name to its catalog entry.
//
// This replaces ParseDirectToolName as the resolution path for the discovery
// filters. Parsing splits on the FIRST "__", which mis-splits any server name
// that itself contains "__" — so the filters could scope-check one origin while
// dispatch executed another. The catalog resolves by the same mapping the
// handler was registered from, which is what makes listing, describe and
// dispatch agree by construction (FR-011, D10).
func (p *MCPProxyServer) resolveDirectTool(displayName string) (*directCatalogEntry, directResolveDecision) {
	if _, ok := builtinDirectToolNames[displayName]; ok {
		return nil, directResolveBuiltin
	}

	cat := p.loadDirectCatalog()

	// THE CATALOG DECIDES FIRST. If this snapshot admits the name, it is an
	// upstream projection — whatever the name looks like — and it must go
	// through the scope, tier and callability gates like any other.
	//
	// This ordering is load-bearing, and getting it wrong was a real disclosure
	// bug: an upstream tool whose NAME IS EMPTY renders as "server__", which
	// ParseDirectToolName rejects (the tool half is empty). A name with no
	// "__" separator was therefore once inferred a proxy built-in structurally
	// — and both direct filters pass built-ins through unconditionally — so an
	// agent token scoped to other servers could see the name, description and
	// annotations of a tool on a server outside its scope. Found by
	// adversarial QA, not by any unit test, because no fixture had ever
	// contained a nameless tool.
	//
	// Spec 105 FR-008 (FR008-G7) closes this at its source: buildDirectCatalog
	// now refuses to admit an entry with an empty raw tool name at all, so
	// "server__" is never in this catalog to begin with, and the structural
	// "no separator -> built-in" inference below is gone entirely — a name is
	// a built-in ONLY via the explicit builtinDirectToolNames set checked
	// above. A name that is neither stamped in the catalog nor a recognised
	// built-in has no registration identity and falls through to denial.
	if entry, ok := cat.Lookup(displayName); ok {
		return entry, directResolveFound
	}

	if cat == nil {
		return nil, directResolveNoCatalog
	}
	return nil, directResolveDenied
}

// publishDirectCatalog swaps in a new snapshot and stamps its generation.
//
// D13 rule 1: only the publisher calls this, and only AFTER SetTools has landed
// the matching tool set. The builder must never publish — if it did, a caller
// could observe a catalog describing tools the registry is not yet serving.
func (p *MCPProxyServer) publishDirectCatalog(cat *directCatalog) {
	if cat == nil {
		p.directCatalogPtr.Store(nil)
		return
	}
	cat.generation = p.directCatalogGeneration.Add(1)
	p.directCatalogPtr.Store(cat)
}

// loadDirectCatalog returns the live snapshot, or nil if none is published.
func (p *MCPProxyServer) loadDirectCatalog() *directCatalog {
	return p.directCatalogPtr.Load()
}
