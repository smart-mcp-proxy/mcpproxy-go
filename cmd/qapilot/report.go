package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/qapilot"
)

// CoverageReport is the machine-readable pilot report.
type CoverageReport struct {
	GeneratedAt time.Time `json:"generated_at"`
	Verdict     string    `json:"verdict"` // pass | fail
	// Requirements maps every requirement id to the checks that evidence it.
	Requirements []RequirementCoverage `json:"requirements"`
	// Scenarios maps every mandatory scenario to its outcome.
	Scenarios []CheckOutcome `json:"scenarios"`
	// DecisionCells is the dispatch decision table: one outcome per cell.
	DecisionCells CellSummary `json:"decision_cells"`
	// StandaloneChecks are the non-scenario mandatory checks.
	StandaloneChecks []CheckOutcome `json:"standalone_checks"`
	// Gaps are reported separately by kind so a reader can tell "not run"
	// from "failed" from "skipped" from "infrastructure".
	Gaps Gaps `json:"gaps"`
	// Mutation is the mutation harness result, when a report was supplied.
	Mutation *MutationReport `json:"mutation,omitempty"`
	// KnownDeviations lists the recorded implementation/policy deviations
	// (informational, never gating).
	KnownDeviations []string `json:"known_deviations"`
	TotalChecks     int      `json:"total_checks"`
}

type RequirementCoverage struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Sources  []string `json:"sources"`
	Status   string   `json:"status"` // covered | failed | skipped | unexecuted
	Checks   []string `json:"checks"`
	Failed   []string `json:"failed,omitempty"`
	Skipped  []string `json:"skipped,omitempty"`
	Note     string   `json:"note,omitempty"`
	Mandator bool     `json:"mandatory"`
}

