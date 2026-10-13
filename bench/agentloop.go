// agentloop.go — Spec 103 US2: the LIVE agent-loop driver. This is where the
// feature's actual claim — tokens per *completed* task — becomes measurable.
//
// # Why this file exists at all
//
// Every other measurement in this harness prices a MENU: how many tokens the
// tool definitions cost an agent before it does any work. That number is real
// and it is reproducible, but it is not the number a user cares about. A mode
// that halves the menu and doubles the number of failed attempts costs more,
// not less. Only a live loop can tell the difference, because the difference
// is made of decisions a model takes — which call it attempts, whether the
// first attempt was right, whether it finished. A recording cannot answer any
// of that (see the Replay Boundary in the spec), which is exactly why US1's
// replay and this file are separate stories with separate accounting.
//
// # Why it is parameterised over function types
//
// A live run costs real money and needs a pinned model, a patched suite and
// credentials. If the arithmetic that decides what gets PUBLISHED could only
// be exercised by such a run, it would never be exercised: the tests would be
// skipped in CI, the classification rules would drift, and the first time
// anybody checked the numbers would be the day they were published.
//
// So the driver takes RunTaskFunc and UsageFunc (the bench/flipgate.go
// precedent, where RetrieveToolsFunc makes the flip-gate maths testable with
// no proxy running). Every rule below — the binding Definitions, the FR-021
// headline bar, the FR-019 regression flag, the never-sum-across-sources
// refusal — is decided in pure functions over records, and the production
// caller supplies real records instead of fakes. Nothing about the arithmetic
// changes between the two.
//
// # The classification is deliberately source-blind
//
// ClassifyIntents sees an attempt sequence and nothing else — not the cell,
// not the endpoint, not whether mcpproxy was in the path. If the baseline arm
// and the proxy arms were classified by different rules, the comparison would
// be biased toward whichever surface reports failure more legibly, and that
// bias would be invisible in the published figure. One rule, both arms.
//
// # What this file will NOT do
//
// It will not sum a provider-reported figure with a tokenizer-counted one. It
// will not publish a percentage without a fleet shape. It will not treat a
// missing completion verdict as a failure, a missing cache-read count as a
// zero, or a single run as a headline. Each of those would understate cost or
// overstate success in this project's favour, which is the failure class the
// whole feature exists to prevent.
package bench

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// MinHeadlineRuns is FR-021's bar: a model-dependent figure is an average over
// at least four runs together with a measure of consistency. A single run of a
// stochastic loop is an anecdote, and the difference between two modes is
// routinely smaller than the run-to-run variance of one of them.
const MinHeadlineRuns = 4

// DefaultCompletionRegressionThresholdPct is FR-019's "stated threshold", in
// PERCENTAGE POINTS of completion rate below the baseline arm. It is stated as
// a named constant rather than buried in a comparison precisely because FR-019
// requires the threshold to be stated: a reader must be able to see what bar a
// mode had to clear before its cheapness was called a saving.
const DefaultCompletionRegressionThresholdPct = 5.0

// Completion verdicts. TASK COMPLETION is the suite's own pass verdict and is
// NEVER inferred. Where a suite emits no verdict the unit is reported as
// CompletionNoSignal and excluded from completion-dependent figures — not
// counted as a success (which would flatter us) and not counted as a failure
// (which would flatter a competing mode). Silence is not evidence either way.
const (
	CompletionPass     = "pass"
	CompletionFail     = "fail"
	CompletionNoSignal = "no-signal"
)

// Attempt outcomes.
//
// The distinction that carries weight is AttemptOutcomeInfrastructure versus
// AttemptOutcomeError. A transport failure measures the NETWORK, not the mode:
// counting it against a cell would mean a flaky hop during one arm's run shows
// up as that mode confusing the agent. So transport failures are counted on
// their own axis and leave the intent indeterminate rather than scoring it.
//
// AttemptOutcomeUnknown exists because a trajectory can end mid-call. An
// unknown outcome is never silently read as success; it makes its intent
// indeterminate, and the record that carries it is partial.
const (
	AttemptOutcomeOK             = "ok"
	AttemptOutcomeError          = "error"
	AttemptOutcomeInfrastructure = "infrastructure"
	AttemptOutcomeUnknown        = "unknown"
)

// ErrAgentLoopBaselineMissing is the FR-020 refusal: no baseline arm, no
// denominator, therefore no publishable percentage. It is an error rather than
// a degraded run because there is nothing to degrade TO — a savings figure
// measured against nothing is not a smaller figure, it is a fabricated one.
var ErrAgentLoopBaselineMissing = errors.New("agent loop requires the FR-020 baseline arm")

// ErrBaselineNotBypassing guards the substance of FR-020 rather than its name.
// The baseline is the same agent doing the same tasks with EVERY upstream tool
// loaded directly, bypassing mcpproxy entirely. A cell called "baseline" that
// points at an mcpproxy endpoint is a sixth proxy cell wearing the
// denominator's name, and every percentage computed against it would be a
// proxy-versus-proxy comparison published as proxy-versus-nothing.
var ErrBaselineNotBypassing = errors.New("the baseline arm must bypass mcpproxy entirely (no endpoint)")

// ErrAgentLoopModelUnpinned refuses provider-reported usage from an unpinned
// model. Usage numbers are only interpretable against the model that produced
// them: the same task under a different model has a different token shape, so
// an unpinned figure is not comparable to any later run and cannot honour
// FR-028's "a later run is comparable to an earlier one".
var ErrAgentLoopModelUnpinned = errors.New("provider-sourced agent-loop figures require a pinned model")

// ErrDriverCacheReadMissing fires when the driver's own usage hook returns no
// cache-read count. The hook exists for exactly one reason — the suite's
// per-task output has no cache-read field, so that axis can only come from the
// driver reading provider responses. A hook that returns nothing for it is a
// wiring bug, and treating it as "unavailable" would hide the bug behind the
// legitimate unavailable path.
var ErrDriverCacheReadMissing = errors.New("driver usage hook returned no cache-read count")

// Attempt is ONE tool call issued toward a given intent — the spec's ATTEMPT,
// not its unit of work.
//
// ArgsFingerprint is what makes the binding Definition's carve-out decidable.
// "A schema-valid call the agent immediately re-issues DIFFERENTLY is not a
// success" needs a notion of "differently", and the arguments are it: a
// re-issue with the same arguments after a transport failure is the network
// being retried, while a re-issue with changed arguments is the agent
// correcting itself, which is the thing a routing/serialization mode can
// actually be blamed for.
type Attempt struct {
	// IntentID groups attempts aimed at the same goal. Attempts sharing an
	// IntentID form one intent; the tool name is the usual value.
	IntentID string `json:"intent_id"`
	// Seq orders attempts within an intent (1-based).
	Seq int `json:"seq"`
	// Outcome is one of the AttemptOutcome* values.
	Outcome string `json:"outcome"`
	// ArgsFingerprint identifies the arguments this attempt was issued with.
	ArgsFingerprint string `json:"args_fingerprint,omitempty"`
}

