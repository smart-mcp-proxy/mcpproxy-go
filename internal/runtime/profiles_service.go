package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// reservedRESTProfileNames are the names POST /profiles and rename refuse for
// a NEW profile (F5): chi's static /profiles/active and /profiles/try routes
// shadow /profiles/{name}. The config loader is unchanged, so a hand-written
// profile with such a name still loads.
var reservedRESTProfileNames = map[string]bool{"active": true, "try": true}

// ProfileSessionHook is implemented by the server: a rename rewrites the stored
// set_profile selection of live sessions, a delete clears it (F3).
type ProfileSessionHook interface {
	ProfileRenamed(from, to string)
	ProfileDeleted(name string)
	// ProfileTokensMoved is called after a delete-with-reassign moved these
	// tokens' pins: their sessions' base changed, so each is re-listed and its
	// stored selection cleared (FR-026 semantics).
	ProfileTokensMoved(tokenNames []string)
}

// ProfilesService is THE service behind every profile operation (Spec 108
// FR-034): list, get, create, update, rename, delete, classify, set the
// anonymous_profile, plus the evaluator-backed effective-tools, try and explain.
// REST, the CLI, the MCP `profiles` tool, the Web UI and the macOS app all call
// it, so records, guards and refusals cannot drift between surfaces.
//
// Every mutation goes through Runtime.MutateConfig, which holds the runtime's
// binding lock across "read, mutate, FR-008a guard, token re-pin, apply, write
// the profile_change record" (F2), so a profile write and a client-binding
// write can never combine into a bypassable state.
type ProfilesService struct {
	rt *Runtime

	statsMu    sync.Mutex
	statsCache map[string]cachedStats
	now        func() time.Time
}

type cachedStats struct {
	at    time.Time
	stats ActivityStats24h
}

// statsTTL is how long the 24 h activity rollup is reused (F27).
const statsTTL = 30 * time.Second

// Counter is a calls/blocked pair over the last 24 hours.
type Counter struct {
	Calls   int
	Blocked int
}

// ActivityStats24h is the last-24 h rollup of tool calls and blocked calls by
// profile and by client (F27).
type ActivityStats24h struct {
	ByProfile map[string]Counter
	ByClient  map[string]Counter
}

func newProfilesService(rt *Runtime) *ProfilesService {
	return &ProfilesService{rt: rt, statsCache: map[string]cachedStats{}, now: time.Now}
}

// ProfilesService returns the runtime's single profiles service.
func (r *Runtime) ProfilesService() *ProfilesService { return r.profilesService }

// SetProfileEvaluator installs the server-side evaluator (the SetBindingGuard
// pattern). Until it is installed, effective-tools, try and explain answer
// ErrEvaluatorUnavailable.
func (r *Runtime) SetProfileEvaluator(e ProfileEvaluator) {
	r.profileEvaluatorMu.Lock()
	r.profileEvaluator = e
	r.profileEvaluatorMu.Unlock()
}

// SetSessionHook installs the live-session rewriter used by rename and delete.
func (s *ProfilesService) SetSessionHook(h ProfileSessionHook) {
	s.rt.profileEvaluatorMu.Lock()
	s.rt.profileSessionHook = h
	s.rt.profileEvaluatorMu.Unlock()
}

func (s *ProfilesService) evaluator() ProfileEvaluator {
	s.rt.profileEvaluatorMu.RLock()
	defer s.rt.profileEvaluatorMu.RUnlock()
	return s.rt.profileEvaluator
}

func (s *ProfilesService) sessionHook() ProfileSessionHook {
	s.rt.profileEvaluatorMu.RLock()
	defer s.rt.profileEvaluatorMu.RUnlock()
	return s.rt.profileSessionHook
}

// --- reads -----------------------------------------------------------------

