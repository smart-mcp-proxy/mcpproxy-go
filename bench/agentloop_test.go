package bench

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec 103 US2 — T034-T038: the live agent-loop driver's arithmetic and its
// classification of the spec's BINDING Definitions.
//
// Every test here runs with NO suite, NO model and NO network: the driver is
// parameterised over function types (the bench/flipgate.go precedent) so the
// arithmetic that decides what gets published is exercised by fakes. A test
// that needed a paid run would never be run, and an unrun test gates nothing.

const (
	testProvider = "test-provider"
	testModel    = "test-model-2026-01-01"
)

func testAttempt(intent string, seq int, outcome, args string) Attempt {
	return Attempt{IntentID: intent, Seq: seq, Outcome: outcome, ArgsFingerprint: args}
}

func testUnit(cellID, unitID string, run int, completion string, attempts []Attempt, in, out int, cacheRead *int) UnitRecord {
	return UnitRecord{
		CellID:     cellID,
		UnitID:     unitID,
		RunIndex:   run,
		Completion: completion,
		Attempts:   attempts,
		Usage: ProviderUsage{
			InputTokens:     in,
			OutputTokens:    out,
			CacheReadTokens: cacheRead,
			Responses:       1,
		},
	}
}

func intPtr(v int) *int { return &v }

// testOpts builds valid options: the FR-020 baseline cell plus the given proxy
// cells, a pinned provider+model, and a fleet shape (IC-004).
func testOpts(proxy ...ModeCell) AgentLoopOptions {
	cells := append([]ModeCell{BaselineCell()}, proxy...)
	return AgentLoopOptions{
		Suite:        "mcpmark",
		SuiteVersion: "deadbeef",
		Provider:     testProvider,
		Model:        testModel,
		FleetShape:   FleetShape{ID: "fleet-45", ToolCount: 45},
		Cells:        cells,
		Units:        []string{"t1"},
		Runs:         1,
	}
}

func cellByID(t *testing.T, b *AgentLoopBlock, id string) AgentLoopCell {
	t.Helper()
	for _, c := range b.Cells {
		if c.CellID == id {
			return c
		}
	}
	t.Fatalf("block has no cell %q (cells: %+v)", id, b.Cells)
	return AgentLoopCell{}
}

// ---------------------------------------------------------------------------
// T034 — the binding Definitions
// ---------------------------------------------------------------------------

func TestClassifyIntents_FirstAttemptSuccessDefinition(t *testing.T) {
	tests := []struct {
		name        string
		attempts    []Attempt
		wantSuccess bool
		wantIndet   bool
		wantCorrect int
		wantInfra   int
	}{
		{
			name:        "single non-error attempt is a first-attempt success",
			attempts:    []Attempt{testAttempt("read", 1, AttemptOutcomeOK, `{"p":"/a"}`)},
			wantSuccess: true,
		},
		{
			// The binding Definition's explicit carve-out: a schema-valid call
			// the agent immediately re-issues DIFFERENTLY is NOT a success.
			name: "non-error call re-issued differently is not a success",
			attempts: []Attempt{
				testAttempt("read", 1, AttemptOutcomeOK, `{"p":"/a"}`),
				testAttempt("read", 2, AttemptOutcomeOK, `{"p":"/b"}`),
			},
			wantSuccess: false,
			wantCorrect: 1,
		},
		{
			name: "error then corrected call is not a first-attempt success",
			attempts: []Attempt{
				testAttempt("read", 1, AttemptOutcomeError, `{"p":"/a"}`),
				testAttempt("read", 2, AttemptOutcomeOK, `{"path":"/a"}`),
			},
			wantSuccess: false,
			wantCorrect: 1,
		},
		{
			// Transport retries measure the network, not the mode, so they are
			// counted separately AND leave the intent indeterminate rather than
			// scoring against the cell that happened to hit a flaky hop.
			name: "identical re-issue after a transport failure is an infrastructure retry",
			attempts: []Attempt{
				testAttempt("read", 1, AttemptOutcomeInfrastructure, `{"p":"/a"}`),
				testAttempt("read", 2, AttemptOutcomeOK, `{"p":"/a"}`),
			},
			wantSuccess: false,
			wantIndet:   true,
			wantInfra:   1,
		},
		{
			name: "changed arguments after a transport failure is corrective, not infrastructure",
			attempts: []Attempt{
				testAttempt("read", 1, AttemptOutcomeInfrastructure, `{"p":"/a"}`),
				testAttempt("read", 2, AttemptOutcomeOK, `{"path":"/a"}`),
			},
			wantSuccess: false,
			wantIndet:   true,
			wantCorrect: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyIntents(tc.attempts)
			if len(got) != 1 {
				t.Fatalf("ClassifyIntents returned %d intents, want 1: %+v", len(got), got)
			}
			o := got[0]
			if o.FirstAttemptSuccess != tc.wantSuccess {
				t.Errorf("FirstAttemptSuccess = %v, want %v", o.FirstAttemptSuccess, tc.wantSuccess)
			}
			if o.Indeterminate != tc.wantIndet {
				t.Errorf("Indeterminate = %v, want %v", o.Indeterminate, tc.wantIndet)
			}
			if o.RetriesCorrective != tc.wantCorrect {
				t.Errorf("RetriesCorrective = %d, want %d", o.RetriesCorrective, tc.wantCorrect)
			}
			if o.RetriesInfrastructure != tc.wantInfra {
				t.Errorf("RetriesInfrastructure = %d, want %d", o.RetriesInfrastructure, tc.wantInfra)
			}
		})
	}
}

