package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The round-2 runner (scripts/tpa-lab-redteam-round2.sh) carries the same
// guards as the round-1 suite (Sol 6.1 round-2 review, findings 4-7): it drops
// inherited MCPPROXY_* overrides and pins --data-dir/--listen, refuses a
// template it cannot check, uses a fresh run dir per invocation, and fails
// closed on its result lines.

func round2Script(t *testing.T) string {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "tpa-lab-redteam-round2.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return script
}

// TestRound2RunnerIgnoresInheritedOverrides: inherited MCPPROXY_DATA /
// MCPPROXY_LISTEN / MCPPROXY_API_KEY must never reach the lab daemon, which
// runs with --data-dir and --listen pinned to the generated config.
func TestRound2RunnerIgnoresInheritedOverrides(t *testing.T) {
	verdictsLib(t)
	skipIfLabPortBusy(t)
	stub, record := recordingStub(t)
	runRoot := filepath.Join(t.TempDir(), "runs")
	runDir := filepath.Join(runRoot, "r")
	cmd := exec.Command("bash", round2Script(t))
	cmd.Env = append(append(os.Environ(), inheritedOverrides...),
		"SKIP_BUILD=1", "ONLY=R2-1", "TPA_LAB_BINARY="+stub, "TPA_LAB_RUN_ROOT="+runRoot, "RUN_DIR="+runDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the stub daemon exits, so the run must fail:\n%s", out)
	}
	if !strings.Contains(string(out), "ignoring inherited MCPPROXY_DATA") {
		t.Errorf("the scrub must be reported:\n%s", out)
	}
	assertPinnedInvocation(t, record, filepath.Join(runDir, "r2-1-data"))
}

// TestRound2RunnerRefusesUncheckedTemplateAndNeverRunsTheBinary: a template
// that fails generation or the isolation check aborts before the binary is
// invoked, and the result line fails the run.
func TestRound2RunnerRefusesUncheckedTemplateAndNeverRunsTheBinary(t *testing.T) {
	verdictsLib(t)
	skipIfLabPortBusy(t)
	unreadable := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(unreadable, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, template := range map[string]string{
		"unreadable template": unreadable,
		"missing template":    filepath.Join(t.TempDir(), "absent.json"),
		"unisolated template": labConfigVariant(t, isolationVariants["network_mode bridge"]),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "binary-invoked")
			stub := filepath.Join(dir, "mcpproxy-stub")
			if err := os.WriteFile(stub, []byte("#!/bin/sh\necho \"$@\" >> '"+marker+"'\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", round2Script(t))
			cmd.Env = append(os.Environ(), "SKIP_BUILD=1", "ONLY=R2-1",
				"TPA_LAB_TEMPLATE="+template, "TPA_LAB_BINARY="+stub, "TPA_LAB_RUN_ROOT="+filepath.Join(dir, "runs"))
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("the run must fail when the lab config is refused; output:\n%s", out)
			}
			if !strings.Contains(string(out), "INCONCLUSIVE: lab config refused") {
				t.Errorf("unexpected failure:\n%s", out)
			}
			if _, statErr := os.Stat(marker); statErr == nil {
				invoked, _ := os.ReadFile(marker)
				t.Fatalf("the binary ran despite the refused config: %s", invoked)
			}
		})
	}
}

// TestRound2RunnerUsesAFreshRunDirPerInvocation: two invocations never share a
// data dir, so approvals from an earlier run cannot turn R2-1 into a
// non-first-seen pass; the latest run is reachable through round2-latest.
func TestRound2RunnerUsesAFreshRunDirPerInvocation(t *testing.T) {
	verdictsLib(t)
	skipIfLabPortBusy(t)
	stub, _ := recordingStub(t)
	runRoot := filepath.Join(t.TempDir(), "runs")
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		cmd := exec.Command("bash", round2Script(t))
		cmd.Env = append(os.Environ(), "SKIP_BUILD=1", "ONLY=R2-1", "TPA_LAB_BINARY="+stub, "TPA_LAB_RUN_ROOT="+runRoot)
		_, _ = cmd.CombinedOutput() // the stub daemon exits; the run fails by design
		latest, err := os.Readlink(filepath.Join(runRoot, "round2-latest"))
		if err != nil {
			t.Fatalf("round2-latest symlink missing: %v", err)
		}
		if seen[latest] {
			t.Fatalf("invocation %d reused run dir %s", i+1, latest)
		}
		seen[latest] = true
		if _, err := os.Stat(filepath.Join(latest, "r2-1.json")); err != nil {
			t.Errorf("invocation %d left no generated config in %s: %v", i+1, latest, err)
		}
	}
}