// List returns every profile as a view, and the anonymous_profile. viewer
// bounds tool counts and stats; the caller-specific redaction of names
// (servers, rules, switchable_to) and the used_by omission are the REST layer's
// (it holds the auth context).
func (s *ProfilesService) List(ctx context.Context, viewer ViewerScope) (*ProfileList, error) {
	cfg := s.rt.Config()
	if cfg == nil {
		return nil, ErrConfigUnavailable
	}
	stats := s.Stats24h(ctx, viewer)
	var used map[string]*UsedBy
	if !viewer.Restricted {
		var err error
		if used, err = s.usedByAll(cfg); err != nil {
			return nil, err
		}
	}
	out := &ProfileList{Profiles: make([]ProfileView, 0, len(cfg.Profiles))}
	if !viewer.Restricted {
		out.AnonymousProfile = cfg.AnonymousProfile
	}
	for i := range cfg.Profiles {
		v := s.view(ctx, cfg, &cfg.Profiles[i], viewer, stats)
		if used != nil {
			v.UsedBy = used[cfg.Profiles[i].Name]
		}
		out.Profiles = append(out.Profiles, v)
	}
	return out, nil
}

// Get returns one profile's view, or *ProfileNotFoundError.
func (s *ProfilesService) Get(ctx context.Context, name string, viewer ViewerScope) (*ProfileView, error) {
	cfg := s.rt.Config()
	if cfg == nil {
		return nil, ErrConfigUnavailable
	}
	return s.getFrom(ctx, cfg, name, viewer)
}

func (s *ProfilesService) getFrom(ctx context.Context, cfg *config.Config, name string, viewer ViewerScope) (*ProfileView, error) {
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name != name {
			continue
		}
		v := s.view(ctx, cfg, &cfg.Profiles[i], viewer, s.Stats24h(ctx, viewer))
		if !viewer.Restricted {
			u, err := s.UsedBy(name)
			if err != nil {
				return nil, err
			}
			u.AnonymousProfile = cfg.AnonymousProfile == name
			v.UsedBy = &u
		}
		return &v, nil
	}
	return nil, &ProfileNotFoundError{Name: name}
}

// view builds a ProfileView from stored config. The 24 h stats and tool counts
// are computed for the viewer's scope.
func (s *ProfilesService) view(ctx context.Context, cfg *config.Config, p *config.ProfileConfig, viewer ViewerScope, stats ActivityStats24h) ProfileView {
	v := ProfileViewFromConfig(cfg, p)
	if ev := s.evaluator(); ev != nil {
		v.ToolCounts = ev.ToolCounts(ctx, p.Name, viewer)
	}
	c := stats.ByProfile[p.Name]
	v.Calls24h, v.Blocked24h = c.Calls, c.Blocked
	return v
}

// ProfileViewFromConfig projects a stored profile and its derived effective
// values (no tool counts, stats or used_by: those need the runtime). It copies
// every slice and map, so the view never aliases the live config.
func ProfileViewFromConfig(cfg *config.Config, p *config.ProfileConfig) ProfileView {
	v := ProfileView{
		Name: p.Name, Title: p.Title, Description: p.Description,
		Servers: append([]string{}, p.Servers...), MaxTier: p.MaxTier, Unannotated: p.Unannotated,
		CodeExecution: p.CodeExecution, ManagementTools: p.ManagementTools,
		EffectiveServers:       p.EffectiveServers(cfg),
		EffectiveUnannotated:   p.EffectiveUnannotated(),
		EffectiveCodeExecution: p.EffectiveCodeExecution(),
		IsLegacy:               p.IsLegacy(),
	}
	if p.Tools != nil {
		t := config.ProfileToolRules{
			Allow: append([]string(nil), p.Tools.Allow...),
			Deny:  append([]string(nil), p.Tools.Deny...),
		}
		if p.Tools.Classify != nil {
			t.Classify = make(map[string]string, len(p.Tools.Classify))
			for k, val := range p.Tools.Classify {
				t.Classify[k] = val
			}
		}
		v.Tools = &t
	}
	if p.SwitchableTo != nil {
		st := append([]string{}, (*p.SwitchableTo)...)
		v.SwitchableTo = &st
	}
	return v
}