// The classification must be a property of the ATTEMPT SEQUENCE alone. If the
// baseline arm and the proxy arms were classified by different rules — because
// one carries richer error signal than the other — the comparison would be
// biased toward whichever surface reports failure more legibly, which is the
// single easiest way to fake a favourable result.
func TestClassifyIntents_SameRuleForBaselineAndProxyArms(t *testing.T) {
	attempts := []Attempt{
		testAttempt("read", 1, AttemptOutcomeError, `{"p":"/a"}`),
		testAttempt("read", 2, AttemptOutcomeOK, `{"path":"/a"}`),
		testAttempt("write", 1, AttemptOutcomeOK, `{"path":"/b"}`),
	}

	opts := testOpts(mustCell(t, CellRetrieveFull))
	records := []UnitRecord{
		testUnit(CellBaseline, "t1", 0, CompletionPass, attempts, 100, 10, intPtr(0)),
		testUnit(CellRetrieveFull, "t1", 0, CompletionPass, attempts, 100, 10, intPtr(0)),
	}

	block, err := AgentLoopBlockFor(records, opts)
	if err != nil {
		t.Fatalf("AgentLoopBlockFor: %v", err)
	}
	base := cellByID(t, block, CellBaseline)
	proxy := cellByID(t, block, CellRetrieveFull)

	// Compare VALUES, not pointers. The figure is optional (nil = not
	// measured), so a bare != compares addresses and always differs.
	if (base.FirstAttemptSuccessPct == nil) != (proxy.FirstAttemptSuccessPct == nil) {
		t.Errorf("one arm measured first-attempt success and the other did not: baseline=%v proxy=%v",
			base.FirstAttemptSuccessPct, proxy.FirstAttemptSuccessPct)
	} else if base.FirstAttemptSuccessPct != nil &&
		*base.FirstAttemptSuccessPct != *proxy.FirstAttemptSuccessPct {
		t.Errorf("first-attempt success differs across arms for identical attempts: baseline %v, proxy %v",
			*base.FirstAttemptSuccessPct, *proxy.FirstAttemptSuccessPct)
	}
	if base.RetriesCorrective != proxy.RetriesCorrective || base.RetriesInfrastructure != proxy.RetriesInfrastructure {
		t.Errorf("retry counts differ across arms for identical attempts: baseline %d/%d, proxy %d/%d",
			base.RetriesCorrective, base.RetriesInfrastructure,
			proxy.RetriesCorrective, proxy.RetriesInfrastructure)
	}
	// 2 intents, one corrected and one clean.
	if base.FirstAttemptSuccessPct == nil {
		t.Fatal("FirstAttemptSuccessPct must be measured here — 2 determinate intents were supplied")
	} else if *base.FirstAttemptSuccessPct != 50 {
		t.Errorf("FirstAttemptSuccessPct = %v, want 50 (1 of 2 intents clean)", *base.FirstAttemptSuccessPct)
	}
}

// UNIT OF WORK is one task, never an individual tool call. A unit with many
// attempts is still exactly one unit in the completion denominator.
func TestAgentLoopBlockFor_UnitOfWorkIsTaskNotToolCall(t *testing.T) {
	attempts := []Attempt{
		testAttempt("a", 1, AttemptOutcomeError, `{}`),
		testAttempt("a", 2, AttemptOutcomeOK, `{"x":1}`),
		testAttempt("b", 1, AttemptOutcomeOK, `{}`),
		testAttempt("c", 1, AttemptOutcomeOK, `{}`),
		testAttempt("d", 1, AttemptOutcomeOK, `{}`),
	}
	opts := testOpts()
	records := []UnitRecord{
		testUnit(CellBaseline, "t1", 0, CompletionPass, attempts, 100, 10, intPtr(0)),
	}
	block, err := AgentLoopBlockFor(records, opts)
	if err != nil {
		t.Fatalf("AgentLoopBlockFor: %v", err)
	}
	base := cellByID(t, block, CellBaseline)
	if base.CompletionRatePct != 100 {
		t.Errorf("CompletionRatePct = %v, want 100 — one completed task with five attempts is ONE unit of work",
			base.CompletionRatePct)
	}
	if base.TokensPerCompletedTask != 110 {
		t.Errorf("TokensPerCompletedTask = %v, want 110 (per TASK, not per call)", base.TokensPerCompletedTask)
	}
}

// ---------------------------------------------------------------------------
// T035 — no completion signal excludes, never assumes
// ---------------------------------------------------------------------------

