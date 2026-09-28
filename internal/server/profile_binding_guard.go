package server

import (
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"go.uber.org/zap"
)

type bindingGuardTool struct {
	server string
	tool   string
	tier   profile.Tier
}

// bindingGuardActive is the fail-closed runtime half of FR-008a. It reads
// current client bindings on every anonymous request because token bindings
// can change without publishing a config snapshot. Policy comparisons use the
// same profile index and the currently published tool snapshot that execution
// uses; a newly discovered tool can therefore activate the guard immediately.
func (p *MCPProxyServer) bindingGuardActive(idx *profileIndex) bool {
	cfg := p.currentConfig()
	if idx != nil && idx.cfg != nil {
		cfg = idx.cfg
	}
	if config.EffectiveRequireMCPAuth(cfg) {
		return false
	}
	if p.storage == nil {
		// A production proxy always has storage. A bare proxy used by unit
		// tests has no credential records to inspect, so it has no bindings.
		return false
	}
	tokens, err := p.storage.ListAgentTokens()
	if err != nil {
		if p.logger != nil {
			p.logger.Error("cannot inspect client bindings for anonymous profile guard", zap.Error(err))
		}
		return true
	}
	if cfg == nil || idx == nil || idx.cfg == nil {
		// During a publication gap the guard cannot compare reachability. Any
		// live named client binding therefore denies anonymous access until a
		// coherent index/snapshot pair is available again.
		for i := range tokens {
			if activeNamedClientBinding(&tokens[i], time.Now()) {
				return true
			}
		}
		return false
	}
	now := time.Now()
	var tools []bindingGuardTool
	toolsLoaded := false
	for i := range tokens {
		token := &tokens[i]
		if !activeNamedClientBinding(token, now) {
			continue
		}
		if !toolsLoaded {
			tools = p.bindingGuardTools()
			toolsLoaded = true
		}
		if bindingBypassable(idx, cfg, token, tools) {
			return true
		}
	}
	return false
}

func activeNamedClientBinding(token *auth.AgentToken, now time.Time) bool {
	return token != nil && token.Kind == auth.KindClient && !token.Revoked &&
		token.ExpiresAt.After(now) && token.ProfilePin != "" &&
		(token.ProfileMode == auth.ProfileModeLocked || token.ProfileMode == auth.ProfileModeSwitchable)
}