// usedByAll computes used_by for every profile in one pass over the token
// store.
func (s *ProfilesService) usedByAll(cfg *config.Config) (map[string]*UsedBy, error) {
	tokens, err := s.tokens()
	if err != nil {
		return nil, err
	}
	out := make(map[string]*UsedBy, len(cfg.Profiles))
	for i := range cfg.Profiles {
		out[cfg.Profiles[i].Name] = &UsedBy{Clients: []UsedByClient{}, Tokens: []string{}, AnonymousProfile: cfg.AnonymousProfile == cfg.Profiles[i].Name}
	}
	for i := range tokens {
		t := &tokens[i]
		u, ok := out[t.ProfilePin]
		if !ok || t.Revoked || t.UserID != "" {
			continue
		}
		if t.Kind == auth.KindClient {
			u.Clients = append(u.Clients, UsedByClient{ID: t.ClientID, Mode: t.ProfileMode})
		} else {
			u.Tokens = append(u.Tokens, t.Name)
		}
	}
	for _, u := range out {
		sortUsedBy(u)
	}
	return out, nil
}

func sortUsedBy(u *UsedBy) {
	sort.Slice(u.Clients, func(i, j int) bool { return u.Clients[i].ID < u.Clients[j].ID })
	sort.Strings(u.Tokens)
}

func (s *ProfilesService) tokens() ([]auth.AgentToken, error) {
	sm := s.rt.StorageManager()
	if sm == nil {
		return nil, nil
	}
	all, err := sm.ListAgentTokens()
	if err != nil {
		return nil, fmt.Errorf("cannot read tokens: %w", err)
	}
	return all, nil
}

// UsedBy lists what points at the named profile.
func (s *ProfilesService) UsedBy(name string) (UsedBy, error) {
	u := UsedBy{Clients: []UsedByClient{}, Tokens: []string{}}
	tokens, err := s.tokens()
	if err != nil {
		return u, err
	}
	for i := range tokens {
		t := &tokens[i]
		if t.ProfilePin != name || t.Revoked || t.UserID != "" {
			continue
		}
		if t.Kind == auth.KindClient {
			u.Clients = append(u.Clients, UsedByClient{ID: t.ClientID, Mode: t.ProfileMode})
		} else {
			u.Tokens = append(u.Tokens, t.Name)
		}
	}
	sortUsedBy(&u)
	if cfg := s.rt.Config(); cfg != nil {
		u.AnonymousProfile = cfg.AnonymousProfile == name
	}
	return u, nil
}

// Stats24h returns the last-24 h rollup of calls and blocked calls by profile
// and by client for the viewer's scope, cached for 30 s per scope (F27). It is
// one streaming pass over a 24 h window: no new aggregate exists to keep.
func (s *ProfilesService) Stats24h(ctx context.Context, viewer ViewerScope) ActivityStats24h {
	key := ""
	var filterServers []string
	if viewer.Restricted {
		filterServers = append([]string{}, viewer.AllowedServers...)
		if filterServers == nil {
			filterServers = []string{}
		}
		sort.Strings(filterServers)
		key = "scoped:" + strings.Join(filterServers, ",")
	}
	now := s.now()
	s.statsMu.Lock()
	if c, ok := s.statsCache[key]; ok && now.Sub(c.at) < statsTTL {
		s.statsMu.Unlock()
		return c.stats
	}
	s.statsMu.Unlock()

	stats := ActivityStats24h{ByProfile: map[string]Counter{}, ByClient: map[string]Counter{}}
	filter := storage.ActivityFilter{
		Types:          []string{string(storage.ActivityTypeToolCall), string(storage.ActivityTypePolicyDecision)},
		StartTime:      now.Add(-24 * time.Hour),
		AllowedServers: filterServers,
	}
	for rec := range s.rt.StreamActivities(filter) {
		if ctx.Err() != nil {
			break
		}
		blocked := rec.Status == "blocked"
		isCall := rec.Type == storage.ActivityTypeToolCall
		if !isCall && !blocked {
			continue
		}
		add := func(m map[string]Counter, k string) {
			if k == "" {
				return
			}
			c := m[k]
			if isCall {
				c.Calls++
			}
			if blocked {
				c.Blocked++
			}
			m[k] = c
		}
		add(stats.ByProfile, rec.EffectiveProfile())
		add(stats.ByClient, rec.ClientID)
	}

	s.statsMu.Lock()
	s.statsCache[key] = cachedStats{at: now, stats: stats}
	s.statsMu.Unlock()
	return stats
}