func TestAgentLoopBlockFor_NoSignalIsExcludedNotCounted(t *testing.T) {
	ok := []Attempt{testAttempt("a", 1, AttemptOutcomeOK, `{}`)}
	opts := testOpts()
	opts.Units = []string{"t1", "t2", "t3"}
	records := []UnitRecord{
		testUnit(CellBaseline, "t1", 0, CompletionPass, ok, 100, 0, intPtr(0)),
		testUnit(CellBaseline, "t2", 0, CompletionFail, ok, 100, 0, intPtr(0)),
		testUnit(CellBaseline, "t3", 0, CompletionNoSignal, ok, 900, 0, intPtr(0)),
	}
	block, err := AgentLoopBlockFor(records, opts)
	if err != nil {
		t.Fatalf("AgentLoopBlockFor: %v", err)
	}
	base := cellByID(t, block, CellBaseline)

	// 1 pass of 2 units carrying a verdict. Counting the no-signal unit as a
	// failure would give 33.3; counting it as a success would give 66.7.
	if base.CompletionRatePct != 50 {
		t.Errorf("CompletionRatePct = %v, want 50 — the no-signal unit must be EXCLUDED, not scored",
			base.CompletionRatePct)
	}
	// Its 900 tokens must not enter a completion-dependent figure either.
	if base.TokensPerCompletedTask != 200 {
		t.Errorf("TokensPerCompletedTask = %v, want 200 — the no-signal unit's tokens must be excluded too",
			base.TokensPerCompletedTask)
	}
	if base.InputTokens != 200 {
		t.Errorf("InputTokens = %v, want 200 (the reported totals must reconcile with the headline)", base.InputTokens)
	}
}

// ---------------------------------------------------------------------------
// T036 — FR-021: a model-dependent figure needs runs >= 4
// ---------------------------------------------------------------------------

func TestAgentLoopBlockFor_RunsBelowFourIsRefusedAsHeadline(t *testing.T) {
	ok := []Attempt{testAttempt("a", 1, AttemptOutcomeOK, `{}`)}
	build := func(runs int) *AgentLoopBlock {
		t.Helper()
		opts := testOpts()
		opts.Runs = runs
		var records []UnitRecord
		for r := 0; r < runs; r++ {
			// Vary the cost slightly so a spread is computable.
			records = append(records, testUnit(CellBaseline, "t1", r, CompletionPass, ok, 100+r, 0, intPtr(0)))
		}
		block, err := AgentLoopBlockFor(records, opts)
		if err != nil {
			t.Fatalf("AgentLoopBlockFor(runs=%d): %v", runs, err)
		}
		return block
	}

	for _, runs := range []int{1, 2, 3} {
		c := cellByID(t, build(runs), CellBaseline)
		if c.Runs != runs {
			t.Errorf("runs=%d: Runs = %d, want %d", runs, c.Runs, runs)
		}
		if c.Headline {
			t.Errorf("runs=%d: Headline = true, want false — FR-021 requires at least %d runs",
				runs, MinHeadlineRuns)
		}
	}
	c := cellByID(t, build(4), CellBaseline)
	if !c.Headline {
		t.Errorf("runs=4: Headline = false, want true (%+v)", c)
	}
	if c.SpreadPct == nil {
		t.Errorf("runs=4: SpreadPct is nil, but four runs give a DEFINED spread — FR-021 requires a " +
			"measure of consistency beside the average")
	} else if *c.SpreadPct <= 0 {
		t.Errorf("runs=4: SpreadPct = %v, want > 0 — FR-021 requires a measure of consistency beside the average",
			*c.SpreadPct)
	}
}

// ---------------------------------------------------------------------------
// T037 — SC-007: cheaper but completing less is a REGRESSION, not a saving
// ---------------------------------------------------------------------------

func TestAgentLoopBlockFor_DegradedModeIsRegressionNotSaving(t *testing.T) {
	ok := []Attempt{testAttempt("a", 1, AttemptOutcomeOK, `{}`)}
	opts := testOpts(mustCell(t, CellRetrieveCompact))
	opts.Units = []string{"t1", "t2", "t3", "t4"}
	opts.Runs = 4

	var records []UnitRecord
	for r := 0; r < 4; r++ {
		// Baseline: 4 of 4 completed, 1000 tokens each.
		for i, u := range opts.Units {
			records = append(records, testUnit(CellBaseline, u, r, CompletionPass, ok, 1000+i, 0, intPtr(0)))
		}
		// Degraded mode: MUCH cheaper (100 tokens) but completes only 2 of 4.
		for i, u := range opts.Units {
			verdict := CompletionPass
			if i >= 2 {
				verdict = CompletionFail
			}
			records = append(records, testUnit(CellRetrieveCompact, u, r, verdict, ok, 100+i, 0, intPtr(0)))
		}
	}

	block, err := AgentLoopBlockFor(records, opts)
	if err != nil {
		t.Fatalf("AgentLoopBlockFor: %v", err)
	}
	degraded := cellByID(t, block, CellRetrieveCompact)

	if !degraded.Regression {
		t.Fatalf("degraded cell not flagged as a regression: completion %v vs baseline %v (%+v)",
			degraded.CompletionRatePct, cellByID(t, block, CellBaseline).CompletionRatePct, degraded)
	}
	verdict := block.SavingVsBaseline(CellRetrieveCompact)
	if !verdict.Withheld {
		t.Errorf("SavingVsBaseline(%s) reported a saving of %v%% for a regressed mode; it must be WITHHELD (FR-019/SC-007)",
			CellRetrieveCompact, verdict.SavingPct)
	}
	if verdict.SavingPct != 0 {
		t.Errorf("withheld verdict still carries SavingPct = %v; a withheld figure has no value", verdict.SavingPct)
	}
	if !strings.Contains(strings.ToLower(verdict.WithheldReason), "regression") {
		t.Errorf("WithheldReason = %q, want it to state the regression", verdict.WithheldReason)
	}
	// Every percentage carries its fleet shape (IC-004).
	if verdict.FleetShape.ID != "fleet-45" || verdict.FleetShape.ToolCount != 45 {
		t.Errorf("verdict.FleetShape = %+v, want the block's fleet shape", verdict.FleetShape)
	}

	// A mode that is cheaper AND keeps completion is a genuine saving — the
	// control that proves the regression flag is not simply always set.
	var good []UnitRecord
	for r := 0; r < 4; r++ {
		for i, u := range opts.Units {
			good = append(good, testUnit(CellBaseline, u, r, CompletionPass, ok, 1000+i, 0, intPtr(0)))
			good = append(good, testUnit(CellRetrieveCompact, u, r, CompletionPass, ok, 500+i, 0, intPtr(0)))
		}
	}
	block2, err := AgentLoopBlockFor(good, opts)
	if err != nil {
		t.Fatalf("AgentLoopBlockFor (control): %v", err)
	}
	if c := cellByID(t, block2, CellRetrieveCompact); c.Regression {
		t.Errorf("non-degraded cell flagged as a regression (%+v)", c)
	}
	if v := block2.SavingVsBaseline(CellRetrieveCompact); v.Withheld || v.SavingPct <= 0 {
		t.Errorf("SavingVsBaseline(control) = %+v, want a real positive saving", v)
	}
}

