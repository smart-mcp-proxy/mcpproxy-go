package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Outcome classifies one mutation run. Only "detected" counts as a kill;
// everything else is reported on its own so a broken build or an unrelated
// failure can never masquerade as detection.
type Outcome string

const (
	OutcomeDetected         Outcome = "detected"          // a designated detecting test failed
	OutcomeSurvived         Outcome = "survived"          // every designated test passed
	OutcomeBuildError       Outcome = "build_error"       // the mutated tree does not compile
	OutcomeUnrelatedFailure Outcome = "unrelated_failure" // tests failed, but none of the designated ones
	OutcomeTimeout          Outcome = "timeout"
	OutcomeRunnerError      Outcome = "runner_error"    // worktree/apply/anchor problems
	OutcomeBaselineFailed   Outcome = "baseline_failed" // the unmutated tree failed the same tests
	OutcomeSkipped          Outcome = "skipped"         // excluded by --only
)

// Mutation is one entry of the versioned mutation set.
type Mutation struct {
	ID string `json:"id"`
	// Title says what policy the mutation violates.
	Title string `json:"title"`
	// Requirement is the pilot requirement the mutation attacks.
	Requirement string `json:"requirement"`
	File        string `json:"file"`
	// Find must occur EXACTLY once in File; it is replaced by Replace.
	Find    string `json:"find"`
	Replace string `json:"replace"`
	// Required mutations must be detected for the gate to pass. A
	// non-required mutation is reported only (documented exclusion).
	Required bool `json:"required"`
	// Exclusion explains why a mutation is not required.
	Exclusion string `json:"exclusion,omitempty"`
	// Tests are the detecting tests: packages plus a -run regex, and the
	// test names whose failure counts as detection.
	Tests []MutationTest `json:"tests"`
}

type MutationTest struct {
	Package string   `json:"package"`
	Run     string   `json:"run"`
	Detect  []string `json:"detect"`
}

// MutationSet is the versioned file.
type MutationSet struct {
	Version   int        `json:"version"`
	Mutations []Mutation `json:"mutations"`
}

type MutationResult struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Requirement string        `json:"requirement"`
	Required    bool          `json:"required"`
	Exclusion   string        `json:"exclusion,omitempty"`
	Outcome     Outcome       `json:"outcome"`
	Detail      string        `json:"detail,omitempty"`
	FailedTests []string      `json:"failed_tests,omitempty"`
	Duration    time.Duration `json:"duration_ns"`
	DurationStr string        `json:"duration"`
}

type MutationReport struct {
	GeneratedAt     time.Time        `json:"generated_at"`
	SetVersion      int              `json:"set_version"`
	Repo            string           `json:"repo"`
	Commit          string           `json:"commit"`
	BaselineOutcome string           `json:"baseline_outcome"`
	BaselineDetail  string           `json:"baseline_detail,omitempty"`
	Results         []MutationResult `json:"results"`
	Detected        int              `json:"detected"`
	Survived        int              `json:"survived"`
	Other           int              `json:"other"`
	TotalDuration   string           `json:"total_duration"`
}

