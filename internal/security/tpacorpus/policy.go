package tpacorpus

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// datasetEntry is the subset of a detect-corpus entry the policy reads. Unknown
// keys are ignored, as in every other decoder of that file.
type datasetEntry struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Category  string `json:"category"`
	Resembles string `json:"resembles"`
	TPAID     string `json:"tpa_id"`
	Canonical bool   `json:"canonical"`
}

// HardNegativePrefix is the id prefix hard negatives of a class must carry.
func HardNegativePrefix(class string) string { return "hn_" + class + "_" }

// ValidatePolicy checks a built bundle against the eval dataset (spec 101
// D-US2-3, policy layer). It reports every violation at once:
//
//   - the bundle has a non-empty signatures[] section (the runtime loader stays
//     lenient about legacy bundles without one; the shipped corpus may not);
//   - the class vocabulary has no prefix collisions, and every signature uses a
//     known class and an allowlisted licence;
//   - every dataset tpa_id resolves to a signature and its entry is a malicious
//     sample of that signature's class;
//   - every gating signature has at least one such malicious sample;
//   - every class with a gating signature has at least one hard negative
//     (id hn_<class>_*, category hard_negative, resembles == class) and exactly one canonical sample.
//
// Whether the gate scores a class (categoryCheck) is closed by a scan-eval test,
// because categoryCheck lives in that package.
func ValidatePolicy(bundle, dataset []byte) error {
	sigs, err := scanner.ParseBundleSignatures(bundle)
	if err != nil {
		return err
	}
	var ds struct {
		Entries []datasetEntry `json:"entries"`
	}
	if err := json.Unmarshal(dataset, &ds); err != nil {
		return fmt.Errorf("parse dataset: %w", err)
	}

	var problems []string
	addf := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if len(sigs) == 0 {
		addf("bundle has no signatures[]: the shipped corpus must carry per-signature licence, provenance and eval pairs")
	}

	for i, a := range Classes {
		for j, b := range Classes {
			if i != j && strings.HasPrefix(b, a) {
				addf("class %q is a prefix of class %q", a, b)
			}
		}
	}

	byID := make(map[string]scanner.Signature, len(sigs))
	var gatingClasses []string
	for _, s := range sigs {
		byID[s.ID] = s
		if !isClass(s.Category) {
			addf("signature %s: unknown class %q", s.ID, s.Category)
		}
		if !isAllowedLicence(s.License) {
			addf("signature %s: license %q is not in the allowlist %v", s.ID, s.License, LicenceAllowlist)
		}
		if s.Gating && !slices.Contains(gatingClasses, s.Category) {
			gatingClasses = append(gatingClasses, s.Category)
		}
	}

	samples := map[string]int{}   // signature id -> qualifying malicious samples
	canonical := map[string]int{} // class -> valid canonical entries
	hardNegs := map[string]int{}  // class -> qualifying hard negatives
	for _, e := range ds.Entries {
		if e.TPAID != "" {
			s, ok := byID[e.TPAID]
			switch {
			case !ok:
				addf("dataset entry %q: tpa_id %s resolves to no signature in the bundle", e.ID, e.TPAID)
			case e.Label != "malicious":
				addf("dataset entry %q: tpa_id %s is set but label is %q, not malicious", e.ID, e.TPAID, e.Label)
			case e.Category != s.Category:
				addf("dataset entry %q: category %q differs from signature %s class %q", e.ID, e.Category, s.ID, s.Category)
			default:
				samples[s.ID]++
			}
		}
		if e.Canonical {
			s, ok := byID[e.TPAID]
			if !ok || e.Label != "malicious" || e.Category != s.Category || !s.Gating {
				addf("dataset entry %q: canonical entries must be a malicious sample of a gating signature", e.ID)
			} else {
				canonical[s.Category]++
			}
		}
		if e.Label == "benign" && e.Category == "hard_negative" && e.Resembles != "" && strings.HasPrefix(e.ID, HardNegativePrefix(e.Resembles)) {
			hardNegs[e.Resembles]++
		}
	}

	for _, s := range sigs {
		if s.Gating && samples[s.ID] == 0 {
			addf("gating signature %s (%s) has no malicious sample in the dataset (tpa_id %s)", s.ID, s.Category, s.ID)
		}
	}
	for _, class := range gatingClasses {
		if hardNegs[class] == 0 {
			addf("class %s has no hard negative (id %s*, resembles %s)", class, HardNegativePrefix(class), class)
		}
		if n := canonical[class]; n != 1 {
			addf("class %s must have exactly one canonical sample, has %d", class, n)
		}
	}

	if len(problems) == 0 {
		return nil
	}
	errs := make([]error, len(problems))
	for i, p := range problems {
		errs[i] = errors.New(p)
	}
	return fmt.Errorf("corpus policy: %d violation(s):\n%w", len(problems), errors.Join(errs...))
}
