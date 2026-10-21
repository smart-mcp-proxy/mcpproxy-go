package scanner

import (
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/bundlesig"
)

// Publisher trust anchors (spec 101 FR-004, FR-005, FR-005a; tasks.md A1).
//
// These are package variables, not constants, only so a test can swap them
// through withTrustAnchors; production code never assigns them.

// publisherSigners is the embedded publisher signer set: standard padded
// base64 of each raw 32-byte Ed25519 public key. It is EMPTY today because no
// publisher key has been issued yet, so every publisher-signed candidate is
// refused key_not_trusted and the operator authority is the only signed path.
//
// A release that adds a publisher key MUST also stamp the embedded
// bundled/scanner-bundle.json with a `sequence` (that sequence is the publisher
// baseline, FR-009b) and, when it revokes a compromised key, bump
// publisherTrustEpoch. Key removal and epoch bumps are binary-release events
// only (FR-005, FR-005a).
var publisherSigners = []string{}

// publisherTrustEpoch is the publisher authority's current trust epoch
// (FR-005a). It advances only through a binary release.
var publisherTrustEpoch uint64

// publisherFallbackBaseline seeds the publisher watermark only when the
// embedded manifest carries no `sequence` (FR-009b).
var publisherFallbackBaseline uint64

// buildTrustSet builds the verification key set: every publisher signer
// (authority publisher) plus every valid operator signer (authority operator).
//
// An invalid or duplicate signer is DROPPED with a warning rather than
// failing the build: a bad operator key must never brick boot (config.Load
// drops them too) and must never widen what is trusted. A key listed in both
// sets keeps its publisher entry and loses its operator copy, so one key can
// never satisfy whichever root happens to be active. Warnings never echo the
// offending value.
func buildTrustSet(publisher, operator []string) (*bundlesig.TrustSet, []string) {
	ts, _, warnings := buildTrustKeys(publisher, operator)
	return ts, warnings
}

// buildTrustKeys is buildTrustSet that also returns the accepted keys, which
// the policy fingerprint needs (a TrustSet is deliberately not enumerable).
func buildTrustKeys(publisher, operator []string) (*bundlesig.TrustSet, []bundlesig.Key, []string) {
	var (
		warnings []string
		keys     []bundlesig.Key
		seen     = make(map[string]struct{}, len(publisher)+len(operator))
	)
	add := func(set string, signers []string, authority string) {
		for i, s := range signers {
			pub, err := bundlesig.ParsePublicKey(s)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s signer %d dropped: not a valid Ed25519 public key (%v)", set, i, err))
				continue
			}
			id := bundlesig.KeyID(pub)
			if _, dup := seen[id]; dup {
				warnings = append(warnings, fmt.Sprintf("%s signer %d dropped: key %s is already trusted", set, i, id))
				continue
			}
			seen[id] = struct{}{}
			keys = append(keys, bundlesig.Key{ID: id, Public: pub, Authority: authority})
		}
	}
	// Publisher first, so a key listed in both sets keeps the publisher copy.
	add("publisher", publisher, AuthorityPublisher)
	add("operator", operator, AuthorityOperator)

	ts, err := bundlesig.NewTrustSet(keys...)
	if err != nil {
		// Unreachable: every key above is length-checked, fingerprint-derived
		// and de-duplicated. Fail closed to an empty set rather than guess.
		empty, _ := bundlesig.NewTrustSet()
		return empty, nil, append(warnings, "trust set could not be built; no key is trusted: "+err.Error())
	}
	return ts, keys, warnings
}
