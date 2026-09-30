package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// ProfilesAPI is what the /profiles and /access/explain handlers need from the
// profiles service (Spec 108-f). *runtime.ProfilesService implements it; a test
// supplies a fake. Keeping it an interface means the handlers' own logic - the
// administrator gates, the non-administrator omission and redaction, the error
// mapping - is tested without a runtime behind it.
type ProfilesAPI interface {
	List(ctx context.Context, viewer internalRuntime.ViewerScope) (*internalRuntime.ProfileList, error)
	Get(ctx context.Context, name string, viewer internalRuntime.ViewerScope) (*internalRuntime.ProfileView, error)
	Create(ctx context.Context, a internalRuntime.Actor, p config.ProfileConfig) (*internalRuntime.WriteResult, error)
	Update(ctx context.Context, a internalRuntime.Actor, name string, p config.ProfileConfig) (*internalRuntime.WriteResult, error)
	Rename(ctx context.Context, a internalRuntime.Actor, name, newName string) (*internalRuntime.RenameResult, error)
	Delete(ctx context.Context, a internalRuntime.Actor, name, reassignTo string, force bool) (*internalRuntime.DeleteResult, error)
	Try(ctx context.Context, draft config.ProfileConfig, query string, limit int) (*internalRuntime.TryResult, error)
	EffectiveTools(ctx context.Context, name string, opt internalRuntime.EffectiveToolsOptions) (*internalRuntime.EffectiveToolsResult, error)
	Explain(ctx context.Context, subject profile.AccessSubject, tool string) (*internalRuntime.AccessExplanation, error)
}

var _ ProfilesAPI = (*internalRuntime.ProfilesService)(nil)

// SetProfilesService installs the profiles service behind the /profiles routes.
func (s *Server) SetProfilesService(svc ProfilesAPI) { s.profilesService = svc }

// profiles returns the installed service, or a config-only reader (list and get
// from the controller's config, no counts, stats or used_by; every write and
// every evaluator call answers 503) when the runtime did not wire one - a bare
// test controller.
func (s *Server) profiles() ProfilesAPI {
	if s.profilesService != nil {
		return s.profilesService
	}
	return configOnlyProfiles{s: s}
}

// errProfilesUnavailable is what the config-only reader answers for anything it
// cannot compute.
var errProfilesUnavailable = errors.New("profiles service unavailable")

type configOnlyProfiles struct{ s *Server }

func (c configOnlyProfiles) cfg() (*config.Config, error) {
	cfg, err := c.s.controller.GetConfig()
	if err != nil || cfg == nil {
		return nil, internalRuntime.ErrConfigUnavailable
	}
	return cfg, nil
}

func (c configOnlyProfiles) List(_ context.Context, viewer internalRuntime.ViewerScope) (*internalRuntime.ProfileList, error) {
	cfg, err := c.cfg()
	if err != nil {
		return nil, err
	}
	out := &internalRuntime.ProfileList{Profiles: make([]internalRuntime.ProfileView, 0, len(cfg.Profiles))}
	if !viewer.Restricted {
		out.AnonymousProfile = cfg.AnonymousProfile
	}
	for i := range cfg.Profiles {
		out.Profiles = append(out.Profiles, internalRuntime.ProfileViewFromConfig(cfg, &cfg.Profiles[i]))
	}
	return out, nil
}

func (c configOnlyProfiles) Get(_ context.Context, name string, _ internalRuntime.ViewerScope) (*internalRuntime.ProfileView, error) {
	cfg, err := c.cfg()
	if err != nil {
		return nil, err
	}
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == name {
			v := internalRuntime.ProfileViewFromConfig(cfg, &cfg.Profiles[i])
			return &v, nil
		}
	}
	return nil, &internalRuntime.ProfileNotFoundError{Name: name}
}

func (configOnlyProfiles) Create(context.Context, internalRuntime.Actor, config.ProfileConfig) (*internalRuntime.WriteResult, error) {
	return nil, errProfilesUnavailable
}
func (configOnlyProfiles) Update(context.Context, internalRuntime.Actor, string, config.ProfileConfig) (*internalRuntime.WriteResult, error) {
	return nil, errProfilesUnavailable
}
func (configOnlyProfiles) Rename(context.Context, internalRuntime.Actor, string, string) (*internalRuntime.RenameResult, error) {
	return nil, errProfilesUnavailable
}
func (configOnlyProfiles) Delete(context.Context, internalRuntime.Actor, string, string, bool) (*internalRuntime.DeleteResult, error) {
	return nil, errProfilesUnavailable
}
func (configOnlyProfiles) Try(context.Context, config.ProfileConfig, string, int) (*internalRuntime.TryResult, error) {
	return nil, internalRuntime.ErrEvaluatorUnavailable
}
func (configOnlyProfiles) EffectiveTools(context.Context, string, internalRuntime.EffectiveToolsOptions) (*internalRuntime.EffectiveToolsResult, error) {
	return nil, internalRuntime.ErrEvaluatorUnavailable
}
func (configOnlyProfiles) Explain(context.Context, profile.AccessSubject, string) (*internalRuntime.AccessExplanation, error) {
	return nil, internalRuntime.ErrEvaluatorUnavailable
}

