package server

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// MCPProxyServer is the runtime.ProfileEvaluator (Spec 108-f F1): everything
// the profiles service needs from the unexported profile index, the shared
// per-tool gate and the search index.
var _ runtime.ProfileEvaluator = (*MCPProxyServer)(nil)

// hiddenListCap bounds TryResult.Hidden.
const hiddenListCap = 100

// profileToolCountsCache memoizes the administrator ToolCounts of each profile
// for one published (profileIndex, ToolTierGeneration) pair (F35): a list of N
// profiles over T tools would otherwise evaluate N*T chains per request.
type profileToolCountsCache struct {
	mu     sync.Mutex
	idx    *profileIndex
	gen    uint64
	counts map[string]runtime.ToolCounts
}

func (c *profileToolCountsCache) get(idx *profileIndex, gen uint64, name string) (runtime.ToolCounts, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.idx != idx || c.gen != gen {
		return runtime.ToolCounts{}, false
	}
	v, ok := c.counts[name]
	return v, ok
}

func (c *profileToolCountsCache) put(idx *profileIndex, gen uint64, name string, v runtime.ToolCounts) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.idx != idx || c.gen != gen {
		c.idx, c.gen, c.counts = idx, gen, map[string]runtime.ToolCounts{}
	}
	c.counts[name] = v
}

func (p *MCPProxyServer) toolTierGeneration() uint64 {
	if p.mainServer == nil || p.mainServer.runtime == nil {
		return 0
	}
	if sup := p.mainServer.runtime.Supervisor(); sup != nil {
		return sup.ToolTierGeneration()
	}
	return 0
}

// ToolCounts implements runtime.ProfileEvaluator: the visible tools of the
// named profile by the tier the profile gives them, plus the unannotated tools
// it hides. An administrator's counts are memoized per published snapshot; a
// restricted viewer's are computed per request over the servers it may see.
func (p *MCPProxyServer) ToolCounts(ctx context.Context, name string, viewer runtime.ViewerScope) runtime.ToolCounts {
	admin := !viewer.Restricted
	var idx *profileIndex
	var gen uint64
	if admin {
		idx = p.profileIndexCurrent(ctx)
		gen = p.toolTierGeneration()
		if c, ok := p.toolCounts.get(idx, gen, name); ok {
			return c
		}
	}
	ev, err := p.NewAccessEvaluator(profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: name})
	if err != nil {
		return runtime.ToolCounts{}
	}
	var counts runtime.ToolCounts
	for _, t := range p.bindingGuardTools() {
		if viewer.Visible != nil && !viewer.Visible(t.server) {
			continue
		}
		v := ev.Evaluate(t.server, t.tool)
		switch {
		case v.Visible:
			switch v.ProfileTier {
			case profile.TierRead:
				counts.Read++
			case profile.TierWrite:
				counts.Write++
			case profile.TierDestructive:
				counts.Destructive++
			}
		case v.Reason == profile.AccessReasonUnannotatedHidden:
			counts.UnannotatedHidden++
		}
	}
	if admin {
		p.toolCounts.put(idx, gen, name, counts)
	}
	return counts
}

