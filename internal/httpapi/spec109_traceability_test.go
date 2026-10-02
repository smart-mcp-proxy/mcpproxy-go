package httpapi

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 109-m (SC-001, T148a, T148c, M10): traceability made falsifiable.
//
//	(a) every SC-001 finding id has a row in checklists/requirements.md, and
//	    every FR and task that row names exists and resolves to a test file;
//	(b) every FR in spec.md is cited by at least one task (reserved ids excepted);
//	(c) acceptance-index.json maps all 43 acceptance scenarios and SC-002 to
//	    SC-012 to refs that resolve.
//
// A task resolves when its line names a backticked path that exists in the
// repository (a **Shipped as:** list replaces the line's own paths), unless it
// is marked **Obsolete:**. A test-file path must resolve uniquely, so a bare
// basename that matches two files is refused.

const p109SpecDir = "specs/109-ux-navigation-consistency"

var p109SC001Findings = []string{
	"O2", "O3", "O4", "O5", "O6",
	"N1", "N2", "N3", "N4", "N5", "N6", "N7", "N8",
	"H1", "H2", "H3", "H4",
	"S2", "S4", "S5", "S6", "S7",
	"C1", "C2",
	"A1", "A2", "A3",
	"T1",
}

type p109Task struct {
	ID       string
	Line     string
	Obsolete bool
}

var (
	p109TaskLine  = regexp.MustCompile(`^- \[[ xX]\] (T\d+[a-z]*)\b`)
	p109Backtick  = regexp.MustCompile("`([^`]+)`")
	p109PathLike  = regexp.MustCompile(`^[\w@./-]+\.(go|ts|vue|swift|json|sh|md|yml|yaml|js|py)$`)
	p109TestLike  = regexp.MustCompile(`(_test\.go|\.spec\.ts|\.test\.ts|Tests?\.swift|\.test\.sh)$`)
	p109FRToken   = regexp.MustCompile(`FR-(\d+)([a-z]?)((?:/\d+)*)(?:\s*[–-]\s*(\d+))?`)
	p109TaskToken = regexp.MustCompile(`T(\d+)([a-z]*)(?:\s*[–-]\s*T(\d+)([a-z]*))?`)
)

var (
	p109FilesOnce sync.Once
	p109Files     []string
)

// p109RepoFiles lists every repo file as a slash path relative to the root.
func p109RepoFiles(t testing.TB) []string {
	t.Helper()
	p109FilesOnce.Do(func() {
		root := p109Root(t)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "node_modules", ".build", "dist", "build", ".claude", ".worktrees":
					return fs.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			p109Files = append(p109Files, filepath.ToSlash(rel))
			return nil
		})
		sort.Strings(p109Files)
	})
	return p109Files
}

func p109ReadRepo(t testing.TB, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p109Root(t), rel))
	require.NoError(t, err, rel)
	return string(b)
}

func p109Tasks(t testing.TB) map[string]p109Task {
	t.Helper()
	tasks := map[string]p109Task{}
	for _, line := range strings.Split(p109ReadRepo(t, p109SpecDir+"/tasks.md"), "\n") {
		m := p109TaskLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		tasks[m[1]] = p109Task{ID: m[1], Line: line, Obsolete: strings.Contains(line, "**Obsolete:**")}
	}
	return tasks
}

