package main

import (
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/qapilot"
)

// fullLedger fabricates a ledger in which every mandatory check passed.
func fullLedger(status string) []qapilot.Check {
	var checks []qapilot.Check
	now := time.Now()
	for _, sc := range qapilot.Scenarios {
		checks = append(checks, qapilot.Check{ID: sc.ID, Requirements: sc.Requirements, Test: "TestQAPilot_Scenarios/" + sc.ID, Package: "runtime", Status: status, FinishedAt: now})
	}
	reqFor := map[string]string{
		"fixture/verdicts": "QP-19", "admission/table": "QP-01", "authz/table": "QP-05", "invariant/table": "QP-13",
		"mcp/no-unquarantine-op": "QP-06", "mcp/patch-ignores-unquarantine": "QP-06", "dispatch/counting-upstream": "QP-16",
		"security-approve/table": "QP-04", "review-binding/stale-hash": "QP-17", "incarnation/server-replaced": "QP-18",
	}
	for _, id := range qapilot.MandatoryCheckIDs() {
		req := reqFor[id]
		if req == "" {
			req = "QP-14"
		}
		checks = append(checks, qapilot.Check{ID: id, Requirements: []string{req}, Test: "T", Package: "p", Status: status, FinishedAt: now})
	}
	return checks
}

func TestBuildReport_AllPassed(t *testing.T) {
	rep := buildReport(fullLedger("passed"), nil, nil)
	if rep.Verdict != "pass" {
		t.Fatalf("verdict: want pass, got %s: %+v", rep.Verdict, rep.Gaps)
	}
	for _, r := range rep.Requirements {
		if r.Status != "covered" {
			t.Fatalf("%s: want covered, got %s", r.ID, r.Status)
		}
	}
	if rep.DecisionCells.Passed != len(qapilot.ClassifyCells()) {
		t.Fatalf("decision cells: %d/%d", rep.DecisionCells.Passed, rep.DecisionCells.Total)
	}
}

func TestBuildReport_EmptyLedgerFails(t *testing.T) {
	rep := buildReport(nil, nil, nil)
	if rep.Verdict != "fail" || !rep.Gaps.LedgerEmpty {
		t.Fatalf("an empty ledger must fail: %+v", rep.Gaps)
	}
	if len(rep.Gaps.MissingScenarios) != len(qapilot.Scenarios) {
		t.Fatalf("every scenario must be reported missing")
	}
}

func TestBuildReport_SkippedScenarioFails(t *testing.T) {
	checks := fullLedger("passed")
	for i := range checks {
		if checks[i].ID == "S01" {
			checks[i].Status = "skipped"
		}
	}
	rep := buildReport(checks, nil, nil)
	if rep.Verdict != "fail" || len(rep.Gaps.SkippedScenarios) != 1 {
		t.Fatalf("a skipped mandatory scenario must fail the gate: %+v", rep.Gaps)
	}
}

func TestBuildReport_MissingCellFails(t *testing.T) {
	checks := fullLedger("passed")
	var kept []qapilot.Check
	for _, c := range checks {
		if c.ID == qapilot.ClassifyCells()[0].ID {
			continue
		}
		kept = append(kept, c)
	}
	rep := buildReport(kept, nil, nil)
	if rep.Verdict != "fail" || rep.Gaps.MissingDecisionCells != 1 {
		t.Fatalf("a missing decision cell must fail the gate: %+v", rep.Gaps)
	}
}

func TestBuildReport_MutationAccounting(t *testing.T) {
	mut := &MutationReport{BaselineOutcome: "passed", Results: []MutationResult{
		{ID: "M01", Required: true, Outcome: OutcomeDetected},
		{ID: "M02", Required: true, Outcome: OutcomeSurvived},
		{ID: "M03", Required: false, Outcome: OutcomeSurvived},
		{ID: "M04", Required: true, Outcome: OutcomeBuildError},
	}}
	rep := buildReport(fullLedger("passed"), mut, nil)
	if rep.Verdict != "fail" {
		t.Fatal("a surviving required mutation must fail the gate")
	}
	if len(rep.Gaps.SurvivingMutations) != 1 || rep.Gaps.SurvivingMutations[0] != "M02" {
		t.Fatalf("only the required survivor counts: %v", rep.Gaps.SurvivingMutations)
	}
	if len(rep.Gaps.MutationInfrastructure) != 1 {
		t.Fatalf("a build error is infrastructure, not detection: %v", rep.Gaps.MutationInfrastructure)
	}
	mut.Results[1].Outcome = OutcomeDetected
	mut.Results[3].Outcome = OutcomeDetected
	if rep := buildReport(fullLedger("passed"), mut, nil); rep.Verdict != "pass" {
		t.Fatalf("all required detected must pass: %+v", rep.Gaps)
	}
	mut.BaselineOutcome = "baseline_failed"
	if rep := buildReport(fullLedger("passed"), mut, nil); rep.Verdict != "fail" {
		t.Fatal("a failed baseline must fail the gate")
	}
}

func TestBuildReport_IncompleteMutationReportFails(t *testing.T) {
	required := []string{"M01", "M02"}
	detected := func(id string) MutationResult {
		return MutationResult{ID: id, Required: true, Outcome: OutcomeDetected}
	}

	cases := map[string]*MutationReport{
		"empty report":     {BaselineOutcome: "passed"},
		"partial report":   {BaselineOutcome: "passed", Results: []MutationResult{detected("M01")}},
		"required skipped": {BaselineOutcome: "passed", Results: []MutationResult{detected("M01"), {ID: "M02", Required: true, Outcome: OutcomeSkipped}}},
		"missing baseline": {Results: []MutationResult{detected("M01"), detected("M02")}},
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			if rep := buildReport(fullLedger("passed"), mut, required); rep.Verdict != "fail" {
				t.Fatalf("%s must fail the gate: %+v", name, rep.Gaps)
			}
		})
	}

	full := &MutationReport{BaselineOutcome: "passed", Results: []MutationResult{detected("M01"), detected("M02"), {ID: "M03", Outcome: OutcomeSkipped}}}
	if rep := buildReport(fullLedger("passed"), full, required); rep.Verdict != "pass" {
		t.Fatalf("every required mutation detected (optional one skipped) must pass: %+v", rep.Gaps)
	}
}
