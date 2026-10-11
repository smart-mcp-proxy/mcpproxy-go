package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The red-team scripts' verdict predicates live in
// scripts/lib/tpa-lab-verdicts.sh so they can be exercised here without a
// daemon or Docker (Sol r3 P2).

func verdictsLib(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash lab scripts are not run on Windows")
	}
	for _, bin := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	lib, err := filepath.Abs(filepath.Join("..", "..", "scripts", "lib", "tpa-lab-verdicts.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lib); err != nil {
		t.Fatalf("verdict lib missing: %v", err)
	}
	return lib
}

// runLib sources the lib and runs script with args as "$@"; it returns the
// trimmed stdout and the exit code.
func runLib(t *testing.T, lib, script string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"-c", "source \"$0\"; " + script, lib}, args...)...)
	out, err := cmd.Output()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %q: %v", script, err)
		}
		code = exitErr.ExitCode()
	}
	return strings.TrimSpace(string(out)), code
}

func TestExp2aVerdictRequiresBothToolsHeld(t *testing.T) {
	lib := verdictsLib(t)
	for _, tc := range []struct{ cal, stock, want string }{
		{"changed", "changed", "HOLDS"},
		{"changed(blocked)", "blocked", "HOLDS"},
		{"changed", "approved", "HOLE"},
		{"approved", "changed", "HOLE"},
		{"approved", "approved", "HOLE"},
		{"pending", "pending", "INCONCLUSIVE"},
		{"changed", "pending", "INCONCLUSIVE"},
		{"pending", "changed", "INCONCLUSIVE"},
		{"not-found", "changed", "INCONCLUSIVE"},
		{"changed", "not-found", "INCONCLUSIVE"},
		{"", "", "INCONCLUSIVE"},
	} {
		got, code := runLib(t, lib, `exp2a_verdict "$1" "$2"`, tc.cal, tc.stock)
		if code != 0 || got != tc.want {
			t.Errorf("exp2a_verdict(%q, %q) = %q (exit %d), want %q", tc.cal, tc.stock, got, code, tc.want)
		}
	}
}

func TestSuiteExitStatusAggregatesResults(t *testing.T) {
	lib := verdictsLib(t)
	for _, tc := range []struct {
		name    string
		results []string
		want    int
	}{
		{"all hold", []string{"EXP-1 HOLDS: baseline scan → dangerous", "EXP-2a HOLDS: calendar=changed stock=changed", "EXP-6 HOLDS: x"}, 0},
		{"deferred EXP-3 hole allowed", []string{"EXP-1 HOLDS: x", "EXP-3 HOLE: mutants verdict=clean"}, 0},
		{"confirmed bypass", []string{"EXP-1 HOLDS: x", "EXP-6 HOLE: scan-mode auto-approved the pull"}, 1},
		{"partial hole", []string{"EXP-4 PARTIAL-HOLE: scan=clean"}, 1},
		{"baseline hole?", []string{"EXP-1 HOLE?: baseline verdict=clean"}, 1},
		{"baseline approval failed", []string{"EXP-2a INCONCLUSIVE: stage-1 baseline was not approved"}, 1},
		{"scan failed", []string{"EXP-1 INCONCLUSIVE: scan=scan-failed"}, 1},
		{"daemon failed", []string{"EXP-3: INCONCLUSIVE (daemon)"}, 1},
		{"EXP-3 inconclusive is not the deferred hole", []string{"EXP-3 INCONCLUSIVE: scan=scan-failed"}, 1},
		{"unrecognised line", []string{"EXP-1 something else"}, 1},
		{"nothing ran", nil, 1},
	} {
		_, code := runLib(t, lib, `suite_exit_status "$@"`, tc.results...)
		if code != tc.want {
			t.Errorf("%s: suite_exit_status = %d, want %d", tc.name, code, tc.want)
		}
	}
}

func TestLabConfigUseImagePropagatesToEveryIsolationBlock(t *testing.T) {
	lib := verdictsLib(t)
	tpl, err := os.ReadFile(filepath.Join("..", "tpa-lab-config-docker.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(cfg, tpl, 0o644); err != nil {
		t.Fatal(err)
	}

	if out, code := runLib(t, lib, `lab_config_use_image "$1" "$2"`, cfg, "tpa-attack-lab:custom"); code != 0 {
		t.Fatalf("lab_config_use_image failed (%d): %s", code, out)
	}
	images, code := runLib(t, lib, `lab_config_images "$1"`, cfg)
	if code != 0 {
		t.Fatalf("lab_config_images exit %d", code)
	}
	lines := strings.Split(images, "\n")
	if len(lines) != 2 {
		t.Fatalf("want an image per lab server (2), got %q", images)
	}
	for _, img := range lines {
		if img != "tpa-attack-lab:custom" {
			t.Errorf("isolation image %q was not rewritten", img)
		}
	}

	// Isolation itself must survive the rewrite.
	var parsed struct {
		MCPServers []struct {
			Isolation struct {
				Mode        string `json:"mode"`
				NetworkMode string `json:"network_mode"`
			} `json:"isolation"`
		} `json:"mcpServers"`
	}
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, s := range parsed.MCPServers {
		if s.Isolation.Mode != "docker" || s.Isolation.NetworkMode != "none" {
			t.Errorf("isolation weakened: %+v", s.Isolation)
		}
	}
}

func TestLabConfigUseImageRefusesUnisolatedServer(t *testing.T) {
	lib := verdictsLib(t)
	cfg := filepath.Join(t.TempDir(), "cfg.json")
	bare := `{"mcpServers":[{"name":"tpa-lab-attacker","isolation":{"mode":"docker","image":"tpa-attack-lab:lab","network_mode":"bridge"}}]}`
	if err := os.WriteFile(cfg, []byte(bare), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := runLib(t, lib, `lab_config_use_image "$1" "$2" 2>/dev/null`, cfg, "tpa-attack-lab:custom"); code == 0 {
		t.Fatal("a server with network access must be refused")
	}
	data, _ := os.ReadFile(cfg)
	if string(data) != bare {
		t.Errorf("refused config was modified: %s", data)
	}
}

// TestRunLabDockerRejectsImageOverride: run-tpa-lab-docker.sh runs the
// checked-in config, so an IMAGE override that does not match its isolation
// image is refused before anything is built or started.
func TestRunLabDockerRejectsImageOverride(t *testing.T) {
	verdictsLib(t)
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "run-tpa-lab-docker.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// TPA_LAB_PREFLIGHT_ONLY stops the script before it builds or starts
	// anything, whatever the preflight decides.
	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(), "IMAGE=tpa-attack-lab:not-the-config-image", "TPA_LAB_PREFLIGHT_ONLY=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("override must be refused; output:\n%s", out)
	}
	if !strings.Contains(string(out), "the override is not supported here") {
		t.Fatalf("unexpected failure:\n%s", out)
	}

	// Control: the default image matches the config and passes preflight.
	cmd = exec.Command("bash", script)
	cmd.Env = append(os.Environ(), "IMAGE=tpa-attack-lab:lab", "TPA_LAB_PREFLIGHT_ONLY=1")
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "preflight ok") {
		t.Fatalf("default image must pass preflight (%v):\n%s", err, out)
	}
}