// --- viewer and redaction --------------------------------------------------

// viewerScope derives the service's ViewerScope from the request's caller: the
// zero value for an administrator, else a restricted viewer that may learn only
// the servers it is entitled to.
func viewerScope(ctx context.Context) internalRuntime.ViewerScope {
	if !auth.IsScopedCaller(ctx) {
		return internalRuntime.ViewerScope{}
	}
	allowed, _ := scopeAllowedServers(ctx)
	return internalRuntime.ViewerScope{
		Restricted:     true,
		Visible:        func(server string) bool { return canSeeServer(ctx, server) },
		AllowedServers: allowed,
	}
}

// redactProfileView narrows a view for a non-administrator (FR-034, F26),
// failing closed: rules and switchable_to name servers and profiles a scoped
// caller must not learn (#1166). servers and effective_servers keep only the
// servers the caller may see; allow/deny/classify entries whose LITERAL server
// segment is not visible are dropped (a wildcard server segment is kept);
// switchable_to keeps only profiles the caller can reach; used_by is never
// present.
func redactProfileView(ctx context.Context, cfg *config.Config, v *internalRuntime.ProfileView) {
	v.UsedBy = nil
	v.Servers = keepVisible(ctx, v.Servers)
	v.EffectiveServers = keepVisible(ctx, v.EffectiveServers)
	if v.Tools != nil {
		t := *v.Tools
		t.Allow = keepVisibleRules(ctx, t.Allow)
		t.Deny = keepVisibleRules(ctx, t.Deny)
		if t.Classify != nil {
			kept := make(map[string]string, len(t.Classify))
			for pat, tier := range t.Classify {
				if ruleServerVisible(ctx, pat) {
					kept[pat] = tier
				}
			}
			t.Classify = kept
		}
		v.Tools = &t
	}
	if v.SwitchableTo != nil {
		kept := make([]string, 0, len(*v.SwitchableTo))
		for _, name := range *v.SwitchableTo {
			if profileReachable(ctx, cfg, name) {
				kept = append(kept, name)
			}
		}
		v.SwitchableTo = &kept
	}
}

func keepVisible(ctx context.Context, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if canSeeServer(ctx, n) {
			out = append(out, n)
		}
	}
	return out
}

// ruleServerVisible reports whether a server:tool rule pattern may be shown:
// a literal server segment must be visible; a wildcard server segment names no
// server and is kept.
func ruleServerVisible(ctx context.Context, pattern string) bool {
	i := strings.Index(pattern, ":")
	if i <= 0 {
		return false
	}
	server := pattern[:i]
	if strings.Contains(server, "*") {
		return true
	}
	return canSeeServer(ctx, server)
}

func keepVisibleRules(ctx context.Context, rules []string) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		if ruleServerVisible(ctx, r) {
			out = append(out, r)
		}
	}
	return out
}

// fillV2ToolCount sets the deprecated v2 tool_count: indexed tools on the
// profile's (already narrowed) effective servers.
func fillV2ToolCount(v *internalRuntime.ProfileView, toolCounts map[string]int) {
	total := 0
	for _, name := range v.EffectiveServers {
		total += toolCounts[name]
	}
	v.ToolCount = total
}

// serverToolCounts builds a server-name to indexed-tool-count map from the
// controller's server listing, tolerating the numeric type the map value is
// decoded as.
func (s *Server) serverToolCounts() map[string]int {
	counts := map[string]int{}
	servers, err := s.controller.GetAllServers()
	if err != nil {
		return counts
	}
	for _, sv := range servers {
		name, _ := sv["name"].(string)
		if name == "" {
			continue
		}
		switch v := sv["tool_count"].(type) {
		case int:
			counts[name] = v
		case int64:
			counts[name] = int(v)
		case float64:
			counts[name] = int(v)
		}
	}
	return counts
}

// --- reads -------------------------------------------------------------------

// handleListProfiles godoc
// @Summary List configured profiles
// @Description Lists every profile as a ProfileView: the config fields as stored, the derived effective values, visible tool counts by tier, 24 h calls and blocked calls, and (administrators only) used_by and the anonymous_profile. A caller that is not an administrator sees only the profiles its entitlement reaches, with servers, rules and switchable_to narrowed to what it may see; an unreachable profile is omitted, never shown empty.
// @Tags profiles
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.APIResponse{data=internalRuntime.ProfileList} "Profile list"
// @Failure 500 {object} contracts.ErrorResponse "Configuration unavailable"
// @Router /api/v1/profiles [get]
func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg, err := s.controller.GetConfig()
	if err != nil || cfg == nil {
		s.writeError(w, r, http.StatusInternalServerError, "Configuration unavailable")
		return
	}
	list, err := s.profiles().List(ctx, viewerScope(ctx))
	if err != nil {
		s.writeProfileServiceError(w, r, err)
		return
	}
	toolCounts := s.serverToolCounts()
	scoped := auth.IsScopedCaller(ctx)
	out := make([]internalRuntime.ProfileView, 0, len(list.Profiles))
	for i := range list.Profiles {
		v := list.Profiles[i]
		if scoped {
			// FR-034: a profile the caller's entitlement does not reach is
			// omitted entirely - never shown with servers:[] and a zero count,
			// which would confirm it exists and hand back a count oracle.
			if !profileReachable(ctx, cfg, v.Name) {
				continue
			}
			redactProfileView(ctx, cfg, &v)
		}
		fillV2ToolCount(&v, toolCounts)
		out = append(out, v)
	}
	resp := internalRuntime.ProfileList{Profiles: out}
	if !scoped {
		resp.AnonymousProfile = list.AnonymousProfile
	}
	s.writeSuccess(w, resp)
}

