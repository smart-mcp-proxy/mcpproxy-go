package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/gatereport"
)

// runSuite executes one of the existing suite entry points (FR-003 —
// test-api-e2e.sh, go race tests, scan-eval --gate) and records its outcome
// as a report fragment. The command's output streams through so CI logs stay
// useful.
//
// advisory marks a non-blocking entry (Spec 081 T2 Web UI sweep, T3 macOS
// smoke): its failures are recorded as `advisory-fail` so the merged report
// distinguishes "went red but cannot block the tag" from a real blocking
// failure. The returned ok is false either way — the CI job still goes red
// (under continue-on-error) so a maintainer sees it.
func runSuite(ctx context.Context, name, reportDir string, advisory bool, cmdArgs []string) (bool, error) {
	frag := &gatereport.Fragment{Name: name, StartedAt: time.Now().UTC()}
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	logf("run-suite %s: %v", name, cmdArgs)
	err := cmd.Run()
	frag.FinishedAt = time.Now().UTC()
	frag.DurationMS = frag.FinishedAt.Sub(frag.StartedAt).Milliseconds()
	if err != nil {
		frag.Status = gatereport.StatusFail
		if advisory {
			frag.Status = gatereport.StatusAdvisoryFail
		}
		frag.Reason = fmt.Sprintf("suite command failed: %v", err)
		frag.Classification = gatereport.ClassificationProduct
	} else {
		frag.Status = gatereport.StatusPass
	}
	if werr := gatereport.WriteFragment(reportDir, frag); werr != nil {
		return false, werr
	}
	logf("run-suite %s -> %s", name, frag.Status)
	return frag.Status == gatereport.StatusPass, nil
}
