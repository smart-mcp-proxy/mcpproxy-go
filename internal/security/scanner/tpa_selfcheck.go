package scanner

import (
	_ "embed" // selfcheck-corpus.json
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect/checks"
)

// Activation self-check (Spec 087 FR-008, Spec 101 FR-016a(c); tasks B13, B14).
//
// Before a freshly compiled bundle replaces the serving one, it is scored over
// the labeled detect corpus with the SAME decision as the CI gate
// (cmd/scan-eval --gate): malicious recall over the gated categories must be at
// least selfCheckMinRecall and the false-positive rate over the hard-negative
// set at most selfCheckMaxFP. A signature that is well formed, correctly signed
// and newer, but so broad that it would flag benign tools (or so narrow that it
// stopped detecting known attacks), is refused as gate_failure and the
// last-known-good keeps serving.
//
// This is a parity-tested PORT of the gate, not a shared package:
// cmd/scan-eval is `package main` and cannot be imported (tasks Δ17; recorded as
// a deviation from FR-016a(c)). tpa_selfcheck_parity_test.go keeps the two from
// drifting.
//
// DEADLOCK HAZARD: the hook runs while bundleRT.mu is held. Everything here
// builds its own engine from its own check list and never calls BundleStatus,
// defaultBundleCheck, activeBundleSnapshot or ScanToolMetadataVerdict (each of
// which can reach the runtime lock through the lazy embedded install).

// selfCheckMinRecall and selfCheckMaxFP are the CI gate thresholds
// (cmd/scan-eval --min-recall / --max-fp defaults, FR-013).
const (
	selfCheckMinRecall = 0.90
	selfCheckMaxFP     = 0.05
)

// selfCheckTPABundleCheckID is the detect check id of the bundle-backed check.
const selfCheckTPABundleCheckID = "tpa.bundle"

// selfCheckTPACategoryPrefix marks a corpus category whose malicious samples
// the bundle check (and no built-in) is expected to catch.
const selfCheckTPACategoryPrefix = "tpa_"

// selfCheckMaxListedIDs caps the corpus ids named in a rejection detail.
const selfCheckMaxListedIDs = 5

// embeddedSelfCheckCorpus is the compiled-in labeled corpus the activation
// self-check scores a candidate on: a byte copy of
// specs/065-evaluation-foundation/datasets/detect_corpus_v1.json (a subset would
// change the denominators and therefore the decision, tasks Δ18). Whichever of
// Spec 101 US2/US3 merges second refreshes the copy (tasks B14).
//
//go:embed bundled/selfcheck-corpus.json
var embeddedSelfCheckCorpus []byte

// selfCheckCorpus is the test seam that replaces the embedded self-check
// corpus bytes (to feed vacuous or unparsable corpora). nil means the embedded
// corpus.
var selfCheckCorpus func() []byte

// selfCheckCorpusBytes returns the corpus the self-check scores on.
func selfCheckCorpusBytes() []byte {
	if selfCheckCorpus != nil {
		return selfCheckCorpus()
	}
	return embeddedSelfCheckCorpus
}

// selfCheckBuiltins is the built-in detect check set the CI gate runs
// (cmd/scan-eval gateChecks). The soft checks of baselineEngineChecks are
// deliberately absent: the gate measures the HARD (auto-quarantine) tier only.
func selfCheckBuiltins() []detect.Check {
	return []detect.Check{
		&checks.UnicodeHidden{},
		&checks.Shadowing{},
		&checks.PayloadDecoded{},
		&checks.PhraseInjection{},
	}
}

// selfCheckCategoryCheck maps a malicious corpus category to the detect check
// expected to catch it (the gate's categoryCheck). tpa_* categories are mapped
// to the bundle check by prefix in selfCheckCheckID. A category is only gated
// when its check is actually registered in the scored check list.
var selfCheckCategoryCheck = map[string]string{
	"unicode_smuggling":   "unicode.hidden",
	"decoded_payload":     "payload.decoded",
	"shadowing":           "shadowing.cross_server",
	"phrase_injection":    "phrase.injection",
	"capability_mismatch": "capability.mismatch",
}