// IntentOutcome is the classification of one intent's attempt sequence.
type IntentOutcome struct {
	IntentID string `json:"intent_id"`
	// FirstAttemptSuccess implements the binding Definition verbatim: the
	// first attempt returned a non-error result AND was not followed by a
	// corrective retry for the same intent.
	FirstAttemptSuccess bool `json:"first_attempt_success"`
	// Indeterminate marks an intent whose first attempt failed for
	// infrastructure reasons or whose outcome is unknown. Such an intent is
	// EXCLUDED from the first-attempt-success denominator rather than counted
	// as a failure: it measures the network (or a truncated record), not the
	// mode, and scoring it would let network weather move a published figure.
	Indeterminate bool `json:"indeterminate"`
	// Corrective and infrastructure retries are counted separately (FR-011)
	// because only the first says anything about how well the mode serves the
	// agent.
	RetriesCorrective     int `json:"retries_corrective"`
	RetriesInfrastructure int `json:"retries_infrastructure"`
}

// ProviderUsage is provider-reported consumption for one unit of work, with
// the FR-014 axes kept apart.
//
// CacheReadTokens is a POINTER on purpose. The pinned suite's own per-task
// output records input / output / total / reasoning and has NO cache-read
// field, so a suite-derived record genuinely cannot know it — and a 0 there
// would read as "measured, and nothing was served from cache", which in a long
// agent loop is the difference between an honest total and one understated in
// this project's favour. nil means unavailable and is reported as such.
type ProviderUsage struct {
	InputTokens     int  `json:"input_tokens"`
	OutputTokens    int  `json:"output_tokens"`
	CacheReadTokens *int `json:"cache_read_tokens"`
	// ReasoningTokens is recorded because the suite reports it, and is
	// deliberately NOT added into the total: providers report it as a
	// component of output, so adding it would double-count.
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	// Responses counts the provider responses this usage was summed from, so
	// a partially-captured unit is distinguishable from a genuinely cheap one
	// (data-model.md RunRecord rule).
	Responses int `json:"responses,omitempty"`
}

// Total is input + output + cache-read, and reports whether every axis was
// available. A caller that ignores the second return value and publishes the
// first has published an understated cost.
func (u ProviderUsage) Total() (total int, complete bool) {
	total = u.InputTokens + u.OutputTokens
	if u.CacheReadTokens == nil {
		return total, false
	}
	return total + *u.CacheReadTokens, true
}

// UnitRecord is the raw outcome of ONE UNIT OF WORK — one task from the fixed
// task set — under one mode cell on one run. It is the traceable unit behind
// every headline figure (FR-029). A unit is never an individual tool call, no
// matter how many attempts it took.
type UnitRecord struct {
	CellID   string `json:"cell_id"`
	UnitID   string `json:"unit_id"`
	RunIndex int    `json:"run_index"`
	// Completion is the suite's verdict: pass, fail, or no-signal.
	Completion string `json:"completion"`
	// Partial marks a run record that did not finish. Partial records are
	// excluded from every figure and counted separately (FR-032).
	Partial  bool          `json:"partial,omitempty"`
	Attempts []Attempt     `json:"attempts,omitempty"`
	Usage    ProviderUsage `json:"usage"`
	// Source names where THIS record's token figures came from.
	//
	// It lives on the record rather than only on the block because the block's
	// source is a LABEL applied at assembly, while the sum it labels is built
	// here, record by record. Without a per-record source, aggregateCell adds
	// integers whose origin nothing checked and AgentLoopBlockFor then stamps
	// the caller's provider onto the result — so a tokenizer-derived figure
	// mixed into a provider run produces a plausible hybrid wearing a provider
	// label. The never-sum rule has to be checkable at the point of addition,
	// not asserted at the point of naming.
	//
	// Zero value means "unset", which aggregation treats as the block's own
	// source: the common case is one driver producing every record, and
	// requiring every fake in every test to restate it would be noise.
	Source    AccountingSource `json:"source,omitzero"`
	TurnCount int              `json:"turn_count,omitempty"`
}

// RunTaskFunc runs ONE unit of work under ONE mode cell and returns its raw
// record. It is a function type for the reason RetrieveToolsFunc is
// (flipgate.go): a concrete suite in the signature would pin every test to a
// paid run, and an untestable rule is an unenforced rule.
type RunTaskFunc func(cell ModeCell, unitID string, runIndex int) (*UnitRecord, error)

// UsageFunc returns the provider-reported usage the DRIVER captured for one
// unit of work, read off the model responses themselves.
//
// It exists because of a specific gap: the suite's per-task report has no
// cache-read field (research.md §5), so FR-014's third axis cannot come from
// the suite at all. Either the driver supplies it here, or the axis is
// recorded as unavailable — never as zero.
type UsageFunc func(cell ModeCell, unitID string, runIndex int) (ProviderUsage, error)

// AgentLoopDriver is the pair of seams a live run plugs into.
type AgentLoopDriver struct {
	// RunTask is mandatory.
	RunTask RunTaskFunc
	// CaptureUsage is OPTIONAL. When nil, records keep the usage RunTask
	// returned — which, for a suite-derived record, has no cache-read count,
	// and the resulting cells are marked accordingly rather than zero-filled.
	CaptureUsage UsageFunc
}

// AgentLoopOptions configures one live measurement.
type AgentLoopOptions struct {
	// Suite and SuiteVersion pin the task suite (FR-028). SuiteVersion should
	// be the commit SHA the run was executed at.
	Suite        string
	SuiteVersion string
	// Provider and Model name the accounting source. Both are mandatory: see
	// ErrAgentLoopModelUnpinned.
	Provider string
	Model    string
	// FleetShape is the tool set every percentage from this run holds for
	// (IC-004). A percentage without it does not get published.
	FleetShape FleetShape
	// Cells is the matrix under measurement. It MUST include the baseline
	// cell — that is the denominator, not an optional extra row.
	Cells []ModeCell
	// Units is the fixed task set, run identically under every cell.
	Units []string
	// Runs is k, the number of repetitions per cell (FR-021 wants >= 4).
	Runs int
	// CompletionRegressionThresholdPct overrides the stated FR-019 threshold.
	// Zero means DefaultCompletionRegressionThresholdPct.
	CompletionRegressionThresholdPct float64
}

func (o AgentLoopOptions) regressionThresholdPct() float64 {
	if o.CompletionRegressionThresholdPct > 0 {
		return o.CompletionRegressionThresholdPct
	}
	return DefaultCompletionRegressionThresholdPct
}

