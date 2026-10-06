package server

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// SearchToolsForProfile performs REST tool search with the same profile policy
// and pre-limit admission used by retrieve_tools. handled is false when no
// v3 profile resolution applies, preserving the existing search path.
func (s *Server) SearchToolsForProfile(ctx context.Context, query string, limit int, inScope func(string) bool) (results []map[string]interface{}, handled bool, err error) {
	proxy := s.mcpProxy
	if proxy == nil || proxy.index == nil {
		return nil, false, nil
	}
	profileIndex := proxy.profileIndexCurrent(ctx)
	resolution := proxy.ResolveProfileV3(ctx, profileIndex)
	if resolution.Scope == nil && resolution.Policy == nil {
		return nil, false, nil
	}

	if inScope == nil {
		inScope = func(string) bool { return true }
	}
	withheld := s.quarantinedServerFilter()
	admit := func(hit index.Hit) index.Admission {
		if withheld(hit.Server) || (resolution.Scope != nil && !resolution.Scope.Allows(hit.Server)) || !inScope(hit.Server) {
			return index.RejectScope
		}
		if resolution.Policy == nil {
			return index.RejectScope
		}
		annotations, found := proxy.EffectiveAnnotations(hit.Server, hit.Tool)
		intrinsic := profile.IntrinsicTier(annotations, found)
		ok, reason, _ := resolution.Policy.Decide(hit.Server, hit.Tool, intrinsic)
		if !ok {
			if reason == profile.ReasonServerNotInProfile {
				return index.RejectScope
			}
			return index.RejectPolicy
		}
		return index.Admit
	}
	searchResults, _, err := proxy.index.SearchToolsAdmitted(query, limit, admit)
	if err != nil {
		return nil, true, err
	}
	return s.searchResultsToMaps(searchResults), true, nil
}

// ToolAllowedByProfile is the row-level REST discovery check. It fails closed
// for dangling or policy-excluded profile resolutions and is a no-op when the
// request has no effective v3 profile.
func (s *Server) ToolAllowedByProfile(ctx context.Context, serverName, toolName string) bool {
	proxy := s.mcpProxy
	if proxy == nil {
		return true
	}
	resolution := proxy.ResolveProfileV3(ctx, proxy.profileIndexCurrent(ctx))
	if resolution.Scope == nil && resolution.Policy == nil {
		return true
	}
	if resolution.Scope != nil && !resolution.Scope.Allows(serverName) {
		return false
	}
	if resolution.Policy == nil {
		return false
	}
	annotations, found := proxy.EffectiveAnnotations(serverName, toolName)
	allowed, _, _ := resolution.Policy.Decide(serverName, toolName, profile.IntrinsicTier(annotations, found))
	return allowed
}