// handleGetProfile godoc
// @Summary Get one profile
// @Description Returns the ProfileView of the named profile. A caller that is not an administrator gets the same 404 for an unreachable profile as for an unknown one.
// @Tags profiles
// @Produce json
// @Param name path string true "Profile name"
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.APIResponse{data=internalRuntime.ProfileView} "The profile"
// @Failure 404 {object} contracts.ErrorResponse "profile not found"
// @Router /api/v1/profiles/{name} [get]
func (s *Server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := chi.URLParam(r, "name")
	cfg, err := s.controller.GetConfig()
	if err != nil || cfg == nil {
		s.writeError(w, r, http.StatusInternalServerError, "Configuration unavailable")
		return
	}
	scoped := auth.IsScopedCaller(ctx)
	// One lookup, then the reachability check: an unknown name and an
	// unreachable one take the same path and answer the same bytes.
	if scoped && !profileReachable(ctx, cfg, name) {
		s.writeError(w, r, http.StatusNotFound, errProfileNotFound)
		return
	}
	v, err := s.profiles().Get(ctx, name, viewerScope(ctx))
	if err != nil {
		s.writeProfileServiceError(w, r, err)
		return
	}
	if scoped {
		redactProfileView(ctx, cfg, v)
	}
	fillV2ToolCount(v, s.serverToolCounts())
	s.writeSuccess(w, v)
}

// --- error mapping ---------------------------------------------------------------

// ProfileConflictResponse is the 409 body of a refused profile write.
type ProfileConflictResponse struct {
	Success   bool                    `json:"success"` // Always false
	Error     string                  `json:"error"`
	Code      string                  `json:"code"`
	UsedBy    *internalRuntime.UsedBy `json:"used_by,omitempty"`
	RequestID string                  `json:"request_id,omitempty"`
}

func (s *Server) writeProfileConflict(w http.ResponseWriter, r *http.Request, msg, code string, used *internalRuntime.UsedBy) {
	s.writeJSON(w, http.StatusConflict, ProfileConflictResponse{
		Success: false, Error: msg, Code: code, UsedBy: used, RequestID: reqcontext.GetRequestID(r.Context()),
	})
}

// writeProfileServiceError maps a profiles-service error to its wire shape
// (contracts/rest-api.md, contracts/refusals.md).
func (s *Server) writeProfileServiceError(w http.ResponseWriter, r *http.Request, err error) {
	if s.writeIfBindingGuardRefusal(w, r, err) {
		return
	}
	var notFound *internalRuntime.ProfileNotFoundError
	var exists *internalRuntime.ProfileExistsError
	var mismatch *internalRuntime.NameMismatchError
	var inUse *internalRuntime.ProfileInUseError
	var anon *internalRuntime.ProfileIsAnonymousError
	var val *internalRuntime.ValidationError
	switch {
	case errors.As(err, &notFound), errors.Is(err, profile.ErrUnknownProfile):
		s.writeError(w, r, http.StatusNotFound, errProfileNotFound)
	case errors.As(err, &exists):
		s.writeProfileConflict(w, r, exists.Error(), exists.Code(), nil)
	case errors.As(err, &mismatch):
		s.writeProfileConflict(w, r, mismatch.Error(), mismatch.Code(), nil)
	case errors.As(err, &inUse):
		u := inUse.UsedBy
		s.writeProfileConflict(w, r, inUse.Error(), inUse.Code(), &u)
	case errors.As(err, &anon):
		u := anon.UsedBy
		s.writeProfileConflict(w, r, anon.Error(), anon.Code(), &u)
	case errors.As(err, &val):
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", val.Field, val.Message)
	case errors.Is(err, profile.ErrUnknownClient):
		s.writeError(w, r, http.StatusNotFound, errClientNotFound)
	case errors.Is(err, internalRuntime.ErrEvaluatorUnavailable), errors.Is(err, errProfilesUnavailable):
		s.writeError(w, r, http.StatusServiceUnavailable, "profiles service unavailable")
	case errors.Is(err, internalRuntime.ErrConfigUnavailable):
		s.writeError(w, r, http.StatusInternalServerError, "Configuration unavailable")
	default:
		s.logger.Errorw("profiles operation failed", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "profiles operation failed")
	}
}
