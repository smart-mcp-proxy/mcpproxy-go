// Validator for the Spec-076 detect-engine labeled evaluation corpus
// (detect_corpus_v1.json). This corpus is distinct from the Spec-065 D2 corpus
// (security_corpus_v1.json): it carries the full ToolView fields the offline
// detect.Engine needs (server, tool name, schema, cross-server peers) and uses
// the detect taxonomy (unicode_smuggling / decoded_payload / shadowing /
// capability_mismatch). The eval gate (cmd/scan-eval --gate) scores against it.
//
// This test enforces the cross-entity invariants plain JSON Schema cannot:
// coherent label/category, redistributable provenance (reusing the allowlist in
// corpus_test.go), and that every GATED attack category is covered by at least
// one malicious sample and one attack-resembling hard-negative (SC-006).
package datasets

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const detectCorpusFile = "detect_corpus_v1.json"

// gatedDetectCategories are the malicious taxonomies the detect.Engine can
// measure today. phrase_injection joined the gate in Spec 077 US1 (its curated
// hard-tier check is registered in gateChecks). capability_mismatch is still
// intentionally excluded — its check is soft/measured-not-gated, so the corpus
// may carry samples but the gate must not enforce them (the gate handles this
// via its category→check registration map).
var gatedDetectCategories = []string{"unicode_smuggling", "decoded_payload", "shadowing", "phrase_injection"}

// hardNegPrefix maps a gated category to the id prefix its resembling
// hard-negatives use, so INV-3 (which attack a benign FP mimics) stays
// machine-readable for the detect corpus.
var hardNegPrefix = map[string]string{
	"unicode_smuggling": "hn_unicode",
	"decoded_payload":   "hn_decoded",
	"shadowing":         "hn_shadowing",
	"phrase_injection":  "hn_phrase",
}

type detectTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type detectPeer struct {
	Server string     `json:"server"`
	Tool   detectTool `json:"tool"`
}

type detectEntry struct {
	ID         string       `json:"id"`
	Label      string       `json:"label"`
	Category   string       `json:"category"`
	Resembles  string       `json:"resembles"`
	Server     string       `json:"server"`
	Tool       detectTool   `json:"tool"`
	Peers      []detectPeer `json:"peers"`
	Provenance provenance   `json:"provenance"`
}

type detectCorpus struct {
	Version string        `json:"version"`
	Entries []detectEntry `json:"entries"`
}

// validDetectCategory reports whether a (label, category) pair is coherent.
func validDetectCategory(label, category string) bool {
	switch label {
	case "malicious":
		switch category {
		case "unicode_smuggling", "decoded_payload", "shadowing", "phrase_injection", "capability_mismatch":
			return true
		}
	case "benign":
		return category == "benign" || category == "hard_negative"
	}
	return false
}

func loadDetectCorpus(t *testing.T) detectCorpus {
	t.Helper()
	raw, err := os.ReadFile(detectCorpusFile)
	if err != nil {
		t.Fatalf("read %s: %v", detectCorpusFile, err)
	}
	var c detectCorpus
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse %s: %v", detectCorpusFile, err)
	}
	if len(c.Entries) == 0 {
		t.Fatalf("%s has no entries", detectCorpusFile)
	}
	return c
}

func TestDetectCorpus_SchemaAndProvenance(t *testing.T) {
	c := loadDetectCorpus(t)

	seen := map[string]bool{}
	for i, e := range c.Entries {
		if e.ID == "" {
			t.Errorf("entry %d: empty id", i)
		}
		if seen[e.ID] {
			t.Errorf("entry %d: duplicate id %q", i, e.ID)
		}
		seen[e.ID] = true

		if strings.TrimSpace(e.Server) == "" {
			t.Errorf("entry %q: empty server", e.ID)
		}
		if strings.TrimSpace(e.Tool.Name) == "" {
			t.Errorf("entry %q: empty tool.name", e.ID)
		}
		if strings.TrimSpace(e.Tool.Description) == "" {
			t.Errorf("entry %q: empty tool.description", e.ID)
		}
		if !validDetectCategory(e.Label, e.Category) {
			t.Errorf("entry %q: incoherent label/category %q/%q", e.ID, e.Label, e.Category)
		}

		// Peers must point at a DIFFERENT server to be meaningful cross-server
		// context (a same-server peer is allowed only for the intra-server
		// shadowing hard-negative, which is intentional).
		for _, p := range e.Peers {
			if strings.TrimSpace(p.Server) == "" || strings.TrimSpace(p.Tool.Name) == "" {
				t.Errorf("entry %q: peer with empty server/name", e.ID)
			}
		}

		// CN-005 / FR-007: redistributable provenance only (allowlist defined in
		// corpus_test.go, same package). Restricted sources never vendored.
		if e.Provenance.Source == "" {
			t.Errorf("entry %q: empty provenance.source", e.ID)
		}
		if restrictedSources[e.Provenance.Source] {
			t.Errorf("entry %q: source %q is restricted and must not be vendored", e.ID, e.Provenance.Source)
		}
		if !redistributableLicenses[e.Provenance.License] {
			t.Errorf("entry %q: license %q not in redistributable allowlist", e.ID, e.Provenance.License)
		}
	}
}

func TestDetectCorpus_GatedCoverage(t *testing.T) {
	c := loadDetectCorpus(t)

	maliciousByCat := map[string]int{}
	hardNegByCat := map[string]int{}
	for _, e := range c.Entries {
		if e.Label == "malicious" {
			maliciousByCat[e.Category]++
		}
		if e.Label == "benign" && e.Category == "hard_negative" {
			// `resembles` is the machine-readable attribution the gate uses for
			// per-category precision/FP; it must be set, name a gated category, and
			// agree with the id prefix convention.
			if e.Resembles == "" {
				t.Errorf("hard_negative %q: missing resembles (needed for per-category FP)", e.ID)
				continue
			}
			if prefix, ok := hardNegPrefix[e.Resembles]; !ok {
				t.Errorf("hard_negative %q: resembles %q is not a gated category", e.ID, e.Resembles)
			} else if !strings.HasPrefix(e.ID, prefix) {
				t.Errorf("hard_negative %q: id should start with %q to match resembles %q", e.ID, prefix, e.Resembles)
			}
			hardNegByCat[e.Resembles]++
		}
	}

	// SC-006 / FR-013: every gated attack category needs >=1 malicious sample so
	// the gate can measure recall, AND >=1 attack-resembling hard-negative so it
	// can measure the false-positive rate.
	for _, cat := range gatedDetectCategories {
		if maliciousByCat[cat] == 0 {
			t.Errorf("gated category %q has no malicious sample", cat)
		}
		if hardNegByCat[cat] == 0 {
			t.Errorf("gated category %q has no resembling hard-negative (id prefix %q)", cat, hardNegPrefix[cat])
		}
	}
}