var failLine = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+)`)

func runMutate(args []string) error {
	fs := flag.NewFlagSet("mutate", flag.ContinueOnError)
	repo := fs.String("repo", ".", "repository checkout to mutate (a disposable worktree of its HEAD is used)")
	setFile := fs.String("mutations", "tests/qapilot/mutations.json", "mutation set")
	out := fs.String("out", "", "write the mutation report here")
	only := fs.String("only", "", "comma-separated mutation ids to run (others reported as skipped)")
	timeout := fs.Duration("timeout", 10*time.Minute, "per-test-run timeout")
	keep := fs.Bool("keep-worktrees", false, "keep mutated worktrees for inspection")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data, err := os.ReadFile(*setFile)
	if err != nil {
		return err
	}
	var set MutationSet
	if err := json.Unmarshal(data, &set); err != nil {
		return fmt.Errorf("mutation set: %w", err)
	}
	repoAbs, err := filepath.Abs(*repo)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	if *only != "" {
		for _, id := range strings.Split(*only, ",") {
			selected[strings.TrimSpace(id)] = true
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A gate that evaluates nothing must not pass.
	selectedCount := 0
	for _, m := range set.Mutations {
		if len(selected) == 0 || selected[m.ID] {
			selectedCount++
			if len(m.Tests) == 0 {
				return fmt.Errorf("mutation %s has no detecting tests", m.ID)
			}
			for _, t := range m.Tests {
				if len(t.Detect) == 0 {
					return fmt.Errorf("mutation %s: test entry for %s names no detecting test", m.ID, t.Package)
				}
			}
		}
	}
	if selectedCount == 0 {
		return fmt.Errorf("no mutations selected (set has %d, --only=%q)", len(set.Mutations), *only)
	}
	for id := range selected {
		found := false
		for _, m := range set.Mutations {
			if m.ID == id {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("--only names unknown mutation %q", id)
		}
	}

	start := time.Now()
	rep := MutationReport{GeneratedAt: start.UTC(), SetVersion: set.Version, Repo: repoAbs}
	rep.Commit = gitOutput(ctx, repoAbs, "rev-parse", "HEAD")

	// Uncommitted changes are invisible to `git worktree add HEAD`: refuse
	// to run against them silently.
	if dirty := gitOutput(ctx, repoAbs, "status", "--porcelain", "--untracked-files=no"); dirty != "" {
		return fmt.Errorf("repository has uncommitted tracked changes; commit them first so the mutated copy is HEAD (%d lines)", strings.Count(dirty, "\n")+1)
	}

	// Baseline: the union of every selected mutation's tests must pass on
	// the unmutated tree, in a disposable worktree too (same environment).
	baseWT, cleanupBase, err := addWorktree(ctx, repoAbs)
	if err != nil {
		return err
	}
	defer cleanupBase(*keep)
	baselineTests := map[string]MutationTest{}
	for _, m := range set.Mutations {
		if len(selected) > 0 && !selected[m.ID] {
			continue
		}
		for _, t := range m.Tests {
			key := t.Package + "|" + t.Run
			merged, ok := baselineTests[key]
			if !ok {
				merged = MutationTest{Package: t.Package, Run: t.Run}
			}
			// Union the designated tests of every mutation sharing this
			// package/run pair, so the "did it run" check covers all of them.
			for _, d := range t.Detect {
				dup := false
				for _, have := range merged.Detect {
					if have == d {
						dup = true
					}
				}
				if !dup {
					merged.Detect = append(merged.Detect, d)
				}
			}
			baselineTests[key] = merged
		}
	}
	rep.BaselineOutcome = "passed"
	for _, t := range baselineTests {
		outcome, detail, _, ran := runTestsVerbose(ctx, baseWT, t, *timeout)
		if outcome != OutcomeSurvived { // "survived" == all tests passed
			rep.BaselineOutcome = string(OutcomeBaselineFailed)
			rep.BaselineDetail += fmt.Sprintf("%s -run %s: %s: %s\n", t.Package, t.Run, outcome, detail)
			continue
		}
		// Every designated detecting test must have RUN in the baseline: a
		// renamed or filtered-out test would otherwise make its mutation
		// undetectable while the gate reports a clean baseline.
		for _, d := range t.Detect {
			if !ran[d] {
				rep.BaselineOutcome = string(OutcomeBaselineFailed)
				rep.BaselineDetail += fmt.Sprintf("%s -run %s: designated test %q did not run in the baseline\n", t.Package, t.Run, d)
			}
		}
	}
	cleanupBase(*keep)

	for _, m := range set.Mutations {
		res := MutationResult{ID: m.ID, Title: m.Title, Requirement: m.Requirement, Required: m.Required, Exclusion: m.Exclusion}
		mStart := time.Now()
		switch {
		case len(selected) > 0 && !selected[m.ID]:
			res.Outcome = OutcomeSkipped
		case rep.BaselineOutcome != "passed":
			res.Outcome = OutcomeBaselineFailed
			res.Detail = "baseline did not pass; mutation not evaluated"
		case ctx.Err() != nil:
			res.Outcome = OutcomeRunnerError
			res.Detail = "cancelled"
		default:
			res.Outcome, res.Detail, res.FailedTests = runMutation(ctx, repoAbs, m, *timeout, *keep)
		}
		res.Duration = time.Since(mStart)
		res.DurationStr = res.Duration.Round(time.Millisecond).String()
		switch res.Outcome {
		case OutcomeDetected:
			rep.Detected++
		case OutcomeSurvived:
			rep.Survived++
		case OutcomeSkipped:
		default:
			rep.Other++
		}
		fmt.Fprintf(os.Stderr, "mutation %-4s %-18s %-8s %s\n", m.ID, res.Outcome, res.DurationStr, m.Title)
		if res.Detail != "" && res.Outcome != OutcomeDetected {
			fmt.Fprintf(os.Stderr, "             %s\n", strings.ReplaceAll(strings.TrimSpace(res.Detail), "\n", "\n             "))
		}
		rep.Results = append(rep.Results, res)
	}
	rep.TotalDuration = time.Since(start).Round(time.Second).String()

	enc, _ := json.MarshalIndent(rep, "", "  ")
	if *out != "" {
		if err := os.WriteFile(*out, enc, 0o644); err != nil {
			return err
		}
	} else {
		fmt.Println(string(enc))
	}
	fmt.Fprintf(os.Stderr, "mutations: detected=%d survived=%d other=%d baseline=%s total=%s\n", rep.Detected, rep.Survived, rep.Other, rep.BaselineOutcome, rep.TotalDuration)

	var failures []string
	if rep.BaselineOutcome != "passed" {
		failures = append(failures, "baseline failed")
	}
	for _, r := range rep.Results {
		switch r.Outcome {
		case OutcomeDetected, OutcomeSkipped:
		case OutcomeSurvived:
			if r.Required {
				failures = append(failures, r.ID+" survived")
			}
		default:
			failures = append(failures, r.ID+" "+string(r.Outcome))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("mutation gate failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

// runMutation applies one mutation in a fresh worktree and runs its tests.
func runMutation(ctx context.Context, repo string, m Mutation, timeout time.Duration, keep bool) (Outcome, string, []string) {
	wt, cleanup, err := addWorktree(ctx, repo)
	if err != nil {
		return OutcomeRunnerError, err.Error(), nil
	}
	defer cleanup(keep)
	path := filepath.Join(wt, m.File)
	src, err := os.ReadFile(path)
	if err != nil {
		return OutcomeRunnerError, err.Error(), nil
	}
	if n := bytes.Count(src, []byte(m.Find)); n != 1 {
		return OutcomeRunnerError, fmt.Sprintf("anchor occurs %d times in %s (must be exactly 1); the mutation set needs updating", n, m.File), nil
	}
	mutated := bytes.Replace(src, []byte(m.Find), []byte(m.Replace), 1)
	if bytes.Equal(mutated, src) {
		return OutcomeRunnerError, "mutation is a no-op", nil
	}
	if err := os.WriteFile(path, mutated, 0o644); err != nil {
		return OutcomeRunnerError, err.Error(), nil
	}
	// Compile first so a build failure is never counted as detection.
	for _, t := range m.Tests {
		if out, err := runCmd(ctx, wt, timeout, "go", "vet", t.Package); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return OutcomeTimeout, "go vet timed out", nil
			}
			return OutcomeBuildError, truncate(out, 2000), nil
		}
	}
	detectedAny := false
	var allFailed []string
	var details []string
	for _, t := range m.Tests {
		outcome, detail, failed := runTests(ctx, wt, t, timeout)
		switch outcome {
		case OutcomeTimeout, OutcomeRunnerError, OutcomeBuildError:
			return outcome, detail, failed
		}
		allFailed = append(allFailed, failed...)
		for _, f := range failed {
			for _, d := range t.Detect {
				if f == d || strings.HasPrefix(f, d+"/") {
					detectedAny = true
				}
			}
		}
		if len(failed) > 0 {
			details = append(details, truncate(detail, 1500))
		}
	}
	switch {
	case detectedAny:
		return OutcomeDetected, strings.Join(details, "\n"), allFailed
	case len(allFailed) > 0:
		return OutcomeUnrelatedFailure, strings.Join(details, "\n"), allFailed
	default:
		return OutcomeSurvived, "", nil
	}
}

// runTests runs one package's designated tests. It returns Survived when
// everything passed (no failures), UnrelatedFailure with the failed test
// names when something failed (the caller decides whether they are the
// designated ones), BuildError, Timeout or RunnerError.
func runTests(ctx context.Context, dir string, t MutationTest, timeout time.Duration) (Outcome, string, []string) {
	args := []string{"test", t.Package, "-run", t.Run, "-count=1", "-timeout", timeout.String()}
	out, err := runCmd(ctx, dir, timeout+time.Minute, "go", args...)
	if strings.Contains(out, "no tests to run") {
		return OutcomeRunnerError, "no tests matched -run " + t.Run + " in " + t.Package, nil
	}
	if err == nil {
		return OutcomeSurvived, "", nil
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(out, "panic: test timed out") {
		return OutcomeTimeout, truncate(out, 2000), nil
	}
	if strings.Contains(out, "[build failed]") || strings.Contains(out, "setup failed") {
		return OutcomeBuildError, truncate(out, 2000), nil
	}
	var failed []string
	for _, m := range failLine.FindAllStringSubmatch(out, -1) {
		failed = append(failed, m[1])
	}
	if len(failed) == 0 {
		return OutcomeRunnerError, "go test failed without a --- FAIL line: " + truncate(out, 2000), nil
	}
	return OutcomeUnrelatedFailure, out, failed
}

var runLine = regexp.MustCompile(`(?m)^=== RUN\s+(\S+)`)

// runTestsVerbose is runTests with -v, returning the set of test names that
// ran (top-level and subtests), for the baseline's "did the designated test
// actually run" check.
func runTestsVerbose(ctx context.Context, dir string, t MutationTest, timeout time.Duration) (Outcome, string, []string, map[string]bool) {
	args := []string{"test", t.Package, "-run", t.Run, "-count=1", "-v", "-timeout", timeout.String()}
	out, err := runCmd(ctx, dir, timeout+time.Minute, "go", args...)
	ran := map[string]bool{}
	for _, m := range runLine.FindAllStringSubmatch(out, -1) {
		ran[m[1]] = true
	}
	if strings.Contains(out, "no tests to run") || len(ran) == 0 {
		return OutcomeRunnerError, "no tests ran for -run " + t.Run + " in " + t.Package, nil, ran
	}
	if err == nil {
		return OutcomeSurvived, "", nil, ran
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(out, "panic: test timed out") {
		return OutcomeTimeout, truncate(out, 2000), nil, ran
	}
	if strings.Contains(out, "[build failed]") || strings.Contains(out, "setup failed") {
		return OutcomeBuildError, truncate(out, 2000), nil, ran
	}
	var failed []string
	for _, m := range failLine.FindAllStringSubmatch(out, -1) {
		failed = append(failed, m[1])
	}
	return OutcomeUnrelatedFailure, truncate(out, 4000), failed, ran
}

func addWorktree(ctx context.Context, repo string) (string, func(keep bool), error) {
	dir, err := os.MkdirTemp("", "qapilot-mutant-*")
	if err != nil {
		return "", nil, err
	}
	wt := filepath.Join(dir, "wt")
	if out, err := runCmd(ctx, repo, 2*time.Minute, "git", "worktree", "add", "--detach", wt, "HEAD"); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("git worktree add: %v: %s", err, out)
	}
	var done bool
	cleanup := func(keep bool) {
		if done {
			return
		}
		done = true
		if keep {
			fmt.Fprintf(os.Stderr, "keeping worktree %s\n", wt)
			return
		}
		// Use a fresh context: cleanup must run even after cancellation.
		_, _ = runCmd(context.Background(), repo, 2*time.Minute, "git", "worktree", "remove", "--force", wt)
		_ = os.RemoveAll(dir)
	}
	return wt, cleanup, nil
}

func runCmd(ctx context.Context, dir string, timeout time.Duration, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	// On cancellation kill the whole process group (go test spawns the test
	// binary as a child) and never wait on its pipes for more than 15s, so a
	// cancelled run cannot delay the deferred worktree cleanup indefinitely.
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = 15 * time.Second
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if cctx.Err() != nil {
		return buf.String(), context.DeadlineExceeded
	}
	return buf.String(), err
}

func gitOutput(ctx context.Context, repo string, args ...string) string {
	out, _ := runCmd(ctx, repo, time.Minute, "git", args...)
	return strings.TrimSpace(out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(truncated)"
}
