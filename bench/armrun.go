// armrun.go — the encoding-arm runner (Spec 083 US2, T024): orchestrates arms
// over one corpus and assembles the ArmResult report rows (FR-005/006/007/
// 008/009/020).
//
// Per arm it measures: total tokens over the amortized whole-corpus listing
// (EncodeListing, so shared preambles/dictionaries are paid once — contract
// rule 6), per-tool mean and p95 from EncodeTool (comparable across arms),
// savings % against the baseline_json arm's listing total (the single
// denominator, research D7b), per-tool skip counting with capped examples
// (FR-009), the heaviest-tools top-N (FR-020), and — for index-altering arms
// when a golden set is supplied — retrieval quality through the production
// index funnel (armindex.go, FR-008).
//
// Determinism (FR-010): results follow the caller's arm order, per-tool
// iteration is corpus order, skip examples are the first failures in corpus
// order, and heaviest-tools ties break on tool_id — no map iteration anywhere.
package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// EncodingArm is the bench-side mirror of the arms.Arm contract
// (specs/083-discovery-profiler/contracts/arm-interface.md). It is declared
// structurally here because bench cannot import bench/arms (arms imports
// bench); every arms.Arm value satisfies it as-is.
type EncodingArm interface {
	Name() string
	IndexAltering() bool
	LowerBound() bool
	EncodeTool(t Tool) (string, error)
	EncodeListing(ts []Tool) (string, error)
	EncodeIndexMetadata(t Tool) (config.ToolMetadata, error)
}

// baselineArmName is the registry key of the mandatory baseline arm — the
// savings denominator every RunArms call must include.
const baselineArmName = "baseline_json"

// DefaultHeaviestN is the FR-020 default size of the heaviest-tools list.
const DefaultHeaviestN = 10

// maxSkipExamples caps the per-arm skip-example list (FR-009 asks for
// examples, not the full failure inventory); the examples kept are the FIRST
// failures in corpus order, so the list is deterministic.
const maxSkipExamples = 5

// payloadClassListing marks tool-listing arm rows (FR-007; toon_results rows
// carry "results" and are assembled by their fixture-driven runner, not here).
const payloadClassListing = "listing"

// scoredMetricNote documents the gain formula of a scored quality block
// (FR-012), mirroring the definitions in metrics.go.
const scoredMetricNote = "recall/MRR/MAP use binary relevance (relevance>=1); nDCG@10 uses linear graded gain relevance/log2(rank+1)"

// unlabeledMetricNote explains a quality block with no numbers: the arm is
// index-altering but the corpus carries no relevance labels (FR-011).
const unlabeledMetricNote = "corpus has no relevance labels; retrieval quality is not scorable for this index-altering arm"

// ArmRunOptions configures one RunArms invocation.
type ArmRunOptions struct {
	// CorpusID identifies the corpus in report rows (e.g. "corpus_v2@<sha>");
	// empty defaults to the corpus's own Version.
	CorpusID string
	// Golden enables retrieval-quality scoring for index-altering arms. nil
	// means the corpus has no relevance labels: index-altering rows then carry
	// a quality block with only an explanatory MetricNote (FR-008/011).
	Golden *GoldenSet
	// HeaviestN sizes the heaviest-tools list; <=0 defaults to DefaultHeaviestN.
	HeaviestN int
	// IndexDir is the scratch parent directory for per-arm retrieval indexes.
	// Empty creates (and removes) a temp directory per run.
	IndexDir string
}

