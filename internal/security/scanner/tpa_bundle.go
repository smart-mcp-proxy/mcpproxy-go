package scanner

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// bundledScannerBundle is the known-good default TPA signature database compiled
// by tpa-db (data/scanner-bundle.json, contract v0.1.0). It is embedded so the
// offline scanner always has a corpus with zero filesystem/network dependency at
// scan time (spec 086 FR-019; the embed + file I/O MUST live outside the
// import-guarded detect package — hence here in scanner).
//
//go:embed bundled/scanner-bundle.json
var bundledScannerBundle []byte

// supportedBundleMajorMinor is the bundle_version major.minor this build knows
// how to run. The loader refuses any other major/minor (fail-closed → keep
// last-known-good) rather than executing stale/forward rules; a differing PATCH
// level is accepted and unknown additive keys are ignored (contract §4).
const supportedBundleMajorMinor = "0.1"

// bundleTargetToolDescription is the only rule target with a ToolView surface in
// the offline tier for v1 (maps to ToolView.Description). Other targets
// (resource_content, server_manifest) are declared not-runnable (FR-007).
const bundleTargetToolDescription = "tool_description"

// bundleEngineRegex is the only offline-runnable engine for v1; structural_diff
// (runtime: stateful) has no prior manifest in RegistryView and is skipped
// (FR-006).
const bundleEngineRegex = "regex"

// rawBundle mirrors the tpa-db scanner-bundle.json top level. Unknown additive
// keys are ignored by encoding/json, which is exactly the forward-compat
// behavior the contract requires.
type rawBundle struct {
	BundleVersion  string `json:"bundle_version"`
	SchemaVersion  string `json:"schema_version"`
	SignatureCount int    `json:"signature_count"`
	// GeneratedAt is an OPTIONAL additive freshness stamp (the v0.1.0 format
	// does not emit one yet). When a corpus carries it, it is surfaced verbatim
	// in BundleInfo so an operator can tell a stale corpus from a fresh export.
	GeneratedAt string    `json:"generated_at"`
	Rules       []rawRule `json:"rules"`
	Skipped     []rawSkip `json:"skipped"`
}

// bundleMeta is the operator-facing header of a bundle, extracted without
// re-compiling any rules.
type bundleMeta struct {
	BundleVersion  string
	SchemaVersion  string
	SignatureCount int
	GeneratedAt    string
}

// bundleMetadata re-reads just the bundle header. It is only called on bytes
// that loadBundleCheck already parsed successfully, so a decode error here is
// impossible in practice and yields an empty header rather than a hard failure.
func bundleMetadata(data []byte) bundleMeta {
	var b rawBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return bundleMeta{}
	}
	return bundleMeta{
		BundleVersion:  b.BundleVersion,
		SchemaVersion:  b.SchemaVersion,
		SignatureCount: b.SignatureCount,
		GeneratedAt:    b.GeneratedAt,
	}
}

// rawRule is one rule object. Only the fields the offline tier consumes are
// modeled; any other key (indicators, flags, severity, type, rule, runtime …)
// is ignored, keeping the loader forward-compatible.
type rawRule struct {
	ID         string  `json:"id"`
	Detector   string  `json:"detector"`
	Engine     string  `json:"engine"`
	Target     string  `json:"target"`
	Pattern    string  `json:"pattern"`
	Category   string  `json:"category"`
	Level      string  `json:"level"`
	Confidence float64 `json:"confidence"`
}

// rawSkip is a bundle-declared skipped detector (LLM/jsonpath tier); it is not
// runnable offline and is counted toward not-runnable coverage.
type rawSkip struct {
	ID       string `json:"id"`
	Detector string `json:"detector"`
	Reason   string `json:"reason"`
}

// compiledRule is a runnable regex rule with its pattern compiled once under
// RE2. Held pre-sorted (the bundle is emitted sorted by (id, detector)) so
// Inspect's output order is deterministic without re-sorting.
type compiledRule struct {
	id         string
	detector   string
	category   string
	level      string
	confidence float64
	re         *regexp.Regexp
	threatType string // derived once from category
}

