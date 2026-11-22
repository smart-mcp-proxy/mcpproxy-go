package runtime

import (
	"errors"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// restoreRefusedInner reports whether undoing a connect (restoring the binding
// the revoked record replaced) would create a binding the guard now refuses
// (s.mu held). It only matters for a client that has LIVE browser logins: the
// restore also rewrites their binding, and the configuration may have changed
// since the connect. The candidate is the API key revoked with the prior
// binding plus the surviving live logins bound to it. A client without live
// logins, or without a prior binding to restore, answers false: the rollback
// behaves exactly as before. A read error is returned (fail closed).
func (s *ClientsService) restoreRefusedInner(clientID string) (bool, error) {
	o := s.oauthStore()
	has, err := s.hasLoginRecords(o, clientID)
	if err != nil || !has {
		return false, err
	}
	grants, err := o.ListOAuthGrants()
	if err != nil {
		return false, fmt.Errorf("cannot read browser logins: %w", err)
	}
	live := false
	for i := range grants {
		if grants[i].Client == clientID && GrantStateOf(&grants[i], s.now()) == GrantStateActive {
			live = true
		}
	}
	if !live {
		return false, nil
	}
	all, err := s.records()
	if err != nil {
		return false, err
	}
	rec := clientRecord(all, clientID)
	if rec == nil || rec.Kind != auth.KindClient || rec.PriorProfileMode == "" {
		return false, nil
	}
	oauth, err := s.loadOAuthBindings()
	if err != nil {
		return false, err
	}
	next := *rec
	next.Revoked = true
	next.ProfilePin, next.ProfileMode = rec.PriorProfilePin, rec.PriorProfileMode
	cfg := s.cfg()
	gerr := CheckBindingGuard(s.guard(),
		GuardState{Config: cfg, Tokens: guardedOnly(all), OAuthBindings: oauth},
		GuardState{Config: cfg, Tokens: candidateTokens(all, clientID, &next),
			OAuthBindings: repinOAuthClient(oauth, clientID, next.ProfilePin, next.ProfileMode)})
	var ge *BindingGuardError
	if errors.As(gerr, &ge) {
		return true, nil
	}
	return false, gerr
}

// revokeKeyKeepingBinding revokes the API key alone and leaves the binding of
// the client's logins as it is (storage.RevokeClientAPIKey, the one writer).
func (s *ClientsService) revokeKeyKeepingBinding(clientID string) (*auth.AgentToken, error) {
	return s.oauthStore().RevokeClientAPIKey(clientID)
}