// SourcedFigure is one value together with the accounting source that produced
// it. The pairing is the whole point: a bare float64 cannot be checked against
// the never-sum rule, and by the time figures are floats in a slice the source
// is exactly the information that has been lost.
type SourcedFigure struct {
	Value  float64
	Source AccountingSource
}

// AggregateResult is an aggregate that either has a value or has a reason it
// does not. It mirrors the harness's existing withhold-rather-than-compute
// pattern (AuthoritativeHeadline / withholdHeadline in live_report.go): when
// the inputs cannot honestly be combined, the number is WITHHELD with a stated
// reason rather than computed and quietly caveated somewhere else.
type AggregateResult struct {
	Value          float64          `json:"value"`
	Source         AccountingSource `json:"source"`
	Withheld       bool             `json:"withheld,omitempty"`
	WithheldReason string           `json:"withheld_reason,omitempty"`
}

// SavingVerdict is a per-cell saving against the baseline arm, or the stated
// reason there is none to publish. FleetShape travels with it because a
// percentage without its fleet shape does not appear (IC-004/SC-005): the
// figure moves with fleet size, so quoting it bare is quoting it at an
// unstated size.
type SavingVerdict struct {
	CellID         string     `json:"cell_id"`
	SavingPct      float64    `json:"saving_pct"`
	Regression     bool       `json:"regression,omitempty"`
	Withheld       bool       `json:"withheld,omitempty"`
	WithheldReason string     `json:"withheld_reason,omitempty"`
	FleetShape     FleetShape `json:"fleet_shape"`
}

// AgentLoopAccountingSource builds the block's accounting source (T043).
//
// A provider-sourced block names the provider AND the pinned model, because
// the two together are what makes a usage number mean anything. This is the
// same refusal shape as ErrStubToolSchemas in mcptools.go: the guard returns
// an error rather than a plausible-looking default, so an unpinned run cannot
// produce a report that merely looks comparable to a pinned one.
func AgentLoopAccountingSource(provider, model string) (AccountingSource, error) {
	if strings.TrimSpace(provider) == "" {
		return AccountingSource{}, errors.New("agent loop: a provider must be named for provider-reported usage")
	}
	if strings.TrimSpace(model) == "" {
		return AccountingSource{}, fmt.Errorf("agent loop: provider %q: %w", provider, ErrAgentLoopModelUnpinned)
	}
	return AccountingSource{
		Kind:     AccountingKindProvider,
		Identity: provider,
		Model:    model,
	}, nil
}

// AggregateTokenFigures sums figures ONLY when every one of them came from the
// same accounting source, and otherwise withholds with a stated reason (T038).
//
// This is data-model.md invariant 2, and it is enforced here rather than left
// to callers because the failure it prevents is silent: tokenizer counts and
// provider-reported usage are both "tokens", both plausible magnitudes, and a
// summed hybrid looks exactly like a real number. There is no downstream check
// that would catch it. Two figures from the same provider but DIFFERENT pinned
// models are also different sources — the same task has a different token
// shape under a different model.
func AggregateTokenFigures(figures []SourcedFigure) AggregateResult {
	if len(figures) == 0 {
		return AggregateResult{
			Withheld:       true,
			WithheldReason: "aggregate withheld: no figures supplied",
		}
	}

	src := figures[0].Source
	if src.IsZero() {
		return AggregateResult{
			Withheld: true,
			WithheldReason: "aggregate withheld: a figure carries no accounting_source, and a figure " +
				"whose source is unknown cannot enter any aggregate (data-model.md: accounting_source is mandatory)",
		}
	}

	total := 0.0
	for _, f := range figures {
		if f.Source.IsZero() {
			return AggregateResult{
				Withheld: true,
				WithheldReason: "aggregate withheld: a figure carries no accounting_source, and a figure " +
					"whose source is unknown cannot enter any aggregate (data-model.md: accounting_source is mandatory)",
			}
		}
		if f.Source != src {
			return AggregateResult{
				Withheld: true,
				WithheldReason: fmt.Sprintf(
					"aggregate WITHHELD: figures come from different accounting sources (%s and %s). "+
						"Deterministic tokenizer counts and provider-reported usage are never summed — a "+
						"cross-source total looks like a measurement and is not one. Report each source's "+
						"figure separately instead.",
					describeSource(src), describeSource(f.Source)),
			}
		}
		total += f.Value
	}
	return AggregateResult{Value: total, Source: src}
}

// describeSource renders an accounting source for a withheld reason. It always
// names the KIND, because the kind is what makes two sources incompatible.
func describeSource(s AccountingSource) string {
	switch {
	case s.IsZero():
		return "unset"
	case s.Model != "":
		return fmt.Sprintf("%s:%s/%s", s.Kind, s.Identity, s.Model)
	default:
		return fmt.Sprintf("%s:%s", s.Kind, s.Identity)
	}
}

// ClassifyIntents implements the spec's binding Definitions over one unit of
// work's attempt sequence, and NOTHING else is in scope for it: no cell, no
// endpoint, no knowledge of whether mcpproxy was in the path. That is the
// point — the same rule must apply to the baseline arm and the proxy arms, or
// the comparison is biased toward whichever carries richer error signal.
//
// The rules, verbatim from the Definitions:
//
//   - ATTEMPT: one tool call issued toward a given intent.
//   - FIRST-ATTEMPT SUCCESS: the first attempt returned a non-error result AND
//     was not followed by a corrective retry for the same intent. A
//     schema-valid call the agent immediately re-issues differently is NOT a
//     success — hence the arguments comparison rather than a status check.
//   - RETRY: a subsequent attempt for the same intent. Transport/infrastructure
//     retries are counted SEPARATELY because they measure the network.
//
// A retry is INFRASTRUCTURE only when the attempt it follows failed for
// transport reasons AND the arguments are unchanged: that is the shape of a
// re-send. Change the arguments and the agent has decided the call was wrong,
// which is a correction no matter what failed first. Anything following a
// non-transport outcome is corrective — including a re-issue after a
// successful call, which means the agent was not satisfied by the result.
func ClassifyIntents(attempts []Attempt) []IntentOutcome {
	if len(attempts) == 0 {
		return nil
	}

	order := make([]string, 0, len(attempts))
	byIntent := make(map[string][]Attempt, len(attempts))
	for _, a := range attempts {
		if _, seen := byIntent[a.IntentID]; !seen {
			order = append(order, a.IntentID)
		}
		byIntent[a.IntentID] = append(byIntent[a.IntentID], a)
	}

	out := make([]IntentOutcome, 0, len(order))
	for _, id := range order {
		group := byIntent[id]
		sort.SliceStable(group, func(i, j int) bool { return group[i].Seq < group[j].Seq })

		outcome := IntentOutcome{IntentID: id}
		first := group[0]
		outcome.Indeterminate = first.Outcome == AttemptOutcomeInfrastructure ||
			first.Outcome == AttemptOutcomeUnknown

		for i := 1; i < len(group); i++ {
			prev := group[i-1]
			sameArgs := group[i].ArgsFingerprint == prev.ArgsFingerprint
			if prev.Outcome == AttemptOutcomeInfrastructure && sameArgs {
				outcome.RetriesInfrastructure++
				continue
			}
			outcome.RetriesCorrective++
		}

		outcome.FirstAttemptSuccess = !outcome.Indeterminate &&
			first.Outcome == AttemptOutcomeOK &&
			outcome.RetriesCorrective == 0
		out = append(out, outcome)
	}
	return out
}