// --- evaluator-backed reads ---------------------------------------------------

// EffectiveTools delegates to the evaluator (ErrEvaluatorUnavailable when none).
func (s *ProfilesService) EffectiveTools(ctx context.Context, name string, opt EffectiveToolsOptions) (*EffectiveToolsResult, error) {
	ev := s.evaluator()
	if ev == nil {
		return nil, ErrEvaluatorUnavailable
	}
	return ev.EffectiveTools(ctx, name, opt)
}

// Try evaluates a draft profile against a query: nothing is persisted, no
// record is written, no event is published and the FR-008a guard does not run.
func (s *ProfilesService) Try(ctx context.Context, draft config.ProfileConfig, query string, limit int) (*TryResult, error) {
	ev := s.evaluator()
	if ev == nil {
		return nil, ErrEvaluatorUnavailable
	}
	cfg := s.rt.Config()
	if cfg == nil {
		return nil, ErrConfigUnavailable
	}
	if _, err := validateDraft(cfg, draft); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	return ev.TryProfile(ctx, draft, query, limit)
}

// validateDraft validates a draft as if it replaced the same-named profile, or
// was appended under a placeholder name when it has none (F28).
func validateDraft(cfg *config.Config, draft config.ProfileConfig) ([]string, error) {
	candidate := *cfg
	candidate.Profiles = append([]config.ProfileConfig{}, cfg.Profiles...)
	name := draft.Name
	if name == "" {
		name = "try-draft"
		draft.Name = name
	}
	replaced := false
	for i := range candidate.Profiles {
		if candidate.Profiles[i].Name == name {
			candidate.Profiles[i] = draft
			replaced = true
		}
	}
	if !replaced {
		candidate.Profiles = append(candidate.Profiles, draft)
	}
	return validateProfilesFor(&candidate, name)
}

// Explain delegates to the evaluator (ErrEvaluatorUnavailable when none).
func (s *ProfilesService) Explain(ctx context.Context, subject profile.AccessSubject, tool string) (*AccessExplanation, error) {
	ev := s.evaluator()
	if ev == nil {
		return nil, ErrEvaluatorUnavailable
	}
	return ev.Explain(ctx, subject, tool)
}

// --- validation ---------------------------------------------------------------

// validateProfilesFor runs the ONE profile validator over cfg and returns the
// warnings about the named profile; a fatal rule is a *ValidationError naming
// the field (F6).
func validateProfilesFor(cfg *config.Config, name string) ([]string, error) {
	warnings, err := config.ValidateProfilesDetailed(cfg)
	if err != nil {
		var pe *config.ProfileValidationError
		if errors.As(err, &pe) {
			return nil, &ValidationError{Field: pe.Field, Message: pe.Message}
		}
		return nil, &ValidationError{Message: err.Error()}
	}
	out := []string{}
	for _, w := range warnings {
		if w.Profile == name {
			out = append(out, w.Message)
		}
	}
	return out, nil
}

func indexOfProfile(cfg *config.Config, name string) int {
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == name {
			return i
		}
	}
	return -1
}