// RunArms measures every given arm on the corpus and returns one ArmResult
// per arm, in caller order. The baseline_json arm MUST be among armsToRun —
// its listing total is the savings denominator (research D7b). Arms whose
// runtime is absent never reach this function: the harness records them via
// SkippedArmResult after registry resolution fails (contract rule 5).
func RunArms(tk *Tokenizer, corpus *Corpus, armsToRun []EncodingArm, opts ArmRunOptions) ([]ArmResult, error) {
	if corpus == nil || len(corpus.Tools) == 0 {
		return nil, fmt.Errorf("run arms: corpus is empty")
	}
	if opts.CorpusID == "" {
		opts.CorpusID = corpus.Version
	}
	if opts.HeaviestN <= 0 {
		opts.HeaviestN = DefaultHeaviestN
	}

	baselineIdx := -1
	for i, a := range armsToRun {
		if a.Name() == baselineArmName {
			baselineIdx = i
			break
		}
	}
	if baselineIdx < 0 {
		return nil, fmt.Errorf("run arms: the %s arm is required (savings denominator, FR-005)", baselineArmName)
	}

	indexDir := opts.IndexDir
	if indexDir == "" {
		dir, err := os.MkdirTemp("", "bench-armindex-")
		if err != nil {
			return nil, fmt.Errorf("run arms: create scratch index dir: %w", err)
		}
		defer os.RemoveAll(dir)
		indexDir = dir
	}

	// Baseline first: its listing total is every other arm's denominator.
	baseResult, err := runOneArm(tk, corpus, armsToRun[baselineIdx], opts, indexDir)
	if err != nil {
		return nil, err
	}
	baseTotal := baseResult.TotalTokens

	results := make([]ArmResult, len(armsToRun))
	for i, a := range armsToRun {
		var r *ArmResult
		if i == baselineIdx {
			r = baseResult
		} else {
			r, err = runOneArm(tk, corpus, a, opts, indexDir)
			if err != nil {
				return nil, err
			}
		}
		if baseTotal > 0 {
			r.SavingsVsBaselinePct = (1 - float64(r.TotalTokens)/float64(baseTotal)) * 100
		}
		results[i] = *r
	}
	return results, nil
}

// runOneArm measures a single arm: per-tool encodings (skip-counted), the
// amortized listing total, heaviest tools, and quality for index-altering
// arms. SavingsVsBaselinePct is filled by the caller once the baseline total
// is known.
func runOneArm(tk *Tokenizer, corpus *Corpus, arm EncodingArm, opts ArmRunOptions, indexDir string) (*ArmResult, error) {
	r := &ArmResult{
		Arm:           arm.Name(),
		CorpusID:      opts.CorpusID,
		LowerBound:    arm.LowerBound(),
		IndexAltering: arm.IndexAltering(),
		PayloadClass:  payloadClassListing,
	}

	// Per-tool pass: encode each tool, counting failures as skips (FR-009).
	encodable := make([]Tool, 0, len(corpus.Tools))
	perTool := make([]ToolTokenEntry, 0, len(corpus.Tools))
	skip := func(toolID string, err error) {
		r.SkippedTools++
		if len(r.SkipExamples) < maxSkipExamples {
			r.SkipExamples = append(r.SkipExamples, SkipExample{ToolID: toolID, Error: err.Error()})
		}
	}
	sum := 0
	for _, tl := range corpus.Tools {
		enc, err := arm.EncodeTool(tl)
		if err != nil {
			skip(tl.ToolID, err)
			continue
		}
		n := tk.Count(enc)
		perTool = append(perTool, ToolTokenEntry{ToolID: tl.ToolID, Tokens: n})
		encodable = append(encodable, tl)
		sum += n
	}

	// Amortized whole-listing total over the encodable tools (contract rule 6).
	if len(encodable) > 0 {
		listing, err := arm.EncodeListing(encodable)
		if err != nil {
			// Every tool encoded individually, so a listing failure is an
			// arm-level infrastructure fault, not a per-tool skip.
			return nil, fmt.Errorf("arm %s: encode listing over %d tools: %w", arm.Name(), len(encodable), err)
		}
		r.TotalTokens = tk.Count(listing)
		r.MeanTokens = float64(sum) / float64(len(perTool))
		tokens := make([]int, len(perTool))
		for i, e := range perTool {
			tokens[i] = e.Tokens
		}
		sort.Ints(tokens)
		r.P95Tokens = percentileInt(tokens, 95)
		r.HeaviestTools = heaviestTools(perTool, opts.HeaviestN)
	}

	if arm.IndexAltering() {
		q, err := scoreArmQuality(arm, encodable, opts.Golden, indexDir, skip)
		if err != nil {
			return nil, err
		}
		r.Quality = q
	}
	return r, nil
}

// heaviestTools returns the top-n entries by token count, descending, with
// ties broken by tool_id ascending (deterministic, FR-010).
func heaviestTools(perTool []ToolTokenEntry, n int) []ToolTokenEntry {
	sorted := make([]ToolTokenEntry, len(perTool))
	copy(sorted, perTool)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Tokens != sorted[j].Tokens {
			return sorted[i].Tokens > sorted[j].Tokens
		}
		return sorted[i].ToolID < sorted[j].ToolID
	})
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

