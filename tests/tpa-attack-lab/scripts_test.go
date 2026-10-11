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

// TestExp3VerdictOnlyRecognisedVerdictsAreTheDeferredHole (Sol r4 P2): only a
// recognised clean/warnings verdict may be recorded as the deferred EXP-3
// detector hole; an unreadable or unknown report is INCONCLUSIVE and fails
// the suite.
func TestExp3VerdictOnlyRecognisedVerdictsAreTheDeferredHole(t *testing.T) {
	lib := verdictsLib(t)
	for _, tc := range []struct{ in, want string }{
		{"dangerous", "HOLDS"},
		{"clean", "HOLE"},
		{"warnings", "HOLE"},
		{"unknown", "INCONCLUSIVE"},
		{"scan-failed", "INCONCLUSIVE"},
		{"stale-report", "INCONCLUSIVE"},
		{"", "INCONCLUSIVE"},
	} {
		got, code := runLib(t, lib, `exp3_verdict "$1"`, tc.in)
		if code != 0 || got != tc.want {
			t.Errorf("exp3_verdict(%q) = %q (exit %d), want %q", tc.in, got, code, tc.want)
		}
	}
	// End to end through the result line the suite records for it.
	_, code := runLib(t, lib, `v=unknown; case "$(exp3_verdict "$v")" in HOLE) r="EXP-3 HOLE: mutants verdict=$v";; HOLDS) r="EXP-3 HOLDS";; *) r="EXP-3 INCONCLUSIVE: scan=$v";; esac; suite_exit_status "$r"`)
	if code == 0 {
		t.Error("an unknown EXP-3 report must fail the suite")
	}
}

// TestExp2bVerdictRequiresProvenUnquarantine (Sol r4 P2): the EXP-2a records
// already read "changed", so EXP-2b HOLDS only when the unquarantine itself
// succeeded, the server is no longer quarantined, post-unquarantine discovery
// ran, and both held tools stayed out of the index.
func TestExp2bVerdictRequiresProvenUnquarantine(t *testing.T) {
	lib := verdictsLib(t)
	for _, tc := range []struct {
		name                                     string
		unq, quarantined, discovered, cal, stock string
		calIdx, stockIdx, want                   string
	}{
		{"held after a proven unquarantine", "1", "false", "yes", "changed", "changed", "no", "no", "HOLDS"},
		{"failed unquarantine", "0", "true", "no", "changed", "changed", "no", "no", "INCONCLUSIVE"},
		{"unquarantine reported ok but still quarantined", "1", "true", "yes", "changed", "changed", "no", "no", "INCONCLUSIVE"},
		{"server state unknown", "1", "unknown", "yes", "changed", "changed", "no", "no", "INCONCLUSIVE"},
		{"discovery never ran", "1", "false", "no", "changed", "changed", "no", "no", "INCONCLUSIVE"},
		{"stale approval", "1", "false", "yes", "approved", "changed", "no", "no", "HOLE"},
		{"held but indexed", "1", "false", "yes", "changed", "changed", "yes", "no", "HOLE"},
		{"index unknown", "1", "false", "yes", "changed", "changed", "unknown", "no", "INCONCLUSIVE"},
		{"pending", "1", "false", "yes", "pending", "changed", "no", "no", "INCONCLUSIVE"},
	} {
		got, code := runLib(t, lib, `exp2b_verdict "$@"`, tc.unq, tc.quarantined, tc.discovered, tc.cal, tc.stock, tc.calIdx, tc.stockIdx)
		if code != 0 || got != tc.want {
			t.Errorf("%s: exp2b_verdict = %q (exit %d), want %q", tc.name, got, code, tc.want)
		}
	}
}

// TestRunLabDockerResolvesDataDirFromAnyCwd (Sol r4 P2): the baseline runner
// must keep the daemon's state and socket inside the worktree whatever
// directory it is invoked from.
func TestRunLabDockerResolvesDataDirFromAnyCwd(t *testing.T) {
	verdictsLib(t)
	project, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(project, "scripts", "run-tpa-lab-docker.sh")
	cmd := exec.Command("bash", script)
	cmd.Dir = t.TempDir() // somewhere else entirely
	cmd.Env = append(os.Environ(), "IMAGE=tpa-attack-lab:lab", "TPA_LAB_PREFLIGHT_ONLY=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("preflight failed (%v):\n%s", err, out)
	}
	want := "cwd=" + project + " data_dir=" + filepath.Join(project, "test-data-tpa-lab-docker")
	if !strings.Contains(string(out), want) {
		t.Fatalf("want %q in preflight output:\n%s", want, out)
	}
}

