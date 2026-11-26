package server

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// adminToolAccess is THE visibility and execution-time predicate of the
// administrator MCP tools (`profiles`, Spec 108-h FR-017, and `credentials`,
// Spec 115 FR-011): one definition, so the two tools can never disagree about
// who is an administrator.
//
// admin reports that the credential is an administrator kind at all (api_key or
// socket, never anonymous): only then is a refusal attributable, because for
// every other caller nothing names who was refused. visible additionally
// requires that the request's effective profile is none or sets
// management_tools: true - an unset field is hidden, FR-017 has no legacy - and
// that no dangling base or binding guard applies.
func (p *MCPProxyServer) adminToolAccess(ctx context.Context) (admin, visible bool) {
	admin, visible, _ = p.adminToolAccessProfile(ctx)
	return admin, visible
}

// adminToolAccessProfile is adminToolAccess that also returns the profile the
// decision resolved, so a caller can record exactly the profile that admitted
// the call (Spec 133: the admin_action line's profile) without resolving it a
// second time.
func (p *MCPProxyServer) adminToolAccessProfile(ctx context.Context) (admin, visible bool, profileName string) {
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil || !ac.IsAdmin() || ac.Anonymous {
		return false, false, ""
	}
	if ac.CredentialKind != auth.CredentialKindAPIKey && ac.CredentialKind != auth.CredentialKindSocket {
		return false, false, ""
	}
	idx, ok := profileRequestIndexFromContext(ctx)
	if !ok {
		idx = p.profileIndexCurrent(ctx)
	}
	if idx == nil {
		res := p.ResolveProfileV3(ctx, nil)
		return true, res.Name == "" && res.Scope == nil && res.Policy == nil, res.Name
	}
	res := p.ResolveProfileV3(ctx, idx)
	if res.BindingGuarded || (res.Base != "" && idx.position(res.Base) < 0) {
		return true, false, res.Name
	}
	if res.Name == "" && res.Scope == nil && res.Policy == nil {
		return true, true, res.Name
	}
	return true, res.Policy != nil && res.Policy.ManagementTools != nil && *res.Policy.ManagementTools, res.Name
}