type CheckOutcome struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status"` // passed | failed | skipped | unexecuted
	Test   string `json:"test,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type CellSummary struct {
	Total      int      `json:"total"`
	Passed     int      `json:"passed"`
	Failed     []string `json:"failed,omitempty"`
	Skipped    []string `json:"skipped,omitempty"`
	Unexecuted []string `json:"unexecuted,omitempty"`
}

type Gaps struct {
	UnexecutedRequirements []string `json:"unexecuted_requirements"`
	FailedRequirements     []string `json:"failed_requirements"`
	MissingScenarios       []string `json:"missing_scenarios"`
	FailedScenarios        []string `json:"failed_scenarios"`
	SkippedScenarios       []string `json:"skipped_scenarios"`
	MissingDecisionCells   int      `json:"missing_decision_cells"`
	MissingStandalone      []string `json:"missing_standalone_checks"`
	SkippedStandalone      []string `json:"skipped_standalone_checks"`
	FailedStandalone       []string `json:"failed_standalone_checks"`
	SurvivingMutations     []string `json:"surviving_mutations"`
	MutationInfrastructure []string `json:"mutation_infrastructure_failures"`
	LedgerEmpty            bool     `json:"ledger_empty"`
}

func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	dir := fs.String("dir", "", "ledger directory (QAPILOT_REPORT_DIR)")
	mutFile := fs.String("mutation-report", "", "mutation report JSON to merge")
	out := fs.String("out", "", "write the coverage report to this file (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("--dir is required")
	}
	checks, err := qapilot.ReadChecks(*dir)
	if err != nil {
		return err
	}
	var mut *MutationReport
	if *mutFile != "" {
		data, err := os.ReadFile(*mutFile)
		if err != nil {
			return fmt.Errorf("mutation report: %w", err)
		}
		mut = &MutationReport{}
		if err := json.Unmarshal(data, mut); err != nil {
			return fmt.Errorf("mutation report: %w", err)
		}
	}
	rep := buildReport(checks, mut)
	data, _ := json.MarshalIndent(rep, "", "  ")
	if *out != "" {
		if err := os.WriteFile(*out, data, 0o644); err != nil {
			return err
		}
	} else {
		fmt.Println(string(data))
	}
	printSummary(rep)
	if rep.Verdict != "pass" {
		return fmt.Errorf("pilot gate failed: see gaps")
	}
	return nil
}

// buildReport is pure so it can be unit-tested.
func buildReport(checks []qapilot.Check, mut *MutationReport) CoverageReport {
	rep := CoverageReport{GeneratedAt: time.Now().UTC(), TotalChecks: len(checks)}
	rep.Gaps.LedgerEmpty = len(checks) == 0
	// Index checks by id; a check that ran more than once (several packages
	// or subtests) is failed if any run failed, skipped if all runs skipped.
	byID := map[string][]qapilot.Check{}
	byReq := map[string][]qapilot.Check{}
	for _, c := range checks {
		byID[c.ID] = append(byID[c.ID], c)
		for _, r := range c.Requirements {
			byReq[r] = append(byReq[r], c)
		}
	}
	status := func(runs []qapilot.Check) (string, string) {
		if len(runs) == 0 {
			return "unexecuted", ""
		}
		anyPass, allSkip := false, true
		for _, r := range runs {
			switch r.Status {
			case "failed":
				return "failed", r.Test
			case "passed":
				anyPass, allSkip = true, false
			default:
				// skipped
			}
		}
		if allSkip {
			return "skipped", runs[0].Test
		}
		if anyPass {
			return "passed", runs[0].Test
		}
		return "unexecuted", ""
	}

	for _, req := range qapilot.Requirements {
		rc := RequirementCoverage{ID: req.ID, Title: req.Title, Sources: req.Sources, Note: req.Note, Mandator: req.Mandatory}
		ids := map[string]bool{}
		for _, c := range byReq[req.ID] {
			if ids[c.ID] {
				continue
			}
			ids[c.ID] = true
			rc.Checks = append(rc.Checks, c.ID)
			st, _ := status(byID[c.ID])
			switch st {
			case "failed":
				rc.Failed = append(rc.Failed, c.ID)
			case "skipped":
				rc.Skipped = append(rc.Skipped, c.ID)
			}
		}
		sort.Strings(rc.Checks)
		passed := 0
		for _, id := range rc.Checks {
			if st, _ := status(byID[id]); st == "passed" {
				passed++
			}
		}
		switch {
		case len(rc.Failed) > 0:
			rc.Status = "failed"
			rep.Gaps.FailedRequirements = append(rep.Gaps.FailedRequirements, req.ID)
		case passed > 0:
			rc.Status = "covered"
		case len(rc.Skipped) > 0:
			rc.Status = "skipped"
			rep.Gaps.UnexecutedRequirements = append(rep.Gaps.UnexecutedRequirements, req.ID)
		default:
			rc.Status = "unexecuted"
			rep.Gaps.UnexecutedRequirements = append(rep.Gaps.UnexecutedRequirements, req.ID)
		}
		// Collapse the 896 classify cells to one line in the requirement view.
		rc.Checks = collapseCells(rc.Checks)
		rc.Failed = collapseCells(rc.Failed)
		rc.Skipped = collapseCells(rc.Skipped)
		rep.Requirements = append(rep.Requirements, rc)
	}

	for _, sc := range qapilot.Scenarios {
		st, test := status(byID[sc.ID])
		rep.Scenarios = append(rep.Scenarios, CheckOutcome{ID: sc.ID, Title: sc.Title, Status: st, Test: test})
		switch st {
		case "unexecuted":
			rep.Gaps.MissingScenarios = append(rep.Gaps.MissingScenarios, sc.ID)
		case "failed":
			rep.Gaps.FailedScenarios = append(rep.Gaps.FailedScenarios, sc.ID)
		case "skipped":
			rep.Gaps.SkippedScenarios = append(rep.Gaps.SkippedScenarios, sc.ID)
		}
	}

	for _, id := range qapilot.MandatoryCheckIDs() {
		st, test := status(byID[id])
		if strings.HasPrefix(id, "classify/") {
			rep.DecisionCells.Total++
			switch st {
			case "passed":
				rep.DecisionCells.Passed++
			case "failed":
				rep.DecisionCells.Failed = append(rep.DecisionCells.Failed, id)
			case "skipped":
				rep.DecisionCells.Skipped = append(rep.DecisionCells.Skipped, id)
			default:
				rep.DecisionCells.Unexecuted = append(rep.DecisionCells.Unexecuted, id)
			}
			continue
		}
		rep.StandaloneChecks = append(rep.StandaloneChecks, CheckOutcome{ID: id, Status: st, Test: test})
		switch st {
		case "unexecuted":
			rep.Gaps.MissingStandalone = append(rep.Gaps.MissingStandalone, id)
		case "skipped":
			rep.Gaps.SkippedStandalone = append(rep.Gaps.SkippedStandalone, id)
		case "failed":
			rep.Gaps.FailedStandalone = append(rep.Gaps.FailedStandalone, id)
		}
	}
	rep.Gaps.MissingDecisionCells = len(rep.DecisionCells.Unexecuted) + len(rep.DecisionCells.Skipped)

	for _, d := range qapilot.KnownDeviations {
		rep.KnownDeviations = append(rep.KnownDeviations, d.ID+": "+d.Title)
	}

	if mut != nil {
		rep.Mutation = mut
		for _, m := range mut.Results {
			switch m.Outcome {
			case OutcomeSurvived:
				if m.Required {
					rep.Gaps.SurvivingMutations = append(rep.Gaps.SurvivingMutations, m.ID)
				}
			case OutcomeBuildError, OutcomeUnrelatedFailure, OutcomeTimeout, OutcomeRunnerError, OutcomeBaselineFailed:
				rep.Gaps.MutationInfrastructure = append(rep.Gaps.MutationInfrastructure, m.ID+": "+string(m.Outcome))
			}
		}
		if mut.BaselineOutcome != "" && mut.BaselineOutcome != "passed" {
			rep.Gaps.MutationInfrastructure = append(rep.Gaps.MutationInfrastructure, "baseline: "+mut.BaselineOutcome)
		}
	}

	g := rep.Gaps
	rep.Verdict = "pass"
	if g.LedgerEmpty || len(g.UnexecutedRequirements) > 0 || len(g.FailedRequirements) > 0 ||
		len(g.MissingScenarios) > 0 || len(g.FailedScenarios) > 0 || len(g.SkippedScenarios) > 0 ||
		g.MissingDecisionCells > 0 || len(rep.DecisionCells.Failed) > 0 ||
		len(g.MissingStandalone) > 0 || len(g.SkippedStandalone) > 0 || len(g.FailedStandalone) > 0 ||
		len(g.SurvivingMutations) > 0 || len(g.MutationInfrastructure) > 0 {
		rep.Verdict = "fail"
	}
	return rep
}

func collapseCells(ids []string) []string {
	var out []string
	cells := 0
	for _, id := range ids {
		if strings.HasPrefix(id, "classify/") {
			cells++
			continue
		}
		out = append(out, id)
	}
	if cells > 0 {
		out = append(out, fmt.Sprintf("classify/* (%d cells)", cells))
	}
	return out
}

func printSummary(rep CoverageReport) {
	fmt.Fprintf(os.Stderr, "qapilot report: verdict=%s checks=%d\n", rep.Verdict, rep.TotalChecks)
	for _, r := range rep.Requirements {
		fmt.Fprintf(os.Stderr, "  %-6s %-10s %s\n", r.ID, r.Status, strings.Join(r.Checks, ", "))
	}
	fmt.Fprintf(os.Stderr, "  decision cells: %d/%d passed\n", rep.DecisionCells.Passed, rep.DecisionCells.Total)
	g := rep.Gaps
	if rep.Verdict != "pass" {
		fmt.Fprintf(os.Stderr, "  GAPS: ledger_empty=%v unexecuted_req=%v failed_req=%v missing_scn=%v failed_scn=%v skipped_scn=%v missing_cells=%d missing_checks=%v skipped_checks=%v failed_checks=%v surviving_mutations=%v mutation_infra=%v\n",
			g.LedgerEmpty, g.UnexecutedRequirements, g.FailedRequirements, g.MissingScenarios, g.FailedScenarios, g.SkippedScenarios, g.MissingDecisionCells, g.MissingStandalone, g.SkippedStandalone, g.FailedStandalone, g.SurvivingMutations, g.MutationInfrastructure)
	}
	if rep.Mutation != nil {
		fmt.Fprintf(os.Stderr, "  mutations: baseline=%s", rep.Mutation.BaselineOutcome)
		for _, m := range rep.Mutation.Results {
			fmt.Fprintf(os.Stderr, " %s=%s", m.ID, m.Outcome)
		}
		fmt.Fprintln(os.Stderr)
	}
}

func runList(_ []string) error {
	for _, r := range qapilot.Requirements {
		fmt.Printf("%s\t%s\n", r.ID, r.Title)
	}
	for _, s := range qapilot.Scenarios {
		fmt.Printf("%s\t%s\n", s.ID, s.Title)
	}
	return nil
}