// ---------------------------------------------------------------------------
// T038 — a cross-accounting-source aggregate is WITHHELD, never computed
// ---------------------------------------------------------------------------

func TestAggregateTokenFigures_CrossSourceIsWithheld(t *testing.T) {
	tokenizer := AccountingSource{Kind: AccountingKindTokenizer, Identity: DefaultEncoding}
	provider := AccountingSource{Kind: AccountingKindProvider, Identity: testProvider, Model: testModel}

	mixed := AggregateTokenFigures([]SourcedFigure{
		{Value: 1000, Source: tokenizer},
		{Value: 250, Source: provider},
	})
	if !mixed.Withheld {
		t.Fatalf("cross-source aggregate was COMPUTED (%v); it must be withheld with a reason", mixed.Value)
	}
	if mixed.Value != 0 {
		t.Errorf("withheld aggregate carries Value = %v; a withheld figure has no value", mixed.Value)
	}
	if mixed.WithheldReason == "" {
		t.Error("withheld aggregate carries no reason — 'withheld with a stated reason' is the whole rule")
	}
	for _, want := range []string{AccountingKindTokenizer, AccountingKindProvider} {
		if !strings.Contains(mixed.WithheldReason, want) {
			t.Errorf("WithheldReason = %q, want it to name the source %q", mixed.WithheldReason, want)
		}
	}

	// Same source: summed, and the source travels with the result.
	same := AggregateTokenFigures([]SourcedFigure{
		{Value: 100, Source: provider},
		{Value: 250, Source: provider},
	})
	if same.Withheld {
		t.Fatalf("same-source aggregate was withheld: %q", same.WithheldReason)
	}
	if same.Value != 350 {
		t.Errorf("Value = %v, want 350", same.Value)
	}
	if same.Source != provider {
		t.Errorf("Source = %+v, want %+v", same.Source, provider)
	}

	// The same provider on a DIFFERENT model is a different source: usage from
	// two models is not one number.
	otherModel := provider
	otherModel.Model = "some-other-model"
	crossModel := AggregateTokenFigures([]SourcedFigure{
		{Value: 100, Source: provider},
		{Value: 100, Source: otherModel},
	})
	if !crossModel.Withheld {
		t.Errorf("aggregate across two pinned models was computed (%v); models are not interchangeable", crossModel.Value)
	}

	// An unpopulated source cannot enter any aggregate at all.
	unset := AggregateTokenFigures([]SourcedFigure{{Value: 5, Source: AccountingSource{}}})
	if !unset.Withheld {
		t.Errorf("aggregate over an unset accounting source was computed (%v)", unset.Value)
	}
}

// ---------------------------------------------------------------------------
// T040 — the baseline arm bypasses mcpproxy entirely
// ---------------------------------------------------------------------------