// bundleLoadStats reports the offline-tier coverage split for a successfully
// loaded bundle. It never carries a partial load — a compile error rejects the
// whole candidate (FR-006/FR-007: not-runnable rules are counted, never treated
// as clean coverage).
type bundleLoadStats struct {
	// Runnable is the count of engine==regex AND target==tool_description rules
	// that compiled and are live in the check.
	Runnable int
	// Skipped is the count of rules[] entries that are NOT runnable offline in
	// v1 — engine==structural_diff or a target without a ToolView surface
	// (resource_content, server_manifest).
	Skipped int
	// Declared is the count of the bundle's own skipped[] detectors (LLM/jsonpath
	// tier), surfaced as un-evaluated coverage, distinct from the not-runnable
	// rules above.
	Declared int
}

// BundleCheck is the bundle-backed detect.Check (spec 086 FR-003). It holds the
// pre-compiled, pre-sorted runnable rules and emits one hard-tier Signal per
// regex hit against a tool's description. Pure, total, and deterministic per the
// detect.Check contract; regexp is allowed in any package so no detect import
// guard is violated.
type BundleCheck struct {
	rules []compiledRule
}

// ID implements detect.Check with a stable identifier.
func (*BundleCheck) ID() string { return "tpa.bundle" }

// Inspect ranges the pre-sorted runnable rules and emits one hard-tier Signal
// for each regex that matches the tool's description. Order follows the bundle's
// (id, detector) sort, so output is byte-stable across calls.
func (c *BundleCheck) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	if tool.Description == "" || len(c.rules) == 0 {
		return nil
	}
	var sigs []detect.Signal
	for i := range c.rules {
		r := &c.rules[i]
		loc := r.re.FindStringIndex(tool.Description)
		if loc == nil {
			continue
		}
		sigs = append(sigs, detect.Signal{
			CheckID:    "tpa." + r.id + "." + r.detector,
			Tier:       detect.TierHard,
			ThreatType: r.threatType,
			Confidence: r.confidence,
			// detect.CapEvidence is the render-safe contract every other check
			// honors: it escapes control / zero-width / bidi runes to a visible
			// \uXXXX form AND caps to MaxEvidenceLen. The bundle's dot-all regexes
			// can match such runes from an attacker-controlled description, so the
			// raw span must never land verbatim in Signal.Evidence.
			Evidence: detect.CapEvidence(tool.Description[loc[0]:loc[1]]),
			Detail:   fmt.Sprintf("bundle rule %s (%s, level=%s) matched tool description", r.id, r.detector, r.level),
		})
	}
	return sigs
}

// bundleCategoryToThreatType maps a bundle rule.category onto the detect threat
// vocabulary. Unknown categories fall back to uncategorized rather than failing
// the load (forward-compat: a new campaign category still produces a finding).
func bundleCategoryToThreatType(category string) string {
	switch category {
	case "rug-pull":
		return detect.ThreatRugPull
	case "prompt-injection", "hidden-instructions":
		return detect.ThreatPromptInjection
	case "tool-shadowing", "cross-server-shadowing":
		return detect.ThreatToolPoisoning
	default:
		return detect.ThreatUncategorized
	}
}

// loadEmbeddedBundleCheck loads the compiled-in default bundle. This is the
// expected happy path at scanner construction.
func loadEmbeddedBundleCheck() (*BundleCheck, bundleLoadStats, error) {
	return loadBundleCheck(bundledScannerBundle)
}

// defaultBundleCheck returns the ACTIVE bundle check — the corpus installed by
// ConfigureBundle (configured path, else the embedded default). It returns nil
// only when no corpus could be loaded at all, in which case callers continue
// scanning WITHOUT bundle-backed checks; a bundle problem must never break the
// scanner (spec 086 FR-005), and the trust_mode:scan gate independently fails
// closed when the bundle is absent (FR-014).
//
// Configuration/logging live in tpa_bundle_source.go so this file stays about
// parsing and matching.
func defaultBundleCheck() *BundleCheck {
	if check, info := snapshotBundle(); check != nil || info.LoadError != "" {
		return check
	}
	// Never configured (e.g. a unit test constructing the engine directly):
	// lazily install the embedded default so behavior is unchanged.
	ConfigureBundle("", zap.NewNop())
	check, _ := snapshotBundle()
	return check
}

