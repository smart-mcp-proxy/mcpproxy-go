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

// Review-tier gate (Spec 122 FR-060). The hard gate (--gate) deliberately
// counts the auto-quarantine tier only; the review gate measures the checks
// whose job is to SURFACE a tool for human review. A malicious entry counts as
// detected when the scanned tool's finding lists the category's check id in
// its signals (any tier); a hard negative is a false positive when that same
// check id appears. For the prohibition category the finding must also land
// on the expected tier (soft = warning, hard = dangerous). Every entry
// labeled expect_tier "soft" MUST NOT produce a dangerous verdict: that is the
// "a review warning never becomes a quarantine" contract.

// reviewCategoryCheck maps each review category to the check that owns it.
var reviewCategoryCheck = map[string]string{
	"anti_refusal":          "refusal.directive",
	"padding":               "padding.hidden",
	"composite_exfil":       "exfil.composite",
	"prohibition":           "phrase.injection",
	"reconstruction_limits": "payload.reconstructed",
}

// reviewEntry is a gateEntry plus the expected tier of a malicious sample.
type reviewEntry struct {
	gateEntry
	ExpectTier string `json:"expect_tier,omitempty"` // "soft" | "hard" | "any"
}

type reviewCorpus struct {
	Version     string        `json:"version"`
	Description string        `json:"description"`
	Entries     []reviewEntry `json:"entries"`
}

// reviewChecks is the full baseline set the product ships, so the review gate
// measures the same detector that holds a change under trust_mode: scan.
func reviewChecks() []detect.Check {
	return []detect.Check{
		&checks.UnicodeHidden{},
		&checks.Shadowing{},
		&checks.PayloadDecoded{},
		&checks.PhraseInjection{},
		&checks.DirectiveImperative{},
		&checks.CapabilityMismatch{},
		&checks.EmbeddedSecret{},
		&checks.AntiRefusal{},
		&checks.PaddingHidden{},
		&checks.CompositeExfil{},
		&checks.MarkerReconstruction{},
	}
}

type reviewCategoryMetric struct {
	Category       string   `json:"category"`
	Malicious      int      `json:"malicious"`
	Detected       int      `json:"detected"`
	Recall         float64  `json:"recall"`
	HardNegatives  int      `json:"hard_negatives"`
	FalsePositives int      `json:"false_positives"`
	FPRate         float64  `json:"fp_rate"`
	Missed         []string `json:"missed,omitempty"`
	FalsePositive  []string `json:"false_positive_ids,omitempty"`
}

type reviewMetrics struct {
	Corpus              string                 `json:"corpus_version"`
	Categories          []reviewCategoryMetric `json:"categories"`
	Malicious           int                    `json:"malicious"`
	Detected            int                    `json:"detected"`
	Recall              float64                `json:"recall"`
	HardNegatives       int                    `json:"hard_negatives"`
	FalsePositives      int                    `json:"false_positives"`
	FPRate              float64                `json:"fp_rate"`
	SoftBecameDangerous []string               `json:"soft_became_dangerous,omitempty"`
}