func refuseReservedName(field, name string) error {
	if reservedRESTProfileNames[name] {
		return &ValidationError{Field: field, Message: fmt.Sprintf("profile name %q is reserved by the REST API", name)}
	}
	return nil
}

// --- mutations ------------------------------------------------------------------

// Create adds a profile. A name that exists is *ProfileExistsError; a fatal
// validation rule is a *ValidationError with the offending field; the FR-008a
// guard may refuse with *BindingGuardError. Warnings are the validator's
// non-fatal findings about the new profile.
func (s *ProfilesService) Create(ctx context.Context, a Actor, p config.ProfileConfig) (*WriteResult, error) {
	if err := refuseReservedName("name", p.Name); err != nil {
		return nil, err
	}
	var warnings []string
	_, _, err := s.rt.MutateConfig(ctx, a, func(d *config.Config) (ChangeHint, error) {
		if indexOfProfile(d, p.Name) >= 0 {
			return ChangeHint{}, &ProfileExistsError{Name: p.Name}
		}
		d.Profiles = append(d.Profiles, p)
		var err error
		warnings, err = validateProfilesFor(d, p.Name)
		return ChangeHint{}, err
	}, TokenRewrite{})
	if err != nil {
		return nil, err
	}
	return s.written(ctx, p.Name, warnings)
}

func (s *ProfilesService) written(ctx context.Context, name string, warnings []string) (*WriteResult, error) {
	v, err := s.Get(ctx, name, ViewerScope{})
	if err != nil {
		return nil, err
	}
	if warnings == nil {
		warnings = []string{}
	}
	return &WriteResult{Profile: *v, Warnings: warnings}, nil
}

// Update replaces the named profile with p. The body's name must equal name
// (*NameMismatchError; a rename has its own operation). A write whose only
// change is tools.classify is recorded as `classify`.
func (s *ProfilesService) Update(ctx context.Context, a Actor, name string, p config.ProfileConfig) (*WriteResult, error) {
	if p.Name != name {
		return nil, &NameMismatchError{Path: name, Body: p.Name}
	}
	var warnings []string
	_, _, err := s.rt.MutateConfig(ctx, a, func(d *config.Config) (ChangeHint, error) {
		i := indexOfProfile(d, name)
		if i < 0 {
			return ChangeHint{}, &ProfileNotFoundError{Name: name}
		}
		old := d.Profiles[i]
		d.Profiles[i] = p
		var err error
		if warnings, err = validateProfilesFor(d, name); err != nil {
			return ChangeHint{}, err
		}
		if diff := diffProfileFields(old, p); len(diff) == 1 {
			if _, only := diff["tools.classify"]; only {
				return ChangeHint{Kind: profile.ChangeClassify, Profile: name, PreviousProfile: name, Diff: diff}, nil
			}
		}
		return ChangeHint{}, nil
	}, TokenRewrite{})
	if err != nil {
		return nil, err
	}
	return s.written(ctx, name, warnings)
}

