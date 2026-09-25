package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// catalogSections is the REST-facing shape of the empty-query landing
// sections (contracts/rest-api.md#catalog).
type catalogSections struct {
	Official []registries.CatalogResult `json:"official"`
	Popular  []registries.CatalogResult `json:"popular"`
}

// catalogSearchResponse is the GET /catalog/search response body
// (data-model §9, contracts/rest-api.md#catalog).
type catalogSearchResponse struct {
	Query       string                     `json:"query"`
	Results     []registries.CatalogResult `json:"results"`
	Sections    *catalogSections           `json:"sections"`
	Unavailable []registries.SourceError   `json:"unavailable"`
}

// handleCatalogSearch godoc
// @Summary      Search the server catalog across every enabled source
// @Description  Fans out to every enabled catalog source (registry) in parallel, merges, de-duplicates and ranks the results (FR-060/061). An empty q returns official + popular sections instead of a flat list. Open to any authenticated caller — added is the only field filtered per caller scope (FR-007).
// @Tags         catalog
// @Produce      json
// @Param        q       query  string  false  "Free-text search"
// @Param        source  query  string  false  "Narrow to one catalog source id"
// @Param        tag     query  string  false  "Filter by tag"
// @Param        limit   query  int     false  "Max results (default 20, max 50)"
// @Success      200  {object}  contracts.SuccessResponse
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/catalog/search [get]
func (s *Server) handleCatalogSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	source := r.URL.Query().Get("source")
	tag := r.URL.Query().Get("tag")

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	hits, sections, unavailable := registries.SearchAll(r.Context(), q, tag, limit, registries.SearchOptions{})
	if source != "" {
		hits = filterCatalogHitsBySource(hits, source)
		if sections != nil {
			sections = &registries.CatalogSections{
				Official: filterCatalogHitsBySource(sections.Official, source),
				Popular:  filterCatalogHitsBySource(sections.Popular, source),
			}
		}
	}

	added := s.catalogAddedPredicate(r.Context())

	resp := catalogSearchResponse{
		Query:       q,
		Results:     toCatalogResults(hits, added),
		Unavailable: unavailable,
	}
	if unavailable == nil {
		resp.Unavailable = []registries.SourceError{}
	}
	if sections != nil {
		resp.Sections = &catalogSections{
			Official: toCatalogResults(sections.Official, added),
			Popular:  toCatalogResults(sections.Popular, added),
		}
	}

	s.writeSuccess(w, resp)
}

func filterCatalogHitsBySource(hits []registries.CatalogHit, source string) []registries.CatalogHit {
	out := make([]registries.CatalogHit, 0, len(hits))
	for _, h := range hits {
		if h.Source == source {
			out = append(out, h)
		}
	}
	return out
}

func toCatalogResults(hits []registries.CatalogHit, added func(registries.CatalogHit) bool) []registries.CatalogResult {
	out := make([]registries.CatalogResult, 0, len(hits))
	for _, h := range hits {
		out = append(out, registries.ToCatalogResult(h, added(h)))
	}
	return out
}

// catalogAddedPredicate returns a function reporting whether a catalog hit is
// already configured, joined only against the servers visible to ctx's caller
// (FR-007, contracts/rest-api.md#catalog): "added" is computed only over
// servers passing CanEnumerateServer, so a scoped caller never learns from
// "added" that an out-of-scope server exists. A configured server with a
// source_registry_id matches on (source, install target); one without (a
// manual add) matches on install target alone.
func (s *Server) catalogAddedPredicate(ctx context.Context) func(registries.CatalogHit) bool {
	servers := s.getVisibleServersForCatalog(ctx)

	// byRegistryAndTarget indexes servers that declare which registry they
	// came from — those must match on (source, target); byTargetOnly indexes
	// only the manual adds (no source_registry_id), which match on target
	// alone. A registry-sourced server is deliberately NOT also added to
	// byTargetOnly: without this split it would falsely read added:true for
	// every OTHER source whose entry happens to share the same install
	// target (contracts/rest-api.md#catalog "added").
	byRegistryAndTarget := make(map[string]bool, len(servers))
	byTargetOnly := make(map[string]bool, len(servers))
	for _, srv := range servers {
		target := catalogInstallTargetForServer(srv)
		if srv.SourceRegistryID == "" {
			byTargetOnly[target] = true
		} else {
			byRegistryAndTarget[srv.SourceRegistryID+"\x00"+target] = true
		}
	}

	return func(h registries.CatalogHit) bool {
		// added is never itself computed from the DTO's Added field (still
		// false here) — only its Install target, which is pure derived data
		// from the catalog entry.
		target := registries.CatalogInstallTarget(registries.ToCatalogResult(h, false).Install)
		if byRegistryAndTarget[h.Source+"\x00"+target] {
			return true
		}
		return byTargetOnly[target]
	}
}

// getVisibleServersForCatalog returns the servers the caller carried by ctx
// may enumerate, preferring the management service (typed) and falling back
// to the legacy generic path — the same two sources handleGetServers reads,
// narrowed the same way (visibleServers).
func (s *Server) getVisibleServersForCatalog(ctx context.Context) []contracts.Server {
	var servers []contracts.Server
	if mgmtSvc := s.controller.GetManagementService(); mgmtSvc != nil {
		if list, _, err := mgmtSvc.ListServers(ctx); err == nil {
			servers = make([]contracts.Server, 0, len(list))
			for _, srv := range list {
				if srv != nil {
					servers = append(servers, *srv)
				}
			}
		}
	} else if generic, err := s.controller.GetAllServers(); err == nil {
		servers = contracts.ConvertGenericServersToTyped(generic)
	}
	return visibleServers(ctx, servers)
}

// catalogInstallTargetForServer computes the same install-target key as
// registries.CatalogInstallTarget, from a configured contracts.Server.
func catalogInstallTargetForServer(srv contracts.Server) string {
	if srv.URL != "" {
		return "url:" + srv.URL
	}
	args := ""
	for i, a := range srv.Args {
		if i > 0 {
			args += " "
		}
		args += a
	}
	return "cmd:" + srv.Command + " " + args
}