// p109ExpandTasks turns "T030, T031", "T053–T059" and "T124–T128" into task ids.
func p109ExpandTasks(text string, tasks map[string]p109Task) []string {
	set := map[string]bool{}
	for _, m := range p109TaskToken.FindAllStringSubmatch(text, -1) {
		first := "T" + m[1] + m[2]
		if m[3] == "" {
			set[first] = true
			continue
		}
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[3])
		for id := range tasks {
			n, _ := strconv.Atoi(regexp.MustCompile(`\d+`).FindString(id))
			if n >= lo && n <= hi {
				set[id] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// p109ExpandFRs turns "FR-021–024", "FR-031/036" and "FR-041, FR-042" into ids
// such as "FR-021".
func p109ExpandFRs(text string) []string {
	set := map[string]bool{}
	pad := func(n int) string { return fmt.Sprintf("FR-%03d", n) }
	for _, m := range p109FRToken.FindAllStringSubmatch(text, -1) {
		lo, _ := strconv.Atoi(m[1])
		set[pad(lo)+m[2]] = true
		for _, extra := range strings.Split(strings.Trim(m[3], "/"), "/") {
			if extra != "" {
				n, _ := strconv.Atoi(extra)
				set[pad(n)] = true
			}
		}
		if m[4] != "" {
			hi, _ := strconv.Atoi(m[4])
			for n := lo; n <= hi; n++ {
				set[pad(n)] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// p109SpecFRs returns the FR ids a spec defines (`- **FR-001**:`) and the
// subset it declares as reserved identifiers.
func p109SpecFRs(t testing.TB, rel string) (defined, reserved map[string]bool) {
	t.Helper()
	defined, reserved = map[string]bool{}, map[string]bool{}
	def := regexp.MustCompile(`^- \*\*(FR-\d+[a-z]?)\*\*:\s*(.*)`)
	for _, line := range strings.Split(p109ReadRepo(t, rel), "\n") {
		if m := def.FindStringSubmatch(line); m != nil {
			defined[m[1]] = true
			if strings.HasPrefix(m[2], "Reserved identifier") {
				reserved[m[1]] = true
			}
		}
	}
	return defined, reserved
}

// p109ResolvePath reports whether a backticked token names a repo file. A test
// file must match exactly one file; any other file may match by path suffix.
func p109ResolvePath(t testing.TB, token string) bool {
	t.Helper()
	files := p109RepoFiles(t)
	token = strings.TrimPrefix(token, "./")
	if strings.HasSuffix(token, "/") {
		for _, f := range files {
			if strings.HasPrefix(f, token) {
				return true
			}
		}
		return false
	}
	var exact, suffix int
	for _, f := range files {
		switch {
		case f == token:
			exact++
		case strings.HasSuffix(f, "/"+token):
			suffix++
		}
	}
	if exact == 1 {
		return true
	}
	if p109TestLike.MatchString(token) {
		return exact+suffix == 1
	}
	return exact+suffix >= 1
}

// p109TaskPaths returns the backticked path tokens that decide a task: the
// ones after **Shipped as:** when present, else every one on the line.
func p109TaskPaths(line string) []string {
	if idx := strings.Index(line, "**Shipped as:**"); idx >= 0 {
		line = line[idx:]
	}
	var out []string
	for _, m := range p109Backtick.FindAllStringSubmatch(line, -1) {
		if p109PathLike.MatchString(m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// p109TaskResolves checks one task. It returns "" when the task resolves, or
// the reason it does not.
func p109TaskResolves(t testing.TB, task p109Task) string {
	t.Helper()
	if task.Obsolete {
		return ""
	}
	paths := p109TaskPaths(task.Line)
	if len(paths) == 0 {
		return "no backticked path in the task line (name its test, or add **Shipped as:**)"
	}
	any := false
	for _, p := range paths {
		ok := p109ResolvePath(t, p)
		if ok {
			any = true
		} else if p109TestLike.MatchString(p) {
			return "test path " + p + " does not resolve to exactly one repo file"
		}
	}
	if !any {
		return "none of its paths exists: " + strings.Join(paths, ", ")
	}
	return ""
}

type p109FindingRow struct {
	ID    string
	FRs   string
	PR    string
	Tests string
}

// p109AllTraceRows returns every data row of the Finding traceability table,
// including the ones keyed by a name rather than a finding id.
func p109AllTraceRows(t testing.TB) []p109FindingRow {
	t.Helper()
	text := p109ReadRepo(t, p109SpecDir+"/checklists/requirements.md")
	start := strings.Index(text, "## Finding traceability")
	require.GreaterOrEqual(t, start, 0, "requirements.md has no Finding traceability section")
	var rows []p109FindingRow
	for _, line := range strings.Split(text[start:], "\n")[1:] {
		if strings.HasPrefix(line, "## ") {
			break
		}
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		first := strings.TrimSpace(cells[0])
		if len(cells) < 4 || first == "Finding" || strings.HasPrefix(first, "---") {
			continue
		}
		rows = append(rows, p109FindingRow{ID: first, FRs: cells[1], PR: cells[2], Tests: cells[3]})
	}
	return rows
}

func p109FindingRows(t testing.TB) map[string]p109FindingRow {
	t.Helper()
	rows := map[string]p109FindingRow{}
	idRe := regexp.MustCompile(`^([A-Z]\d+)\b`)
	for _, row := range p109AllTraceRows(t) {
		if m := idRe.FindStringSubmatch(row.ID); m != nil {
			rows[m[1]] = row
		}
	}
	return rows
}

// TestSpec109Traceability_Findings is rule (a): every SC-001 finding has a row
// whose FRs exist and whose tasks exist and resolve.
func TestSpec109Traceability_Findings(t *testing.T) {
	rows := p109FindingRows(t)
	tasks := p109Tasks(t)
	frs, _ := p109SpecFRs(t, p109SpecDir+"/spec.md")

	for _, id := range p109SC001Findings {
		row, ok := rows[id]
		if !assert.True(t, ok, "finding %s has no row in checklists/requirements.md", id) {
			continue
		}
		frIDs := p109ExpandFRs(row.FRs)
		assert.NotEmpty(t, frIDs, "finding %s names no FR", id)
		for _, fr := range frIDs {
			assert.True(t, frs[fr], "finding %s names %s, which spec.md does not define", id, fr)
		}
		taskIDs := p109ExpandTasks(row.Tests, tasks)
		assert.NotEmpty(t, taskIDs, "finding %s names no task", id)
		for _, tid := range taskIDs {
			task, exists := tasks[tid]
			if !assert.True(t, exists, "finding %s names %s, which tasks.md does not define", id, tid) {
				continue
			}
			if reason := p109TaskResolves(t, task); reason != "" {
				assert.Fail(t, "unresolved task", "finding %s -> %s: %s", id, tid, reason)
			}
		}
	}
}

// TestSpec109Traceability_EveryTableRowResolves: the rows that carry no
// finding id (URL filter contract, Needs-attention endpoint, caller classes,
// X12) are held to the same rule as the finding rows, because FR coverage and
// the register tracer count them.
func TestSpec109Traceability_EveryTableRowResolves(t *testing.T) {
	tasks := p109Tasks(t)
	frs, _ := p109SpecFRs(t, p109SpecDir+"/spec.md")
	rows := p109AllTraceRows(t)
	require.Greater(t, len(rows), len(p109SC001Findings), "the table has rows beyond the finding ids")
	for _, row := range rows {
		for _, fr := range p109ExpandFRs(row.FRs) {
			assert.True(t, frs[fr], "row %q names %s, which spec.md does not define", row.ID, fr)
		}
		taskIDs := p109ExpandTasks(row.Tests, tasks)
		assert.NotEmpty(t, taskIDs, "row %q names no task", row.ID)
		for _, tid := range taskIDs {
			task, ok := tasks[tid]
			if !assert.True(t, ok, "row %q names %s, which tasks.md does not define", row.ID, tid) {
				continue
			}
			if reason := p109TaskResolves(t, task); reason != "" {
				assert.Fail(t, "unresolved task", "row %q -> %s: %s", row.ID, tid, reason)
			}
		}
	}
}

// TestSpec109Traceability_FRCoverage is rule (b).
func TestSpec109Traceability_FRCoverage(t *testing.T) {
	defined, reserved := p109SpecFRs(t, p109SpecDir+"/spec.md")
	cited := map[string]bool{}
	// An FR is traced when a task line, the heading of the PR phase that owns
	// it, or the finding traceability table cites it (ranges expanded).
	for _, task := range p109Tasks(t) {
		for _, fr := range p109ExpandFRs(task.Line) {
			cited[fr] = true
		}
	}
	for _, line := range strings.Split(p109ReadRepo(t, p109SpecDir+"/tasks.md"), "\n") {
		if strings.HasPrefix(line, "## Phase") {
			for _, fr := range p109ExpandFRs(line) {
				cited[fr] = true
			}
		}
	}
	for _, row := range p109AllTraceRows(t) {
		for _, fr := range p109ExpandFRs(row.FRs) {
			cited[fr] = true
		}
	}
	require.NotEmpty(t, defined)
	for fr := range defined {
		if reserved[fr] {
			continue
		}
		assert.True(t, cited[fr], "%s is defined in spec.md but no task, phase or finding row cites it", fr)
	}
}

type p109AcceptanceEntry struct {
	Summary  string   `json:"summary"`
	Tests    []string `json:"tests"`
	LiveOnly string   `json:"live_only"`
}

type p109AcceptanceIndex struct {
	Scenarios       map[string]p109AcceptanceEntry `json:"scenarios"`
	SuccessCriteria map[string]p109AcceptanceEntry `json:"success_criteria"`
}

// p109ResolveTestRef resolves go:/vitest:/xctest:/pw:/sh:/golden: references.
// A #name suffix must be a func in the file (Go: Test or Benchmark; Swift: a
// test method).
func p109ResolveTestRef(t testing.TB, ref string) string {
	t.Helper()
	kind, rest, ok := strings.Cut(ref, ":")
	if !ok {
		return "malformed ref"
	}
	switch kind {
	case "go", "vitest", "xctest", "pw", "sh", "golden":
	default:
		return "unknown ref kind " + kind
	}
	path, name, _ := strings.Cut(rest, "#")
	// A ref must name a test file, not any file that happens to exist.
	fileKinds := map[string]*regexp.Regexp{
		"go":     regexp.MustCompile(`_test\.go$`),
		"vitest": regexp.MustCompile(`\.spec\.ts$`),
		"pw":     regexp.MustCompile(`\.spec\.ts$`),
		"xctest": regexp.MustCompile(`Tests?\.swift$`),
		"sh":     regexp.MustCompile(`\.sh$`),
		"golden": regexp.MustCompile(`\.json$`),
	}
	if !fileKinds[kind].MatchString(path) {
		return path + " is not a " + kind + " test or golden file"
	}
	b, err := os.ReadFile(filepath.Join(p109Root(t), filepath.FromSlash(path)))
	if err != nil {
		return "missing file " + path
	}
	if name == "" {
		return ""
	}
	var re *regexp.Regexp
	switch kind {
	case "go":
		re = regexp.MustCompile(`(?m)^func (\([^)]*\) )?` + regexp.QuoteMeta(name) + `\(`)
	case "xctest":
		re = regexp.MustCompile(`func ` + regexp.QuoteMeta(name) + `\(`)
	default:
		return kind + " refs take no #name"
	}
	if !re.Match(b) {
		return "no func " + name + " in " + path
	}
	return ""
}

// p109ScenarioIDs lists US1-1 ... US7-6 as spec.md numbers them.
func p109ScenarioIDs(t testing.TB) []string {
	t.Helper()
	var ids []string
	story := 0
	storyRe := regexp.MustCompile(`^### User Story (\d+)`)
	scenRe := regexp.MustCompile(`^(\d+)\. \*\*Given\*\*`)
	for _, line := range strings.Split(p109ReadRepo(t, p109SpecDir+"/spec.md"), "\n") {
		if m := storyRe.FindStringSubmatch(line); m != nil {
			story, _ = strconv.Atoi(m[1])
		} else if m := scenRe.FindStringSubmatch(line); m != nil && story > 0 {
			ids = append(ids, fmt.Sprintf("US%d-%s", story, m[1]))
		}
	}
	return ids
}

// TestSpec109Traceability_AcceptanceIndex is rule (c).
func TestSpec109Traceability_AcceptanceIndex(t *testing.T) {
	var idx p109AcceptanceIndex
	p108ReadJSON(t, p109SpecDir+"/acceptance-index.json", &idx)

	ids := p109ScenarioIDs(t)
	require.Len(t, ids, 43, "spec.md should define 43 acceptance scenarios")
	liveOnly := 0
	check := func(kind, id string, e p109AcceptanceEntry) {
		if e.LiveOnly != "" {
			liveOnly++
			assert.GreaterOrEqual(t, len(e.LiveOnly), 20, "%s %s: live_only needs a real reason", kind, id)
			return
		}
		assert.NotEmpty(t, e.Tests, "%s %s has no test ref", kind, id)
		for _, ref := range e.Tests {
			if reason := p109ResolveTestRef(t, ref); reason != "" {
				assert.Fail(t, "unresolved ref", "%s %s: %s: %s", kind, id, ref, reason)
			}
		}
	}
	for _, id := range ids {
		e, ok := idx.Scenarios[id]
		if assert.True(t, ok, "scenario %s is missing from acceptance-index.json", id) {
			check("scenario", id, e)
		}
	}
	assert.Len(t, idx.Scenarios, len(ids), "acceptance-index.json lists a scenario spec.md does not have")
	for n := 2; n <= 12; n++ {
		id := fmt.Sprintf("SC-%03d", n)
		e, ok := idx.SuccessCriteria[id]
		if assert.True(t, ok, "%s is missing from acceptance-index.json", id) {
			check("success criterion", id, e)
		}
	}
	t.Logf("acceptance index: %d scenarios, %d success criteria, %d live_only", len(idx.Scenarios), len(idx.SuccessCriteria), liveOnly)
}

// TestSpec109Traceability_ResolverBites proves the checks above can fail.
func TestSpec109Traceability_ResolverBites(t *testing.T) {
	assert.NotEmpty(t, p109ResolveTestRef(t, "vitest:frontend/tests/unit/no-such-file.spec.ts"))
	assert.NotEmpty(t, p109ResolveTestRef(t, "go:internal/httpapi/spec109_parity_test.go#TestNoSuchFunction"))
	assert.Empty(t, p109ResolveTestRef(t, "go:internal/httpapi/spec109_parity_test.go#TestSpec109TerminologyGolden"))

	missing := p109Task{ID: "T999", Line: "- [x] T999 `internal/nowhere/ghost_test.go` proves it"}
	assert.NotEmpty(t, p109TaskResolves(t, missing), "a task naming a missing test path must fail")
	bare := p109Task{ID: "T998", Line: "- [x] T998 no path at all"}
	assert.NotEmpty(t, p109TaskResolves(t, bare), "a task with no path must fail")
	obsolete := p109Task{ID: "T997", Line: "- [x] T997 `internal/nowhere/ghost_test.go` **Obsolete:** replaced", Obsolete: true}
	assert.Empty(t, p109TaskResolves(t, obsolete), "an obsolete task is skipped")
	shipped := p109Task{ID: "T996", Line: "- [x] T996 `internal/nowhere/ghost_test.go` **Shipped as:** `internal/httpapi/spec109_parity_test.go`"}
	assert.Empty(t, p109TaskResolves(t, shipped), "Shipped as: replaces the line's own paths")

	// A bare basename that matches two files is refused.
	assert.False(t, p109ResolvePath(t, "catalog_test.go"), "catalog_test.go exists in several packages, so a bare basename is ambiguous")
	assert.True(t, p109ResolvePath(t, "internal/registries/catalog_test.go"), "the full path is not")

	assert.Equal(t, []string{"FR-021", "FR-022", "FR-023", "FR-024"}, p109ExpandFRs("FR-021–024"))
	assert.Equal(t, []string{"FR-031", "FR-036"}, p109ExpandFRs("FR-031/036"))
	raw, _ := json.Marshal(p109ExpandTasks("T053–T055, T124a", map[string]p109Task{"T053": {}, "T054": {}, "T055": {}, "T055a": {}, "T124a": {}}))
	assert.JSONEq(t, `["T053","T054","T055","T055a","T124a"]`, string(raw))
}
