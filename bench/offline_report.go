// offline_report.go — assembly of the versioned v2 report for offline
// profiler runs (Spec 083, research D12): encoding-arm rows over one or more
// frozen corpora, corpus descriptors with license/attribution provenance, and
// provenance labels for every headline number (SC-005). The same assembly
// path serves the bench CLI and the end-to-end report-validation test
// (bench/reportv2_e2e_test.go), so what CI validates is what the CLI ships.
package bench

import (
	"fmt"
	"time"
)

// TokenizerCaveatText is the mandatory accuracy caveat rendered wherever
// absolute token numbers appear (research D11, SC-005).
const TokenizerCaveatText = "Token counts use the tiktoken cl100k_base encoding as a reproducible, model-agnostic, offline estimator; it can underestimate other tokenizers (e.g. Claude's) by up to ~60%, so absolute numbers are estimates — RELATIVE savings between arms and modes are stable across vocabularies."

// GeneratedAtNow returns the RFC 3339 UTC timestamp the CLI stamps into
// GeneratedAt. It lives at the CLI boundary on purpose: no library encoder
// below this call reads the wall clock (FR-010), and tests pass fixed values.
func GeneratedAtNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// OfflineSection is one corpus measured by a set of encoding arms (or, with a
// nil Corpus, a container for pre-computed rows such as the fixture-driven
// toon_results measurement).
type OfflineSection struct {
	// Corpus is the frozen tool corpus to run Arms over; nil skips the arm
	// runner and keeps only Descriptor + SkippedArms + ExtraArmRows.
	Corpus *Corpus
	// Descriptor identifies the corpus in the report (FR-011/012/013).
	// ToolCount defaults to len(Corpus.Tools) and DegenerateDescriptions is
	// computed with the FR-020 default rules when unset.
	Descriptor CorpusDescriptor
	// Golden enables retrieval-quality scoring for index-altering arms; nil
	// means the corpus has no relevance labels (FR-011 explicit absence).
	Golden *GoldenSet
	// Arms are the resolved encoding arms to measure (must include
	// baseline_json when non-empty — the savings denominator).
	Arms []EncodingArm
	// SkippedArms are arm-level skip rows for arms whose runtime is absent
	// (contract rule 5), recorded with the section's corpus ID.
	SkippedArms []ArmResult
	// ExtraArmRows are pre-computed rows appended verbatim (e.g. the
	// toon_results fixture rows, which do not run over a tool corpus).
	ExtraArmRows []ArmResult
}

// InHouseProxyMenuTokens is the in-house count of the proxy's retrieve_tools
// menu surface (management tools WITH schemas) — the same surface LAP lints,
// used as the divergence comparison base (FR-016) in offline runs where no
// live menu was measured.
func InHouseProxyMenuTokens(tk *Tokenizer) int {
	return tk.countToolsWithSchema(ProxyToolsForMode(ModeRetrieveTools))
}

// BuildOfflineReportV2 measures every section and assembles the v2 envelope.
// Sections are processed in caller order; within a section rows follow the
// caller's arm order, then skip rows, then extra rows — fully deterministic
// (FR-010). Lap/Subset merging stays with the caller (CLI flags own those).
func BuildOfflineReportV2(tk *Tokenizer, generatedAt string, sections []OfflineSection) (*ReportV2, error) {
	if len(sections) == 0 {
		return nil, fmt.Errorf("build offline report: no sections")
	}

	report := &ReportV2{
		ReportVersion: ReportVersion2,
		GeneratedAt:   generatedAt,
		Tokenizer:     TokenizerInfo{Name: tk.encoding, Caveat: TokenizerCaveatText},
		Corpora:       []CorpusDescriptor{},
		Arms:          []ArmResult{},
		Provenance: map[string]string{
			"arms":                    ProvenanceMeasured,
			"corpora":                 ProvenanceMeasured,
			"retrieval_quality":       ProvenanceMeasured,
			"savings_vs_baseline_pct": ProvenanceComputed,
		},
	}

	for i := range sections {
		sec := &sections[i]
		desc := sec.Descriptor
		if sec.Corpus != nil {
			if desc.ToolCount == 0 {
				desc.ToolCount = len(sec.Corpus.Tools)
			}
			if desc.DegenerateDescriptions == nil {
				dd, err := CountDegenerateDescriptions(sec.Corpus.Tools, DefaultStubPatterns())
				if err != nil {
					return nil, fmt.Errorf("section %q: %w", desc.ID, err)
				}
				desc.DegenerateDescriptions = dd
			}
			if len(sec.Arms) > 0 {
				rows, err := RunArms(tk, sec.Corpus, sec.Arms, ArmRunOptions{
					CorpusID: desc.ID,
					Golden:   sec.Golden,
				})
				if err != nil {
					return nil, fmt.Errorf("section %q: %w", desc.ID, err)
				}
				report.Arms = append(report.Arms, rows...)
			}
		}
		report.Corpora = append(report.Corpora, desc)
		report.Arms = append(report.Arms, sec.SkippedArms...)
		report.Arms = append(report.Arms, sec.ExtraArmRows...)
	}
	return report, nil
}
