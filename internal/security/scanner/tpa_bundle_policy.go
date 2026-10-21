package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/bundlesig"
)

// bundlePolicy is the config-resident half of the bundle trust decision: the
// part an attacker who can write the bundle path or the data directory cannot
// reach (spec 101 FR-004b, FR-007, FR-009b). It is derived once per pipeline
// run from the live SecurityConfig and the binary's own trust anchors.
type bundlePolicy struct {
	// Path is the configured bundle path ("" => the embedded default serves).
	Path string
	// RequireSigned makes the sidecar mandatory for file drops (FR-007).
	RequireSigned bool
	// ActiveRoot is the declared active signing authority (FR-004b). It is kept
	// verbatim: an unrecognised value matches no key's authority, so every
	// signed candidate is refused authority_not_active.
	ActiveRoot string
	// Trust is the union of publisher and valid operator keys.
	Trust *bundlesig.TrustSet

	OperatorBaseline    uint64
	OperatorBaselineSet bool
	OperatorEpoch       uint64

	// FetchEnabled and FetchURL mirror security.tpa_fetch_enabled (with its
	// environment override) and security.tpa_fetch_url. They are part of the
	// policy fingerprint: a change to either supersedes an in-flight download
	// (tasks B8, B9).
	FetchEnabled bool
	FetchURL     string

	// ActiveRootKeys is how many trusted keys belong to ActiveRoot. Zero means
	// no fetched pair could ever verify, so the fetch leg issues no request
	// (B10 no_trusted_signer). Derived from the key set, so it adds nothing to
	// the fingerprint.
	ActiveRootKeys int

	// RefreshInterval and StaleAfterDays are display and scheduling settings,
	// not policy: they are deliberately NOT part of Fingerprint (B15), so a
	// threshold edit takes effect on the next status read without rerunning
	// the pipeline.
	RefreshInterval time.Duration
	StaleAfterDays  int

	// PublisherEpoch and PublisherBaseline are the binary's anchors, captured
	// when the policy is built so that one run sees one consistent view.
	PublisherEpoch    uint64
	PublisherBaseline uint64

	// Fingerprint identifies every field above (and the key set); two runs with
	// equal Fingerprint apply identical policy. Part of the idempotence key.
	Fingerprint string
	// Warnings are dropped-signer notes to log once per distinct policy.
	Warnings []string
}

// policyFromConfig builds the policy from sec, honouring the
// MCPPROXY_TPA_BUNDLE_PATH override through sec.EffectiveTPABundlePath.
func policyFromConfig(sec *config.SecurityConfig) *bundlePolicy {
	return policyFor(sec, sec.EffectiveTPABundlePath())
}

// policyFor builds the policy for an explicit path (ConfigureBundle passes its
// argument verbatim, with no env override). sec may be nil.
func policyFor(sec *config.SecurityConfig, path string) *bundlePolicy {
	trust, keys, warnings := buildTrustKeys(publisherSigners, sec.EffectiveTPAOperatorSigners())
	opBase, opBaseSet := sec.EffectiveTPAOperatorBaseline()

	pubBase := publisherFallbackBaseline
	if seq, ok := embeddedSequence(); ok {
		pubBase = seq
	}

	p := &bundlePolicy{
		Path:                path,
		RequireSigned:       sec.IsRequireSignedBundle(),
		ActiveRoot:          sec.EffectiveTPAActiveRoot(),
		Trust:               trust,
		OperatorBaseline:    opBase,
		OperatorBaselineSet: opBaseSet,
		OperatorEpoch:       sec.EffectiveTPAOperatorEpoch(),
		FetchEnabled:        sec.IsTPAFetchEnabled(),
		FetchURL:            sec.EffectiveTPAFetchURL(),
		RefreshInterval:     clampRefreshInterval(sec.EffectiveTPARefreshInterval()),
		StaleAfterDays:      sec.EffectiveTPAStaleAfterDays(),
		PublisherEpoch:      publisherTrustEpoch,
		PublisherBaseline:   pubBase,
		Warnings:            warnings,
	}
	for _, k := range keys {
		if k.Authority == p.ActiveRoot {
			p.ActiveRootKeys++
		}
	}
	p.Fingerprint = p.fingerprint(keys)
	return p
}

