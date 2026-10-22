package runtime

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// `access suggest` (issue #1700): turn the profile refusals already in the
// activity log into reviewed profile changes. It is a READER. It reads blocked
// records, groups them, re-asks the access explainer whether each group is
// still blocked, and renders the explainer's own fixes. It never writes a
// profile: applying a change is a separate, explicit act (`profile update`,
// `profile classify`, or PUT /api/v1/profiles/{name}).
//
// Only profile causes are handled (profile_tier, profile_rule,
// profile_unannotated, profile_server_scope, profile_code_execution,
// profile_management). A call refused for another reason (quarantine, a token
// permission, a global gate) is counted in NonProfileBlocked and left to
// `access explain`.

// DefaultSuggestWindow is the look-back window when the caller names none. The
// audit-log retention is the only upper limit.
const DefaultSuggestWindow = 24 * time.Hour

// ParseSuggestWindow parses the --since / since= value: a Go duration (90m,
// 24h) or whole days (7d). "" is DefaultSuggestWindow. Zero and negative
// windows are refused.
// maxSuggestWindowDays bounds the "<n>d" form: retention is the real limit,
// and a day count past this would overflow time.Duration and wrap.
const maxSuggestWindowDays = 3650

func ParseSuggestWindow(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return DefaultSuggestWindow, nil
	}
	var d time.Duration
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("invalid since %q: use a duration such as 90m, 24h or 7d", v)
		}
		if n > maxSuggestWindowDays {
			return 0, fmt.Errorf("invalid since %q: at most %dd", v, maxSuggestWindowDays)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(v); err != nil {
			return 0, fmt.Errorf("invalid since %q: use a duration such as 90m, 24h or 7d", v)
		}
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid since %q: must be positive", v)
	}
	return d, nil
}

// SuggestionKind is the one change a suggestion proposes.
type SuggestionKind string

const (
	// SuggestAllow adds the tool to the profile's tools.allow.
	SuggestAllow SuggestionKind = "allow"
	// SuggestAddServer adds the server to the profile's servers.
	SuggestAddServer SuggestionKind = "add_server"
	// SuggestClassify gives an unannotated tool a tier. The tier is never
	// pre-filled: a person decides read, write or destructive.
	SuggestClassify SuggestionKind = "classify"
	// SuggestNone means no profile change is proposed; Note says why.
	SuggestNone SuggestionKind = "none"
)

// SuggestRequest narrows one suggest run.
type SuggestRequest struct {
	// Since is the look-back window; 0 means DefaultSuggestWindow.
	Since time.Duration
	// Profile and Token restrict the run to one profile or one token ("" = all).
	Profile string
	Token   string
	// IncludePatch also returns, per profile, the proposed profile document.
	IncludePatch bool
}

// AccessSuggestion is one group of identical refusals: the same profile,
// server, tool and block reason.
type AccessSuggestion struct {
	Profile     string              `json:"profile"`
	Tool        string              `json:"tool"` // "server:tool", or the built-in name
	Server      string              `json:"server,omitempty"`
	BlockReason profile.BlockReason `json:"block_reason"`
	Count       int                 `json:"count"`
	Tokens      []string            `json:"tokens"`
	Clients     []string            `json:"clients"`
	FirstSeen   time.Time           `json:"first_seen"`
	LastSeen    time.Time           `json:"last_seen"`
	Suggestion  SuggestionKind      `json:"suggestion"`
	// Fixes are the access explainer's remediations of the first failing step,
	// in preference order. Empty for a deny rule and for the built-in tools.
	Fixes []Fix `json:"fixes"`
	// NeedsInput names what a person must supply before a change is
	// apply-ready (classify: "tier").
	NeedsInput []string `json:"needs_input,omitempty"`
	// Warning is set for profile_code_execution and profile_management: both
	// change what a profile can do beyond tool access.
	Warning string `json:"warning,omitempty"`
	Note    string `json:"note,omitempty"`
}

// ProfilePatch is the proposed stored profile after applying every apply-ready
// suggestion for it. Proposed is a whole config.ProfileConfig, the document
// `PUT /api/v1/profiles/{name}` takes and the `profiles[]` entry of the config
// file. Changes lists what differs from the stored profile.
type ProfilePatch struct {
	Profile  string               `json:"profile"`
	Changes  []string             `json:"changes"`
	Proposed config.ProfileConfig `json:"proposed"`
}

