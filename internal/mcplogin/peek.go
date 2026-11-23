package mcplogin

// PeekCode returns a copy of the request an unconsumed, unexpired code belongs
// to, while that request is still approved. It consumes nothing and changes
// nothing: the token endpoint uses it to compare the request's origin with the
// stored issuer BEFORE the code's single use is spent (a mismatch must leave
// the code usable). ok is false for an unknown, expired, consumed or cancelled
// code, with nothing to tell the cases apart.
func (s *Store) PeekCode(code string) (Request, bool) {
	h := HashSecret(s.key, KindCode, code)
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.codes[h]
	if e == nil || e.consumed || !s.now().Before(e.expiresAt) {
		return Request{}, false
	}
	r := s.live(e.requestID)
	if r == nil || r.Status != StatusApproved {
		return Request{}, false
	}
	return *r, true
}
