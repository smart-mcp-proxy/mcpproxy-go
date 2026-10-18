package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSurfaceConsistencyJobIsBlocking audits the T4 wiring in
// release-qa-gate.yml: the surface-consistency job exists, is not advisory,
// actually compares the Web UI, feeds the verdict job, and its seed knob can
// only be set on a manual dry run (never on the workflow_call tag path).
func TestSurfaceConsistencyJobIsBlocking(t *testing.T) {
	wf := parseWorkflow(t, filepath.Join("..", "..", ".github", "workflows", "release-qa-gate.yml"))
	j, ok := wf.Jobs["surface-consistency"]
	if !ok {
		t.Fatal("release-qa-gate.yml has no surface-consistency job")
	}
	if v := strings.TrimSpace(j.ContinueOnError.Value); v != "" && v != "false" {
		t.Errorf("surface-consistency must block: continue-on-error=%q", v)
	}
	var run string
	for _, s := range j.Steps {
		if strings.Contains(s.Run, "release-gate consistency") {
			run = s.Run
		}
	}
	if run == "" {
		t.Fatal("surface-consistency job never runs `release-gate consistency`")
	}
	if !strings.Contains(run, "--webui-script") || strings.Contains(run, "--no-webui") {
		t.Errorf("surface-consistency must compare the Web UI (--webui-script, never --no-webui):\n%s", run)
	}
	if !strings.Contains(run, "workflow_dispatch") {
		t.Errorf("the --seed-mismatch knob must be guarded to workflow_dispatch runs:\n%s", run)
	}
	verdict, ok := wf.Jobs["verdict"]
	if !ok {
		t.Fatal("no verdict job")
	}
	if !containsStr(verdict.Needs, "surface-consistency") {
		t.Errorf("verdict job needs %v, missing surface-consistency", verdict.Needs)
	}
}