func TestAgentLoopBlockFor_BaselineArmIsMandatoryAndBypassesProxy(t *testing.T) {
	ok := []Attempt{testAttempt("a", 1, AttemptOutcomeOK, `{}`)}

	// No baseline cell configured: there is no denominator, so no percentage
	// can be published (FR-020).
	noBase := testOpts()
	noBase.Cells = []ModeCell{mustCell(t, CellRetrieveFull)}
	if _, err := AgentLoopBlockFor(
		[]UnitRecord{testUnit(CellRetrieveFull, "t1", 0, CompletionPass, ok, 10, 0, intPtr(0))},
		noBase,
	); !errors.Is(err, ErrAgentLoopBaselineMissing) {
		t.Errorf("AgentLoopBlockFor without a baseline cell: err = %v, want ErrAgentLoopBaselineMissing", err)
	}

	// A "baseline" pointed at an mcpproxy endpoint is not a baseline: it is a
	// sixth proxy cell wearing the denominator's name.
	fake := testOpts()
	fake.Cells[0].Endpoint = EndpointDirect
	if _, err := AgentLoopBlockFor(
		[]UnitRecord{testUnit(CellBaseline, "t1", 0, CompletionPass, ok, 10, 0, intPtr(0))},
		fake,
	); !errors.Is(err, ErrBaselineNotBypassing) {
		t.Errorf("AgentLoopBlockFor with an endpoint-bearing baseline: err = %v, want ErrBaselineNotBypassing", err)
	}

	// A baseline that produced no usable records: every proxy percentage is
	// withheld rather than measured against nothing.
	opts := testOpts(mustCell(t, CellRetrieveFull))
	block, err := AgentLoopBlockFor(
		[]UnitRecord{testUnit(CellRetrieveFull, "t1", 0, CompletionPass, ok, 10, 0, intPtr(0))},
		opts,
	)
	if err != nil {
		t.Fatalf("AgentLoopBlockFor: %v", err)
	}
	v := block.SavingVsBaseline(CellRetrieveFull)
	if !v.Withheld || !strings.Contains(strings.ToLower(v.WithheldReason), "baseline") {
		t.Errorf("saving against an absent baseline = %+v, want withheld naming the missing baseline", v)
	}
}

// The driver must actually RUN the baseline arm alongside the proxy cells.
func TestRunAgentLoop_DrivesBaselineAndProxyOverTheSameTaskSet(t *testing.T) {
	opts := testOpts(mustCell(t, CellRetrieveFull))
	opts.Units = []string{"t1", "t2"}
	opts.Runs = 4

	var seen []string
	driver := AgentLoopDriver{
		RunTask: func(cell ModeCell, unitID string, runIndex int) (*UnitRecord, error) {
			seen = append(seen, cell.ID+"/"+unitID)
			return &UnitRecord{
				CellID:     cell.ID,
				UnitID:     unitID,
				RunIndex:   runIndex,
				Completion: CompletionPass,
				Attempts:   []Attempt{testAttempt("a", 1, AttemptOutcomeOK, `{}`)},
				Usage:      ProviderUsage{InputTokens: 100 + runIndex, OutputTokens: 10, CacheReadTokens: intPtr(5), Responses: 1},
			}, nil
		},
	}
	block, err := RunAgentLoop(driver, opts)
	if err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}
	if len(seen) != 2*2*4 {
		t.Errorf("driver invoked %d times, want %d (2 cells x 2 units x 4 runs)", len(seen), 2*2*4)
	}
	base := cellByID(t, block, CellBaseline)
	if !base.Headline {
		t.Errorf("baseline cell is not a headline after 4 runs: %+v", base)
	}
	if base.CacheReadTokens == 0 {
		t.Error("driver-captured cache-read tokens did not reach the block")
	}
}

// ---------------------------------------------------------------------------
// T041 — MCPMark per-task meta.json ingestion
// ---------------------------------------------------------------------------

func writeMeta(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "meta.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadMCPMarkMeta_HappyPath(t *testing.T) {
	path := writeMeta(t, `{
	  "task_name": "filesystem/find_file",
	  "execution_result": {"success": true},
	  "token_usage": {"input": 12000, "output": 340, "total": 12340, "reasoning": 0},
	  "turn_count": 7
	}`)
	meta, err := LoadMCPMarkMeta(path)
	if err != nil {
		t.Fatalf("LoadMCPMarkMeta: %v", err)
	}
	rec, err := meta.UnitRecord(CellRetrieveFull, "filesystem/find_file", 0)
	if err != nil {
		t.Fatalf("UnitRecord: %v", err)
	}
	if rec.Completion != CompletionPass {
		t.Errorf("Completion = %q, want %q", rec.Completion, CompletionPass)
	}
	if rec.Usage.InputTokens != 12000 || rec.Usage.OutputTokens != 340 {
		t.Errorf("usage = %+v, want input 12000 / output 340", rec.Usage)
	}
	if rec.TurnCount != 7 {
		t.Errorf("TurnCount = %d, want 7", rec.TurnCount)
	}
	// T042: the suite's own output has NO cache-read field. It must arrive as
	// unavailable, never as a zero that reads like a measurement.
	if rec.Usage.CacheReadTokens != nil {
		t.Errorf("CacheReadTokens = %v, want nil — meta.json has no cache-read field",
			*rec.Usage.CacheReadTokens)
	}
}