// scoreArmQuality attaches retrieval quality to an index-altering arm
// (FR-008): the arm's EncodeIndexMetadata output over the encodable tools is
// indexed through the production funnel (BuildArmIndex) and scored against
// the golden set. Without a golden set the block carries only the explanatory
// note. A tool whose metadata mapping fails is counted as a skip via skipFn
// (it encoded, but cannot be represented in this arm's index).
func scoreArmQuality(arm EncodingArm, encodable []Tool, golden *GoldenSet, indexDir string, skipFn func(string, error)) (*RetrievalScore, error) {
	if golden == nil {
		return &RetrievalScore{MetricNote: unlabeledMetricNote}, nil
	}

	metas := make([]*config.ToolMetadata, 0, len(encodable))
	for _, tl := range encodable {
		meta, err := arm.EncodeIndexMetadata(tl)
		if err != nil {
			skipFn(tl.ToolID, fmt.Errorf("index metadata: %w", err))
			continue
		}
		metas = append(metas, &meta)
	}
	if len(metas) == 0 {
		return nil, fmt.Errorf("arm %s: no tool produced index metadata; cannot score retrieval quality", arm.Name())
	}

	dir := filepath.Join(indexDir, arm.Name())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("arm %s: create index dir %q: %w", arm.Name(), dir, err)
	}
	ai, err := BuildArmIndex(dir, metas)
	if err != nil {
		return nil, fmt.Errorf("arm %s: %w", arm.Name(), err)
	}
	defer ai.Close()

	metrics, err := ScoreRetrieval(golden, ai.SearchFunc(), recallCutoffs)
	if err != nil {
		return nil, fmt.Errorf("arm %s: score retrieval: %w", arm.Name(), err)
	}
	score := MapRetrievalMetrics(metrics)
	score.MetricNote = scoredMetricNote
	return score, nil
}

// SkippedArmResult is the arm-level skip row (FR-006, contract rule 5): the
// arm's external runtime is absent, so no tool was processed and only the
// identity + reason fields are meaningful.
func SkippedArmResult(armName, corpusID, reason string) ArmResult {
	return ArmResult{
		Arm:        armName,
		CorpusID:   corpusID,
		Skipped:    true,
		SkipReason: reason,
	}
}

// degenerateMinDescriptionRunes is the FR-020 short-description threshold:
// descriptions shorter than this many characters are a known
// retrieval-failure class.
const degenerateMinDescriptionRunes = 20

// DefaultStubPatterns returns the FR-020 default stub-description pattern
// list as a fresh slice (callers may append their own patterns).
func DefaultStubPatterns() []string {
	return []string{`^Proxy for `}
}

// CountDegenerateDescriptions counts corpus tools whose descriptions trip any
// FR-020 rule — empty/whitespace-only, shorter than 20 characters, or
// matching a stub pattern — each tool counted once, with the applied rule
// list echoed for reproducibility. An invalid pattern is an explicit error.
func CountDegenerateDescriptions(tools []Tool, stubPatterns []string) (*DegenerateDescriptions, error) {
	compiled := make([]*regexp.Regexp, 0, len(stubPatterns))
	for _, p := range stubPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid stub pattern %q: %w", p, err)
		}
		compiled = append(compiled, re)
	}

	dd := &DegenerateDescriptions{
		Rules: []string{
			"description empty or whitespace-only",
			fmt.Sprintf("description shorter than %d characters", degenerateMinDescriptionRunes),
		},
	}
	for _, p := range stubPatterns {
		dd.Rules = append(dd.Rules, "description matches stub pattern "+p)
	}

	for _, tl := range tools {
		if isDegenerateDescription(tl.Description, compiled) {
			dd.Count++
		}
	}
	return dd, nil
}

// isDegenerateDescription reports whether a description trips any FR-020 rule.
func isDegenerateDescription(desc string, stubs []*regexp.Regexp) bool {
	if strings.TrimSpace(desc) == "" {
		return true
	}
	if utf8.RuneCountInString(desc) < degenerateMinDescriptionRunes {
		return true
	}
	for _, re := range stubs {
		if re.MatchString(desc) {
			return true
		}
	}
	return false
}