func TestLabConfigDataDirResolvesRelativeAgainstBase(t *testing.T) {
	lib := verdictsLib(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(cfg, []byte(`{"data_dir":"./state"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, code := runLib(t, lib, `lab_config_data_dir "$1" "$2"`, cfg, "/base/root"); code != 0 || got != "/base/root/state" {
		t.Errorf("relative data_dir = %q (exit %d)", got, code)
	}
	if err := os.WriteFile(cfg, []byte(`{"data_dir":"/abs/state"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, code := runLib(t, lib, `lab_config_data_dir "$1" "$2"`, cfg, "/base/root"); code != 0 || got != "/abs/state" {
		t.Errorf("absolute data_dir = %q (exit %d)", got, code)
	}
}

// TestHeldUnquarantinedVerdictRequiresProvenTrustedGate (Sol r5 P2): EXP-5
// and EXP-6 records may already read "changed" from the quarantined
// verification at restart, so a hold counts only when the step that trusted
// the server succeeded, the server is not quarantined, the trusted gate is
// proven to have run, and every held tool is out of the index.
func TestHeldUnquarantinedVerdictRequiresProvenTrustedGate(t *testing.T) {
	lib := verdictsLib(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"single tool held after a proven unquarantine", []string{"1", "false", "yes", "changed", "no"}, "HOLDS"},
		{"two tools held", []string{"1", "false", "yes", "changed", "no", "changed(blocked)", "no"}, "HOLDS"},
		{"unquarantine failed (hold from quarantined discovery)", []string{"0", "true", "no", "changed", "no"}, "INCONCLUSIVE"},
		{"unquarantine ok but still quarantined", []string{"1", "true", "yes", "changed", "no"}, "INCONCLUSIVE"},
		{"post-unquarantine discovery unproven", []string{"1", "false", "no", "changed", "no"}, "INCONCLUSIVE"},
		{"discovery proof stale (indexed before)", []string{"1", "false", "unproven(indexed-before=yes)", "changed", "no"}, "INCONCLUSIVE"},
		{"trusted gate never logged", []string{"1", "false", "no(notes_export)", "changed", "no"}, "INCONCLUSIVE"},
		{"stale approval", []string{"1", "false", "yes", "approved", "no"}, "HOLE"},
		{"held but indexed", []string{"1", "false", "yes", "changed", "yes"}, "HOLE"},
		{"second tool approved", []string{"1", "false", "yes", "changed", "no", "approved", "no"}, "HOLE"},
		{"index unknown", []string{"1", "false", "yes", "changed", "unknown"}, "INCONCLUSIVE"},
		{"pending", []string{"1", "false", "yes", "pending", "no"}, "INCONCLUSIVE"},
		{"no tools", []string{"1", "false", "yes"}, "INCONCLUSIVE"},
		{"odd pair", []string{"1", "false", "yes", "changed"}, "INCONCLUSIVE"},
	} {
		got, code := runLib(t, lib, `held_unquarantined_verdict "$@"`, tc.args...)
		if code != 0 || got != tc.want {
			t.Errorf("%s: held_unquarantined_verdict = %q (exit %d), want %q", tc.name, got, code, tc.want)
		}
	}
}

// labConfigVariant writes a copy of the checked-in lab config with mutate
// applied to the attacker's isolation block.
func labConfigVariant(t *testing.T, mutate func(iso map[string]interface{}, server map[string]interface{})) string {
	t.Helper()
	project, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(project, "tests", "tpa-lab-config-docker.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, raw := range cfg["mcpServers"].([]interface{}) {
		s := raw.(map[string]interface{})
		if s["name"] == "tpa-lab-attacker" {
			iso, _ := s["isolation"].(map[string]interface{})
			mutate(iso, s)
		}
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

var isolationVariants = map[string]func(iso, server map[string]interface{}){
	"network_mode bridge": func(iso, _ map[string]interface{}) { iso["network_mode"] = "bridge" },
	"network_mode unset":  func(iso, _ map[string]interface{}) { delete(iso, "network_mode") },
	"isolation mode none": func(iso, _ map[string]interface{}) { iso["mode"] = "none" },
	"no isolation block":  func(_, server map[string]interface{}) { delete(server, "isolation") },
}

// TestLabConfigCheckIsolationRejectsNetworkOrBareUpstream (Sol r5 P2).
func TestLabConfigCheckIsolationRejectsNetworkOrBareUpstream(t *testing.T) {
	lib := verdictsLib(t)
	ok := labConfigVariant(t, func(map[string]interface{}, map[string]interface{}) {})
	if _, code := runLib(t, lib, `lab_config_check_isolation "$1"`, ok); code != 0 {
		t.Fatal("the checked-in config must pass")
	}
	for name, mutate := range isolationVariants {
		cfg := labConfigVariant(t, mutate)
		if _, code := runLib(t, lib, `lab_config_check_isolation "$1" 2>/dev/null`, cfg); code == 0 {
			t.Errorf("%s: must be refused", name)
		}
	}
}

// TestRunLabDockerRejectsUnisolatedConfig (Sol r5 P2): the baseline runner
// validated only the isolation image, so a config with network_mode bridge
// ran the attacker with network access. The preflight must refuse it.
func TestRunLabDockerRejectsUnisolatedConfig(t *testing.T) {
	verdictsLib(t)
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "run-tpa-lab-docker.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range isolationVariants {
		cfg := labConfigVariant(t, mutate)
		cmd := exec.Command("bash", script)
		cmd.Env = append(os.Environ(), "IMAGE=tpa-attack-lab:lab", "TPA_LAB_PREFLIGHT_ONLY=1", "TPA_LAB_CONFIG="+cfg)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s: preflight must refuse the config; output:\n%s", name, out)
			continue
		}
		if !strings.Contains(string(out), "does not Docker-isolate every server") {
			t.Errorf("%s: unexpected failure:\n%s", name, out)
		}
	}
}
