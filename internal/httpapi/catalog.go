package httpapi

import (
	"context"
	"fmt"
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
// @Param        tag     query  string  false  "Not supported: catalog entries carry no tags; a non-empty value returns 400"
// @Param        limit   query  int     false  "Max results (default 20, max 50)"
// @Success      200  {object}  contracts.SuccessResponse
// @Failure      400  {object}  contracts.APIResponse "tag filtering is not supported"
// @Failure      500  {object}  contracts.APIResponse "configured servers could not be listed"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/catalog/search [get]
func (s *Server) handleCatalogSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	source := r.URL.Query().Get("source")
	// Catalog entries (registries.ServerEntry) carry no tags, so a tag filter
	// cannot be honoured. Reject it explicitly instead of silently returning
	// unfiltered results that look like a tag match.
	if r.URL.Query().Get("tag") != "" {
		s.writeError(w, r, http.StatusBadRequest, "tag filtering is not supported: catalog entries carry no tags")
		return
	}

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	// Source is applied inside SearchAll, BEFORE ranking/truncation to
	// limit — filtering after truncation could silently drop a narrower
	// source's real matches that simply lost out to an official/verified
	// source for one of the truncated top-`limit` slots.
	hits, sections, unavailable := registries.SearchAll(r.Context(), q, "", limit, registries.SearchOptions{Source: source})

	// A failed server listing must not degrade to "nothing is added": every
	// entry would then read added:false with no error signal (as
	// handleGetServers, it fails the request instead).
	added, err := s.catalogAddedResolver(r.Context())
	if err != nil {
		s.logger.Errorw("catalog search: failed to list configured servers", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to list configured servers")
		return
	}

	resp := catalogSearchResponse{
		Query:       q,
		Results:     toCatalogResults(hits, added),
		Unavailable: unavailable,
	}
	if unavailable == nil {
		resp.Unavailable = []registries.SourceError{}
	}
	if sections != nil {
		// contracts/rest-api.md#catalog: "Empty q → results: [],
		// sections: {...}" — an empty q always yields sections (see
		// registries.SearchAll), so results must be emptied here rather
		// than carrying the same ranked list sections is already showing.
		resp.Results = []registries.CatalogResult{}
		resp.Sections = &catalogSections{
			Official: toCatalogResults(sections.Official, added),
			Popular:  toCatalogResults(sections.Popular, added),
		}
	}

	s.writeSuccess(w, resp)
}

func toCatalogResults(hits []registries.CatalogHit, added func(registries.CatalogHit) (bool, string)) []registries.CatalogResult {
	out := make([]registries.CatalogResult, 0, len(hits))
	for _, h := range hits {
		isAdded, addedServerName := added(h)
		result := registries.ToCatalogResult(h, isAdded)
		result.AddedServerName = addedServerName
		out = append(out, result)
	}
	return out
}

// catalogAddedResolver returns a function reporting whether a catalog hit is
// already configured and, only for a unique visible match, that server's name.
// The join uses raw configuration before GET /servers redacts credential-bearing
// URLs and arguments. It considers only servers visible to ctx's caller (FR-007),
// so it cannot reveal an out-of-scope server name. A configured server with a
// source_registry_id matches on (source, install target); one without (a manual
// add) matches on install target alone.
func (s *Server) catalogAddedResolver(ctx context.Context) (func(registries.CatalogHit) (bool, string), error) {
	servers, err := s.getVisibleServersForCatalog(ctx)
	if err != nil {
		return nil, err
	}

	// byRegistryAndTarget indexes servers that declare which registry they
	// came from — those must match on (source, target); byTargetOnly indexes
	// only the manual adds (no source_registry_id), which match on target
	// alone. A registry-sourced server is deliberately NOT also added to
	// byTargetOnly: without this split it would falsely read added:true for
	// every OTHER source whose entry happens to share the same install
	// target (contracts/rest-api.md#catalog "added").
	byRegistryAndTarget := make(map[string][]string, len(servers))
	byTargetOnly := make(map[string][]string, len(servers))
	for _, srv := range servers {
		target := catalogInstallTargetForServer(srv)
		if srv.SourceRegistryID == "" {
			byTargetOnly[target] = append(byTargetOnly[target], srv.Name)
		} else {
			key := srv.SourceRegistryID + "\x00" + target
			byRegistryAndTarget[key] = append(byRegistryAndTarget[key], srv.Name)
		}
	}

	return func(h registries.CatalogHit) (bool, string) {
		// added is never itself computed from the DTO's Added field (still
		// false here) — only its Install target, which is pure derived data
		// from the catalog entry.
		target := registries.CatalogInstallTarget(registries.ToCatalogResult(h, false).Install)
		names := append([]string(nil), byRegistryAndTarget[h.Source+"\x00"+target]...)
		names = append(names, byTargetOnly[target]...)
		if len(names) == 0 {
			return false, ""
		}
		// Names are normally unique configuration keys. De-duplicate defensively
		// so a malformed legacy configuration cannot turn one logical server into
		// a false ambiguity.
		unique := make(map[string]struct{}, len(names))
		for _, name := range names {
			unique[name] = struct{}{}
		}
		if len(unique) == 1 {
			for name := range unique {
				return true, name
			}
		}
		return true, ""
	}, nil
}

// getVisibleServersForCatalog returns the servers the caller carried by ctx
// may enumerate, preferring the management service (typed) and falling back
// to the legacy generic path — the same two sources handleGetServers reads,
// narrowed the same way (visibleServers).
func (s *Server) getVisibleServersForCatalog(ctx context.Context) ([]contracts.Server, error) {
	var servers []contracts.Server
	if mgmtSvc := s.controller.GetManagementService(); mgmtSvc != nil {
		list, _, err := mgmtSvc.ListServers(ctx)
		if err != nil {
			return nil, fmt.Errorf("list servers: %w", err)
		}
		servers = make([]contracts.Server, 0, len(list))
		for _, srv := range list {
			if srv != nil {
				servers = append(servers, *srv)
			}
		}
	} else {
		generic, err := s.controller.GetAllServers()
		if err != nil {
			return nil, fmt.Errorf("get all servers: %w", err)
		}
		servers = contracts.ConvertGenericServersToTyped(generic)
	}
	return visibleServers(ctx, servers), nil
}

// catalogInstallTargetForServer computes the same install-target key as
// registries.CatalogInstallTarget, from a configured contracts.Server.
func catalogInstallTargetForServer(srv contracts.Server) string {
	return registries.CatalogInstallTarget(registries.CatalogInstall{
		URL:     srv.URL,
		Command: srv.Command,
		Args:    srv.Args,
	})
}