// RunAgentLoop drives the fixed task set across every configured cell — the
// FR-020 baseline arm included — and aggregates the result into the report
// block (T039/T040/T043).
func RunAgentLoop(driver AgentLoopDriver, opts AgentLoopOptions) (*AgentLoopBlock, error) {
	records, err := CollectAgentLoop(driver, opts)
	if err != nil {
		return nil, err
	}
	return AgentLoopBlockFor(records, opts)
}

// CollectAgentLoop runs cells x runs x units through the driver.
//
// The iteration is cell-major and deterministic so a partially completed run
// is a prefix of a full one rather than an arbitrary subset, and so a reader
// comparing two result sets is comparing the same order of work.
func CollectAgentLoop(driver AgentLoopDriver, opts AgentLoopOptions) ([]UnitRecord, error) {
	if driver.RunTask == nil {
		return nil, errors.New("agent loop: driver has no RunTask function")
	}
	if err := validateAgentLoopOptions(opts); err != nil {
		return nil, err
	}
	if len(opts.Units) == 0 {
		return nil, errors.New("agent loop: the task set is empty; there is no unit of work to measure")
	}
	if opts.Runs < 1 {
		return nil, fmt.Errorf("agent loop: runs = %d; at least one run is required (FR-021 wants %d for a headline)",
			opts.Runs, MinHeadlineRuns)
	}

	records := make([]UnitRecord, 0, len(opts.Cells)*opts.Runs*len(opts.Units))
	for _, cell := range opts.Cells {
		for run := 0; run < opts.Runs; run++ {
			for _, unit := range opts.Units {
				rec, err := driver.RunTask(cell, unit, run)
				if err != nil {
					return nil, fmt.Errorf("agent loop: cell %s, unit %s, run %d: %w", cell.ID, unit, run, err)
				}
				if rec == nil {
					return nil, fmt.Errorf("agent loop: cell %s, unit %s, run %d: driver returned no record",
						cell.ID, unit, run)
				}
				// A mislabelled record would land in the wrong cell and
				// silently move a published comparison, so the labels are
				// checked rather than trusted.
				if rec.CellID != cell.ID || rec.UnitID != unit || rec.RunIndex != run {
					return nil, fmt.Errorf(
						"agent loop: driver returned a record labelled %s/%s/run%d for %s/%s/run%d",
						rec.CellID, rec.UnitID, rec.RunIndex, cell.ID, unit, run)
				}

				if driver.CaptureUsage != nil {
					usage, err := driver.CaptureUsage(cell, unit, run)
					if err != nil {
						return nil, fmt.Errorf("agent loop: capture usage for %s/%s/run%d: %w",
							cell.ID, unit, run, err)
					}
					if usage.CacheReadTokens == nil {
						return nil, fmt.Errorf("agent loop: %s/%s/run%d: %w",
							cell.ID, unit, run, ErrDriverCacheReadMissing)
					}
					rec.Usage = usage
				}
				records = append(records, *rec)
			}
		}
	}
	return records, nil
}

// validateAgentLoopOptions enforces the two structural preconditions that
// cannot be recovered from later: the denominator exists, and it is really the
// denominator.
func validateAgentLoopOptions(opts AgentLoopOptions) error {
	var baseline *ModeCell
	for i := range opts.Cells {
		if opts.Cells[i].ID == CellBaseline {
			baseline = &opts.Cells[i]
			break
		}
	}
	if baseline == nil {
		return fmt.Errorf("agent loop: %w — the baseline is the denominator every published "+
			"percentage is measured against (FR-020), not an optional extra row", ErrAgentLoopBaselineMissing)
	}
	if baseline.Endpoint != "" {
		return fmt.Errorf("agent loop: baseline cell carries endpoint %q: %w — the baseline is the same "+
			"agent doing the same tasks with every upstream tool loaded DIRECTLY",
			baseline.Endpoint, ErrBaselineNotBypassing)
	}
	if baseline.RoutingMode != ModeBaseline {
		return fmt.Errorf("agent loop: baseline cell has routing mode %q, want %q: %w",
			baseline.RoutingMode, ModeBaseline, ErrBaselineNotBypassing)
	}
	return nil
}

// cellAggregate accumulates one cell's counted records before they become a
// report row.
type cellAggregate struct {
	// source is the accounting source every summed record agreed on;
	// mixedSources records that at least one disagreed and was excluded.
	source       AccountingSource
	mixedSources bool

	runs                  int
	partialRuns           int
	completionEligible    int
	completed             int
	tokensEligible        int
	inputTokens           int
	outputTokens          int
	cacheReadTokens       int
	cacheReadAvailable    bool
	sawRecord             bool
	determinateIntents    int
	firstAttemptSuccesses int
	retriesCorrective     int
	retriesInfrastructure int
	perRunCostPerTask     []float64
}

