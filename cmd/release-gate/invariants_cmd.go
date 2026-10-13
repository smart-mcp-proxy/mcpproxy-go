package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/gatereport"
)

// cmdInvariants runs the US2 invariant checks against the live instance the
// matrix left running (FR-011/012/013), then the upgrade-in-place check
// (FR-014) on its own scratch instances, and finally tears everything down.
func cmdInvariants(ctx context.Context, args []string) (bool, error) {
	fs := flag.NewFlagSet("invariants", flag.ExitOnError)
	stateFile := fs.String("state-file", "", "state file written by `matrix --state-file` (required)")
	reportDir := fs.String("report-dir", "", "directory for report fragments (required)")
	prevBinary := fs.String("prev-binary", "", "previous released mcpproxy binary (skips the GitHub download)")
	upgradeRepo := fs.String("upgrade-repo", defaultUpgradeRepo, "GitHub repo to resolve the latest stable release from")
	skipUpgrade := fs.Bool("skip-upgrade", false, "record invariant/upgrade-in-place as skipped instead of running it")
	keepCore := fs.Bool("keep-core", false, "leave the matrix core running after the checks")
	if err := fs.Parse(args); err != nil {
		return false, err
	}
	if *stateFile == "" || *reportDir == "" {
		return false, fmt.Errorf("--state-file and --report-dir are required")
	}
	st, err := readState(*stateFile)
	if err != nil {
		return false, err
	}
	client := newClient(st.BaseURL, st.APIKey)

	allGreen := true
	write := func(frag *gatereport.Fragment) {
		green := frag.Status == gatereport.StatusPass || frag.Status == gatereport.StatusFlaky
		// --skip-upgrade is an explicit operator choice: it keeps the local
		// exit code green, but the merged report still refuses to pass a
		// blocking skipped entry (FR-004).
		deliberateSkip := frag.Name == gatereport.EntryInvariantUpgrade &&
			frag.Status == gatereport.StatusSkipped && *skipUpgrade
		if !green && !deliberateSkip {
			allGreen = false
		}
		if err := gatereport.WriteFragment(*reportDir, frag); err != nil {
			logf("invariants: write fragment %s: %v", frag.Name, err)
			allGreen = false
		}
		logf("invariants: %s -> %s %s", frag.Name, frag.Status, frag.Reason)
	}

	// If the core is gone, every live-instance invariant fails loudly.
	liveErr := client.statusOK(ctx)
	if liveErr != nil {
		reason := fmt.Sprintf("matrix core instance unreachable at %s: %v", st.BaseURL, liveErr)
		for _, name := range []string{gatereport.EntryInvariantActivity, gatereport.EntryInvariantCounters, gatereport.EntryInvariantQuarantine} {
			write(&gatereport.Fragment{Name: name, Status: gatereport.StatusFail, Reason: reason,
				Classification: gatereport.ClassificationInfrastructure})
		}
	} else {
		// FR-011 — activity-log completeness with request-id resolution.
		write(runTimed(gatereport.EntryInvariantActivity, func(frag *gatereport.Fragment) error {
			res, err := checkActivityRequestIDs(ctx, client, st.IssuedCalls, 60*time.Second)
			if res != nil {
				frag.Details = map[string]any{
					"correlation_mode": res.CorrelationMode,
					"issued_calls":     len(st.IssuedCalls),
					"resolved":         res.Resolved,
				}
				if res.Limitation != "" {
					frag.Details["limitation"] = res.Limitation
				}
			}
			return err
		}))

		// FR-013 — quarantine + approval end-to-end (before the counters
		// after-snapshot so its traffic is included in the deltas).
		write(runTimed(gatereport.EntryInvariantQuarantine, func(frag *gatereport.Fragment) error {
			steps, cleanup, err := checkQuarantineFlow(ctx, client, quarantineFlowDeps{
				FixtureBinary: st.FixtureBinary,
				WorkDir:       st.WorkDir,
			})
			cleanup()
			frag.Steps = steps
			return err
		}))

		// FR-012 — counters strictly increase under the matrix traffic.
		write(runTimed(gatereport.EntryInvariantCounters, func(frag *gatereport.Fragment) error {
			steps, after, err := checkCountersMoved(ctx, client, st.Before, 45*time.Second)
			frag.Steps = steps
			if st.Before != nil && after != nil {
				frag.Details = map[string]any{"before": st.Before, "after": after}
			}
			return err
		}))
	}

	// Teardown the matrix instance before the upgrade check (frees the data
	// dir locks and CPU; the upgrade check owns its own cores).
	if !*keepCore {
		teardownState(ctx, st)
	}

	// FR-014 — upgrade-in-place.
	if *skipUpgrade {
		write(&gatereport.Fragment{
			Name:   gatereport.EntryInvariantUpgrade,
			Status: gatereport.StatusSkipped,
			Reason: "skipped by --skip-upgrade flag for this run",
		})
	} else {
		write(runTimed(gatereport.EntryInvariantUpgrade, func(frag *gatereport.Fragment) error {
			steps, details, err := runUpgradeCheck(ctx, upgradeOpts{
				CandidateBinary: st.CoreBinary,
				FixtureBinary:   st.FixtureBinary,
				PrevBinary:      *prevBinary,
				Repo:            *upgradeRepo,
				WorkDir:         st.WorkDir,
			})
			frag.Steps = steps
			frag.Details = details
			return err
		}))
	}

	return allGreen, nil
}

// runTimed wraps a check into a fragment with duration + classification.
func runTimed(name string, fn func(frag *gatereport.Fragment) error) *gatereport.Fragment {
	frag := &gatereport.Fragment{Name: name, StartedAt: time.Now().UTC()}
	err := fn(frag)
	frag.FinishedAt = time.Now().UTC()
	frag.DurationMS = frag.FinishedAt.Sub(frag.StartedAt).Milliseconds()
	if err != nil {
		frag.Status = gatereport.StatusFail
		frag.Reason = err.Error()
		if isInfraErr(err) {
			frag.Classification = gatereport.ClassificationInfrastructure
		} else {
			frag.Classification = gatereport.ClassificationProduct
		}
	} else {
		frag.Status = gatereport.StatusPass
	}
	return frag
}

// teardownState kills everything recorded by the matrix run.
func teardownState(ctx context.Context, st *gateState) {
	if err := stopPID(st.CorePID, 30*time.Second); err != nil {
		logf("invariants: teardown core pid %d: %v", st.CorePID, err)
	}
	for _, f := range st.Fixtures {
		if err := stopPID(f.PID, 5*time.Second); err != nil {
			logf("invariants: teardown fixture %s pid %d: %v", f.Name, f.PID, err)
		}
	}
	for _, pattern := range st.StdioKillPatterns {
		_, _ = killByPattern(pattern)
	}
	if st.DockerNamePrefix != "" {
		if _, err := killDockerByNamePrefix(ctx, st.DockerNamePrefix); err != nil {
			logf("invariants: teardown docker: %v", err)
		}
	}
}
