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
		byDisplayName: make(map[string]*directCatalogEntry, len(tools)),
		displayNames:  make([]string, 0, len(tools)),
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
		name := FormatDirectToolName(t.ServerName, t.Name)
		if _, seen := grouped[name]; !seen {
			order = append(order, name)
		}
		grouped[name] = append(grouped[name], t)
	}

	for _, name := range order {
		group := grouped[name]

		if len(group) > 1 {
			// A display name must never denote two origins. Withhold ALL of
			// them: picking a winner would hand one caller the other tool's
			// schema, and the loser is invisible either way.
			collision := directCatalogCollision{DisplayName: name}
			for _, t := range group {
				collision.Origins = append(collision.Origins, directCatalogOrigin{
					ServerName: t.ServerName,
					ToolName:   t.Name,
				})
			}
			cat.withheld = append(cat.withheld, collision)

			if logger != nil {
				logger.Warn("Withholding colliding direct display name: two upstream tools flatten to one name, so neither is listed or describable",
					zap.String("display_name", name),
					zap.Any("origins", collision.Origins))
			}
			continue
		}

		t := group[0]
		cat.byDisplayName[name] = &directCatalogEntry{
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
		cat.displayNames = append(cat.displayNames, name)
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

// builtinDirectToolNames is an explicit allowlist for built-ins whose display
// name WOULD parse as server__tool and so cannot be recognised structurally.
// Empty today; it exists so adding such a built-in is a deliberate act rather
// than an accidental denial.
var builtinDirectToolNames = map[string]struct{}{}

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

	// A name with no "__" separator cannot be an upstream projection: every
	// upstream tool is named through FormatDirectToolName, which always inserts
	// one. So it is something this proxy registered itself — describe_tool,
	// retrieve_tools on a shared surface — and denying it would delete built-ins
	// off their own surface.
	//
	// This is the structural half of D13 rule 2's "built-ins by explicit name
	// set". The set above covers the residual case a structural test cannot: a
	// built-in whose name happens to contain "__".
	if _, _, ok := ParseDirectToolName(displayName); !ok {
		return nil, directResolveBuiltin
	}

	cat := p.loadDirectCatalog()
	if cat == nil {
		return nil, directResolveNoCatalog
	}

	if entry, ok := cat.Lookup(displayName); ok {
		return entry, directResolveFound
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