func TestLoadMCPMarkMeta_DefensiveParsing(t *testing.T) {
	t.Run("missing file is an error, never a zero", func(t *testing.T) {
		if _, err := LoadMCPMarkMeta(filepath.Join(t.TempDir(), "nope.json")); err == nil {
			t.Fatal("LoadMCPMarkMeta on a missing file returned no error")
		}
	})
	t.Run("malformed json is an error, never a zero", func(t *testing.T) {
		if _, err := LoadMCPMarkMeta(writeMeta(t, `{"execution_result":`)); err == nil {
			t.Fatal("LoadMCPMarkMeta on malformed JSON returned no error")
		}
	})
	t.Run("absent success verdict is no-signal, not failure", func(t *testing.T) {
		meta, err := LoadMCPMarkMeta(writeMeta(t, `{"execution_result": {}, "token_usage": {"input": 1, "output": 2}}`))
		if err != nil {
			t.Fatalf("LoadMCPMarkMeta: %v", err)
		}
		rec, err := meta.UnitRecord(CellRetrieveFull, "t1", 0)
		if err != nil {
			t.Fatalf("UnitRecord: %v", err)
		}
		if rec.Completion != CompletionNoSignal {
			t.Errorf("Completion = %q, want %q — an absent verdict is NOT a failure",
				rec.Completion, CompletionNoSignal)
		}
	})
	t.Run("absent token_usage is an error, never a zero cost", func(t *testing.T) {
		meta, err := LoadMCPMarkMeta(writeMeta(t, `{"execution_result": {"success": false}}`))
		if err != nil {
			t.Fatalf("LoadMCPMarkMeta: %v", err)
		}
		if _, err := meta.UnitRecord(CellRetrieveFull, "t1", 0); err == nil {
			t.Fatal("UnitRecord with no token_usage returned no error — a zero cost understates in our favour")
		}
	})
	t.Run("suffixed key spelling is accepted", func(t *testing.T) {
		meta, err := LoadMCPMarkMeta(writeMeta(t,
			`{"execution_result": {"success": false}, "token_usage": {"input_tokens": 5, "output_tokens": 6}}`))
		if err != nil {
			t.Fatalf("LoadMCPMarkMeta: %v", err)
		}
		rec, err := meta.UnitRecord(CellRetrieveFull, "t1", 0)
		if err != nil {
			t.Fatalf("UnitRecord: %v", err)
		}
		if rec.Usage.InputTokens != 5 || rec.Usage.OutputTokens != 6 {
			t.Errorf("usage = %+v, want input 5 / output 6", rec.Usage)
		}
		if rec.Completion != CompletionFail {
			t.Errorf("Completion = %q, want %q", rec.Completion, CompletionFail)
		}
	})
}

func TestLoadMCPMarkTrajectory_ClassifiesRetries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.json")
	msgs := []any{
		map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"id": "c1", "function": map[string]any{"name": "fs__read", "arguments": `{"p":"/a"}`}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "c1", "is_error": true, "content": "unknown argument p"},
		map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"id": "c2", "function": map[string]any{"name": "fs__read", "arguments": `{"path":"/a"}`}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "c2", "content": "ok"},
	}
	body, _ := json.Marshal(msgs)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	attempts, truncated, err := LoadMCPMarkTrajectory(path)
	if err != nil {
		t.Fatalf("LoadMCPMarkTrajectory: %v", err)
	}
	if truncated {
		t.Error("truncated = true for a complete trajectory")
	}
	if len(attempts) != 2 {
		t.Fatalf("got %d attempts, want 2: %+v", len(attempts), attempts)
	}
	outcomes := ClassifyIntents(attempts)
	if len(outcomes) != 1 {
		t.Fatalf("got %d intents, want 1: %+v", len(outcomes), outcomes)
	}
	if outcomes[0].FirstAttemptSuccess {
		t.Error("FirstAttemptSuccess = true for an errored first attempt")
	}
	if outcomes[0].RetriesCorrective != 1 {
		t.Errorf("RetriesCorrective = %d, want 1", outcomes[0].RetriesCorrective)
	}

	t.Run("a trailing unanswered call marks the record truncated", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "messages.json")
		body, _ := json.Marshal([]any{
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c1", "function": map[string]any{"name": "fs__read", "arguments": `{}`}},
			}},
		})
		if err := os.WriteFile(p, body, 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		attempts, truncated, err := LoadMCPMarkTrajectory(p)
		if err != nil {
			t.Fatalf("LoadMCPMarkTrajectory: %v", err)
		}
		if !truncated {
			t.Error("truncated = false for a trajectory whose last call has no result")
		}
		if len(attempts) != 1 || attempts[0].Outcome != AttemptOutcomeUnknown {
			t.Errorf("attempts = %+v, want one attempt with an unknown outcome", attempts)
		}
	})

	t.Run("malformed trajectory is an error", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "messages.json")
		if err := os.WriteFile(p, []byte(`{"not":"a list"`), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		if _, _, err := LoadMCPMarkTrajectory(p); err == nil {
			t.Fatal("LoadMCPMarkTrajectory on malformed JSON returned no error")
		}
	})
}

// ---------------------------------------------------------------------------
// T042 — input / output / cache-read stay separate; unavailable != zero
// ---------------------------------------------------------------------------

