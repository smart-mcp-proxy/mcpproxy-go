package scanner

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// signatureIDPattern is the campaign id grammar (contract §2).
var signatureIDPattern = regexp.MustCompile(`^TPA-[0-9]{4}-[0-9]{4}$`)

// rawProvenance is the public disclosure behind a signature.
type rawProvenance struct {
	Source    string `json:"source"`
	Published string `json:"published"`
	Note      string `json:"note"`
}

// rawSignature is one signatures[] entry (spec 101 FR-001a). It names the
// rules[] entries that implement a campaign; rule_ids entries are
// "<TPA-id>/<detector>" so the CheckID tpa.<id>.<detector> stays unchanged.
type rawSignature struct {
	ID         string        `json:"id"`
	Category   string        `json:"category"`
	Gating     bool          `json:"gating"`
	Provenance rawProvenance `json:"provenance"`
	License    string        `json:"license"`
	RuleIDs    []string      `json:"rule_ids"`
}

// isRunnableRule reports whether the offline tier executes the rule. It is the
// single definition shared with the loader's compile loop semantics.
func isRunnableRule(r *rawRule) bool {
	return r.Engine == bundleEngineRegex && r.Target == bundleTargetToolDescription
}

// validateSignatures enforces the bundle's referential integrity (spec 101
// D-US2-1..D-US2-3). It is a no-op when signatures[] is absent, so v0.1 bundles
// keep loading unchanged. Any violation rejects the WHOLE candidate. The licence
// allowlist and class vocabulary are policy, enforced at corpus build time, not
// here, so operators can still load private corpora.
func validateSignatures(b *rawBundle) error {
	if b.Signatures == nil {
		return nil
	}

	sigByID := make(map[string]*rawSignature, len(b.Signatures))
	for i := range b.Signatures {
		s := &b.Signatures[i]
		if !signatureIDPattern.MatchString(s.ID) {
			return fmt.Errorf("scanner bundle: signatures[%d] id %q does not match TPA-YYYY-NNNN", i, s.ID)
		}
		if _, dup := sigByID[s.ID]; dup {
			return fmt.Errorf("scanner bundle: duplicate signature id %s", s.ID)
		}
		sigByID[s.ID] = s
		if strings.TrimSpace(s.Provenance.Source) == "" {
			return fmt.Errorf("scanner bundle: signature %s has an empty provenance.source", s.ID)
		}
		if _, err := time.Parse("2006-01-02", s.Provenance.Published); err != nil {
			return fmt.Errorf("scanner bundle: signature %s provenance.published %q is not YYYY-MM-DD", s.ID, s.Provenance.Published)
		}
		if strings.TrimSpace(s.License) == "" {
			return fmt.Errorf("scanner bundle: signature %s has an empty license", s.ID)
		}
		if len(s.RuleIDs) == 0 {
			return fmt.Errorf("scanner bundle: signature %s lists no rule_ids", s.ID)
		}
	}

	if b.SignatureCount != len(b.Signatures) {
		return fmt.Errorf("scanner bundle: signature_count %d != len(signatures) %d", b.SignatureCount, len(b.Signatures))
	}

	type ruleKey struct{ id, detector string }
	ruleByKey := make(map[ruleKey]*rawRule, len(b.Rules))
	for i := range b.Rules {
		r := &b.Rules[i]
		k := ruleKey{r.ID, r.Detector}
		if _, dup := ruleByKey[k]; dup {
			return fmt.Errorf("scanner bundle: duplicate rule %s/%s", r.ID, r.Detector)
		}
		ruleByKey[k] = r
		if i > 0 {
			p := &b.Rules[i-1]
			if p.ID > r.ID || (p.ID == r.ID && p.Detector > r.Detector) {
				return fmt.Errorf("scanner bundle: rules[] not sorted by (id, detector): %s/%s precedes %s/%s",
					p.ID, p.Detector, r.ID, r.Detector)
			}
		}
	}

	owned := make(map[ruleKey]bool, len(b.Rules))
	runnableOwned := make(map[string]bool, len(b.Signatures))
	for i := range b.Signatures {
		s := &b.Signatures[i]
		for _, ref := range s.RuleIDs {
			id, detector, ok := strings.Cut(ref, "/")
			if !ok || id == "" || detector == "" {
				return fmt.Errorf("scanner bundle: signature %s rule_ids entry %q is not \"<TPA-id>/<detector>\"", s.ID, ref)
			}
			if id != s.ID {
				return fmt.Errorf("scanner bundle: signature %s rule_ids entry %q must start with its own id", s.ID, ref)
			}
			k := ruleKey{id, detector}
			r, found := ruleByKey[k]
			if !found {
				return fmt.Errorf("scanner bundle: signature %s rule_ids entry %q does not resolve to a rules[] entry", s.ID, ref)
			}
			if owned[k] {
				return fmt.Errorf("scanner bundle: rule %s owned more than once", ref)
			}
			owned[k] = true
			if isRunnableRule(r) {
				runnableOwned[s.ID] = true
			}
		}
	}

	for i := range b.Rules {
		r := &b.Rules[i]
		if !owned[ruleKey{r.ID, r.Detector}] {
			return fmt.Errorf("scanner bundle: rule %s/%s is not owned by any signature", r.ID, r.Detector)
		}
	}

	// gating is a declaration checked against what the loader really runs: a
	// signature gates exactly when it owns a runnable rule, because every
	// runnable rule emits at the hard tier (BundleCheck.Inspect).
	for i := range b.Signatures {
		s := &b.Signatures[i]
		switch {
		case s.Gating && !runnableOwned[s.ID]:
			return fmt.Errorf("scanner bundle: signature %s declares gating=true but owns no runnable rule", s.ID)
		case !s.Gating && runnableOwned[s.ID]:
			return fmt.Errorf("scanner bundle: signature %s declares gating=false but owns a runnable rule", s.ID)
		}
	}

	for i := range b.Skipped {
		sk := &b.Skipped[i]
		if _, ok := sigByID[sk.ID]; !ok {
			return fmt.Errorf("scanner bundle: skipped entry %s/%s does not resolve to a signature", sk.ID, sk.Detector)
		}
	}
	return nil
}