// Rename renames a profile and moves every reference to it - token pins,
// client bindings, other profiles' switchable_to and the anonymous_profile -
// in one write (D19). The order never widens: tokens move first (F3).
func (s *ProfilesService) Rename(ctx context.Context, a Actor, name, newName string) (*RenameResult, error) {
	if err := refuseReservedName("new_name", newName); err != nil {
		return nil, err
	}
	var moved MovedRefs
	_, _, err := s.rt.MutateConfig(ctx, a, func(d *config.Config) (ChangeHint, error) {
		i := indexOfProfile(d, name)
		if i < 0 {
			return ChangeHint{}, &ProfileNotFoundError{Name: name}
		}
		if newName != name && indexOfProfile(d, newName) >= 0 {
			return ChangeHint{}, &ProfileExistsError{Name: newName}
		}
		if newName == name {
			return ChangeHint{}, &ValidationError{Field: "new_name", Message: "the new name equals the current name"}
		}
		u, err := s.UsedBy(name)
		if err != nil {
			return ChangeHint{}, err
		}
		moved = movedRefs(u)
		d.Profiles[i].Name = newName
		for j := range d.Profiles {
			if d.Profiles[j].SwitchableTo == nil {
				continue
			}
			list := append([]string{}, (*d.Profiles[j].SwitchableTo)...)
			for k := range list {
				if list[k] == name {
					list[k] = newName
				}
			}
			d.Profiles[j].SwitchableTo = &list
		}
		if d.AnonymousProfile == name {
			d.AnonymousProfile = newName
		}
		if _, err := validateProfilesFor(d, newName); err != nil {
			if ve, ok := err.(*ValidationError); ok && ve.Field == "name" {
				ve.Field = "new_name"
			}
			return ChangeHint{}, err
		}
		return ChangeHint{Kind: profile.ChangeRename, Profile: newName, PreviousProfile: name,
			Diff: map[string]interface{}{"name": map[string]interface{}{"from": name, "to": newName}}}, nil
	}, TokenRewrite{From: name, To: newName})
	if err != nil {
		return nil, err
	}
	if h := s.sessionHook(); h != nil {
		h.ProfileRenamed(name, newName)
	}
	v, err := s.Get(ctx, newName, ViewerScope{})
	if err != nil {
		return nil, err
	}
	return &RenameResult{Profile: *v, Moved: moved}, nil
}

func movedRefs(u UsedBy) MovedRefs {
	m := MovedRefs{Clients: []string{}, Tokens: append([]string{}, u.Tokens...)}
	for _, c := range u.Clients {
		m.Clients = append(m.Clients, c.ID)
	}
	return m
}

// Delete removes a profile (F4). It is *ProfileInUseError while clients or
// tokens point at it and neither reassignTo nor force is given;
// *ProfileIsAnonymousError whenever it is the anonymous_profile and reassignTo
// is empty, even with force. reassignTo must name another existing profile;
// force leaves pins dangling (deny-all). Both paths remove the name from every
// switchable_to.
func (s *ProfilesService) Delete(ctx context.Context, a Actor, name, reassignTo string, force bool) (*DeleteResult, error) {
	res := &DeleteResult{Deleted: name, Moved: MovedRefs{Clients: []string{}, Tokens: []string{}}}
	tokens := TokenRewrite{}
	if reassignTo != "" && reassignTo != name {
		tokens = TokenRewrite{From: name, To: reassignTo}
	}
	_, _, err := s.rt.MutateConfig(ctx, a, func(d *config.Config) (ChangeHint, error) {
		i := indexOfProfile(d, name)
		if i < 0 {
			return ChangeHint{}, &ProfileNotFoundError{Name: name}
		}
		u, err := s.UsedBy(name)
		if err != nil {
			return ChangeHint{}, err
		}
		u.AnonymousProfile = d.AnonymousProfile == name
		if u.AnonymousProfile && reassignTo == "" {
			return ChangeHint{}, &ProfileIsAnonymousError{UsedBy: u}
		}
		if !u.Empty() && reassignTo == "" && !force {
			return ChangeHint{}, &ProfileInUseError{UsedBy: u}
		}
		if reassignTo != "" {
			if reassignTo == name || indexOfProfile(d, reassignTo) < 0 {
				return ChangeHint{}, &ValidationError{Field: "reassign_to", Message: fmt.Sprintf("reassign_to must name another existing profile, got %q", reassignTo)}
			}
			res.Moved = movedRefs(u)
			res.movedTokenNames = u.tokenNames()
			if u.AnonymousProfile {
				d.AnonymousProfile = reassignTo
				res.AnonymousProfileMovedTo = reassignTo
			}
		}
		d.Profiles = append(d.Profiles[:i:i], d.Profiles[i+1:]...)
		for j := range d.Profiles {
			if d.Profiles[j].SwitchableTo == nil {
				continue
			}
			kept := []string{}
			for _, t := range *d.Profiles[j].SwitchableTo {
				if t != name {
					kept = append(kept, t)
				}
			}
			d.Profiles[j].SwitchableTo = &kept
		}
		diff := map[string]interface{}{"force": force}
		if reassignTo != "" {
			diff["reassign_to"] = reassignTo
			diff["moved"] = map[string]interface{}{"clients": res.Moved.Clients, "tokens": res.Moved.Tokens}
		}
		if res.AnonymousProfileMovedTo != "" {
			diff["anonymous_profile_moved_to"] = res.AnonymousProfileMovedTo
		}
		return ChangeHint{Kind: profile.ChangeDelete, Profile: reassignTo, PreviousProfile: name, Diff: diff}, nil
	}, tokens)
	if err != nil {
		return nil, err
	}
	if h := s.sessionHook(); h != nil {
		h.ProfileDeleted(name)
		if reassignTo != "" {
			h.ProfileTokensMoved(res.movedTokenNames)
		}
	}
	return res, nil
}

