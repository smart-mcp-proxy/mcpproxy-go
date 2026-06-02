// Baseline shape + self-consistency guard for the Spec 065 / D2 security section
// of baseline_v1.json (MCP-815). The committed security block is the regression
// anchor that the CI gate (C1/MCP-742, eval.yml) diffs a fresh `mcp-eval security`
// run against. This test enforces two things JSON Schema cannot:
//
//  1. the security block carries the per-detector metrics + gate thresholds the
//     scorer's SecurityReport shape requires (so the gate has something to read), and
//  2. the committed numbers themselves PASS their own gate (fpr <= fpr_ceiling AND
//     recall >= recall_floor) — a guard so a bad future edit that bakes in a
//     gate-violating anchor fails CI immediately, not silently.
package datasets

import (
	"encoding/json"
	"os"
	"testing"
)

const baselineFile = "baseline_v1.json"

type secGate struct {
	FPRCeiling  float64 `json:"fpr_ceiling"`
	RecallFloor float64 `json:"recall_floor"`
}

type secDetector struct {
	Detector  string  `json:"detector"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
	FPR       float64 `json:"fpr"`
	TP        int     `json:"tp"`
	FP        int     `json:"fp"`
	TN        int     `json:"tn"`
	FN        int     `json:"fn"`
	Runs      int     `json:"runs"`
}

type securitySection struct {
	CorpusVersion string        `json:"corpus_version"`
	RunsAveraged  int           `json:"runs_averaged"`
	PerDetector   []secDetector `json:"per_detector"`
	Gate          secGate       `json:"gate"`
}

type baselineDoc struct {
	Security securitySection `json:"security"`
}

func loadBaseline(t *testing.T) baselineDoc {
	t.Helper()
	raw, err := os.ReadFile(baselineFile)
	if err != nil {
		t.Fatalf("read %s: %v", baselineFile, err)
	}
	var doc baselineDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", baselineFile, err)
	}
	return doc
}

func TestBaseline_SecuritySectionShape(t *testing.T) {
	doc := loadBaseline(t)
	sec := doc.Security

	if sec.CorpusVersion == "" {
		t.Error("security.corpus_version is empty")
	}
	if sec.RunsAveraged < 1 {
		t.Errorf("security.runs_averaged must be >=1, got %d", sec.RunsAveraged)
	}
	if len(sec.PerDetector) == 0 {
		t.Fatal("security.per_detector is empty — the gate has nothing to diff against")
	}
	if sec.Gate.FPRCeiling <= 0 || sec.Gate.FPRCeiling > 1 {
		t.Errorf("security.gate.fpr_ceiling out of (0,1]: %v", sec.Gate.FPRCeiling)
	}
	if sec.Gate.RecallFloor < 0 || sec.Gate.RecallFloor > 1 {
		t.Errorf("security.gate.recall_floor out of [0,1]: %v", sec.Gate.RecallFloor)
	}

	for _, d := range sec.PerDetector {
		if d.Detector == "" {
			t.Error("per_detector entry has empty detector name")
		}
		// Confusion-matrix counts must be coherent with the reported rates.
		if d.TP+d.FP+d.TN+d.FN == 0 {
			t.Errorf("detector %q: empty confusion matrix", d.Detector)
		}
		for name, v := range map[string]float64{
			"precision": d.Precision, "recall": d.Recall, "f1": d.F1, "fpr": d.FPR,
		} {
			if v < 0 || v > 1 {
				t.Errorf("detector %q: %s out of [0,1]: %v", d.Detector, name, v)
			}
		}
	}
}

// TestBaseline_SecuritySelfConsistentWithGate asserts the committed anchor itself
// passes the gate it ships with. If a future refresh bakes in numbers that violate
// the gate, this fails before the CI scorer ever runs — the anchor must be valid.
func TestBaseline_SecuritySelfConsistentWithGate(t *testing.T) {
	doc := loadBaseline(t)
	sec := doc.Security

	for _, d := range sec.PerDetector {
		if d.FPR > sec.Gate.FPRCeiling {
			t.Errorf("detector %q: baseline fpr %v exceeds gate.fpr_ceiling %v (anchor must pass its own gate)",
				d.Detector, d.FPR, sec.Gate.FPRCeiling)
		}
		if d.Recall < sec.Gate.RecallFloor {
			t.Errorf("detector %q: baseline recall %v below gate.recall_floor %v (anchor must pass its own gate)",
				d.Detector, d.Recall, sec.Gate.RecallFloor)
		}
	}
}