// AccessSuggestions is the whole answer.
type AccessSuggestions struct {
	Since       time.Time          `json:"since"`
	Until       time.Time          `json:"until"`
	Window      string             `json:"window"`
	Suggestions []AccessSuggestion `json:"suggestions"`
	Patches     []ProfilePatch     `json:"patches,omitempty"`

	// BlockedCalls counts every profile-refused call in the window.
	BlockedCalls int `json:"blocked_calls"`
	// AlreadyFixed counts groups the explainer now allows (they drop out).
	AlreadyFixed int `json:"already_fixed"`
	// ProfileRemoved counts groups whose profile no longer exists (they drop out).
	ProfileRemoved int `json:"profile_removed"`
	// NonProfileBlocked counts blocked calls with a cause outside profiles.
	NonProfileBlocked int `json:"non_profile_blocked"`
	// Unattributed counts profile refusals whose record names no profile.
	Unattributed int `json:"unattributed"`
}

type suggestKey struct {
	profile, server, tool string
	reason                profile.BlockReason
}

type suggestGroup struct {
	AccessSuggestion
	tokens, clients map[string]bool
}

// profileBlockReasons is the set of activity block reasons this command reads.
var profileBlockReasons = map[profile.BlockReason]bool{
	profile.BlockReasonTier:          true,
	profile.BlockReasonRule:          true,
	profile.BlockReasonUnannotated:   true,
	profile.BlockReasonCodeExecution: true,
	profile.BlockReasonManagement:    true,
	profile.BlockReasonServerScope:   true,
}

// Suggest reads the blocked profile refusals in the window and returns the
// grouped, re-checked suggestions. Server-scope suggestions name servers the
// profile cannot see, so REST and MCP expose this to administrators only.
func (s *ProfilesService) Suggest(ctx context.Context, req SuggestRequest) (*AccessSuggestions, error) {
	ev := s.evaluator()
	if ev == nil {
		return nil, ErrEvaluatorUnavailable
	}
	cfg := s.rt.Config()
	if cfg == nil {
		return nil, ErrConfigUnavailable
	}
	window := req.Since
	if window <= 0 {
		window = DefaultSuggestWindow
	}
	until := s.now()
	out := &AccessSuggestions{
		Since:       until.Add(-window),
		Until:       until,
		Window:      window.String(),
		Suggestions: []AccessSuggestion{},
	}

	groups := map[suggestKey]*suggestGroup{}
	seen := map[string]bool{}
	filter := storage.ActivityFilter{
		Types:     []string{string(storage.ActivityTypeToolCall), string(storage.ActivityTypePolicyDecision)},
		Status:    "blocked",
		StartTime: out.Since,
		Profile:   req.Profile,
		TokenName: req.Token,
	}
	stream := s.rt.StreamActivities(filter)
	for rec := range stream {
		if ctx.Err() != nil {
			drainActivities(stream)
			return nil, ctx.Err()
		}
		reason := profile.BlockReason(rec.EffectiveBlockReason())
		if !profileBlockReasons[reason] {
			out.NonProfileBlocked++
			continue
		}
		// One refusal can leave a policy_decision and a tool_call record that
		// share a request id; count it once.
		if rec.RequestID != "" {
			id := rec.RequestID + "|" + rec.ServerName + "|" + rec.ToolName + "|" + string(reason)
			if seen[id] {
				continue
			}
			seen[id] = true
		}
		out.BlockedCalls++
		prof := rec.EffectiveProfile()
		if prof == "" {
			out.Unattributed++
			continue
		}
		k := suggestKey{profile: prof, server: rec.ServerName, tool: rec.ToolName, reason: reason}
		g := groups[k]
		if g == nil {
			g = &suggestGroup{tokens: map[string]bool{}, clients: map[string]bool{}}
			g.Profile, g.Server, g.BlockReason = prof, rec.ServerName, reason
			g.Tool = rec.ToolName
			if rec.ServerName != "" {
				g.Tool = rec.ServerName + ":" + rec.ToolName
			}
			g.FirstSeen, g.LastSeen = rec.Timestamp, rec.Timestamp
			groups[k] = g
		}
		g.Count++
		if rec.Timestamp.Before(g.FirstSeen) {
			g.FirstSeen = rec.Timestamp
		}
		if rec.Timestamp.After(g.LastSeen) {
			g.LastSeen = rec.Timestamp
		}
		if t := rec.EffectiveTokenName(); t != "" {
			g.tokens[t] = true
		}
		if rec.ClientID != "" {
			g.clients[rec.ClientID] = true
		}
	}

	for _, g := range groups {
		keep, err := s.finishSuggestion(ctx, ev, cfg, g, out)
		if err != nil {
			return nil, err
		}
		if keep {
			g.Tokens, g.Clients = sortedKeys(g.tokens), sortedKeys(g.clients)
			out.Suggestions = append(out.Suggestions, g.AccessSuggestion)
		}
	}
	sort.Slice(out.Suggestions, func(i, j int) bool {
		a, b := out.Suggestions[i], out.Suggestions[j]
		switch {
		case a.Count != b.Count:
			return a.Count > b.Count
		case !a.LastSeen.Equal(b.LastSeen):
			return a.LastSeen.After(b.LastSeen)
		case a.Profile != b.Profile:
			return a.Profile < b.Profile
		default:
			return a.Tool < b.Tool
		}
	})
	if req.IncludePatch {
		out.Patches = buildPatches(cfg, out.Suggestions)
	}
	return out, nil
}

