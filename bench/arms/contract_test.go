package arms

// contract_test.go — the arm behavioral contract (T022), enforced for EVERY
// arm in the default registry on the schema-bearing corpus_v2
// (specs/083-discovery-profiler/contracts/arm-interface.md):
//
//	(a) Determinism (FR-010, contract rule 1): encoding the same input twice
//	    is byte-equal.
//	(b) Two-sided IndexAltering declaration (FR-008, contract rule 4): the
//	    arm's EncodeIndexMetadata output is diffed against the baseline arm's
//	    over all of corpus_v2. ANY difference with IndexAltering()==false is
//	    an under-declaration (the index would silently ingest different text
//	    without quality scoring) and FAILS; ZERO difference with
//	    IndexAltering()==true is an over-declaration (wastes CI scoring time,
//	    mislabels the report) and FAILS.
//
// Arms whose external runtime is missing (ErrArmUnavailable) are skipped
// locally with an explicit log line naming the arm; CI always provides the
// runtimes, so a mandatory-arm skip there fails SC-002 verification at the
// harness level, not here (contract rule 5).

import (
	"errors"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// contractCorpusV2Path is the schema-bearing frozen corpus the contract test
// runs on — corpus_v1 lacks schemas and cannot catch parameter-text changes
// (contracts/arm-interface.md rule 4).
const contractCorpusV2Path = "../../specs/083-discovery-profiler/datasets/corpus_v2.tools.json"

// loadContractCorpus loads corpus_v2 with the schema-bearing validation
// (every tool must carry a schema; schemas compacted to canonical form).
func loadContractCorpus(t *testing.T) *bench.Corpus {
	t.Helper()
	corpus, err := bench.LoadCorpusV2(contractCorpusV2Path)
	if err != nil {
		t.Fatalf("LoadCorpusV2(%s): %v", contractCorpusV2Path, err)
	}
	return corpus
}

// resolveOrSkip resolves an arm from the default registry, skipping the test
// (with the arm named in the log) when its external runtime is unavailable.
func resolveOrSkip(t *testing.T, name string) Arm {
	t.Helper()
	arm, err := Resolve(name)
	if err != nil {
		if errors.Is(err, ErrArmUnavailable) {
			t.Logf("SKIPPED arm %q: runtime unavailable", name)
			t.Skipf("arm %q unavailable: %v", name, err)
		}
		t.Fatalf("Resolve(%s): %v", name, err)
	}
	return arm
}

// indexMetadataEqual compares the four fields the production index ingests
// (internal/index BleveIndex.BatchIndex: Name, ServerName, Description,
// ParamsJSON). Hash/timestamps/annotations are not part of the arm mapping.
func indexMetadataEqual(a, b config.ToolMetadata) bool {
	return a.Name == b.Name &&
		a.ServerName == b.ServerName &&
		a.Description == b.Description &&
		a.ParamsJSON == b.ParamsJSON
}

// TestArmContract_Determinism enforces contract rule 1 on corpus_v2 for every
// registered arm: EncodeListing over the whole corpus twice is byte-equal, and
// EncodeTool on the first three tools twice is byte-equal (errors, if any,
// must be equally deterministic).
func TestArmContract_Determinism(t *testing.T) {
	corpus := loadContractCorpus(t)

	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			arm := resolveOrSkip(t, name)

			l1, err1 := arm.EncodeListing(corpus.Tools)
			l2, err2 := arm.EncodeListing(corpus.Tools)
			if (err1 == nil) != (err2 == nil) {
				t.Fatalf("EncodeListing error not deterministic: %v vs %v", err1, err2)
			}
			if err1 != nil {
				if err1.Error() != err2.Error() {
					t.Fatalf("EncodeListing errors differ: %q vs %q", err1, err2)
				}
			} else if l1 != l2 {
				t.Errorf("EncodeListing not byte-deterministic across runs (FR-010)")
			}

			n := 3
			if len(corpus.Tools) < n {
				n = len(corpus.Tools)
			}
			for _, tl := range corpus.Tools[:n] {
				e1, terr1 := arm.EncodeTool(tl)
				e2, terr2 := arm.EncodeTool(tl)
				if (terr1 == nil) != (terr2 == nil) {
					t.Fatalf("EncodeTool(%s) error not deterministic: %v vs %v", tl.ToolID, terr1, terr2)
				}
				if terr1 != nil {
					if terr1.Error() != terr2.Error() {
						t.Fatalf("EncodeTool(%s) errors differ: %q vs %q", tl.ToolID, terr1, terr2)
					}
					continue
				}
				if e1 != e2 {
					t.Errorf("EncodeTool(%s) not byte-deterministic across runs (FR-010)", tl.ToolID)
				}
			}
		})
	}
}

// TestArmContract_IndexAlteringTwoSided enforces contract rule 4 on corpus_v2
// for every registered arm: EncodeIndexMetadata is diffed against the baseline
// arm's output tool by tool. Under-declaration (difference while
// IndexAltering()==false) and over-declaration (zero difference while
// IndexAltering()==true) both fail.
func TestArmContract_IndexAlteringTwoSided(t *testing.T) {
	corpus := loadContractCorpus(t)

	baseline := resolveOrSkip(t, BaselineName) // always available (no runtime)
	baseMetas := make(map[string]config.ToolMetadata, len(corpus.Tools))
	for _, tl := range corpus.Tools {
		meta, err := baseline.EncodeIndexMetadata(tl)
		if err != nil {
			t.Fatalf("baseline EncodeIndexMetadata(%s): %v", tl.ToolID, err)
		}
		baseMetas[tl.ToolID] = meta
	}

	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			arm := resolveOrSkip(t, name)

			differs := false
			var firstDiff string
			for _, tl := range corpus.Tools {
				meta, err := arm.EncodeIndexMetadata(tl)
				if err != nil {
					t.Fatalf("EncodeIndexMetadata(%s): %v", tl.ToolID, err)
				}
				if !indexMetadataEqual(meta, baseMetas[tl.ToolID]) {
					if !differs {
						firstDiff = tl.ToolID
					}
					differs = true
				}
			}

			switch {
			case differs && !arm.IndexAltering():
				t.Errorf("UNDER-DECLARATION: arm %q changes the index-ingested text (first diff: tool %s) but IndexAltering()==false — the index would silently ingest different text without retrieval-quality scoring (FR-008)", name, firstDiff)
			case !differs && arm.IndexAltering():
				t.Errorf("OVER-DECLARATION: arm %q declares IndexAltering()==true but its EncodeIndexMetadata output is identical to baseline on all %d corpus_v2 tools — over-declaration wastes CI scoring time and mislabels the report", name, len(corpus.Tools))
			}
		})
	}
}