// selfCheckCheckID returns the detect check id expected to catch a malicious
// category, or "" when the category has no mapping.
func selfCheckCheckID(category string) string {
	if strings.HasPrefix(category, selfCheckTPACategoryPrefix) {
		return selfCheckTPABundleCheckID
	}
	return selfCheckCategoryCheck[category]
}

// selfCheckTool, selfCheckPeer and selfCheckEntry mirror the gate's corpus
// entry projection (cmd/scan-eval gateTool/gatePeer/gateEntry).
type selfCheckTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

type selfCheckPeer struct {
	Server string        `json:"server"`
	Tool   selfCheckTool `json:"tool"`
}

type selfCheckEntry struct {
	ID       string          `json:"id"`
	Label    string          `json:"label"`    // "malicious" | "benign"
	Category string          `json:"category"` // detect taxonomy, tpa_*, benign or hard_negative
	Server   string          `json:"server"`
	Tool     selfCheckTool   `json:"tool"`
	Peers    []selfCheckPeer `json:"peers,omitempty"`
}

type selfCheckCorpusDoc struct {
	Entries []selfCheckEntry `json:"entries"`
}

// selfCheckMetrics is one self-check scoring run.
type selfCheckMetrics struct {
	GatedMalicious int
	GatedDetected  int
	HardNegatives  int
	HardNegFP      int
	// ChecksFailed counts detect checks that panicked while scoring (the sum of
	// the engine's per-entry Coverage.ChecksFailed).
	ChecksFailed int
	Recall       float64
	FPRate       float64
	// Flagged maps a corpus entry id to whether the checks HARD-flagged its tool.
	Flagged map[string]bool
	// HardNegFPIDs and MissedIDs are corpus ids (never candidate text).
	HardNegFPIDs []string
	MissedIDs    []string
}

// selfCheckRatio is n/d with a 0 guard, the same expression as the gate's ratio.
func selfCheckRatio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

func selfCheckView(server string, t selfCheckTool) detect.ToolView {
	return detect.ToolView{
		Server:       server,
		Name:         t.Name,
		Description:  t.Description,
		InputSchema:  t.InputSchema,
		OutputSchema: t.OutputSchema,
	}
}

// selfCheckEvaluate scores checkList over a corpus exactly like the gate's
// evaluateGateCorpus: every entry is scanned in a RegistryView of its own tool
// plus its declared peers, and counts as flagged only when the engine
// HARD-flagged (ThreatLevelDangerous) that very tool.
func selfCheckEvaluate(corpus []byte, checkList []detect.Check) (selfCheckMetrics, error) {
	var doc selfCheckCorpusDoc
	if err := json.Unmarshal(corpus, &doc); err != nil {
		return selfCheckMetrics{}, fmt.Errorf("self-check corpus is not valid JSON: %w", err)
	}
	if len(doc.Entries) == 0 {
		return selfCheckMetrics{}, errors.New("self-check corpus has no entries")
	}

	registered := make(map[string]struct{}, len(checkList))
	for _, ch := range checkList {
		registered[ch.ID()] = struct{}{}
	}
	gated := func(cat string) bool {
		id := selfCheckCheckID(cat)
		if id == "" {
			return false
		}
		_, ok := registered[id]
		return ok
	}

	engine := detect.NewEngine(detect.Options{Checks: checkList})
	m := selfCheckMetrics{Flagged: make(map[string]bool, len(doc.Entries))}
	for i := range doc.Entries {
		e := &doc.Entries[i]
		views := make([]detect.ToolView, 0, 1+len(e.Peers))
		views = append(views, selfCheckView(e.Server, e.Tool))
		for _, p := range e.Peers {
			views = append(views, selfCheckView(p.Server, p.Tool))
		}
		res := engine.Scan(detect.NewRegistryView(views))
		m.ChecksFailed += res.Coverage.ChecksFailed

		want := e.Server + ":" + e.Tool.Name
		flagged := false
		for _, f := range res.Findings {
			if f.Location == want && f.ThreatLevel == detect.ThreatLevelDangerous {
				flagged = true
				break
			}
		}
		m.Flagged[e.ID] = flagged

		switch e.Label {
		case "malicious":
			if gated(e.Category) {
				m.GatedMalicious++
				if flagged {
					m.GatedDetected++
				} else {
					m.MissedIDs = append(m.MissedIDs, e.ID)
				}
			}
		default: // benign / hard_negative; only hard negatives feed the FP gate (Spec 076 SC-002)
			if e.Category == "hard_negative" {
				m.HardNegatives++
				if flagged {
					m.HardNegFP++
					m.HardNegFPIDs = append(m.HardNegFPIDs, e.ID)
				}
			}
		}
	}
	m.Recall = selfCheckRatio(m.GatedDetected, m.GatedMalicious)
	m.FPRate = selfCheckRatio(m.HardNegFP, m.HardNegatives)
	return m, nil
}