func TestAgentLoopBlockFor_CacheReadUnavailableIsRecordedNotZeroed(t *testing.T) {
	ok := []Attempt{testAttempt("a", 1, AttemptOutcomeOK, `{}`)}
	opts := testOpts()
	opts.Runs = 4

	var suiteOnly []UnitRecord
	for r := 0; r < 4; r++ {
		// nil cache-read: exactly what the suite's meta.json yields.
		suiteOnly = append(suiteOnly, testUnit(CellBaseline, "t1", r, CompletionPass, ok, 100+r, 10, nil))
	}
	block, err := AgentLoopBlockFor(suiteOnly, opts)
	if err != nil {
		t.Fatalf("AgentLoopBlockFor: %v", err)
	}
	c := cellByID(t, block, CellBaseline)
	if !c.Withheld {
		t.Fatal("cache-read was unavailable but the cell was not marked withheld — a silent zero")
	}
	if !strings.Contains(strings.ToLower(c.WithheldReason), "cache") {
		t.Errorf("WithheldReason = %q, want it to name the missing cache-read axis", c.WithheldReason)
	}
	if c.Headline {
		t.Error("Headline = true for a cell whose token accounting is missing an axis — that understates cost")
	}
	if c.InputTokens == 0 || c.OutputTokens == 0 {
		t.Errorf("input/output were dropped along with cache-read: %+v", c)
	}

	// With the driver supplying the axis, the same cell becomes a headline.
	var withCache []UnitRecord
	for r := 0; r < 4; r++ {
		withCache = append(withCache, testUnit(CellBaseline, "t1", r, CompletionPass, ok, 100+r, 10, intPtr(20)))
	}
	block2, err := AgentLoopBlockFor(withCache, opts)
	if err != nil {
		t.Fatalf("AgentLoopBlockFor: %v", err)
	}
	c2 := cellByID(t, block2, CellBaseline)
	if c2.Withheld {
		t.Errorf("cell withheld despite complete token accounting: %q", c2.WithheldReason)
	}
	if c2.CacheReadTokens != 80 {
		t.Errorf("CacheReadTokens = %d, want 80 (4 runs x 20)", c2.CacheReadTokens)
	}
	if !c2.Headline {
		t.Errorf("Headline = false for a complete 4-run cell: %+v", c2)
	}
	// Cache reads are real tokens and belong in the cost per completed task.
	if c2.TokensPerCompletedTask != 131.5 {
		t.Errorf("TokensPerCompletedTask = %v, want 131.5 (mean input 101.5 + output 10 + cache 20)",
			c2.TokensPerCompletedTask)
	}
}

func TestRunAgentLoop_DriverUsageHookMustSupplyCacheRead(t *testing.T) {
	opts := testOpts()
	driver := AgentLoopDriver{
		RunTask: func(cell ModeCell, unitID string, runIndex int) (*UnitRecord, error) {
			return &UnitRecord{
				CellID: cell.ID, UnitID: unitID, RunIndex: runIndex,
				Completion: CompletionPass,
				Attempts:   []Attempt{testAttempt("a", 1, AttemptOutcomeOK, `{}`)},
				Usage:      ProviderUsage{InputTokens: 10, OutputTokens: 1, Responses: 1},
			}, nil
		},
		// The hook exists to supply the one axis the suite cannot. Returning
		// no cache-read from it is a wiring bug, not an unavailable axis.
		CaptureUsage: func(ModeCell, string, int) (ProviderUsage, error) {
			return ProviderUsage{InputTokens: 10, OutputTokens: 1, Responses: 1}, nil
		},
	}
	if _, err := RunAgentLoop(driver, opts); !errors.Is(err, ErrDriverCacheReadMissing) {
		t.Errorf("RunAgentLoop: err = %v, want ErrDriverCacheReadMissing", err)
	}
}

// ---------------------------------------------------------------------------
// T043 — the block's accounting source names the provider AND the pinned model
// ---------------------------------------------------------------------------

func TestAgentLoopAccountingSource_RefusesAnUnpinnedModel(t *testing.T) {
	if _, err := AgentLoopAccountingSource(testProvider, ""); !errors.Is(err, ErrAgentLoopModelUnpinned) {
		t.Errorf("AgentLoopAccountingSource with no model: err = %v, want ErrAgentLoopModelUnpinned", err)
	}
	if _, err := AgentLoopAccountingSource("", testModel); err == nil {
		t.Error("AgentLoopAccountingSource with no provider returned no error")
	}
	src, err := AgentLoopAccountingSource(testProvider, testModel)
	if err != nil {
		t.Fatalf("AgentLoopAccountingSource: %v", err)
	}
	if src.Kind != AccountingKindProvider || src.Identity != testProvider || src.Model != testModel {
		t.Errorf("source = %+v, want provider-usage/%s/%s", src, testProvider, testModel)
	}
}

func TestAgentLoopBlockFor_EmitsPopulatedAccountingSource(t *testing.T) {
	ok := []Attempt{testAttempt("a", 1, AttemptOutcomeOK, `{}`)}
	opts := testOpts()
	block, err := AgentLoopBlockFor(
		[]UnitRecord{testUnit(CellBaseline, "t1", 0, CompletionPass, ok, 10, 1, intPtr(0))},
		opts,
	)
	if err != nil {
		t.Fatalf("AgentLoopBlockFor: %v", err)
	}
	if block.AccountingSource.IsZero() {
		t.Fatal("agent_loop block emitted with an unpopulated accounting_source")
	}
	if block.AccountingSource.Model != testModel {
		t.Errorf("accounting_source.model = %q, want the pinned model %q", block.AccountingSource.Model, testModel)
	}
	if block.Suite != "mcpmark" || block.SuiteVersion != "deadbeef" {
		t.Errorf("suite pin lost: %q/%q", block.Suite, block.SuiteVersion)
	}
	if block.FleetShape.ToolCount != 45 {
		t.Errorf("FleetShape = %+v, want the configured fleet shape", block.FleetShape)
	}
	for _, c := range block.Cells {
		if !IsValidProvenance(c.Provenance) {
			t.Errorf("cell %s carries provenance %q, not in the closed enum", c.CellID, c.Provenance)
		}
	}
	// The whole report envelope must validate.
	r2 := &ReportV2{AgentLoop: block}
	if err := r2.ValidateAdditiveBlocks(); err != nil {
		t.Errorf("ValidateAdditiveBlocks: %v", err)
	}

	// An unpinned model is refused at emission, not silently emitted.
	bad := testOpts()
	bad.Model = ""
	if _, err := AgentLoopBlockFor(
		[]UnitRecord{testUnit(CellBaseline, "t1", 0, CompletionPass, ok, 10, 1, intPtr(0))},
		bad,
	); !errors.Is(err, ErrAgentLoopModelUnpinned) {
		t.Errorf("AgentLoopBlockFor with an unpinned model: err = %v, want ErrAgentLoopModelUnpinned", err)
	}
}

