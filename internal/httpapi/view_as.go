package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// View-as (Spec 108 FR-032): GET /api/v1/tools?client=<id> and ?profile=<name>
// answer "what would this client / this profile see, and what could it call",
// row by row, from the SAME access chain a real call walks. This file owns the
// request handling; the verdicts come from internal/server.AccessEvaluator.

// ViewAsEvaluator answers the access chain for one resolved subject.
type ViewAsEvaluator interface {
	Evaluate(server, tool string) profile.AccessVerdict
}

// viewAsController is the optional controller capability behind view-as, kept
// separate from ServerController (like profileToolVisibilityController) so
// controllers that do not evaluate access need no stub.
type viewAsController interface {
	// ResolveViewAs resolves the subject once; it returns profile.ErrUnknownProfile
	// or profile.ErrUnknownClient when the subject does not exist.
	ResolveViewAs(subject profile.AccessSubject) (ViewAsEvaluator, error)
}

// viewAsRequest is a parsed and authorized view-as request.
type viewAsRequest struct {
	subject profile.AccessSubject
	// admin is true for an administrator caller: every row is returned. A
	// non-administrator (profile= only) gets the visible rows plus counts.
	admin bool
}

// parseViewAs reads ?client= / ?profile=. It returns (nil, true) when neither
// is present, and (nil, false) after writing the error response when the
// request is not answerable:
//
//	400 use either client or profile, not both
//	400 '-' (unattributed) is only valid on activity and session filters
//	403 operation requires admin access          (client= by a non-admin)
//	404 client not found / profile not found     (unknown, or not reachable by
//	                                              the caller: byte-identical)
func (s *Server) parseViewAs(w http.ResponseWriter, r *http.Request) (*viewAsRequest, bool) {
	q := r.URL.Query()
	client, prof := q.Get("client"), q.Get("profile")
	if client == "" && prof == "" {
		return nil, true
	}
	if client != "" && prof != "" {
		s.writeError(w, r, http.StatusBadRequest, errClientAndProfile)
		return nil, false
	}
	if client == storage.ScopeFilterUnattributed || prof == storage.ScopeFilterUnattributed {
		s.writeError(w, r, http.StatusBadRequest, errUnattributedOnlyOnGrids)
		return nil, false
	}

	ctx := r.Context()
	admin := auth.AuthorizeServerOp(auth.AuthContextFromContext(ctx), auth.ServerOpConfigWrite)
	req := &viewAsRequest{admin: admin}

	if client != "" {
		// Binding disclosure is administrator-only (FR-032).
		if !admin {
			s.writeError(w, r, http.StatusForbidden, "operation requires admin access")
			return nil, false
		}
		req.subject = profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: client}
		if state, ok := s.clientCredentialState(client); ok {
			req.subject.CredentialState = state
		}
		return req, true
	}

	cfg, err := s.controller.GetConfig()
	if err != nil || cfg == nil || !profileReachableForView(ctx, cfg, prof, admin) {
		s.writeError(w, r, http.StatusNotFound, errProfileNotFound)
		return nil, false
	}
	req.subject = profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: prof}
	return req, true
}

// clientCredentialState reads what the client's connection actually carries
// from its config (the same on-demand read GET /connect/{client} does). It
// reports ok=false when the state cannot be read (no connect service, the
// client is not installed, or the state is unknown); the evaluator then
// derives it from the client's credential record.
func (s *Server) clientCredentialState(clientID string) (profile.CredentialState, bool) {
	svc := s.getConnectService()
	if svc == nil || connect.FindClient(clientID) == nil {
		return "", false
	}
	status, err := svc.GetStatus(clientID)
	if err != nil {
		return "", false
	}
	state := profile.CredentialState(status.CredentialState)
	if state == "" || state == profile.CredentialStateUnknown {
		return "", false
	}
	return state, true
}

// profileReachableForView reports whether the caller may view the named
// profile: an administrator may view any configured profile; a
// non-administrator only one whose servers intersect its own entitlement
// (profileReachable, the FR-032 rule), so an unreachable name answers exactly
// like an unknown one.
func profileReachableForView(ctx context.Context, cfg *config.Config, name string, admin bool) bool {
	if admin {
		for i := range cfg.Profiles {
			if cfg.Profiles[i].Name == name {
				return true
			}
		}
		return false
	}
	return profileReachable(ctx, cfg, name)
}

