package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect/checks"
)

// exitGateBreach is returned when --gate fails its recall/FP thresholds. It is
// distinct from config (4) / write (1) so CI can tell a real regression from a
// tooling error. Any non-zero value fails the CI step (FR-013, SC-006).
const exitGateBreach = 6

// gateTool is the minimal projection of a tool the detect engine needs.
type gateTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

// gatePeer is another server's tool supplied as cross-server context so the
// shadowing check can fire (it only emits when a collision/reference points at a
// DIFFERENT server). Non-shadowing entries leave Peers empty.
type gatePeer struct {
	Server string   `json:"server"`
	Tool   gateTool `json:"tool"`
}

// gateEntry is one labeled sample: a tool, its owning server, optional peers,
// the ground-truth label/category, and redistributable provenance.
type gateEntry struct {
	ID       string `json:"id"`
	Label    string `json:"label"`    // "malicious" | "benign"
	Category string `json:"category"` // detect taxonomy or benign|hard_negative
	// Resembles names the attack class a hard_negative mimics (e.g.
	// "unicode_smuggling"), so a false positive on it counts toward that
	// category's precision/FP (SC-003). Empty for clean-benign entries.
	Resembles  string     `json:"resembles,omitempty"`
	Server     string     `json:"server"`
	Tool       gateTool   `json:"tool"`
	Peers      []gatePeer `json:"peers,omitempty"`
	Provenance struct {
		Source  string `json:"source"`
		License string `json:"license"`
	} `json:"provenance"`
}

// gateCorpus is the Spec-076 detect-engine labeled evaluation corpus.
type gateCorpus struct {
	Version     string      `json:"version"`
	Description string      `json:"description"`
	Entries     []gateEntry `json:"entries"`
}

// categoryCheck maps each malicious category to the detect Check ID expected to
// catch it. A category is only enforced by the gate when its check is actually
// registered (see gateChecks) — so categories whose checks land in a later user
// story are measured and reported but never fail the build prematurely. Add the
// mapping when a new check is registered so the gate begins enforcing it.
var categoryCheck = map[string]string{
	"unicode_smuggling":   "unicode.hidden",
	"decoded_payload":     "payload.decoded",
	"shadowing":           "shadowing.cross_server",
	"phrase_injection":    "phrase.injection",    // Spec 077 US1 — curated hard check
	"capability_mismatch": "capability.mismatch", // US2 (T016) — not yet registered
}

// gateChecks is the canonical set of detect checks the gate runs. It MUST mirror
// the checks registered in the live scanner (internal/security/scanner/
// inprocess.go); when a soft check (US2) or any new check is registered there,
// add it here too so the gate measures the same detector the product ships.
func gateChecks() []detect.Check {
	return []detect.Check{
		&checks.UnicodeHidden{},
		&checks.Shadowing{},
		&checks.PayloadDecoded{},
		&checks.PhraseInjection{}, // Spec 077 US1 — curated hard injection/exfil check
	}
}

// categoryMetric is one category's per-run scorecard (T018: per-category
// recall/precision/FP/F1). Precision and FP are attributed via hard-negatives
// that resemble this category (SC-003); a category with no resembling
// hard-negatives reports zero FP.
type categoryMetric struct {
	Category       string  `json:"category"`
	Gated          bool    `json:"gated"`     // is this category's check registered?
	Malicious      int     `json:"malicious"` // malicious samples in this category
	Detected       int     `json:"detected"`  // malicious samples the engine flagged (TP)
	Recall         float64 `json:"recall"`
	HardNegatives  int     `json:"hard_negatives"`  // resembling hard-negatives
	FalsePositives int     `json:"false_positives"` // resembling hard-negatives flagged (FP)
	FPRate         float64 `json:"fp_rate"`
	Precision      float64 `json:"precision"` // TP / (TP + FP)
	F1             float64 `json:"f1"`
}