func mustCell(t *testing.T, id string) ModeCell {
	t.Helper()
	c, ok := CellByID(id)
	if !ok {
		t.Fatalf("no mode cell %q", id)
	}
	return c
}

// The never-sum rule must hold WHERE THE ADDITION HAPPENS, not only in the
// standalone helper.
//
// Two reviewers independently found the same gap: AggregateTokenFigures was
// exercised by tests but no production path called it, while aggregateCell
// added token integers whose origin nothing checked and AgentLoopBlockFor then
// stamped the caller's provider onto the result. A tokenizer figure mixed into
// a provider run produced a plausible hybrid wearing a provider label.
func TestAgentLoopBlock_MixedAccountingSourcesIsWithheld(t *testing.T) {
	provider := AccountingSource{Kind: AccountingKindProvider, Identity: "anthropic", Model: "pinned-1"}
	tokenizer := AccountingSource{Kind: AccountingKindTokenizer, Identity: DefaultEncoding}

	rec := func(unit string, src AccountingSource, in, out int) UnitRecord {
		cr := 0
		return UnitRecord{
			CellID: CellDirectFull, UnitID: unit, RunIndex: 0,
			Completion: CompletionPass, Source: src,
			Usage: ProviderUsage{InputTokens: in, OutputTokens: out, CacheReadTokens: &cr},
		}
	}

	directCell, ok := CellByID(CellDirectFull)
	if !ok {
		t.Fatalf("cell %q must exist in the matrix", CellDirectFull)
	}
	block, err := AgentLoopBlockFor([]UnitRecord{
		rec("u1", provider, 100, 10),
		// Same cell, DIFFERENT source. Adding this to the sum above would
		// produce a hybrid the block would then label "provider-usage".
		rec("u2", tokenizer, 900, 90),
	}, AgentLoopOptions{
		Provider: "anthropic", Model: "pinned-1",
		Cells: []ModeCell{BaselineCell(), directCell},
	})
	if err != nil {
		t.Fatalf("AgentLoopBlockFor: %v", err)
	}

	var cell *AgentLoopCell
	for i := range block.Cells {
		if block.Cells[i].CellID == CellDirectFull {
			cell = &block.Cells[i]
		}
	}
	if cell == nil {
		t.Fatal("the direct_full cell must appear in the block")
	}
	if !cell.Withheld {
		t.Fatalf("a cell whose records mixed accounting sources must be WITHHELD, got "+
			"input=%d output=%d", cell.InputTokens, cell.OutputTokens)
	}
	if !strings.Contains(cell.WithheldReason, "accounting source") {
		t.Errorf("the reason must name the cause; got %q", cell.WithheldReason)
	}
	// And the partial total must not leak out as if it were the cell's cost.
	if cell.InputTokens != 0 || cell.OutputTokens != 0 {
		t.Errorf("a withheld mixed-source cell must not publish a partial total, got in=%d out=%d",
			cell.InputTokens, cell.OutputTokens)
	}
}

// SavingVsBaseline must DERIVE the regression, not trust the row's flag.
//
// It is documented as the only sanctioned way to turn two cells into a
// percentage, so its guards have to hold for any block handed to it — including
// one unmarshalled from a report.json or mutated after assembly.
func TestSavingVsBaseline_DerivesRegressionFromCompletionRates(t *testing.T) {
	// A hand-built block, as if loaded from disk: the cell completes half as
	// many tasks as the baseline, but its Regression flag says otherwise.
	block := &AgentLoopBlock{
		AccountingSource: AccountingSource{Kind: AccountingKindProvider, Identity: "anthropic", Model: "pinned-1"},
		Cells: []AgentLoopCell{
			{CellID: CellBaseline, Runs: 4, CompletionRatePct: 100, TokensPerCompletedTask: 1000, Provenance: ProvenanceMeasured},
			{CellID: CellDirectFull, Runs: 4, CompletionRatePct: 50, TokensPerCompletedTask: 100,
				Regression: false, Provenance: ProvenanceMeasured},
		},
	}

	v := block.SavingVsBaseline(CellDirectFull)
	if !v.Withheld {
		t.Fatalf("a cell completing 50%% against a 100%% baseline must not yield a saving; got %.1f%%",
			v.SavingPct)
	}
	if !v.Regression {
		t.Error("the verdict must mark the regression it derived, not echo the row's flag")
	}
	if v.SavingPct != 0 {
		t.Errorf("a withheld verdict must carry no percentage, got %.1f", v.SavingPct)
	}
}