// loadBundleCheck parses, version-checks, and compiles a scanner bundle into a
// BundleCheck. It:
//   - refuses an unsupported bundle_version major/minor (do not run stale rules),
//   - compiles every engine==regex rule's pattern once under RE2 and rejects the
//     WHOLE candidate on any compile error (never partially loads),
//   - counts engine!=regex or non-tool_description rules as not-runnable/skipped
//     (never failing the load), and
//   - ignores unknown additive keys (forward-compat).
func loadBundleCheck(data []byte) (*BundleCheck, bundleLoadStats, error) {
	var b rawBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, bundleLoadStats{}, fmt.Errorf("parse scanner bundle: %w", err)
	}

	if err := checkBundleVersion(b.BundleVersion); err != nil {
		return nil, bundleLoadStats{}, err
	}

	var (
		compiled []compiledRule
		stats    bundleLoadStats
	)
	// Bundle-declared skipped detectors (LLM/jsonpath tier) are un-evaluated
	// coverage, tracked separately from not-runnable rules[] entries.
	stats.Declared = len(b.Skipped)

	for i := range b.Rules {
		r := &b.Rules[i]
		// Only engine==regex against target==tool_description runs offline in v1.
		// structural_diff (stateful) and other targets are not-runnable — count,
		// never fail the load.
		if r.Engine != bundleEngineRegex || r.Target != bundleTargetToolDescription {
			stats.Skipped++
			continue
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			// A single un-compilable pattern rejects the whole candidate so we
			// never activate a partially-loaded corpus (FR-001).
			return nil, bundleLoadStats{}, fmt.Errorf("compile rule %s/%s pattern: %w", r.ID, r.Detector, err)
		}
		compiled = append(compiled, compiledRule{
			id:         r.ID,
			detector:   r.Detector,
			category:   r.Category,
			level:      r.Level,
			confidence: r.Confidence,
			re:         re,
			threatType: bundleCategoryToThreatType(r.Category),
		})
	}
	stats.Runnable = len(compiled)

	// A corpus that contributes NO runnable rule is an empty corpus, not a
	// working one. Accepting it produced a non-nil BundleCheck with no rules,
	// which made the scan gate's bundlePresent (and therefore coverageOK) true
	// while zero signatures were actually being matched — a trust_mode:scan
	// server could then be auto-approved on a rug-pulled description with the
	// scanner effectively switched off. Fail the load instead, so the fail-closed
	// fallback in ConfigureBundle keeps the last-known-good corpus live and the
	// reason reaches BundleInfo.LoadError (FR-005/FR-014).
	if stats.Runnable == 0 {
		return nil, bundleLoadStats{}, fmt.Errorf(
			"scanner bundle: no runnable rules (%d rules declared, %d not runnable offline, %d bundle-declared skipped)",
			len(b.Rules), stats.Skipped, stats.Declared)
	}

	return &BundleCheck{rules: compiled}, stats, nil
}

// checkBundleVersion enforces the contract §4 version-compat rule: the loader
// runs a bundle only when its major.minor matches this build's supported
// major.minor. A differing major/minor is refused (fail-closed); a differing
// PATCH is accepted.
func checkBundleVersion(version string) error {
	if version == "" {
		return fmt.Errorf("scanner bundle: missing bundle_version")
	}
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return fmt.Errorf("scanner bundle: malformed bundle_version %q", version)
	}
	majorMinor := parts[0] + "." + parts[1]
	if majorMinor != supportedBundleMajorMinor {
		return fmt.Errorf("scanner bundle: unsupported bundle_version %q (this build supports %s.x)", version, supportedBundleMajorMinor)
	}
	return nil
}