// AgentLoopBlockFor aggregates raw records into the agent_loop report block
// (T043).
//
// The arithmetic decisions worth stating plainly, because each one is a place
// a friendlier number was available:
//
//   - A run in which ANY unit is partial is excluded WHOLESALE, not
//     unit-by-unit. A run that died halfway completed a PREFIX of the task set,
//     and averaging that prefix biases the figure toward whichever tasks the
//     suite happens to order first.
//   - Units with no completion signal are excluded from completion-dependent
//     figures on BOTH sides of the ratio — their tokens leave the numerator
//     with them. Keeping their cost while dropping their (absent) verdict
//     would inflate cost per completed task; the reverse would deflate it.
//   - Tokens per completed task is a POOLED ratio (total tokens over total
//     completions), not a mean of per-run ratios, so a run that completed
//     nothing still contributes the money it spent. Spread is measured across
//     the per-run ratios that are defined.
//   - A cell whose token accounting is missing an axis is not a headline. An
//     incomplete total understates cost, and understating cost in this
//     project's favour is the exact failure this feature exists to prevent.
func AgentLoopBlockFor(records []UnitRecord, opts AgentLoopOptions) (*AgentLoopBlock, error) {
	if err := validateAgentLoopOptions(opts); err != nil {
		return nil, err
	}
	source, err := AgentLoopAccountingSource(opts.Provider, opts.Model)
	if err != nil {
		return nil, err
	}

	known := make(map[string]bool, len(opts.Cells))
	for _, c := range opts.Cells {
		known[c.ID] = true
	}
	byCell := make(map[string][]UnitRecord, len(opts.Cells))
	for _, rec := range records {
		if !known[rec.CellID] {
			// A record for a cell outside the matrix would vanish from every
			// figure without a trace, which is the silent accounting the whole
			// report works to avoid.
			return nil, fmt.Errorf("agent loop: record for unknown cell %q (unit %s, run %d)",
				rec.CellID, rec.UnitID, rec.RunIndex)
		}
		if rec.Completion != CompletionPass && rec.Completion != CompletionFail && rec.Completion != CompletionNoSignal {
			return nil, fmt.Errorf("agent loop: record %s/%s/run%d carries completion %q, not one of {%s,%s,%s} — "+
				"a completion verdict is taken from the suite, never inferred",
				rec.CellID, rec.UnitID, rec.RunIndex, rec.Completion,
				CompletionPass, CompletionFail, CompletionNoSignal)
		}
		byCell[rec.CellID] = append(byCell[rec.CellID], rec)
	}

	block := &AgentLoopBlock{
		AccountingSource: source,
		Suite:            opts.Suite,
		SuiteVersion:     opts.SuiteVersion,
		FleetShape:       opts.FleetShape,
	}

	var baselineCompletion float64
	baselineHasCompletion := false
	cells := make([]AgentLoopCell, 0, len(opts.Cells))
	for _, mc := range opts.Cells {
		agg := aggregateCell(byCell[mc.ID])
		row := agg.toRow(mc.ID)
		if mc.ID == CellBaseline && agg.completionEligible > 0 {
			baselineCompletion = row.CompletionRatePct
			baselineHasCompletion = true
		}
		cells = append(cells, row)
	}

	// FR-019: the regression flag is decided against the baseline arm, so it
	// needs a second pass — the baseline row must exist before any cell can be
	// compared to it.
	threshold := opts.regressionThresholdPct()
	for i := range cells {
		if cells[i].CellID == CellBaseline || !baselineHasCompletion {
			continue
		}
		// Gate on the AGGREGATE, not on a second walk of the raw records.
		//
		// The two disagree, and the disagreement fabricates claims. A run is
		// dropped WHOLESALE if any unit in it is partial, so a cell can hold a
		// record with a completion verdict while the aggregate counted nothing
		// — and the raw-record walk then let a CompletionRatePct of 0, which
		// means "nothing was measured", be compared against the baseline and
		// reported as a 100-point regression. That is a quantitative claim
		// about a cell the same row admits is unmeasured.
		//
		// Withheld is checked too: a withheld cell has no figure to compare,
		// and flagging one produced a report whose JSON said regression:true
		// while the HTML rendered "withheld" — the two disagreeing about the
		// same cell.
		if cells[i].Withheld || !cellCompletionMeasured(byCell[cells[i].CellID]) {
			continue
		}
		if baselineCompletion-cells[i].CompletionRatePct > threshold {
			cells[i].Regression = true
		}
	}
	block.CompletionRegressionThresholdPct = threshold
	block.Cells = cells
	return block, nil
}

// cellCompletionMeasured reports whether a cell's AGGREGATE produced a
// completion rate — the same number the regression check compares.
//
// It deliberately re-aggregates rather than inspecting raw records. An earlier
// version walked the records looking for any verdict-carrying unit, which
// disagrees with the aggregate whenever a run is dropped for partiality: the
// record exists, the aggregate counted nothing, and the cell's implicit 0 was
// then reported as a catastrophic regression. Asking the aggregate is the only
// way to be sure the figure being compared is one that exists.
func cellCompletionMeasured(records []UnitRecord) bool {
	return aggregateCell(records).completionEligible > 0
}

// aggregateCell folds one cell's records, run by run.
func aggregateCell(records []UnitRecord) cellAggregate {
	agg := cellAggregate{cacheReadAvailable: true}
	if len(records) == 0 {
		agg.cacheReadAvailable = false
		return agg
	}
	agg.sawRecord = true

	runIndexes := make([]int, 0, len(records))
	byRun := make(map[int][]UnitRecord, len(records))
	for _, r := range records {
		if _, seen := byRun[r.RunIndex]; !seen {
			runIndexes = append(runIndexes, r.RunIndex)
		}
		byRun[r.RunIndex] = append(byRun[r.RunIndex], r)
	}
	sort.Ints(runIndexes)

	for _, idx := range runIndexes {
		run := byRun[idx]
		partial := false
		for _, r := range run {
			if r.Partial {
				partial = true
				break
			}
		}
		if partial {
			agg.partialRuns++
			continue
		}

		runEligible, runCompleted, runTokens := 0, 0, 0
		for _, r := range run {
			// First-attempt success and retries are NOT completion-dependent:
			// they describe how the agent fared per intent, so every
			// non-partial unit contributes regardless of its verdict.
			for _, o := range ClassifyIntents(r.Attempts) {
				agg.retriesCorrective += o.RetriesCorrective
				agg.retriesInfrastructure += o.RetriesInfrastructure
				if o.Indeterminate {
					continue
				}
				agg.determinateIntents++
				if o.FirstAttemptSuccess {
					agg.firstAttemptSuccesses++
				}
			}

			if r.Completion == CompletionNoSignal {
				continue
			}
			// THE NEVER-SUM RULE, enforced where the addition happens.
			// A record whose source differs from the one this aggregate is
			// accumulating cannot be added to it: the result would be a
			// hybrid, and a hybrid labelled with either source is a false
			// claim about both. Recorded and surfaced, never silently dropped
			// and never silently summed.
			if !r.Source.IsZero() {
				if agg.source.IsZero() {
					agg.source = r.Source
				} else if agg.source != r.Source {
					agg.mixedSources = true
					continue
				}
			}
			total, complete := r.Usage.Total()
			if !complete {
				agg.cacheReadAvailable = false
			}
			runEligible++
			runTokens += total
			agg.inputTokens += r.Usage.InputTokens
			agg.outputTokens += r.Usage.OutputTokens
			if r.Usage.CacheReadTokens != nil {
				agg.cacheReadTokens += *r.Usage.CacheReadTokens
			}
			if r.Completion == CompletionPass {
				runCompleted++
			}
		}

		if runEligible == 0 {
			continue
		}
		agg.runs++
		agg.completionEligible += runEligible
		agg.completed += runCompleted
		agg.tokensEligible += runTokens
		if runCompleted > 0 {
			agg.perRunCostPerTask = append(agg.perRunCostPerTask, float64(runTokens)/float64(runCompleted))
		}
	}
	return agg
}