func bindingBypassable(idx *profileIndex, cfg *config.Config, token *auth.AgentToken, tools []bindingGuardTool) bool {
	if token == nil || token.ProfilePin == "" || idx == nil || idx.cfg == nil || cfg == nil {
		return false
	}
	boundReach, ok := bindingReachableProfiles(idx, token.ProfilePin, token.ProfileMode == auth.ProfileModeSwitchable)
	if !ok {
		// A dangling bound base is already deny-all and cannot be bypassed.
		return false
	}
	if cfg.AnonymousProfile == "" {
		return true // anonymous currently means unrestricted access
	}
	anonymousReach, ok := bindingReachableProfiles(idx, cfg.AnonymousProfile, true)
	if !ok {
		// A dangling anonymous base is itself deny-all.
		return false
	}
	boundPolicies := make([]*profile.CompiledPolicy, 0, len(boundReach))
	boundServers := make(map[string]struct{})
	for _, name := range boundReach {
		pos := idx.position(name)
		if pos < 0 {
			continue // dangling switchable target is deny-all
		}
		policy := idx.PolicyAt(pos)
		if policy == nil {
			continue
		}
		boundPolicies = append(boundPolicies, policy)
		for _, server := range idx.effectiveServersForCandidate(pos, []string{"*"}) {
			boundServers[server] = struct{}{}
		}
	}
	if len(boundPolicies) == 0 {
		return false
	}

	maxBoundCap, maxBoundUnannotated := 0, 0
	allBoundCodeExecutionOff, allBoundManagementOff := true, true
	for _, policy := range boundPolicies {
		if tierCapRank(policy.Cap) > maxBoundCap {
			maxBoundCap = tierCapRank(policy.Cap)
		}
		if unannotatedRank(policy.Unannotated) > maxBoundUnannotated {
			maxBoundUnannotated = unannotatedRank(policy.Unannotated)
		}
		allBoundCodeExecutionOff = allBoundCodeExecutionOff && cfg.EnableCodeExecution && !policy.CodeExecution
		allBoundManagementOff = allBoundManagementOff && !profileManagementEnabled(policy)
	}

	for _, name := range anonymousReach {
		pos := idx.position(name)
		if pos < 0 {
			continue // dangling anonymous-reachable profiles are deny-all
		}
		policy := idx.PolicyAt(pos)
		if policy == nil {
			continue
		}
		for _, server := range idx.effectiveServersForCandidate(pos, []string{"*"}) {
			if _, covered := boundServers[server]; !covered {
				return true
			}
		}
		if tierCapRank(policy.Cap) > maxBoundCap || unannotatedRank(policy.Unannotated) > maxBoundUnannotated {
			return true
		}
		if cfg.EnableCodeExecution && policy.CodeExecution && allBoundCodeExecutionOff {
			return true
		}
		if profileManagementEnabled(policy) && allBoundManagementOff {
			return true
		}
		for _, tool := range tools {
			admitted, _, _ := policy.Decide(tool.server, tool.tool, tool.tier)
			if !admitted {
				continue
			}
			admittedByBinding := false
			for _, bound := range boundPolicies {
				if allowed, _, _ := bound.Decide(tool.server, tool.tool, tool.tier); allowed {
					admittedByBinding = true
					break
				}
			}
			if !admittedByBinding {
				return true
			}
		}
	}
	return false
}

func bindingReachableProfiles(idx *profileIndex, base string, switchable bool) ([]string, bool) {
	pos := idx.position(base)
	if pos < 0 {
		return nil, false
	}
	reachable := []string{base}
	if switchable {
		if policy := idx.PolicyAt(pos); policy != nil {
			for name := range policy.SwitchableTo {
				if idx.position(name) >= 0 {
					reachable = append(reachable, name)
				}
			}
		}
	}
	return reachable, true
}

func tierCapRank(tier profile.Tier) int {
	switch tier {
	case profile.TierRead:
		return 1
	case profile.TierWrite:
		return 2
	case profile.TierDestructive:
		return 3
	default:
		// TierUnannotated is the no-cap sentinel. It admits the same finite
		// tier set as TierDestructive, so treating it as wider would reject
		// otherwise identical profiles during the binding comparison.
		return 3
	}
}

func unannotatedRank(value string) int {
	switch value {
	case config.ProfileUnannotatedDeny:
		return 1
	case config.ProfileUnannotatedAsWrite:
		return 2
	default:
		return 3 // as_read is the most permissive policy
	}
}

func profileManagementEnabled(policy *profile.CompiledPolicy) bool {
	return policy != nil && policy.ManagementTools != nil && *policy.ManagementTools
}

func (p *MCPProxyServer) bindingGuardTools() []bindingGuardTool {
	if p.mainServer == nil || p.mainServer.runtime == nil {
		return nil
	}
	supervisor := p.mainServer.runtime.Supervisor()
	if supervisor == nil || supervisor.StateView() == nil {
		return nil
	}
	snapshot := supervisor.StateView().Snapshot()
	if snapshot == nil {
		return nil
	}
	var tools []bindingGuardTool
	for serverName, status := range snapshot.Servers {
		if status == nil || !status.ToolsDiscovered {
			continue
		}
		for _, info := range status.Tools {
			// StateView stores the upstream's raw tool name. A raw name may
			// itself start with "<server>:"; keep it intact so the guard
			// compares the same exact registration identity as dispatch.
			annotations, found := p.EffectiveAnnotations(serverName, info.Name)
			tools = append(tools, bindingGuardTool{
				server: serverName,
				tool:   info.Name,
				tier:   profile.IntrinsicTier(annotations, found),
			})
		}
	}
	return tools
}
