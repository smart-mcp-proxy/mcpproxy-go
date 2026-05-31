// Package datasets holds the validator for the Spec 065 evaluation datasets.
//
// This test enforces the D2 security-corpus contract
// (specs/065-evaluation-foundation/contracts/security-corpus.schema.json) plus
// the cross-entity invariants from data-model.md that plain JSON Schema cannot
// express (INV-3, INV-4, SC-004): every entry carries a redistributable
// provenance, label/category are coherent, and every attack category is
// covered by at least one malicious sample and at least one attack-resembling
// benign hard negative.
package datasets

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	corpusFile = "security_corpus_v1.json"
	schemaFile = "../contracts/security-corpus.schema.json"
)

// attackCategories are the four malicious taxonomies the corpus must cover.
var attackCategories = []string{"tool_poisoning", "prompt_injection", "shadowing", "rug_pull"}

// redistributableLicenses is the allowlist enforcing CN-005 / FR-007 / INV-4:
// no entry may carry a redistribution-restricted license.
var redistributableLicenses = map[string]bool{
	"self-authored": true,
	"MIT":           true,
	"Apache-2.0":    true,
	"BSD-3-Clause":  true,
	"CC0-1.0":       true,
}

// restrictedSources must never be vendored (CN-005 + R-A): MCPTox / MCP-AttackBench
// are restrictive; mcp-injection-experiments has an unconfirmed LICENSE (R-A) so we
// deliberately reference it externally only and never vendor its text.
var restrictedSources = map[string]bool{
	"MCPTox":                    true,
	"MCP-AttackBench":           true,
	"mcp-injection-experiments": true,
}

type provenance struct {
	Source  string `json:"source"`
	License string `json:"license"`
}

type entry struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Label       string     `json:"label"`
	Category    string     `json:"category"`
	Provenance  provenance `json:"provenance"`
}

type corpus struct {
	Entries []entry `json:"entries"`
}

func loadCorpus(t *testing.T) corpus {
	t.Helper()
	raw, err := os.ReadFile(corpusFile)
	if err != nil {
		t.Fatalf("read %s: %v", corpusFile, err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse %s: %v", corpusFile, err)
	}

	// Validate against the committed JSON Schema contract.
	schemaRaw, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatalf("read %s: %v", schemaFile, err)
	}
	schemaDoc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(schemaRaw)))
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("security-corpus.schema.json", schemaDoc); err != nil {
		t.Fatalf("add schema resource: %v", err)
	}
	sch, err := c.Compile("security-corpus.schema.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("corpus fails schema contract: %v", err)
	}

	// Re-decode into typed structs for the invariant checks.
	var typed corpus
	if err := json.Unmarshal(raw, &typed); err != nil {
		t.Fatalf("decode corpus into structs: %v", err)
	}
	return typed
}

func TestCorpus_SchemaAndStructure(t *testing.T) {
	c := loadCorpus(t)

	if len(c.Entries) == 0 {
		t.Fatal("corpus has no entries (schema minItems=1)")
	}

	seen := map[string]bool{}
	for i, e := range c.Entries {
		if e.ID == "" {
			t.Errorf("entry %d: empty id", i)
		}
		if seen[e.ID] {
			t.Errorf("entry %d: duplicate id %q", i, e.ID)
		}
		seen[e.ID] = true

		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("entry %q: empty description", e.ID)
		}

		// INV-4 / FR-007: label + category + provenance license present and coherent.
		switch e.Label {
		case "malicious":
			if !contains(attackCategories, e.Category) {
				t.Errorf("entry %q: malicious label requires an attack category, got %q", e.ID, e.Category)
			}
		case "benign":
			if e.Category != "benign" && e.Category != "hard_negative" {
				t.Errorf("entry %q: benign label requires category benign|hard_negative, got %q", e.ID, e.Category)
			}
		default:
			t.Errorf("entry %q: invalid label %q", e.ID, e.Label)
		}

		// CN-005 / INV-4: redistributable provenance only; restricted sources never vendored.
		if e.Provenance.Source == "" {
			t.Errorf("entry %q: empty provenance.source", e.ID)
		}
		if restrictedSources[e.Provenance.Source] {
			t.Errorf("entry %q: source %q is restricted/unconfirmed and must not be vendored (CN-005/R-A)", e.ID, e.Provenance.Source)
		}
		if !redistributableLicenses[e.Provenance.License] {
			t.Errorf("entry %q: license %q is not in the redistributable allowlist (CN-005/FR-007)", e.ID, e.Provenance.License)
		}
	}
}

func TestCorpus_AttackCoverageAndHardNegatives(t *testing.T) {
	c := loadCorpus(t)

	maliciousByCat := map[string]int{}
	hardNegByMimic := map[string]int{}

	for _, e := range c.Entries {
		if e.Label == "malicious" {
			maliciousByCat[e.Category]++
		}
		if e.Label == "benign" && e.Category == "hard_negative" {
			// Convention: hard-negative ids encode the attack they mimic as
			// hn_<attack_category>_<n> so INV-3 (which attack a benign FP resembles)
			// is machine-readable.
			mimic := mimickedAttack(e.ID)
			if mimic == "" {
				t.Errorf("hard_negative %q: id must be hn_<attack_category>_<n> to declare the mimicked attack", e.ID)
				continue
			}
			hardNegByMimic[mimic]++
		}
	}

	// INV-3 / SC-004: every attack category is covered by >=1 malicious sample
	// AND >=1 attack-resembling benign hard negative.
	for _, cat := range attackCategories {
		if maliciousByCat[cat] == 0 {
			t.Errorf("attack category %q has no malicious sample", cat)
		}
		if hardNegByMimic[cat] == 0 {
			t.Errorf("attack category %q has no hard-negative benign (SC-004/INV-3)", cat)
		}
	}
}

func mimickedAttack(id string) string {
	rest, ok := strings.CutPrefix(id, "hn_")
	if !ok {
		return ""
	}
	for _, cat := range attackCategories {
		if strings.HasPrefix(rest, cat+"_") {
			return cat
		}
	}
	return ""
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
