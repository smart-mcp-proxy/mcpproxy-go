package scanner

import (
	"errors"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// Activation self-check (Spec 087 FR-008, Spec 101 FR-016a(c); tasks B13, B14).
// The real port of the CI gate lands with T343.

// selfCheckMinRecall and selfCheckMaxFP are the CI gate thresholds
// (cmd/scan-eval --min-recall / --max-fp defaults, FR-013).
const (
	selfCheckMinRecall = 0.90
	selfCheckMaxFP     = 0.05
)

// selfCheckTPABundleCheckID is the detect check id of the bundle-backed check.
const selfCheckTPABundleCheckID = "tpa.bundle"

// embeddedSelfCheckCorpus is the compiled-in labeled corpus the activation
// self-check scores a candidate on.
//
// STUB (Spec 101 US3 wave 0 / T340): empty until T343 adds the go:embed.
var embeddedSelfCheckCorpus []byte

// selfCheckCorpus is the test seam that replaces the embedded self-check
// corpus bytes (to feed vacuous or unparsable corpora). nil means the embedded
// corpus.
var selfCheckCorpus func() []byte

// selfCheckCorpusBytes returns the corpus the self-check scores on.
//
// STUB (T340): returns the (empty) embedded bytes.
func selfCheckCorpusBytes() []byte {
	if selfCheckCorpus != nil {
		return selfCheckCorpus()
	}
	return embeddedSelfCheckCorpus
}

// selfCheckBuiltins is the built-in detect check set the CI gate runs.
//
// STUB (T340): empty until T343.
func selfCheckBuiltins() []detect.Check { return nil }

// selfCheckCategoryCheck maps a malicious corpus category to the detect check
// expected to catch it (the explicit pairs; tpa_* categories are prefix-mapped
// by selfCheckCheckID).
//
// STUB (T340): empty until T343.
var selfCheckCategoryCheck = map[string]string{}

// selfCheckCheckID returns the detect check id expected to catch a malicious
// category, or "" when the category has no mapping.
//
// STUB (T340): always "" until T343.
func selfCheckCheckID(category string) string {
	_ = category
	return ""
}

// selfCheckMetrics is one self-check scoring run.
type selfCheckMetrics struct {
	GatedMalicious int
	GatedDetected  int
	HardNegatives  int
	HardNegFP      int
	ChecksFailed   int
	Recall         float64
	FPRate         float64
	// Flagged maps a corpus entry id to whether the checks HARD-flagged its tool.
	Flagged map[string]bool
	// HardNegFPIDs and MissedIDs are corpus ids (never candidate text).
	HardNegFPIDs []string
	MissedIDs    []string
}

// selfCheckEvaluate scores checkList over a corpus.
//
// STUB (T340): not implemented until T343.
func selfCheckEvaluate(corpus []byte, checkList []detect.Check) (selfCheckMetrics, error) {
	_, _ = corpus, checkList
	return selfCheckMetrics{}, errors.New("self-check not implemented")
}

// EnableActivationSelfCheck installs the activation self-check as the pipeline's
// bundleSelfCheck hook. Production wiring (internal/server/server.go) calls it
// once, next to configureTPABundleRuntime; US1 fixtures and clearBundleRuntime
// keep the nil default.
//
// STUB (Spec 101 US3 wave 0): no-op, the hook stays nil, until T343.
func EnableActivationSelfCheck() {}

// activationSelfCheck scores a compiled candidate over the embedded self-check
// corpus with the same decision as the CI gate and refuses it on a recall or
// false-positive regression. A non-nil error is reported as gate_failure.
//
// STUB (Spec 101 US3 wave 0): accepts everything until T343.
func activationSelfCheck(check *BundleCheck) error {
	_ = check
	return nil
}
