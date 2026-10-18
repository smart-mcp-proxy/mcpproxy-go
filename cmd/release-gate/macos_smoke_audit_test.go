package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/gatereport"
)

const macosSmokeJob = "macos-smoke"

// TestMacOSSmokeJobIsAdvisoryOnMacRunner is the Spec 081 T3 wiring audit
// (US4, FR-019/FR-020): the tray smoke must run inside the gate on a macOS
// runner while being incapable of blocking the release, and the promotion
// criterion (FR-021) must be stated in the workflow file.
func TestMacOSSmokeJobIsAdvisoryOnMacRunner(t *testing.T) {
	wf := parseWorkflow(t, gateWorkflowPath)

	job, ok := wf.Jobs[macosSmokeJob]
	if !ok {
		t.Fatalf("release-qa-gate.yml has no %q job — the tray is never exercised on tag builds", macosSmokeJob)
	}
	if strings.TrimSpace(job.ContinueOnError.Value) != "true" {
		t.Errorf("job %q must set `continue-on-error: true` (got %q) or a red smoke would block the release",
			macosSmokeJob, job.ContinueOnError.Value)
	}
	if job.disabled() {
		t.Errorf("job %q is statically disabled — it would never run on a tag", macosSmokeJob)
	}
	if !strings.HasPrefix(strings.TrimSpace(job.RunsOn.Value), "macos-") {
		t.Errorf("job %q must run on a macOS runner, got runs-on %q", macosSmokeJob, job.RunsOn.Value)
	}

	verdict := wf.Jobs["verdict"]
	var needs bool
	for _, n := range verdict.Needs {
		if n == macosSmokeJob {
			needs = true
		}
	}
	if !needs {
		t.Errorf("verdict job must list %q in needs (got %v) so the fragment is merged", macosSmokeJob, verdict.Needs)
	}

	raw, err := os.ReadFile(gateWorkflowPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "3 consecutive") {
		t.Error("the workflow must state the FR-021 promotion criterion (3 consecutive release tags)")
	}
}

// TestMacOSSmokeJobReportsAndUploads pins the observable outputs: the manifest
// fragment name (recorded --advisory) and a logs artifact for post-mortems.
func TestMacOSSmokeJobReportsAndUploads(t *testing.T) {
	wf := parseWorkflow(t, gateWorkflowPath)
	job := wf.Jobs[macosSmokeJob]

	var runs, uploads string
	for _, s := range job.Steps {
		runs += s.Run + "\n"
		if strings.Contains(s.Uses, "upload-artifact") {
			uploads += yamlFlatten(&s.With) + "\n"
		}
	}
	if !strings.Contains(runs, gatereport.EntryAdvisoryMacOSSmoke) {
		t.Errorf("the job must record its outcome under %q; run steps: %s", gatereport.EntryAdvisoryMacOSSmoke, runs)
	}
	if !strings.Contains(runs, "--advisory") {
		t.Errorf("the job must pass --advisory to release-gate run-suite; run steps: %s", runs)
	}
	if !strings.Contains(runs, "scripts/test-macos-smoke.sh") {
		t.Errorf("the job must run the shared local/CI script scripts/test-macos-smoke.sh; run steps: %s", runs)
	}
	if !strings.Contains(uploads, "macos-smoke-logs") {
		t.Errorf("the job must upload the macos-smoke-logs artifact; upload steps: %s", uploads)
	}
	if !strings.Contains(uploads, "gate-fragment-macos-smoke") {
		t.Errorf("the job must upload its fragment as gate-fragment-macos-smoke (verdict merges gate-fragment-*); upload steps: %s", uploads)
	}
}

// TestMacOSSmokeScriptContract keeps the script honest about what it asserts:
// removing the handshake or clean-exit checks would turn it back into a
// build-only job without anyone noticing.
func TestMacOSSmokeScriptContract(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "test-macos-smoke.sh")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("scripts/test-macos-smoke.sh missing: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("scripts/test-macos-smoke.sh must be executable")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{
		"swift build",
		"swift test",
		"MCPPROXY_TRAY_SKIP_CORE",
		"MCPPROXY_HOME",
		"MCPPROXY_SOCKET_PATH",
		"connectToCore: core is ready",
		"got version=",
		"SIGTERM",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("smoke script no longer mentions %q — the build / unit-test / socket-handshake / clean-exit stages must all stay", want)
		}
	}
}