// minRefreshInterval is the floor of the refresh cadence (B1). A test seam on
// the refresher goes lower.
const minRefreshInterval = time.Hour

// clampRefreshInterval applies the runtime's 1h floor to the configured cadence.
func clampRefreshInterval(d time.Duration) time.Duration {
	if d < minRefreshInterval {
		return minRefreshInterval
	}
	return d
}

// fingerprint hashes a canonical, length-prefixed encoding of every policy
// field plus the sorted (authority, key id) pairs, so no two distinct policies
// share an encoding and key order is irrelevant.
func (p *bundlePolicy) fingerprint(keys []bundlesig.Key) string {
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.Authority+":"+k.ID)
	}
	sort.Strings(ids)

	h := sha256.New()
	field := func(name, value string) { fmt.Fprintf(h, "%s=%d:%s\n", name, len(value), value) }
	field("path", p.Path)
	field("require_signed", strconv.FormatBool(p.RequireSigned))
	field("active_root", p.ActiveRoot)
	field("operator_baseline_set", strconv.FormatBool(p.OperatorBaselineSet))
	field("operator_baseline", strconv.FormatUint(p.OperatorBaseline, 10))
	field("operator_epoch", strconv.FormatUint(p.OperatorEpoch, 10))
	field("publisher_epoch", strconv.FormatUint(p.PublisherEpoch, 10))
	field("publisher_baseline", strconv.FormatUint(p.PublisherBaseline, 10))
	field("fetch_enabled", strconv.FormatBool(p.FetchEnabled))
	field("fetch_url", p.FetchURL)
	field("keys", strconv.Itoa(len(ids)))
	for _, id := range ids {
		field("key", id)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// externalConfigured reports whether an external (non-embedded) bundle source
// is configured or already serving: the configured path, a recorded fetched
// Active bundle, or an enabled fetch with nothing external serving yet (tasks
// B9, Hazard A). It is a function rather than a policy method because
// bundlePolicy holds configuration only; the recorded Active record lives in
// the committed state st (nil-safe).
//
// The fetched-Active clause keeps a fetched bundle alive when the fetch is
// switched off: dropping silently to the weaker embedded corpus would be an
// unrequested degrade (security bundle rollback --embedded is the explicit way
// back). A FILE bundle gets no such grace, with fetch on or off: with its path
// removed the operator removed it, so an enabled fetch does not keep a recorded
// file Active alive (nor restore it from its last-known-good files). An enabled
// fetch with no external bundle recorded still counts as configured, so a
// fetched or pinned bundle can come back.
func externalConfigured(policy *bundlePolicy, st *TPABundleState) bool {
	if policy.Path != "" {
		return true
	}
	if st != nil && st.Active != nil {
		return st.Active.Source == BundleSourceFetch
	}
	return policy.FetchEnabled
}

// currentEpoch returns the trusted epoch of auth (FR-005a). An unknown
// authority has epoch 0 and no records.
func (p *bundlePolicy) currentEpoch(auth string) uint64 {
	switch auth {
	case AuthorityPublisher:
		return p.PublisherEpoch
	case AuthorityOperator:
		return p.OperatorEpoch
	}
	return 0
}

// baseline returns auth's seeded anti-downgrade floor and whether one exists
// (FR-009b). The publisher always has one (the embedded corpus's sequence, else
// the declared fallback); the operator only when configured.
func (p *bundlePolicy) baseline(auth string) (uint64, bool) {
	switch auth {
	case AuthorityPublisher:
		return p.PublisherBaseline, true
	case AuthorityOperator:
		return p.OperatorBaseline, p.OperatorBaselineSet
	}
	return 0, false
}

// embeddedSequence returns the `sequence` of the bundle compiled into this
// binary, and whether it carries a valid one.
func embeddedSequence() (uint64, bool) {
	m, err := parseBundleManifest(bundledScannerBundle)
	if err != nil {
		return 0, false
	}
	seq, _, err := parseSequence(m.Sequence)
	if err != nil {
		return 0, false
	}
	return seq, true
}
