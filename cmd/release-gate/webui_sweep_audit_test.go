package main

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/gatereport"
)

// gateWorkflowPath is the reusable gate workflow the publishers call. go test
// runs with cmd/release-gate as the working directory.
var gateWorkflowPath = filepath.Join("..", "..", ".github", "workflows", "release-qa-gate.yml")

const webUISweepJob = "web-ui-sweep"

// TestWebUISweepJobIsAdvisory is the Spec 081 T2 wiring audit: the Playwright
// Web UI sweep must run inside the gate (so it runs on every tag build, since
// release.yml / prerelease.yml call this workflow) while being incapable of
// blocking the release.
//
// Advisory is enforced structurally: `continue-on-error: true` on the job. A
// failing job inside a reusable workflow makes the whole workflow_call
// conclusion `failure`, which the publishers' `needs:` would treat as a red
// gate — continue-on-error is what keeps the sweep out of that verdict.
func TestWebUISweepJobIsAdvisory(t *testing.T) {
	wf := parseWorkflow(t, gateWorkflowPath)

	sweep, ok := wf.Jobs[webUISweepJob]
	if !ok {
		t.Fatalf("release-qa-gate.yml has no %q job — the Playwright sweep does not run on tag builds", webUISweepJob)
	}
	if strings.TrimSpace(sweep.ContinueOnError.Value) != "true" {
		t.Errorf("job %q must set `continue-on-error: true` (got %q) or a red sweep would block the release",
			webUISweepJob, sweep.ContinueOnError.Value)
	}
	if sweep.disabled() {
		t.Errorf("job %q is statically disabled — it would never run on a tag", webUISweepJob)
	}

	// The verdict job must wait for the sweep, otherwise the fragment can land
	// after the merge and the report would show a missing entry every run.
	verdict, ok := wf.Jobs["verdict"]
	if !ok {
		t.Fatal("release-qa-gate.yml has no verdict job")
	}
	var needsSweep bool
	for _, n := range verdict.Needs {
		if n == webUISweepJob {
			needsSweep = true
		}
	}
	if !needsSweep {
		t.Errorf("verdict job must list %q in needs (got %v) so the sweep fragment is merged", webUISweepJob, verdict.Needs)
	}
}

// TestWebUISweepJobReportsAndUploads pins the two observable outputs of the
// advisory job: the manifest fragment name the merger expects, and the
// Playwright HTML report artifact a maintainer reads after a red sweep.
func TestWebUISweepJobReportsAndUploads(t *testing.T) {
	wf := parseWorkflow(t, gateWorkflowPath)
	sweep := wf.Jobs[webUISweepJob]

	var runs, uploads string
	for _, s := range sweep.Steps {
		runs += s.Run + "\n"
		if strings.Contains(s.Uses, "upload-artifact") {
			uploads += yamlFlatten(&s.With) + "\n"
		}
	}

	if !strings.Contains(runs, gatereport.EntryAdvisoryWebUISweep) {
		t.Errorf("the sweep job must record its outcome under the manifest entry %q; run steps: %s",
			gatereport.EntryAdvisoryWebUISweep, runs)
	}
	if !strings.Contains(runs, "--advisory") {
		t.Errorf("the sweep job must pass --advisory to release-gate run-suite so a failure is recorded as advisory-fail; run steps: %s", runs)
	}
	if !strings.Contains(uploads, "playwright-report") {
		t.Errorf("the sweep job must upload the Playwright HTML report artifact; upload steps: %s", uploads)
	}
}

// yamlFlatten renders a `with:` mapping node as a flat string for substring
// assertions (the audit only cares whether a path/name appears at all).
func yamlFlatten(n *yaml.Node) string {
	var b strings.Builder
	var walk func(node *yaml.Node)
	walk = func(node *yaml.Node) {
		if node == nil {
			return
		}
		b.WriteString(node.Value)
		b.WriteString(" ")
		for _, c := range node.Content {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