// toRow renders one aggregate as a report row, attaching the withheld reasons
// that keep an incomplete figure from reading as a measured one.
func (a cellAggregate) toRow(cellID string) AgentLoopCell {
	// A cell that mixed accounting sources is WITHHELD outright. The records
	// that disagreed were already excluded from the sums, so what remains is a
	// partial total — and a partial total presented as the cell's cost
	// understates it, which is the direction of error this benchmark exists to
	// prevent. Withhold and say why.
	if a.mixedSources {
		return AgentLoopCell{
			CellID:      cellID,
			Provenance:  ProvenanceMeasured,
			Runs:        a.runs,
			PartialRuns: a.partialRuns,
			Withheld:    true,
			WithheldReason: "withheld: this cell's records carried MORE THAN ONE accounting source. " +
				"Provider-reported usage and tokenizer counts measure different things and are never " +
				"summed, so the records that disagreed were excluded — leaving a partial total that " +
				"would understate the cell's cost if published.",
		}
	}
	row := AgentLoopCell{
		CellID: cellID,
		// Every row here is an attempted MEASUREMENT of a live run; a row that
		// could not be measured says so through Withheld rather than by
		// downgrading its provenance, which would suggest an estimate exists.
		Provenance:            ProvenanceMeasured,
		Runs:                  a.runs,
		PartialRuns:           a.partialRuns,
		RetriesCorrective:     a.retriesCorrective,
		RetriesInfrastructure: a.retriesInfrastructure,
		InputTokens:           a.inputTokens,
		OutputTokens:          a.outputTokens,
		CacheReadTokens:       a.cacheReadTokens,
	}
	if a.determinateIntents > 0 {
		v := pct(a.firstAttemptSuccesses, a.determinateIntents)
		row.FirstAttemptSuccessPct = &v
	}
	if a.completionEligible > 0 {
		row.CompletionRatePct = pct(a.completed, a.completionEligible)
	}
	if a.completed > 0 {
		row.TokensPerCompletedTask = float64(a.tokensEligible) / float64(a.completed)
	}
	// Only when a spread is DEFINED. Fewer than two per-run figures leaves it
	// nil, which renders as "spread undefined" rather than as ±0.0%.
	if len(a.perRunCostPerTask) >= 2 {
		sp := relativeSpreadPct(a.perRunCostPerTask)
		row.SpreadPct = &sp
	}

	var reasons []string
	switch {
	case !a.sawRecord:
		reasons = append(reasons, "no run records were supplied for this cell, so nothing about it was measured")
	case a.runs == 0:
		reasons = append(reasons, "no run completed with a unit carrying a completion verdict; "+
			"every supplied run was partial or verdict-less")
	}
	if a.sawRecord && a.runs > 0 && a.completed == 0 {
		reasons = append(reasons, "no unit of work completed under this cell in any counted run, "+
			"so tokens per completed task is undefined (it is not zero)")
	}
	if !a.cacheReadAvailable && a.sawRecord {
		reasons = append(reasons, "cache-read consumption is UNAVAILABLE for at least one counted unit "+
			"(the suite's per-task output carries no cache-read field, so that axis must come from the "+
			"driver): the token totals here are a LOWER BOUND, not a complete FR-014 accounting")
	}
	if len(reasons) > 0 {
		row.Withheld = true
		row.WithheldReason = strings.Join(reasons, "; ")
	}

	row.Headline = row.Runs >= MinHeadlineRuns &&
		len(a.perRunCostPerTask) >= 2 &&
		a.completed > 0 &&
		a.cacheReadAvailable &&
		!row.Withheld
	return row
}

// SavingVsBaseline is the only sanctioned way to turn two agent-loop cells into
// a percentage (FR-019/FR-020/SC-007).
//
// It refuses in three situations, each for a reason that would otherwise be
// invisible in the published number:
//
//  1. There is no usable baseline. FR-020's denominator is the same agent doing
//     the same tasks with every tool loaded directly; without it a "saving" is
//     measured against nothing.
//  2. The cell is a REGRESSION — its completion rate fell more than the stated
//     threshold below the baseline's. A mode that finishes fewer tasks is not
//     cheap, it is broken, and describing it as a saving is precisely what
//     SC-007 exists to prevent. Its token cost is still reported; it is the
//     word "saving" that is refused.
//  3. Either cell's token accounting is incomplete. Comparing a complete total
//     against a lower bound produces a percentage of two different quantities.
//
// The comparison inside one block is legitimate because both cells share the
// block's accounting source. Comparing a cell here against a figure from the
// deterministic blocks is NOT, which is what AggregateTokenFigures refuses.
func (b *AgentLoopBlock) SavingVsBaseline(cellID string) SavingVerdict {
	verdict := SavingVerdict{CellID: cellID}
	if b == nil {
		return SavingVerdict{
			CellID:         cellID,
			Withheld:       true,
			WithheldReason: "saving withheld: no agent_loop block",
		}
	}
	verdict.FleetShape = b.FleetShape

	var cell, baseline *AgentLoopCell
	for i := range b.Cells {
		switch b.Cells[i].CellID {
		case cellID:
			cell = &b.Cells[i]
		case CellBaseline:
			baseline = &b.Cells[i]
		}
	}
	if cell == nil {
		verdict.Withheld = true
		verdict.WithheldReason = fmt.Sprintf("saving withheld: no measured cell %q in this block", cellID)
		return verdict
	}
	if baseline == nil || baseline.TokensPerCompletedTask <= 0 {
		verdict.Withheld = true
		verdict.WithheldReason = "saving WITHHELD: the FR-020 baseline arm produced no tokens-per-completed-task " +
			"figure, so there is no denominator. A saving quoted against a missing baseline is measured against nothing."
		return verdict
	}
	if cell.Withheld || baseline.Withheld {
		verdict.Withheld = true
		verdict.WithheldReason = "saving WITHHELD: a figure on one side of the comparison was itself withheld, " +
			"so the ratio would be a claim built on a number the report declines to publish. Checked BEFORE the " +
			"regression branch on purpose: an unmeasured cell has no completion rate, and describing its implicit " +
			"zero as a regression fabricates a quantitative claim about a cell nothing was counted for. " +
			"Cell reason: " + orNone(cell.WithheldReason) + " Baseline reason: " + orNone(baseline.WithheldReason)
		return verdict
	}
	// FR-021: a model-dependent figure needs at least MinHeadlineRuns runs and
	// a computable spread. This function is documented as the only sanctioned
	// way to turn two cells into a percentage, so the bar has to live HERE —
	// enforcing it only on the row's Headline flag left a caller free to
	// publish a saving off a single run, which is exactly the single-run
	// headline FR-021 exists to forbid.
	if cell.Runs < MinHeadlineRuns || baseline.Runs < MinHeadlineRuns {
		verdict.Withheld = true
		verdict.WithheldReason = fmt.Sprintf(
			"saving WITHHELD: FR-021 requires at least %d runs per side and this comparison has %d (cell) and "+
				"%d (baseline). A single agentic run is noise, and its spread is undefined rather than zero.",
			MinHeadlineRuns, cell.Runs, baseline.Runs)
		return verdict
	}
	// DERIVE the regression from the completion rates rather than trusting the
	// row's flag.
	//
	// This function is documented as the only sanctioned way to turn two cells
	// into a percentage, so every publication rule has to hold for ANY block it
	// is handed — including one unmarshalled from a report.json, assembled by a
	// caller, or mutated after AgentLoopBlockFor set the flag. Trusting a
	// mutable bool meant a block carrying Regression:false with a 50-point
	// completion deficit returned a saving; the numbers needed to catch that
	// are right here on both cells.
	regressed := cell.Regression
	if baseline.CompletionRatePct-cell.CompletionRatePct > CompletionRegressionThresholdPct(b) {
		regressed = true
	}
	if regressed {
		verdict.Regression = true
		verdict.Withheld = true
		verdict.WithheldReason = fmt.Sprintf(
			"saving WITHHELD — REGRESSION: completion rate %.1f%% is %.1f percentage points below the baseline's "+
				"%.1f%%. A mode that completes fewer tasks must not be described as a saving regardless of its "+
				"token cost (FR-019/SC-007); its cost is reported beside its completion rate instead.",
			cell.CompletionRatePct, baseline.CompletionRatePct-cell.CompletionRatePct, baseline.CompletionRatePct)
		return verdict
	}
	if cell.TokensPerCompletedTask <= 0 {
		verdict.Withheld = true
		verdict.WithheldReason = fmt.Sprintf("saving withheld: cell %q has no tokens-per-completed-task figure", cellID)
		return verdict
	}

	verdict.SavingPct = (1.0 - cell.TokensPerCompletedTask/baseline.TokensPerCompletedTask) * 100.0
	return verdict
}