// evaluateReviewCorpus scans every entry and tallies per-category recall and
// hard-negative FP by check id.
func evaluateReviewCorpus(c *reviewCorpus) reviewMetrics {
	engine := detect.NewEngine(detect.Options{Checks: reviewChecks()})
	type tally struct {
		mal, det, hn, fp int
		missed, fps      []string
	}
	cats := map[string]*tally{}
	var order []string
	get := func(cat string) *tally {
		t := cats[cat]
		if t == nil {
			t = &tally{}
			cats[cat] = t
			order = append(order, cat)
		}
		return t
	}
	m := reviewMetrics{Corpus: c.Version}
	for _, e := range c.Entries {
		level, signals := scanReviewEntry(engine, e.gateEntry)
		switch e.Label {
		case "malicious":
			want := reviewCategoryCheck[e.Category]
			t := get(e.Category)
			t.mal++
			m.Malicious++
			hit := want != "" && hasSignal(signals, want)
			switch e.ExpectTier {
			case "soft":
				if level == detect.ThreatLevelDangerous {
					m.SoftBecameDangerous = append(m.SoftBecameDangerous, e.ID)
				}
				hit = hit && level == detect.ThreatLevelWarning
			case "hard":
				hit = hit && level == detect.ThreatLevelDangerous
			}
			if hit {
				t.det++
				m.Detected++
			} else {
				t.missed = append(t.missed, e.ID)
			}
		default:
			if e.Category != "hard_negative" || e.Resembles == "" {
				continue
			}
			want := reviewCategoryCheck[e.Resembles]
			t := get(e.Resembles)
			t.hn++
			m.HardNegatives++
			// The prohibition category measures TIERING of an existing check
			// whose descriptive matches are review findings by design
			// (never-fully-suppress): its false positive is a dangerous
			// verdict, not a review signal.
			falsePositive := want != "" && hasSignal(signals, want)
			if e.Resembles == "prohibition" {
				falsePositive = level == detect.ThreatLevelDangerous
			}
			if falsePositive {
				t.fp++
				m.FalsePositives++
				t.fps = append(t.fps, e.ID)
			}
		}
	}
	sort.Strings(order)
	for _, cat := range order {
		t := cats[cat]
		m.Categories = append(m.Categories, reviewCategoryMetric{
			Category: cat, Malicious: t.mal, Detected: t.det, Recall: ratio(t.det, t.mal),
			HardNegatives: t.hn, FalsePositives: t.fp, FPRate: ratio(t.fp, t.hn),
			Missed: t.missed, FalsePositive: t.fps,
		})
	}
	m.Recall = ratio(m.Detected, m.Malicious)
	m.FPRate = ratio(m.FalsePositives, m.HardNegatives)
	return m
}

// scanReviewEntry returns the threat level and signals of the entry's own
// tool ("" and nil when nothing fired).
func scanReviewEntry(engine *detect.Engine, e gateEntry) (string, []string) {
	views := []detect.ToolView{toGateView(e.Server, e.Tool)}
	for _, p := range e.Peers {
		views = append(views, toGateView(p.Server, p.Tool))
	}
	res := engine.Scan(detect.NewRegistryView(views))
	want := e.Server + ":" + e.Tool.Name
	for _, f := range res.Findings {
		if f.Location == want {
			return f.ThreatLevel, f.Signals
		}
	}
	return "", nil
}

func hasSignal(signals []string, id string) bool {
	for _, s := range signals {
		if s == id {
			return true
		}
	}
	return false
}

// runReviewGate evaluates the review corpus, prints the scorecard and returns
// the exit code: exitOK, or exitGateBreach on a recall/FP breach or a soft
// sample that became dangerous.
func runReviewGate(c *reviewCorpus, minRecall, maxFP float64, stdout, stderr io.Writer) int {
	m := evaluateReviewCorpus(c)
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "error: marshaling review metrics: %v\n", err)
		return exitWriteError
	}
	fmt.Fprintln(stdout, string(out))
	if m.Malicious == 0 || m.HardNegatives == 0 {
		fmt.Fprintln(stderr, "error: the review corpus needs malicious AND hard-negative samples — the gate would be vacuous")
		return exitConfigError
	}
	var reasons []string
	if m.Recall < minRecall {
		reasons = append(reasons, fmt.Sprintf("review recall %.4f < min-review-recall %.4f", m.Recall, minRecall))
	}
	if m.FPRate > maxFP {
		reasons = append(reasons, fmt.Sprintf("review false-positive rate %.4f > max-review-fp %.4f", m.FPRate, maxFP))
	}
	if len(m.SoftBecameDangerous) > 0 {
		reasons = append(reasons, fmt.Sprintf("review-tier samples produced a dangerous verdict: %v", m.SoftBecameDangerous))
	}
	if len(reasons) > 0 {
		for _, r := range reasons {
			fmt.Fprintf(stderr, "REVIEW GATE FAILED: %s\n", r)
		}
		return exitGateBreach
	}
	fmt.Fprintf(stderr, "REVIEW GATE PASSED: recall=%.4f (>=%.4f), fp=%.4f (<=%.4f)\n", m.Recall, minRecall, m.FPRate, maxFP)
	return exitOK
}

func loadReviewCorpus(path string) (*reviewCorpus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading review corpus %q: %w", path, err)
	}
	var c reviewCorpus
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing review corpus %q: %w", path, err)
	}
	if len(c.Entries) == 0 {
		return nil, fmt.Errorf("review corpus %q has no entries", path)
	}
	return &c, nil
}
