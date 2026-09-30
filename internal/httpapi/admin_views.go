package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// The admin read views the REST handlers and the MCP `profiles` tool (Spec
// 108-h) share. Each returns the REST `data` object for an administrator
// caller, so one implementation answers both surfaces and a field added to a
// REST response reaches MCP without a second edit (FR-037). Errors map through
// ProfilesErrorBody.

// ClientsSupported reports whether this edition has per-client credentials: the
// personal edition does, the server edition answers "client not found".
func (s *Server) ClientsSupported() bool { return clientRoutesSupported }

// ProfileListData is GET /profiles for an administrator.
func (s *Server) ProfileListData(ctx context.Context) (*internalRuntime.ProfileList, error) {
	list, err := s.profiles().List(ctx, internalRuntime.ViewerScope{})
	if err != nil {
		return nil, err
	}
	counts := s.serverToolCounts()
	for i := range list.Profiles {
		fillV2ToolCount(&list.Profiles[i], counts)
	}
	return list, nil
}

// ProfileViewData is GET /profiles/{name} for an administrator.
func (s *Server) ProfileViewData(ctx context.Context, name string) (*internalRuntime.ProfileView, error) {
	v, err := s.profiles().Get(ctx, name, internalRuntime.ViewerScope{})
	if err != nil {
		return nil, err
	}
	fillV2ToolCount(v, s.serverToolCounts())
	return v, nil
}

// EffectiveToolsData is GET /profiles/{name}/effective-tools for an
// administrator. client evaluates that client's credential under the profile;
// it is refused as unknown where the edition has no client credentials.
func (s *Server) EffectiveToolsData(ctx context.Context, name, client, server, reason string) (*internalRuntime.EffectiveToolsResult, error) {
	if client != "" && !clientRoutesSupported {
		return nil, profile.ErrUnknownClient
	}
	return s.profiles().EffectiveTools(ctx, name, internalRuntime.EffectiveToolsOptions{
		Client: client, Server: server, Reason: reason,
	})
}

// ExplainRequest is the subject and tool of GET /access/explain: exactly one of
// Client, Token, Profile and Anonymous.
type ExplainRequest struct {
	Client    string
	Token     string
	Profile   string
	Anonymous bool
	Tool      string
}

// ExplainAccess is GET /access/explain for an administrator.
func (s *Server) ExplainAccess(ctx context.Context, q ExplainRequest) (*internalRuntime.AccessExplanation, error) {
	n := 0
	for _, set := range []bool{q.Client != "", q.Token != "", q.Profile != "", q.Anonymous} {
		if set {
			n++
		}
	}
	if n != 1 {
		return nil, &requestError{http.StatusBadRequest, "exactly one of client, token, profile, anonymous is required"}
	}
	if !looksLikeUpstreamTool(q.Tool) {
		return nil, profile.ErrExplainBuiltinTool
	}

	var subject profile.AccessSubject
	switch {
	case q.Client != "":
		if !clientRoutesSupported {
			return nil, profile.ErrUnknownClient
		}
		subject = profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: q.Client}
		if state, ok := s.clientCredentialState(q.Client); ok {
			subject.CredentialState = state
		}
	case q.Token != "":
		if strings.HasPrefix(q.Token, auth.ClientTokenName("")) {
			return nil, profile.ErrClientCredentialToken
		}
		subject = profile.AccessSubject{Kind: profile.AccessSubjectToken, TokenName: q.Token}
	case q.Profile != "":
		subject = profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: q.Profile}
	default:
		subject = profile.AccessSubject{Kind: profile.AccessSubjectAnonymous}
	}
	return s.profiles().Explain(ctx, subject, q.Tool)
}