// Classify sets (or, with tier "", removes) the classification of one exact
// server:tool under the named profile, atomically under the write lock, and
// records a `classify` change (F29).
func (s *ProfilesService) Classify(ctx context.Context, a Actor, name, tool, tier string) (*ProfileView, error) {
	if tier != "" && tier != config.ProfileTierRead && tier != config.ProfileTierWrite && tier != config.ProfileTierDestructive {
		return nil, &ValidationError{Field: "tools.classify", Message: fmt.Sprintf("invalid classify tier %q: must be one of read, write, destructive", tier)}
	}
	if !strings.Contains(tool, ":") || strings.ContainsAny(tool, "*") || strings.HasPrefix(tool, ":") || strings.HasSuffix(tool, ":") {
		return nil, &ValidationError{Field: "tools.classify", Message: fmt.Sprintf("invalid tool pattern %q", tool)}
	}
	_, _, err := s.rt.MutateConfig(ctx, a, func(d *config.Config) (ChangeHint, error) {
		i := indexOfProfile(d, name)
		if i < 0 {
			return ChangeHint{}, &ProfileNotFoundError{Name: name}
		}
		p := &d.Profiles[i]
		rules := config.ProfileToolRules{}
		if p.Tools != nil {
			rules = *p.Tools
		}
		classify := map[string]string{}
		for k, v := range rules.Classify {
			classify[k] = v
		}
		from := classify[tool]
		if tier == "" {
			delete(classify, tool)
		} else {
			classify[tool] = tier
		}
		if len(classify) == 0 {
			classify = nil
		}
		rules.Classify = classify
		p.Tools = &rules
		if _, err := validateProfilesFor(d, name); err != nil {
			return ChangeHint{}, err
		}
		return ChangeHint{Kind: profile.ChangeClassify, Profile: name, PreviousProfile: name,
			Diff: map[string]interface{}{"tools.classify": map[string]interface{}{tool: map[string]interface{}{"from": from, "to": tier}}}}, nil
	}, TokenRewrite{})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, name, ViewerScope{})
}

// SetAnonymous sets (or, with "", clears) the anonymous_profile. An unknown
// name is a *ValidationError on the field anonymous_profile.
func (s *ProfilesService) SetAnonymous(ctx context.Context, a Actor, name string) error {
	_, _, err := s.rt.MutateConfig(ctx, a, func(d *config.Config) (ChangeHint, error) {
		prev := d.AnonymousProfile
		d.AnonymousProfile = name
		return ChangeHint{Kind: profile.ChangeAnonymous, Profile: name, PreviousProfile: prev,
			Diff: map[string]interface{}{"anonymous_profile": map[string]interface{}{"from": prev, "to": name}}}, nil
	}, TokenRewrite{})
	return err
}
