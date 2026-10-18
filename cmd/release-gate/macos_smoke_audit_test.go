package main

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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

// stripShellComments drops whole-line comments and trailing " # ..." comments
// so an audit cannot be satisfied by a commented-out command or by the
// script's own header prose.
func stripShellComments(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if i := strings.Index(line, " #"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// withNames returns the values of `name:` entries in an upload-artifact `with:`.
func withNames(s step) []string {
	var names []string
	for i := 0; i+1 < len(s.With.Content); i += 2 {
		if s.With.Content[i].Value == "name" {
			names = append(names, s.With.Content[i+1].Value)
		}
	}
	return names
}

// auditSmokeJobSteps checks the observable outputs of the macos-smoke job: the
// run steps (comments ignored) and the EXACT upload artifact names.
func auditSmokeJobSteps(steps []step, verdictPattern string) []string {
	var problems []string
	var runs string
	var fragmentUploads, logUploads int
	for _, s := range steps {
		runs += stripShellComments(s.Run) + "\n"
		if !strings.Contains(s.Uses, "upload-artifact") {
			continue
		}
		for _, n := range withNames(s) {
			switch n {
			case "gate-fragment-macos-smoke":
				fragmentUploads++
			case "macos-smoke-logs":
				logUploads++
			}
		}
	}
	if !strings.Contains(runs, gatereport.EntryAdvisoryMacOSSmoke) {
		problems = append(problems, "run steps do not record "+gatereport.EntryAdvisoryMacOSSmoke)
	}
	if !strings.Contains(runs, "--advisory") {
		problems = append(problems, "run steps lack --advisory")
	}
	if !strings.Contains(runs, "scripts/test-macos-smoke.sh") {
		problems = append(problems, "run steps do not run scripts/test-macos-smoke.sh")
	}
	if logUploads != 1 {
		problems = append(problems, "want exactly one upload named macos-smoke-logs")
	}
	if fragmentUploads != 1 {
		problems = append(problems, "want exactly one upload named gate-fragment-macos-smoke")
	}
	if ok, _ := path.Match(verdictPattern, "gate-fragment-macos-smoke"); !ok {
		problems = append(problems, "verdict pattern "+verdictPattern+" does not match gate-fragment-macos-smoke")
	}
	return problems
}

// verdictFragmentPattern reads the `pattern:` the verdict job downloads.
func verdictFragmentPattern(t *testing.T) string {
	t.Helper()
	wf := parseWorkflow(t, gateWorkflowPath)
	for _, s := range wf.Jobs["verdict"].Steps {
		if !strings.Contains(s.Uses, "download-artifact") {
			continue
		}
		for i := 0; i+1 < len(s.With.Content); i += 2 {
			if s.With.Content[i].Value == "pattern" {
				return s.With.Content[i+1].Value
			}
		}
	}
	t.Fatal("verdict job has no download-artifact step with a pattern")
	return ""
}

// TestMacOSSmokeJobReportsAndUploads pins the observable outputs: the manifest
// fragment name (recorded --advisory) and the artifacts the verdict merges.
func TestMacOSSmokeJobReportsAndUploads(t *testing.T) {
	wf := parseWorkflow(t, gateWorkflowPath)
	for _, p := range auditSmokeJobSteps(wf.Jobs[macosSmokeJob].Steps, verdictFragmentPattern(t)) {
		t.Error(p)
	}
}

// The advisory job is built from the checked-in script on a public runner: it
// must never see repository secrets.
func TestMacOSSmokeJobUsesNoSecrets(t *testing.T) {
	raw, err := os.ReadFile(gateWorkflowPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var found bool
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == macosSmokeJob && n.Content[i+1].Kind == yaml.MappingNode {
					found = true
					if strings.Contains(yamlFlatten(n.Content[i+1]), "secrets.") {
						t.Errorf("job %q references secrets.* — the macOS smoke must run without secrets", macosSmokeJob)
					}
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(&doc)
	if !found {
		t.Fatalf("job %q not found", macosSmokeJob)
	}
}

// auditSmokeScript returns the problems found in the script's EXECUTABLE lines
// (comments stripped): the stages and assertions that make it a smoke.
func auditSmokeScript(src string) []string {
	code := stripShellComments(src)
	var problems []string
	for _, want := range []string{
		"swift build",
		"swift test",
		"MCPPROXY_TRAY_SKIP_CORE=1",
		"MCPPROXY_HOME=",
		"MCPPROXY_SOCKET_PATH=",
		"trap cleanup EXIT",
		`grep -q "connectToCore: core is ready"`,
		`grep -q "got version=${CORE_VERSION}"`,
		"kill -TERM",
		`[[ "$TRAY_RC" == "0" ]]`,
		"CFBundleIdentifier",
	} {
		if !strings.Contains(code, want) {
			problems = append(problems, "script code lacks "+want)
		}
	}
	return problems
}

func readSmokeScript(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "test-macos-smoke.sh"))
	if err != nil {
		t.Fatalf("scripts/test-macos-smoke.sh missing: %v", err)
	}
	return string(raw)
}

// TestMacOSSmokeScriptContract keeps the script honest about what it asserts:
// removing the handshake or clean-exit checks would turn it back into a
// build-only job without anyone noticing.
func TestMacOSSmokeScriptContract(t *testing.T) {
	info, err := os.Stat(filepath.Join("..", "..", "scripts", "test-macos-smoke.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no executable bit; the git index mode is what ships.
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		t.Error("scripts/test-macos-smoke.sh must be executable")
	}
	src := readSmokeScript(t)
	for _, p := range auditSmokeScript(src) {
		t.Error(p)
	}
	// The scratch bundle must not share the real app's bundle id, or the app's
	// defaults writes land in the developer's real preferences domain.
	code := stripShellComments(src)
	id := strings.Index(code, "com.smartmcpproxy.mcpproxy.smoke")
	sign := strings.Index(code, "codesign --force")
	if id < 0 {
		t.Error("script must rewrite CFBundleIdentifier to com.smartmcpproxy.mcpproxy.smoke")
	}
	if id >= 0 && (sign < 0 || id > sign) {
		t.Error("the bundle id must be rewritten BEFORE codesign (signing seals Info.plist)")
	}
}

// TestMacOSSmokeAuditsBite proves each audit fails on a mutated copy.
func TestMacOSSmokeAuditsBite(t *testing.T) {
	good := readSmokeScript(t)
	if p := auditSmokeScript(good); len(p) != 0 {
		t.Fatalf("baseline script should audit clean: %v", p)
	}
	for _, m := range []struct{ name, from, to string }{
		{"trap removed", "trap cleanup EXIT", "# trap cleanup EXIT"},
		{"handshake version check dropped", `grep -q "got version=${CORE_VERSION}"`, `grep -q "got version="`},
		{"SIGTERM removed", "kill -TERM", "# kill -TERM"},
		{"exit status check removed", `[[ "$TRAY_RC" == "0" ]]`, `true`},
	} {
		if !strings.Contains(good, m.from) {
			t.Fatalf("%s: mutation anchor %q not in script", m.name, m.from)
		}
		if len(auditSmokeScript(strings.Replace(good, m.from, m.to, 1))) == 0 {
			t.Errorf("%s: audit did not notice", m.name)
		}
	}
	if len(auditSmokeScript("# swift build swift test trap cleanup EXIT kill -TERM\n")) == 0 {
		t.Error("comment-only script passed the audit")
	}

	wf := parseWorkflow(t, gateWorkflowPath)
	steps := wf.Jobs[macosSmokeJob].Steps
	pattern := verdictFragmentPattern(t)
	if p := auditSmokeJobSteps(steps, pattern); len(p) != 0 {
		t.Fatalf("baseline job should audit clean: %v", p)
	}
	mutate := func(f func(s *step)) []step {
		out := make([]step, len(steps))
		for i, s := range steps {
			// deep-copy the With node so the mutation cannot leak
			w := yaml.Node{}
			w.Kind, w.Tag = s.With.Kind, s.With.Tag
			for _, c := range s.With.Content {
				cc := *c
				w.Content = append(w.Content, &cc)
			}
			s.With = w
			f(&s)
			out[i] = s
		}
		return out
	}
	if len(auditSmokeJobSteps(mutate(func(s *step) {
		s.Run = strings.ReplaceAll(s.Run, "--advisory", "# --advisory")
	}), pattern)) == 0 {
		t.Error("commented-out --advisory passed the job audit")
	}
	if len(auditSmokeJobSteps(steps, "gate-report-*")) == 0 {
		t.Error("a verdict pattern that misses the fragment name passed the audit")
	}
	if len(auditSmokeJobSteps(mutate(func(s *step) {
		for _, c := range s.With.Content {
			if c.Value == "gate-fragment-macos-smoke" {
				c.Value = "gate-fragment-macos-smoke-typo"
			}
		}
	}), pattern)) == 0 {
		t.Error("a misnamed fragment artifact passed the audit")
	}
}