// resolveViewAs turns a parsed request into an evaluator, writing the error
// response (404 / 501) when it cannot.
func (s *Server) resolveViewAs(w http.ResponseWriter, r *http.Request, req *viewAsRequest) (ViewAsEvaluator, bool) {
	vc, ok := s.controller.(viewAsController)
	if !ok {
		s.writeError(w, r, http.StatusNotImplemented, "view-as is not available on this server")
		return nil, false
	}
	ev, err := vc.ResolveViewAs(req.subject)
	switch {
	case err == nil:
		return ev, true
	case errors.Is(err, profile.ErrUnknownProfile):
		s.writeError(w, r, http.StatusNotFound, errProfileNotFound)
	case errors.Is(err, profile.ErrUnknownClient):
		s.writeError(w, r, http.StatusNotFound, errClientNotFound)
	default:
		s.logger.Errorw("view-as resolution failed", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to evaluate access")
	}
	return nil, false
}

// applyViewAs stamps the verdict of every row and, for a non-administrator,
// keeps only the visible rows and reports counts. rows must already have had
// the caller's own scope, its own profile and the quarantine rule applied.
func applyViewAs(ev ViewAsEvaluator, req *viewAsRequest, tools []contracts.Tool) (kept []contracts.Tool, counts *contracts.ViewAsCounts) {
	kept = tools[:0:0]
	visible, hidden := 0, 0
	for i := range tools {
		v := ev.Evaluate(tools[i].ServerName, tools[i].Name)
		tools[i].ProfileTier = contracts.Tier(v.ProfileTier.String())
		tools[i].Access = &contracts.ToolAccess{Visible: v.Visible, Callable: v.Callable, Reason: string(v.Reason)}
		if v.Visible {
			visible++
		} else {
			hidden++
			if !req.admin {
				continue
			}
		}
		kept = append(kept, tools[i])
	}
	if !req.admin {
		counts = &contracts.ViewAsCounts{Visible: visible, Hidden: hidden}
	}
	return kept, counts
}

// limitServersToProfile restricts a server listing to the viewed profile's
// effective servers and rewrites each row's tool_count to the number of that
// server's tools visible under the profile (Spec 108 FR-032). servers is
// already limited to what the caller may see.
//
// A caller pinned to its own profile (an agent token with a profile pin) is
// held to that pin as well, exactly as GET /tools is (filterProfileToolRows):
// a tool counts only when the caller's own profile admits it too, and a server
// the caller's pin does not admit disappears, so a count never discloses a
// tool the caller cannot see and /servers never disagrees with /tools
// (Spec 109-l, #1437 item 1). The administrator path is unchanged.
func (s *Server) limitServersToProfile(ctx context.Context, servers []contracts.Server, req *viewAsRequest, ev ViewAsEvaluator) []contracts.Server {
	cfg, err := s.controller.GetConfig()
	if err != nil || cfg == nil {
		return servers[:0:0]
	}
	effective := map[string]struct{}{}
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name != req.subject.Profile {
			continue
		}
		for _, name := range cfg.Profiles[i].EffectiveServers(cfg) {
			effective[name] = struct{}{}
		}
	}
	callerPinned := callerHasProfilePin(ctx)
	callerServers := map[string]struct{}{}
	if callerPinned {
		for _, name := range callerProfileServers(ctx, cfg) {
			callerServers[name] = struct{}{}
		}
	}
	out := make([]contracts.Server, 0, len(servers))
	for i := range servers {
		if _, ok := effective[servers[i].Name]; !ok {
			continue
		}
		row := servers[i]
		visible := make([]map[string]interface{}, 0)
		for _, tool := range s.serverToolNames(ctx, row.Name) {
			if ev.Evaluate(row.Name, tool).Visible {
				visible = append(visible, map[string]interface{}{"name": tool})
			}
		}
		if callerPinned {
			visible = filterProfileToolRows(s.controller, ctx, row.Name, visible)
			if _, inPin := callerServers[row.Name]; !inPin && len(visible) == 0 {
				continue
			}
		}
		row.ToolCount = len(visible)
		out = append(out, row)
	}
	return out
}

// callerHasProfilePin reports whether the request's caller is a
// non-administrator bound to a profile (an agent token with a pin).
func callerHasProfilePin(ctx context.Context) bool {
	ac := auth.AuthContextFromContext(ctx)
	return ac != nil && !ac.IsAdmin() && ac.ProfilePin != ""
}

// callerProfileServers lists the servers the caller's own pinned profile
// admits; a pin naming no configured profile admits nothing (fail closed).
func callerProfileServers(ctx context.Context, cfg *config.Config) []string {
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil {
		return nil
	}
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == ac.ProfilePin {
			return cfg.Profiles[i].EffectiveServers(cfg)
		}
	}
	return nil
}

// serverToolNames lists the raw tool names of one server the same way the
// global tools listing does (management service first, controller fallback).
func (s *Server) serverToolNames(ctx context.Context, serverName string) []string {
	var rows []map[string]interface{}
	var err error
	if svc := s.controller.GetManagementService(); svc != nil {
		rows, err = svc.GetServerTools(ctx, serverName)
	} else {
		rows, err = s.controller.GetServerTools(serverName)
	}
	if err != nil {
		s.logger.Debugw("view-as: server tools unavailable", "server", serverName, "error", err)
		return nil
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		if name, _ := row["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// statsForServers recomputes the server statistics over an already-narrowed
// listing, so total_servers is not a count of servers the listing dropped.
// Deployment-wide aggregates (docker containers, token metrics) cannot be
// re-derived for a subset and are omitted, as recomputeServerStats does for a
// scoped caller.
func statsForServers(filtered []contracts.Server) contracts.ServerStats {
	out := contracts.ServerStats{TotalServers: len(filtered)}
	for i := range filtered {
		if filtered[i].Connected {
			out.ConnectedServers++
		}
		if filtered[i].Quarantined {
			out.QuarantinedServers++
		}
		if config.ServerContributesTools(filtered[i].Enabled, filtered[i].Quarantined) {
			out.TotalTools += filtered[i].ToolCount
		}
	}
	return out
}
