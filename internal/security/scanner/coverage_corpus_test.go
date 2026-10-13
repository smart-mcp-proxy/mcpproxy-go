package scanner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// detectCorpusPath is the labeled detect-engine evaluation corpus, relative to
// this package directory. It is the same corpus the cmd/scan-eval --gate CI step
// runs, so this test and the gate measure the identical detector.
const detectCorpusPath = "../../../specs/065-evaluation-foundation/datasets/detect_corpus_v1.json"

// corpusEntry mirrors the fields of a detect_corpus_v1.json entry this test
// needs. Extra fields in the file are ignored.
type corpusEntry struct {
	ID       string `json:"id"`
	Label    string `json:"label"`    // "malicious" | "benign"
	Category string `json:"category"` // detect taxonomy or benign|hard_negative
	Server   string `json:"server"`
	Tool     struct {
		Name         string          `json:"name"`
		Description  string          `json:"description"`
		InputSchema  json.RawMessage `json:"input_schema,omitempty"`
		OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	} `json:"tool"`
	Peers []struct {
		Server string `json:"server"`
		Tool   struct {
			Name         string          `json:"name"`
			Description  string          `json:"description"`
			InputSchema  json.RawMessage `json:"input_schema,omitempty"`
			OutputSchema json.RawMessage `json:"output_schema,omitempty"`
		} `json:"tool"`
	} `json:"peers,omitempty"`
}

type corpusFile struct {
	Entries []corpusEntry `json:"entries"`
}

func loadDetectCorpus(t *testing.T) corpusFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(detectCorpusPath))
	if err != nil {
		t.Fatalf("read detect corpus: %v", err)
	}
	var c corpusFile
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse detect corpus: %v", err)
	}
	if len(c.Entries) == 0 {
		t.Fatalf("detect corpus has no entries")
	}
	return c
}

// scanCorpusEntry runs the in-process scanner over one corpus entry (its own
// tool + declared peers) exactly as the live scanner does, and returns the
// findings on the entry's own tool.
func scanCorpusEntry(t *testing.T, e corpusEntry) []ScanFinding {
	t.Helper()
	tools := []map[string]interface{}{toolMap(e.Tool.Name, e.Tool.Description, e.Tool.InputSchema, e.Tool.OutputSchema)}
	peers := map[string][]toolDef{}
	for _, p := range e.Peers {
		peers[p.Server] = append(peers[p.Server], toolDef{
			Name:         p.Tool.Name,
			Description:  p.Tool.Description,
			InputSchema:  p.Tool.InputSchema,
			OutputSchema: p.Tool.OutputSchema,
		})
	}
	if len(peers) == 0 {
		peers = nil
	}
	return inProcessToolScan(loadToolsJSON(t, writeToolsJSON(t, tools)), e.Server, peers, "tpa-descriptions")
}

// toolMap builds an MCP tools/list tool entry, omitting empty schemas.
func toolMap(name, desc string, in, out json.RawMessage) map[string]interface{} {
	m := map[string]interface{}{"name": name, "description": desc}
	if len(in) > 0 {
		m["inputSchema"] = json.RawMessage(in)
	}
	if len(out) > 0 {
		m["outputSchema"] = json.RawMessage(out)
	}
	return m
}

// TestCoverage_NoLossAfterLegacyRuleDeletion is the Spec 077 US1 regression
// contract (SC-004): after deleting the legacy tpaRules and legacy
// embedded-secret path, EVERY malicious sample in the evaluation corpus must
// still be detected by the deterministic engine (produce ≥1 finding on the
// scanned tool). This proves the deletion lost no detection coverage.
func TestCoverage_NoLossAfterLegacyRuleDeletion(t *testing.T) {
	c := loadDetectCorpus(t)
	for _, e := range c.Entries {
		if e.Label != "malicious" {
			continue
		}
		// capability_mismatch is a SOFT, measured-not-gated category (see the
		// corpus description): its detection depends on schema/URL structure and
		// some entries (e.g. a URL-less "analytics endpoint" sink) were never
		// blocked by the deleted legacy rules either. Excluding it keeps this a
		// true no-REGRESSION contract rather than asserting new coverage the
		// legacy stack never provided.
		if e.Category == "capability_mismatch" {
			continue
		}
		e := e
		t.Run(e.ID, func(t *testing.T) {
			findings := scanCorpusEntry(t, e)
			if len(findings) == 0 {
				t.Fatalf("coverage loss: malicious corpus entry %q (category %q) produced NO finding after legacy-rule deletion", e.ID, e.Category)
			}
		})
	}
}

// TestCoverage_BenignNotHardBlocked complements the above (FR-005): benign and
// hard-negative corpus samples must NOT produce a hard-tier (approval-blocking)
// baseline finding. A soft/review finding is tolerated; a hard block on a benign
// tool is the false positive this feature must avoid.
func TestCoverage_BenignNotHardBlocked(t *testing.T) {
	c := loadDetectCorpus(t)
	for _, e := range c.Entries {
		if e.Label == "malicious" {
			continue
		}
		e := e
		t.Run(e.ID, func(t *testing.T) {
			for _, f := range scanCorpusEntry(t, e) {
				if f.Tier == TierHard {
					t.Fatalf("false positive: benign entry %q (category %q) hard-blocked by %v (%s)", e.ID, e.Category, f.Signals, f.Description)
				}
			}
		})
	}
}