func orNone(s string) string {
	if s == "" {
		return "(none)."
	}
	return s + "."
}

// pct is a percentage guarded against a zero denominator.
func pct(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100.0
}

// relativeSpreadPct is FR-021's "measure of consistency": the range of the
// per-run values as a percentage of their mean. Range rather than standard
// deviation because at k=4 a standard deviation is barely meaningful, while
// the best-to-worst gap is exactly what a reader needs to judge whether a
// difference between two modes is bigger than the noise inside one of them.
func relativeSpreadPct(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	minV, maxV, sum := values[0], values[0], 0.0
	for _, v := range values {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
		sum += v
	}
	mean := sum / float64(len(values))
	if mean == 0 {
		return 0
	}
	return (maxV - minV) / mean * 100.0
}

// ---------------------------------------------------------------------------
// MCPMark ingestion (T041)
// ---------------------------------------------------------------------------

// MCPMarkTokenUsage mirrors the pinned suite's per-task `token_usage` object.
//
// Every field is a POINTER so an absent key is distinguishable from a reported
// zero — the same reason ReplayCellCost.ResponseTokens is a pointer. A missing
// count silently read as 0 would understate cost, and understating cost in
// this project's favour is the failure class FR-002 forbids for truncation and
// that this file must not reintroduce on a new axis.
//
// Both key spellings are accepted. The suite reports input/output/total/
// reasoning; the `_tokens`-suffixed spelling is common in provider payloads
// that the suite passes through, and refusing to read a file we can obviously
// read would trade a real measurement for a stylistic point. Which spelling
// the pinned SHA actually emits is recorded in bench/README.md alongside the
// pin, and is re-checked whenever the pin moves.
type MCPMarkTokenUsage struct {
	Input        *int `json:"input"`
	Output       *int `json:"output"`
	Total        *int `json:"total"`
	Reasoning    *int `json:"reasoning"`
	InputAlt     *int `json:"input_tokens"`
	OutputAlt    *int `json:"output_tokens"`
	TotalAlt     *int `json:"total_tokens"`
	ReasoningAlt *int `json:"reasoning_tokens"`
}

func firstNonNil(vals ...*int) *int {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

// MCPMarkMeta is the subset of the suite's per-task meta.json this driver
// reads: the pass verdict, the token usage, and the turn count.
//
// execution_result.success is a POINTER because its absence is NOT a failure.
// TASK COMPLETION is the suite's own verdict; where the suite emits none, the
// unit has NO COMPLETION SIGNAL and is excluded from completion-dependent
// figures. Decoding a missing bool as false would quietly convert "we do not
// know" into "it failed", which is an inference the Definitions forbid.
type MCPMarkMeta struct {
	TaskName        string `json:"task_name"`
	ExecutionResult struct {
		Success *bool `json:"success"`
	} `json:"execution_result"`
	TokenUsage *MCPMarkTokenUsage `json:"token_usage"`
	TurnCount  *int               `json:"turn_count"`

	// SourcePath is the file this was read from, kept so a report can
	// reference the raw per-run record it was computed from (FR-029).
	SourcePath string `json:"-"`
}

// LoadMCPMarkMeta reads one per-task meta.json.
//
// A missing or malformed file is a REPORTED ERROR, never a zero-valued record.
// The distinction matters more than it looks: a run over hundreds of tasks
// where a handful of meta files failed to write would, under a zero-filling
// loader, publish a cheaper-and-less-successful-looking number with no trace
// of why — and nothing downstream could recover the difference between "this
// task cost nothing" and "we could not read what it cost".
func LoadMCPMarkMeta(path string) (*MCPMarkMeta, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied suite output path
	if err != nil {
		return nil, fmt.Errorf("read mcpmark meta %q: %w", path, err)
	}
	var meta MCPMarkMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("decode mcpmark meta %q: %w", path, err)
	}
	meta.SourcePath = path
	return &meta, nil
}

// Completion maps the suite's verdict onto the report vocabulary. An absent
// success field is no-signal, never a failure.
func (m *MCPMarkMeta) Completion() string {
	switch {
	case m.ExecutionResult.Success == nil:
		return CompletionNoSignal
	case *m.ExecutionResult.Success:
		return CompletionPass
	default:
		return CompletionFail
	}
}