// gateMetrics is the full metrics report emitted for the CI log.
type gateMetrics struct {
	Corpus         string           `json:"corpus_version"`
	Checks         []string         `json:"checks"`
	Categories     []categoryMetric `json:"categories"`
	GatedMalicious int              `json:"gated_malicious"`
	GatedDetected  int              `json:"gated_detected"`
	OverallRecall  float64          `json:"overall_recall"`
	// FP rate is gated over the HARD-NEGATIVE set only (Spec 076 SC-002): clean
	// benign entries must not dilute it, or growing the corpus could mask a
	// hard-negative regression. BenignTotal/BenignFalsePositives are reported for
	// transparency (SC-003 expects zero FP across benign + hard-negatives), but
	// only FPRate (hard-negative) feeds the gate decision.
	HardNegatives         int     `json:"hard_negatives"`
	HardNegFalsePositives int     `json:"hard_negative_false_positives"`
	FPRate                float64 `json:"fp_rate"` // hard-neg FP / hard-neg total (SC-002, gated)
	BenignTotal           int     `json:"benign_total"`
	BenignFalsePositives  int     `json:"benign_false_positives"`
	Precision             float64 `json:"precision"`
	F1                    float64 `json:"f1"`
}

// evaluateGateCorpus runs the detect engine over every entry and tallies recall
// (over categories whose checks are registered), the false-positive rate over
// the HARD-NEGATIVE set (Spec 076 SC-002), precision, and F1. Each entry is
// scanned in a RegistryView of its own tool plus its declared peers, so
// shadowing fires deterministically and entries never cross-contaminate.
func evaluateGateCorpus(c *gateCorpus, checkList []detect.Check) gateMetrics {
	engine := detect.NewEngine(detect.Options{Checks: checkList})

	registered := make(map[string]struct{}, len(checkList))
	for _, ch := range checkList {
		registered[ch.ID()] = struct{}{}
	}
	gatedCategory := func(cat string) bool {
		id, ok := categoryCheck[cat]
		if !ok {
			return false
		}
		_, reg := registered[id]
		return reg
	}

	type catTally struct {
		gated              bool
		malicious, flagged int
		hardNeg, hardNegFP int
	}
	cats := map[string]*catTally{}
	order := []string{}
	getCat := func(cat string) *catTally {
		ct := cats[cat]
		if ct == nil {
			ct = &catTally{gated: gatedCategory(cat)}
			cats[cat] = ct
			order = append(order, cat)
		}
		return ct
	}

	var gatedMal, gatedDet, truePos int
	var benignTotal, benignFP, hardNegTotal, hardNegFP int

	for i := range c.Entries {
		e := c.Entries[i]
		flagged := scanEntryFlagged(engine, e)

		switch e.Label {
		case "malicious":
			ct := getCat(e.Category)
			ct.malicious++
			if flagged {
				ct.flagged++
			}
			if ct.gated {
				gatedMal++
				if flagged {
					gatedDet++
					truePos++
				}
			}
		default: // benign / hard_negative
			benignTotal++
			if flagged {
				benignFP++
			}
			// SC-002 gates the FP rate on the hard-negative set specifically;
			// SC-003 attributes each hard-negative FP to the attack class it
			// resembles for the per-category precision/FP.
			if e.Category == "hard_negative" {
				hardNegTotal++
				if flagged {
					hardNegFP++
				}
				if e.Resembles != "" {
					ct := getCat(e.Resembles)
					ct.hardNeg++
					if flagged {
						ct.hardNegFP++
					}
				}
			}
		}
	}

	m := gateMetrics{
		Corpus:                c.Version,
		Checks:                sortedCheckIDs(checkList),
		GatedMalicious:        gatedMal,
		GatedDetected:         gatedDet,
		HardNegatives:         hardNegTotal,
		HardNegFalsePositives: hardNegFP,
		BenignTotal:           benignTotal,
		BenignFalsePositives:  benignFP,
	}
	for _, cat := range order {
		ct := cats[cat]
		recall := ratio(ct.flagged, ct.malicious)
		precision := ratio(ct.flagged, ct.flagged+ct.hardNegFP)
		m.Categories = append(m.Categories, categoryMetric{
			Category:       cat,
			Gated:          ct.gated,
			Malicious:      ct.malicious,
			Detected:       ct.flagged,
			Recall:         recall,
			HardNegatives:  ct.hardNeg,
			FalsePositives: ct.hardNegFP,
			FPRate:         ratio(ct.hardNegFP, ct.hardNeg),
			Precision:      precision,
			F1:             f1(precision, recall),
		})
	}
	m.OverallRecall = ratio(gatedDet, gatedMal)
	m.FPRate = ratio(hardNegFP, hardNegTotal)
	m.Precision = ratio(truePos, truePos+benignFP)
	m.F1 = f1(m.Precision, m.OverallRecall)
	return m
}

