package runtime

import (
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// A file reload commits the configuration without bindingWriteMu, so the
// profile and the guard verdict the exchange checked can change between those
// checks and storage.CommitOAuthLogin. The exchange therefore re-reads the live
// configuration once more AFTER the commit and before any token is returned
// (Spec 132 R15), and undoes what it stored when the stored state is no longer
// acceptable. The reload locking itself is untouched.

// postCommitRefusal returns "" when the stored login is still acceptable, else
// the deny reason: profile_gone, or guard (also for anything it cannot read:
// fail closed).
func (s *OAuthLoginService) postCommitRefusal(d mcplogin.Decision, g storage.OAuthGrant) string {
	pin, mode, found, err := s.store.CurrentClientBinding(g.Client)
	if err != nil || !found {
		return "guard"
	}
	if pin != "" && !profileConfigured(s.cfg(), pin) {
		return "profile_gone"
	}
	all, err := s.store.ListAgentTokens()
	if err != nil {
		return "guard"
	}
	oauth, err := LoadOAuthBindings(s.store, s.now())
	if err != nil {
		return "guard"
	}
	grants, err := s.store.ListOAuthGrantsForClient(g.Client)
	if err != nil {
		return "guard"
	}
	otherLive := false
	for i := range grants {
		if grants[i].GrantID != g.GrantID && GrantStateOf(&grants[i], s.now()) == GrantStateActive {
			otherLive = true
		}
	}
	cfg := s.cfg()
	tokens := guardedOnly(all)
	// Before this login the client's entry was live only through its other logins.
	current := GuardState{Config: cfg, Tokens: tokens,
		OAuthBindings: withOAuthClient(oauth, OAuthBinding{ClientID: g.Client, Pin: pin, Mode: mode, Live: otherLive})}
	candidate := GuardState{Config: cfg, Tokens: tokens, OAuthBindings: oauth}
	if CheckBindingGuard(s.guard(), current, candidate) != nil {
		return "guard"
	}
	return ""
}

// undoCommit revokes the grant this exchange created and, for a brand-new
// client, removes what it created (grants, binding entry, registrations left
// without a grant). An existing client only loses the grant.
func (s *OAuthLoginService) undoCommit(d mcplogin.Decision, g storage.OAuthGrant) {
	if _, err := s.store.RevokeOAuthGrant(g.GrantID, storage.OAuthRevokeAdmin, s.now().UTC()); err != nil {
		s.logger.Error("browser login: cannot revoke a login that failed its post-commit check", zap.Error(err))
	}
	if d.NewClient {
		if _, err := s.store.ForgetClientLogins(g.Client); err != nil {
			s.logger.Error("browser login: cannot remove a new client that failed its post-commit check", zap.Error(err))
		}
	}
}