// selfCheckListIDs renders at most selfCheckMaxListedIDs corpus ids.
func selfCheckListIDs(ids []string) string {
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	more := ""
	if len(ids) > selfCheckMaxListedIDs {
		more = fmt.Sprintf(" (+%d more)", len(ids)-selfCheckMaxListedIDs)
		ids = ids[:selfCheckMaxListedIDs]
	}
	return strings.Join(ids, ", ") + more
}

// EnableActivationSelfCheck installs the activation self-check as the pipeline's
// bundleSelfCheck hook. Production wiring (internal/server/server.go) calls it
// once, next to configureTPABundleRuntime; US1 fixtures and clearBundleRuntime
// keep the nil default. It is idempotent and safe to call from concurrent
// server constructions: the hook is read under bundleRT.mu, so it is written
// under it too (Enable is never called from inside the pipeline).
func EnableActivationSelfCheck() {
	bundleRT.mu.Lock()
	bundleSelfCheck = activationSelfCheck
	bundleRT.mu.Unlock()
}

// activationSelfCheck scores a compiled candidate over the embedded self-check
// corpus with the same decision as the CI gate and refuses it on a recall or
// false-positive regression. A non-nil error is reported as gate_failure.
//
// It fails closed: a corpus that cannot be parsed or that cannot support the
// decision (no gated malicious sample, no hard negative), a scoring run whose
// checks panicked, and a nil candidate are all refusals. The detail carries
// counts and corpus ids only, never candidate pattern text.
func activationSelfCheck(check *BundleCheck) error {
	if check == nil {
		return errors.New("activation self-check: no compiled candidate")
	}
	list := append(selfCheckBuiltins(), check)
	m, err := selfCheckEvaluate(selfCheckCorpusBytes(), list)
	if err != nil {
		return fmt.Errorf("activation self-check: corpus unavailable: %w", err)
	}
	if m.ChecksFailed > 0 {
		return fmt.Errorf("activation self-check: %d detect check failure(s) while scoring the candidate; coverage is degraded", m.ChecksFailed)
	}
	if m.GatedMalicious == 0 {
		return errors.New("activation self-check: the corpus has no malicious sample in a gated category (the recall gate would be vacuous)")
	}
	if m.HardNegatives == 0 {
		return errors.New("activation self-check: the corpus has no hard-negative sample (the false-positive gate would be vacuous)")
	}

	var reasons []string
	if m.Recall < selfCheckMinRecall {
		reasons = append(reasons, fmt.Sprintf("recall %d/%d = %.4f < min %.2f (missed corpus ids: %s)",
			m.GatedDetected, m.GatedMalicious, m.Recall, selfCheckMinRecall, selfCheckListIDs(m.MissedIDs)))
	}
	if m.FPRate > selfCheckMaxFP {
		reasons = append(reasons, fmt.Sprintf("false-positive rate %d/%d hard negatives = %.4f > max %.2f (flagged corpus ids: %s)",
			m.HardNegFP, m.HardNegatives, m.FPRate, selfCheckMaxFP, selfCheckListIDs(m.HardNegFPIDs)))
	}
	if len(reasons) > 0 {
		return errors.New("activation self-check: " + strings.Join(reasons, "; "))
	}
	return nil
}