// scanEntryFlagged builds the entry's RegistryView (its tool + peers), scans it,
// and reports whether the engine HARD-flagged (auto-quarantine tier) the entry's
// own tool.
//
// The gate measures the auto-quarantine decision, i.e. the HARD tier only. This
// matters since Spec 077 US1 (Codex round-3) made phrase.injection "never fully
// suppress" a matched injection: a phrase quoted or merely described now surfaces
// as a SOFT review finding instead of nothing. Counting any finding would then
// score those benign hard-negatives (a scanner quoting "ignore previous
// instructions") as false positives, even though they are only review-flagged and
// never blocked. Recall is unaffected — every gated category's malicious samples
// are detected at the HARD tier — so the gate keeps measuring exactly the
// blocking behavior the product ships.
func scanEntryFlagged(engine *detect.Engine, e gateEntry) bool {
	views := []detect.ToolView{toGateView(e.Server, e.Tool)}
	for _, p := range e.Peers {
		views = append(views, toGateView(p.Server, p.Tool))
	}
	res := engine.Scan(detect.NewRegistryView(views))
	want := e.Server + ":" + e.Tool.Name
	for _, f := range res.Findings {
		if f.Location == want && f.ThreatLevel == detect.ThreatLevelDangerous {
			return true
		}
	}
	return false
}

func toGateView(server string, t gateTool) detect.ToolView {
	return detect.ToolView{
		Server:       server,
		Name:         t.Name,
		Description:  t.Description,
		InputSchema:  t.InputSchema,
		OutputSchema: t.OutputSchema,
	}
}

// decide applies the gate thresholds. It returns ok=false plus a human-readable
// reason per breached metric.
func (m gateMetrics) decide(minRecall, maxFP float64) (ok bool, reasons []string) {
	if m.OverallRecall < minRecall {
		reasons = append(reasons, fmt.Sprintf("recall %.4f < min-recall %.4f", m.OverallRecall, minRecall))
	}
	if m.FPRate > maxFP {
		reasons = append(reasons, fmt.Sprintf("false-positive rate %.4f > max-fp %.4f", m.FPRate, maxFP))
	}
	return len(reasons) == 0, reasons
}

// runGate evaluates the corpus, prints the metrics JSON, and returns the process
// exit code: exitOK on pass, exitGateBreach on a recall/FP breach.
func runGate(c *gateCorpus, minRecall, maxFP float64, stdout, stderr io.Writer) int {
	m := evaluateGateCorpus(c, gateChecks())

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "error: marshaling gate metrics: %v\n", err)
		return exitWriteError
	}
	fmt.Fprintln(stdout, string(out))

	if m.GatedMalicious == 0 {
		fmt.Fprintln(stderr, "error: no malicious samples in a gated category — the gate would be vacuous")
		return exitConfigError
	}
	if m.HardNegatives == 0 {
		fmt.Fprintln(stderr, "error: no hard-negative samples — the FP gate (SC-002) would be vacuous")
		return exitConfigError
	}

	ok, reasons := m.decide(minRecall, maxFP)
	if !ok {
		for _, r := range reasons {
			fmt.Fprintf(stderr, "GATE FAILED: %s\n", r)
		}
		return exitGateBreach
	}
	fmt.Fprintf(stderr, "GATE PASSED: recall=%.4f (>=%.4f), fp=%.4f (<=%.4f)\n", m.OverallRecall, minRecall, m.FPRate, maxFP)
	return exitOK
}

// loadGateCorpus reads and decodes the detect-engine eval corpus.
func loadGateCorpus(path string) (*gateCorpus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading gate corpus %q: %w", path, err)
	}
	var c gateCorpus
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing gate corpus %q: %w", path, err)
	}
	if len(c.Entries) == 0 {
		return nil, fmt.Errorf("gate corpus %q has no entries", path)
	}
	return &c, nil
}

func sortedCheckIDs(checkList []detect.Check) []string {
	ids := make([]string, 0, len(checkList))
	for _, ch := range checkList {
		ids = append(ids, ch.ID())
	}
	sort.Strings(ids)
	return ids
}

// ratio is n/d with a 0 guard (an empty denominator yields 0, not NaN).
func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

// f1 is the harmonic mean of precision and recall (0 when both are 0).
func f1(precision, recall float64) float64 {
	if precision+recall == 0 {
		return 0
	}
	return 2 * precision * recall / (precision + recall)
}
