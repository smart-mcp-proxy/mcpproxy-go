package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/tpacorpus"
)

// controlID is the D9 negative control (Spec 101 D-US2-7, FR-016b): the one
// signature whose sample no built-in hard check flags, so removing it from the
// bundle must breach the gate.
const controlID = "TPA-2026-0044"

const (
	controlCorpusPath   = "testdata/bundle_control_corpus.json"
	controlSignaturesFS = "../../internal/security/tpacorpus/signatures"
)

// scanEntry scans one entry (plus its peers) with the given checks and returns
// the finding for the entry's own tool, if any.
func scanEntry(checkList []detect.Check, e gateEntry) (detect.Finding, bool) {
	engine := detect.NewEngine(detect.Options{Checks: checkList})
	views := []detect.ToolView{toGateView(e.Server, e.Tool)}
	for _, p := range e.Peers {
		views = append(views, toGateView(p.Server, p.Tool))
	}
	res := engine.Scan(detect.NewRegistryView(views))
	want := e.Server + ":" + e.Tool.Name
	for _, f := range res.Findings {
		if f.Location == want {
			return f, true
		}
	}
	return detect.Finding{}, false
}

func tpaSignalIDs(f detect.Finding) []string {
	var ids []string
	for _, s := range f.Signals {
		if strings.HasPrefix(s, "tpa.") {
			ids = append(ids, s)
		}
	}
	return ids
}

// TestBundleControl_NoOverlap proves the control is a real control: the gate
// built-ins alone detect nothing on its sample, and with the full embedded
// bundle the only tpa. signal is the control's own, with no hard negative
// flagged.
func TestBundleControl_NoOverlap(t *testing.T) {
	gc, err := loadGateCorpus(controlCorpusPath)
	if err != nil {
		t.Fatal(err)
	}

	m := evaluateGateCorpus(gc, builtinGateChecks())
	// Without the bundle no tpa_* category is gated, but Detected still counts
	// what the engine flagged.
	for _, c := range m.Categories {
		if c.Category == "tpa_persistence_implant" && c.Detected != 0 {
			t.Errorf("built-ins alone detected %d control sample(s); the control must be invisible to them", c.Detected)
		}
	}

	bundle, err := loadGateBundle("")
	if err != nil {
		t.Fatal(err)
	}
	full := gateChecks(bundle.check)

	var sawControl bool
	for _, e := range gc.Entries {
		if e.Label == "malicious" {
			if _, hit := scanEntry(builtinGateChecks(), e); hit {
				t.Errorf("%s: a built-in check produced a finding; the control must not overlap them", e.ID)
			}
			f, ok := scanEntry(full, e)
			if !ok || f.ThreatLevel != detect.ThreatLevelDangerous {
				t.Fatalf("%s: full bundle did not hard-flag the control sample (finding=%v ok=%v)", e.ID, f, ok)
			}
			ids := tpaSignalIDs(f)
			if len(ids) == 0 {
				t.Fatalf("%s: no tpa. signal in %v", e.ID, f.Signals)
			}
			fams := map[string]bool{}
			for _, id := range ids {
				parts := strings.Split(id, ".")
				if len(parts) < 3 {
					t.Fatalf("unexpected signal id %q", id)
				}
				fams[parts[1]] = true
			}
			if len(fams) != 1 || !fams[controlID] {
				t.Errorf("%s: tpa. id families = %v, want exactly {%s}", e.ID, fams, controlID)
			}
			sawControl = true
			continue
		}
		if f, ok := scanEntry(full, e); ok && f.ThreatLevel == detect.ThreatLevelDangerous {
			t.Errorf("%s: benign/hard-negative entry hard-flagged by the full bundle: %v", e.ID, f.Signals)
		}
	}
	if !sawControl {
		t.Fatal("control corpus has no malicious entry")
	}
}

// recallOf parses overall_recall and gated_malicious out of the gate's metrics JSON.
func recallOf(t *testing.T, stdout []byte) (recall float64, gated int) {
	t.Helper()
	var m struct {
		OverallRecall  float64 `json:"overall_recall"`
		GatedMalicious int     `json:"gated_malicious"`
	}
	if err := json.Unmarshal(stdout, &m); err != nil {
		t.Fatalf("stdout is not metrics JSON: %v\n%s", err, stdout)
	}
	return m.OverallRecall, m.GatedMalicious
}

// TestBundleControl_RemovalBreachesGate is the non-vacuity proof (FR-016b): the
// control corpus scores recall 1.0 against the full bundle and 0.0 against the
// bundle built without the control, and the second run exits exactly
// exitGateBreach (a recall breach), never exitConfigError (a load failure).
func TestBundleControl_RemovalBreachesGate(t *testing.T) {
	dir := t.TempDir()
	fullPath := filepath.Join(dir, "full.json")
	if err := os.WriteFile(fullPath, scanner.EmbeddedBundle(), 0o644); err != nil {
		t.Fatal(err)
	}
	reduced, err := tpacorpus.Build(os.DirFS(controlSignaturesFS), tpacorpus.Options{Exclude: []string{controlID}})
	if err != nil {
		t.Fatalf("building the reduced bundle: %v", err)
	}
	// The reduced bundle must still load, so exit 6 is a recall breach and not
	// a load failure.
	if _, _, err := scanner.LoadBundleCheck(reduced); err != nil {
		t.Fatalf("reduced bundle does not load: %v", err)
	}
	if bytes.Contains(reduced, []byte(controlID)) {
		t.Fatalf("reduced bundle still mentions %s", controlID)
	}
	reducedPath := filepath.Join(dir, "reduced.json")
	if err := os.WriteFile(reducedPath, reduced, 0o644); err != nil {
		t.Fatal(err)
	}

	runControl := func(bundle string) (int, float64, int, string) {
		var out, errBuf bytes.Buffer
		code := run([]string{"--corpus", controlCorpusPath, "--bundle", bundle, "--gate", "--min-recall", "0.90", "--max-fp", "0.05"}, &out, &errBuf)
		r, g := recallOf(t, out.Bytes())
		return code, r, g, errBuf.String()
	}

	code, recall, gated, stderr := runControl(fullPath)
	if code != exitOK {
		t.Fatalf("full bundle: exit=%d want %d stderr=%s", code, exitOK, stderr)
	}
	if recall != 1.0 || gated != 1 {
		t.Errorf("full bundle: recall=%v gated_malicious=%d, want 1.0 and 1", recall, gated)
	}

	code, recall, gated, stderr = runControl(reducedPath)
	if code != exitGateBreach {
		t.Fatalf("reduced bundle: exit=%d want exactly %d (gate breach, not config error %d) stderr=%s", code, exitGateBreach, exitConfigError, stderr)
	}
	if recall != 0.0 || gated != 1 {
		t.Errorf("reduced bundle: recall=%v gated_malicious=%d, want 0.0 and 1", recall, gated)
	}
}
