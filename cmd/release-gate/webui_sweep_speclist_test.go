package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Spec 108-l (L12, T123): the Web UI sweep's spec list cannot drift silently.
// scripts/run-web-smoke.sh names the spec files it runs; e2e/web-ui-sweep holds
// the spec files that exist; docs/development/release-gate.md and
// web-ui-verification.md tell a maintainer which ones the gate runs. Before this
// test release-gate.md still said "three spec files" while the launcher ran five,
// so a spec could be added (or forgotten) with nothing failing.
// webui_sweep_audit_test.go checks the workflow around the job; this checks the
// list inside it.

var playwrightTestLine = regexp.MustCompile(`"\$PLAYWRIGHT_BIN"\s+test\s+(.+)`)

// sweepSpecsRun parses the spec names off the launcher's `playwright test` line.
func sweepSpecsRun(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRootFromTest, "scripts", "run-web-smoke.sh"))
	if err != nil {
		t.Fatal(err)
	}
	m := playwrightTestLine.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal(`scripts/run-web-smoke.sh has no "$PLAYWRIGHT_BIN" test <specs> line: the sweep runs nothing`)
	}
	var specs []string
	for _, field := range strings.Fields(m[1]) {
		if strings.HasSuffix(field, ".spec.ts") {
			specs = append(specs, field)
		}
	}
	sort.Strings(specs)
	return specs
}

// sweepSpecsOnDisk lists e2e/web-ui-sweep/*.spec.ts.
func sweepSpecsOnDisk(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(repoRootFromTest, "e2e", "web-ui-sweep", "*.spec.ts"))
	if err != nil {
		t.Fatal(err)
	}
	var specs []string
	for _, m := range matches {
		specs = append(specs, filepath.Base(m))
	}
	sort.Strings(specs)
	return specs
}

// specListProblems compares the three views and returns every disagreement; it is
// pure so the mutation checks below can feed it a corrupted list.
func specListProblems(run, onDisk []string, docs map[string]string) []string {
	var problems []string
	has := func(list []string, name string) bool {
		for _, v := range list {
			if v == name {
				return true
			}
		}
		return false
	}
	for _, name := range onDisk {
		if !has(run, name) {
			problems = append(problems, name+" exists in e2e/web-ui-sweep but scripts/run-web-smoke.sh does not run it (an orphan spec never gates a release)")
		}
	}
	for _, name := range run {
		if !has(onDisk, name) {
			problems = append(problems, "scripts/run-web-smoke.sh runs "+name+" but the file does not exist")
		}
		for doc, text := range docs {
			if !strings.Contains(text, name) {
				problems = append(problems, doc+" does not mention "+name+", which the sweep runs")
			}
		}
	}
	sort.Strings(problems)
	return problems
}

func TestWebUISweepSpecListIsComplete(t *testing.T) {
	run, onDisk := sweepSpecsRun(t), sweepSpecsOnDisk(t)
	docs := map[string]string{}
	for _, rel := range []string{"docs/development/release-gate.md", "docs/development/web-ui-verification.md"} {
		b, err := os.ReadFile(filepath.Join(repoRootFromTest, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		docs[rel] = string(b)
	}

	for _, problem := range specListProblems(run, onDisk, docs) {
		t.Error(problem)
	}
	if len(run) < 5 {
		t.Errorf("the launcher runs %d specs (%v); the sweep has at least web-ui-sweep, visual-a11y-sweep, navigation-consistency, profiles-clients and profiles-scope", len(run), run)
	}

	// The prose count must not go stale again: it names no number that disagrees.
	if strings.Contains(docs["docs/development/release-gate.md"], "runs three spec files") {
		t.Error("release-gate.md still says the sweep \"runs three spec files\"; list every spec instead")
	}

	t.Run("mutation: an orphan, a missing file and an undocumented spec are all caught", func(t *testing.T) {
		if got := specListProblems(append(append([]string{}, run...), "ghost.spec.ts"), onDisk, docs); len(got) == 0 {
			t.Error("a spec run but absent from disk went unreported")
		}
		if got := specListProblems(run, append(append([]string{}, onDisk...), "orphan.spec.ts"), docs); len(got) == 0 {
			t.Error("a spec on disk but not run went unreported")
		}
		if got := specListProblems(run, onDisk, map[string]string{"x.md": "mentions nothing"}); len(got) == 0 {
			t.Error("a spec missing from the docs went unreported")
		}
		if got := specListProblems(run, onDisk, docs); len(got) != 0 {
			t.Errorf("the controls must be clean, got %v", got)
		}
	})
}