// finishSuggestion re-runs the access explainer for one group and fills the
// suggestion. It reports false when the group should drop out of the list.
func (s *ProfilesService) finishSuggestion(ctx context.Context, ev ProfileEvaluator, cfg *config.Config, g *suggestGroup, out *AccessSuggestions) (bool, error) {
	g.Fixes = []Fix{}
	g.Suggestion = SuggestNone

	switch g.BlockReason {
	case profile.BlockReasonCodeExecution, profile.BlockReasonManagement:
		// Built-in tools do not walk the upstream chain, so the explainer does
		// not cover them. The profile field that governs them is named, never
		// set; once it is set the group has been fixed and drops out like any
		// other.
		idx := indexOfProfile(cfg, g.Profile)
		if idx < 0 {
			out.ProfileRemoved++
			return false, nil
		}
		prof := cfg.Profiles[idx]
		field, effect := "code_execution", "lets scripts call every upstream tool the profile can reach"
		enabled := prof.EffectiveCodeExecution()
		if g.BlockReason == profile.BlockReasonManagement {
			field, effect = "management_tools", "lets this caller add, remove and quarantine upstream servers"
			enabled = prof.ManagementTools != nil && *prof.ManagementTools
		}
		if enabled {
			out.AlreadyFixed++
			return false, nil
		}
		g.Warning = fmt.Sprintf("Setting %s on profile %s %s.", field, g.Profile, effect)
		g.Note = fmt.Sprintf("Built-in tool refused by the %s setting; change it only if that access is intended.", field)
		return true, nil
	}

	if indexOfProfile(cfg, g.Profile) < 0 {
		out.ProfileRemoved++
		return false, nil
	}
	exp, err := ev.Explain(ctx, profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: g.Profile}, g.Tool)
	if err != nil {
		return false, fmt.Errorf("explain %s under %s: %w", g.Tool, g.Profile, err)
	}
	if exp.Verdict == profile.ExplainVerdictAllowed {
		out.AlreadyFixed++
		return false, nil
	}

	// A deny rule is a decision somebody wrote. It is reported and never
	// offered for widening, whether the recorded refusal was the rule or an
	// older tier-cap refusal of a tool a rule has denied since: the explainer
	// offers its allow fix for both, and against a rule it would not help.
	if g.BlockReason == profile.BlockReasonRule || deniedByRuleNow(exp) {
		g.Note = "Denied by an explicit rule in the profile. Edit tools.deny if this access is intended."
		return true, nil
	}

	g.Fixes = exp.Fixes
	for _, f := range exp.Fixes {
		switch {
		case g.BlockReason == profile.BlockReasonTier && f.Action == profile.FixAllowInProfile:
			g.Suggestion = SuggestAllow
		case g.BlockReason == profile.BlockReasonServerScope && f.Action == profile.FixAddServerToProfile:
			g.Suggestion = SuggestAddServer
		case g.BlockReason == profile.BlockReasonUnannotated && f.Action == profile.FixClassifyInProfile:
			g.Suggestion = SuggestClassify
		default:
			continue
		}
		break
	}
	switch g.Suggestion {
	case SuggestClassify:
		g.NeedsInput = []string{"tier"}
		g.Note = "The tool declares no tier. Choose read, write or destructive; no tier is assumed."
	case SuggestNone:
		g.Note = fmt.Sprintf("No profile change fixes this now (first failing step: %s).", exp.FirstFailure)
	}
	if g.BlockReason == profile.BlockReasonServerScope && g.Suggestion == SuggestAddServer {
		g.Note = "A code_execution script called a server outside the profile. Adding it widens the profile's reach."
	}
	return true, nil
}