// EffectiveTools implements runtime.ProfileEvaluator: one row per catalog tool
// with the verdict of the ONE access chain (the same rendering GET
// /tools?profile= uses), so `callable` equals what a real call would do.
func (p *MCPProxyServer) EffectiveTools(_ context.Context, name string, opt runtime.EffectiveToolsOptions) (*runtime.EffectiveToolsResult, error) {
	admin := !opt.Viewer.Restricted
	subject := profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: name}
	if opt.Client != "" {
		subject = profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: opt.Client, Profile: name}
		if rec, err := p.clientCredentialRecord(opt.Client); err == nil {
			subject.CredentialState = credentialStateOf(rec, time.Now())
		}
	}
	ev, err := p.NewAccessEvaluator(subject)
	if err != nil {
		return nil, err
	}
	var policyProfile *config.ProfileConfig
	if ev.idx != nil {
		policyProfile = ev.idx.lookup(name)
	}

	tools := p.bindingGuardTools()
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].server != tools[j].server {
			return tools[i].server < tools[j].server
		}
		return tools[i].tool < tools[j].tool
	})

	res := &runtime.EffectiveToolsResult{Profile: name, Tools: []runtime.EffectiveTool{}}
	byReason := map[string]int{}
	callable := 0
	known := map[string]profile.Tier{}
	for _, t := range tools {
		known[t.server+":"+t.tool] = t.tier
		if opt.Viewer.Visible != nil && !opt.Viewer.Visible(t.server) {
			continue
		}
		v := ev.Evaluate(t.server, t.tool)
		if v.Visible {
			res.Counts.Visible++
		} else {
			res.Counts.Hidden++
		}
		if v.Callable {
			callable++
		}
		if v.Reason != profile.AccessReasonNone {
			byReason[string(v.Reason)]++
		}
		if !admin && !v.Visible {
			continue // excluded rows, their tiers and reasons are administrator-only
		}
		if opt.Server != "" && t.server != opt.Server {
			continue
		}
		if admin && opt.Reason != "" && string(v.Reason) != opt.Reason {
			continue
		}
		stale := false
		if policyProfile != nil && policyProfile.Tools != nil && t.tier != profile.TierUnannotated {
			_, stale = policyProfile.Tools.Classify[t.server+":"+t.tool]
		}
		res.Tools = append(res.Tools, runtime.EffectiveTool{
			Server: t.server, Tool: t.tool,
			IntrinsicTier: t.tier.String(), ProfileTier: v.ProfileTier.String(),
			Access:              runtime.ToolAccessView{Visible: v.Visible, Callable: v.Callable, Reason: string(v.Reason)},
			ClassificationStale: stale,
		})
	}
	if admin {
		res.Counts.Callable = &callable
		res.Counts.ByReason = byReason
		if policyProfile != nil && policyProfile.Tools != nil {
			for key := range policyProfile.Tools.Classify {
				tier, exists := known[key]
				reason := ""
				switch {
				case !exists:
					reason = profile.StaleClassificationMissing
				case tier != profile.TierUnannotated:
					reason = profile.StaleClassificationAnnotated
				}
				if reason == "" {
					continue
				}
				res.StaleClassifications = append(res.StaleClassifications, key)
				if res.StaleClassificationReasons == nil {
					res.StaleClassificationReasons = map[string]string{}
				}
				res.StaleClassificationReasons[key] = reason
			}
			sort.Strings(res.StaleClassifications)
		}
	}
	return res, nil
}

// TryProfile implements runtime.ProfileEvaluator: what retrieve_tools would
// return under a DRAFT profile, filtered before the limit exactly as the real
// call filters, plus what the draft hides. Nothing is persisted.
func (p *MCPProxyServer) TryProfile(ctx context.Context, draft config.ProfileConfig, query string, limit int) (*runtime.TryResult, error) {
	if p.index == nil || p.mainServer == nil {
		return nil, errors.New("tool search is unavailable")
	}
	base := p.currentConfig()
	if base == nil {
		return nil, errors.New("configuration unavailable")
	}
	name := draft.Name
	if name == "" {
		name = "try-draft"
		draft.Name = name
	}
	candidate := *base
	candidate.Profiles = append([]config.ProfileConfig{}, base.Profiles...)
	replaced := false
	for i := range candidate.Profiles {
		if candidate.Profiles[i].Name == name {
			candidate.Profiles[i], replaced = draft, true
		}
	}
	if !replaced {
		candidate.Profiles = append(candidate.Profiles, draft)
	}
	idx := newProfileIndex(&candidate)
	scope := profileScopeFromIndex(idx, name)
	policy := idx.PolicyFor(name)
	if scope == nil || policy == nil {
		return nil, profile.ErrUnknownProfile
	}

	withheld := p.mainServer.quarantinedServerFilter()
	var hidden []runtime.TryHidden
	truncated := false
	admit := func(hit index.Hit) index.Admission {
		if withheld(hit.Server) || !scope.Allows(hit.Server) {
			return index.RejectScope
		}
		annotations, found := p.EffectiveAnnotations(hit.Server, hit.Tool)
		intrinsic := profile.IntrinsicTier(annotations, found)
		ok, reason, _ := policy.Decide(hit.Server, hit.Tool, intrinsic)
		if ok {
			return index.Admit
		}
		if reason == profile.ReasonServerNotInProfile {
			return index.RejectScope
		}
		if len(hidden) < hiddenListCap {
			hidden = append(hidden, runtime.TryHidden{Server: hit.Server, Tool: hit.Tool, Reason: string(profile.AccessReasonFromDecision(reason))})
		} else {
			truncated = true
		}
		return index.RejectPolicy
	}
	results, hiddenCount, err := p.index.SearchToolsAdmitted(query, limit, admit)
	if err != nil {
		return nil, err
	}
	out := &runtime.TryResult{
		Results:         p.mainServer.searchResultsToMaps(results),
		HiddenByProfile: hiddenCount,
		Hidden:          hidden,
		HiddenTruncated: truncated,
	}
	if out.Results == nil {
		out.Results = []map[string]interface{}{}
	}
	if out.Hidden == nil {
		out.Hidden = []runtime.TryHidden{}
	}
	return out, nil
}