// UnitRecord projects one meta.json onto a UnitRecord.
//
// The resulting usage has NO cache-read count, and that is not an oversight to
// be tidied away with a zero: the suite's output has no such field at all
// (research.md §5). FR-014's third axis has to come from the driver's own
// capture of provider responses, and a record built from meta.json alone
// carries the gap honestly so the report can say so.
func (m *MCPMarkMeta) UnitRecord(cellID, unitID string, runIndex int) (*UnitRecord, error) {
	if m == nil {
		return nil, errors.New("mcpmark meta: nil")
	}
	if m.TokenUsage == nil {
		return nil, fmt.Errorf("mcpmark meta %q: no token_usage object; a cost figure cannot be produced and "+
			"a zero here would understate cost", m.SourcePath)
	}
	input := firstNonNil(m.TokenUsage.Input, m.TokenUsage.InputAlt)
	output := firstNonNil(m.TokenUsage.Output, m.TokenUsage.OutputAlt)
	if input == nil || output == nil {
		return nil, fmt.Errorf("mcpmark meta %q: token_usage is missing the input and/or output count; "+
			"defaulting either to zero would understate cost", m.SourcePath)
	}
	usage := ProviderUsage{
		InputTokens:  *input,
		OutputTokens: *output,
		// Deliberately nil: the suite reports no cache-read figure.
		CacheReadTokens: nil,
		Responses:       1,
	}
	if r := firstNonNil(m.TokenUsage.Reasoning, m.TokenUsage.ReasoningAlt); r != nil {
		usage.ReasoningTokens = *r
	}

	rec := &UnitRecord{
		CellID:     cellID,
		UnitID:     unitID,
		RunIndex:   runIndex,
		Completion: m.Completion(),
		Usage:      usage,
	}
	if m.TurnCount != nil {
		rec.TurnCount = *m.TurnCount
	}
	return rec, nil
}

// mcpMarkMessage is the subset of one trajectory message this driver reads.
type mcpMarkMessage struct {
	Role      string `json:"role"`
	ToolCalls []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
	IsError    *bool           `json:"is_error"`
	ErrorType  string          `json:"error_type"`
	Content    json.RawMessage `json:"content"`
}

// LoadMCPMarkTrajectory reads the suite's messages.json and reconstructs the
// ordered ATTEMPT sequence that retry classification needs.
//
// Why the trajectory and not just meta.json: meta.json says whether the task
// passed and what it cost, but first-attempt success and corrective-versus-
// infrastructure retries are properties of the SEQUENCE of calls. Without the
// trajectory, FR-010 and FR-011 are unmeasurable and the whole "does this mode
// help the agent succeed" question collapses back into token counting.
//
// Two classification honesty notes:
//
//   - The intent is the tool NAME and the arguments are the fingerprint. This
//     is an approximation of "intent" — two calls to the same tool for
//     genuinely different purposes read as one intent — and it is the
//     approximation that errs toward counting MORE corrective retries, i.e.
//     against mcpproxy rather than for it.
//   - An error whose type is not recognisably a transport failure is
//     classified as a MODE error, not an infrastructure one, for the same
//     reason: the conservative direction is the one that does not flatter us.
//
// A trailing tool call with no result marks the record truncated (the caller
// sets Partial); an unanswered call with later calls after it is a malformed
// trajectory and an error, because it means results were dropped rather than
// cut off.
func LoadMCPMarkTrajectory(path string) (attempts []Attempt, truncated bool, err error) {
	data, rerr := os.ReadFile(path) //nolint:gosec // operator-supplied suite output path
	if rerr != nil {
		return nil, false, fmt.Errorf("read mcpmark trajectory %q: %w", path, rerr)
	}
	var msgs []mcpMarkMessage
	if uerr := json.Unmarshal(data, &msgs); uerr != nil {
		return nil, false, fmt.Errorf("decode mcpmark trajectory %q: %w", path, uerr)
	}

	type pending struct {
		index    int // position in the attempts slice
		issuedAt int // index of the message that issued the call
	}
	byCallID := make(map[string]pending)
	seqByIntent := make(map[string]int)
	resolved := make(map[string]bool)
	lastIssuedAt := -1

	for mi, msg := range msgs {
		for _, tc := range msg.ToolCalls {
			name := tc.Function.Name
			if name == "" {
				return nil, false, fmt.Errorf("mcpmark trajectory %q: message %d issues a tool call with no name", path, mi)
			}
			seqByIntent[name]++
			attempts = append(attempts, Attempt{
				IntentID:        name,
				Seq:             seqByIntent[name],
				Outcome:         AttemptOutcomeUnknown,
				ArgsFingerprint: normalizeArguments(tc.Function.Arguments),
			})
			if tc.ID != "" {
				byCallID[tc.ID] = pending{index: len(attempts) - 1, issuedAt: mi}
			}
			lastIssuedAt = mi
		}
		if msg.ToolCallID == "" {
			continue
		}
		p, ok := byCallID[msg.ToolCallID]
		if !ok {
			return nil, false, fmt.Errorf("mcpmark trajectory %q: message %d is a result for unknown tool call %q",
				path, mi, msg.ToolCallID)
		}
		attempts[p.index].Outcome = classifyResultOutcome(msg)
		resolved[msg.ToolCallID] = true
	}

	for id, p := range byCallID {
		if resolved[id] {
			continue
		}
		if p.issuedAt == lastIssuedAt {
			truncated = true
			continue
		}
		return nil, false, fmt.Errorf("mcpmark trajectory %q: tool call %q has no result but later calls follow it; "+
			"results were dropped rather than cut off", path, id)
	}
	return attempts, truncated, nil
}

// transportErrorTypes are the error_type values read as INFRASTRUCTURE. The
// list is deliberately short: an unrecognised failure is classified as a mode
// error, which counts against mcpproxy rather than for it.
var transportErrorTypes = map[string]bool{
	"transport":  true,
	"network":    true,
	"connection": true,
	"timeout":    true,
}

// classifyResultOutcome maps one tool-result message onto an attempt outcome.
func classifyResultOutcome(msg mcpMarkMessage) string {
	isErr := msg.IsError != nil && *msg.IsError
	if !isErr {
		// MCP results carry their own isError flag inside the content object.
		var content struct {
			IsError *bool `json:"isError"`
		}
		if json.Unmarshal(msg.Content, &content) == nil && content.IsError != nil {
			isErr = *content.IsError
		}
	}
	if !isErr {
		return AttemptOutcomeOK
	}
	if transportErrorTypes[strings.ToLower(strings.TrimSpace(msg.ErrorType))] {
		return AttemptOutcomeInfrastructure
	}
	return AttemptOutcomeError
}

// normalizeArguments renders a tool call's arguments as a comparable
// fingerprint. The suite may carry them as a JSON-encoded STRING (the OpenAI
// shape) or as an object; both are reduced to their textual form so "same
// arguments" is decidable either way. No argument VALUE ever leaves this
// package in a report — the fingerprint is used only for the equality test.
func normalizeArguments(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// CompletionRegressionThresholdPct is the stated FR-019 threshold for a block.
//
// A named accessor rather than a bare constant so the value a verdict uses and
// the value AgentLoopBlockFor applied cannot drift apart — they must be the
// same number or a cell can be flagged by one and cleared by the other.
func CompletionRegressionThresholdPct(b *AgentLoopBlock) float64 {
	if b != nil && b.CompletionRegressionThresholdPct > 0 {
		return b.CompletionRegressionThresholdPct
	}
	return DefaultCompletionRegressionThresholdPct
}
