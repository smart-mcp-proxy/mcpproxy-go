package runtime

// hasLoginRecordsFor reports whether the client has browser-login storage,
// through whichever storage interface this service has: the clients service's
// store when there is one, else the token store itself. A store without
// browser-login storage answers false. A read error is returned (fail closed).
func (s *CredentialsService) hasLoginRecordsFor(clientID string) (bool, error) {
	var o oauthClientStore
	if s.clients != nil {
		o = s.clients.oauthStore()
	} else if st, ok := s.tokens.(oauthClientStore); ok {
		o = st
	}
	if o == nil {
		return false, nil
	}
	b, err := o.GetOAuthClientBinding(clientID)
	if err != nil {
		return false, err
	}
	return b != nil, nil
}
