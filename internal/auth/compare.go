package auth

import "crypto/subtle"

// ConstantTimeEqual reports whether candidate equals secret, in time that does
// not depend on how many leading bytes they share.
//
// Go's `==` on strings returns at the first differing byte, so using it to
// check a presented credential against a configured one leaks a prefix of the
// secret to a caller who can measure response time. Every comparison that
// decides access should go through this helper instead.
//
// An empty candidate or secret is never a match. This matters: subtle's
// ConstantTimeCompare returns 1 for two empty slices, so a bare swap of
// `token == cfg.APIKey` for subtle.ConstantTimeCompare would make an absent
// credential authenticate as admin wherever the caller's `!= ""` guard was
// dropped. Folding the emptiness check in here means call sites cannot make
// that mistake.
//
// The length of the two values is still compared in variable time (a length
// mismatch returns immediately). That is the standard subtle.ConstantTimeCompare
// contract: credential length is not the secret, its contents are.
func ConstantTimeEqual(candidate, secret string) bool {
	if candidate == "" || secret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(secret)) == 1
}