// deniedByRuleNow reports whether the fresh explanation stops at the tool-rule
// step for a deny rule. The same step also reports an unannotated tool the
// profile hides, which the explainer marks with a classify fix; that one is
// a tier question, not a rule.
func deniedByRuleNow(exp *AccessExplanation) bool {
	if exp.FirstFailure != profile.StepToolRule {
		return false
	}
	for _, f := range exp.Fixes {
		if f.Action == profile.FixClassifyInProfile {
			return false
		}
	}
	return true
}

// drainActivities lets a StreamActivities producer run to its end after the
// consumer stops early. The producer sends into a bounded channel from inside
// a BBolt read transaction, so a consumer that walks away with records left
// would leave that transaction open for the life of the process, and an open
// read transaction keeps the database from remapping on a later write.
func drainActivities(ch <-chan *storage.ActivityRecord) {
	go func() {
		for range ch { //nolint:revive // discard the remainder
		}
	}()
}

// buildPatches applies every apply-ready suggestion (allow, add_server) to a
// copy of the stored profile. Classify needs a tier and is left out.
func buildPatches(cfg *config.Config, sugg []AccessSuggestion) []ProfilePatch {
	byProfile := map[string]*ProfilePatch{}
	var order []string
	for _, sg := range sugg {
		if sg.Suggestion != SuggestAllow && sg.Suggestion != SuggestAddServer {
			continue
		}
		idx := indexOfProfile(cfg, sg.Profile)
		if idx < 0 {
			continue
		}
		p := byProfile[sg.Profile]
		if p == nil {
			p = &ProfilePatch{Profile: sg.Profile, Changes: []string{}, Proposed: cloneProfile(cfg.Profiles[idx])}
			byProfile[sg.Profile] = p
			order = append(order, sg.Profile)
		}
		switch sg.Suggestion {
		case SuggestAllow:
			if p.Proposed.Tools == nil {
				p.Proposed.Tools = &config.ProfileToolRules{}
			}
			if !hasString(p.Proposed.Tools.Allow, sg.Tool) {
				p.Proposed.Tools.Allow = append(p.Proposed.Tools.Allow, sg.Tool)
				p.Changes = append(p.Changes, "tools.allow += "+sg.Tool)
			}
		case SuggestAddServer:
			if !hasString(p.Proposed.Servers, sg.Server) {
				p.Proposed.Servers = append(p.Proposed.Servers, sg.Server)
				p.Changes = append(p.Changes, "servers += "+sg.Server)
			}
		}
	}
	sort.Strings(order)
	patches := make([]ProfilePatch, 0, len(order))
	for _, name := range order {
		if p := byProfile[name]; len(p.Changes) > 0 {
			patches = append(patches, *p)
		}
	}
	return patches
}

// cloneProfile copies the slices and maps the patch builder edits.
func cloneProfile(p config.ProfileConfig) config.ProfileConfig {
	c := p
	c.Servers = append([]string{}, p.Servers...)
	if p.Tools != nil {
		t := *p.Tools
		t.Allow = append([]string(nil), p.Tools.Allow...)
		t.Deny = append([]string(nil), p.Tools.Deny...)
		if p.Tools.Classify != nil {
			t.Classify = make(map[string]string, len(p.Tools.Classify))
			for k, v := range p.Tools.Classify {
				t.Classify[k] = v
			}
		}
		c.Tools = &t
	}
	return c
}

func hasString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
