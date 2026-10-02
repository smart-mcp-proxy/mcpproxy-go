package httpapi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 109-m (FR-091, T148b, M11): the parity matrix walk. The checked-in
// specs/109-ux-navigation-consistency/parity-matrix.json uses the same
// `parity-matrix/v1` schema as Spec 108's (profiles_v3_parity_test.go, whose
// identifier resolver this file reuses), with string row ids ("8a", "22a").
// Spec 109 parity table -> JSON: same rows, same status per cell, a reason on
// every dash. Every ticked cell names an implementing identifier that
// resolves and at least one test that exists. A "(108)" cell is delivered by
// Spec 108 and must point at a ticked cell of the 108 matrix on the same
// surface.
//
// Resolution is split by suite: this package resolves rest, mcp, testid,
// a11y, component, symbol and every `tests` ref; the vitest parity spec
// resolves `route:`, the XCTest resolves `nav:`/`add:`/`approute:`, and the
// CLI helper-process test resolves `cmd:` and its flags.

const p109MatrixPath = "specs/109-ux-navigation-consistency/parity-matrix.json"

type p109Cell struct {
	Status string   `json:"status"`
	Reason string   `json:"reason,omitempty"`
	Ref    string   `json:"ref,omitempty"`
	Ids    []string `json:"ids"`
	Fields []string `json:"fields,omitempty"`
	Tests  []string `json:"tests"`
}

type p109MatrixRow struct {
	Row        string              `json:"row"`
	Capability string              `json:"capability"`
	Cells      map[string]p109Cell `json:"cells"`
}

type p109Matrix struct {
	Schema string          `json:"schema"`
	Spec   string          `json:"spec"`
	Rows   []p109MatrixRow `json:"rows"`
}

type p109SpecRow struct {
	Row        string
	Capability string
	Status     map[string]string
}

func p109LoadMatrix(t testing.TB) p109Matrix {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p109Root(t), p109MatrixPath))
	require.NoError(t, err, "parity-matrix.json missing (FR-091): %s", p109MatrixPath)
	var m p109Matrix
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

var p109MatrixRowRe = regexp.MustCompile(`^\| (\d+a?) \| `)

// p109SpecStatus maps one spec cell to a matrix status.
func p109SpecStatus(surface, cell string) string {
	cell = strings.TrimSpace(cell)
	switch {
	case strings.HasPrefix(cell, "✓"):
		return "must"
	case cell == "—" || strings.HasPrefix(cell, "— "):
		return "out"
	case strings.HasPrefix(cell, "n/a"):
		return "na"
	case strings.HasPrefix(cell, "(108)"):
		return "spec108"
	case surface == "rest":
		return "must" // the REST column carries no tick: any other text names a route or field
	}
	return "unrecognised:" + cell
}

func p109ParseSpecTable(t testing.TB) []p109SpecRow {
	t.Helper()
	text := p109ReadRepo(t, p109SpecDir+"/spec.md")
	start := strings.Index(text, "### Parity matrix")
	end := strings.Index(text, "### Terminology")
	require.True(t, start >= 0 && end > start, "spec.md has no Parity matrix section")
	var rows []p109SpecRow
	for _, line := range strings.Split(text[start:end], "\n") {
		m := p109MatrixRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		cols := strings.SplitN(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "| "), "|"), " | ", 8)
		require.Len(t, cols, 8, "row %s of the spec table does not have 8 columns", m[1])
		row := p109SpecRow{Row: strings.TrimSpace(cols[0]), Capability: strings.Join(strings.Fields(cols[1]), " "), Status: map[string]string{}}
		for i, surface := range p108Surfaces {
			row.Status[surface] = p109SpecStatus(surface, cols[2+i])
		}
		rows = append(rows, row)
	}
	return rows
}

// p109CompareToSpec lists every way the matrix disagrees with the spec table.
// Pure, so the mutation self-check can feed it a corrupted matrix.
func p109CompareToSpec(spec []p109SpecRow, m p109Matrix) []string {
	var problems []string
	byRow := map[string]p109MatrixRow{}
	for _, r := range m.Rows {
		byRow[r.Row] = r
	}
	seen := map[string]bool{}
	for _, s := range spec {
		seen[s.Row] = true
		r, ok := byRow[s.Row]
		if !ok {
			problems = append(problems, fmt.Sprintf("row %s is in the spec but not in parity-matrix.json", s.Row))
			continue
		}
		if strings.Join(strings.Fields(r.Capability), " ") != s.Capability {
			problems = append(problems, fmt.Sprintf("row %s capability differs: %q vs spec %q", s.Row, r.Capability, s.Capability))
		}
		for _, surface := range p108Surfaces {
			cell, ok := r.Cells[surface]
			if !ok {
				problems = append(problems, fmt.Sprintf("row %s has no %s cell", s.Row, surface))
				continue
			}
			if cell.Status != s.Status[surface] {
				problems = append(problems, fmt.Sprintf("row %s %s: spec says %s, matrix says %s", s.Row, surface, s.Status[surface], cell.Status))
			}
			if cell.Status == "out" && strings.TrimSpace(cell.Reason) == "" {
				problems = append(problems, fmt.Sprintf("row %s %s is out of scope but carries no reason", s.Row, surface))
			}
		}
	}
	for n := range byRow {
		if !seen[n] {
			problems = append(problems, fmt.Sprintf("row %s is in parity-matrix.json but not in the spec", n))
		}
	}
	sort.Strings(problems)
	return problems
}

func p109CloneMatrix(m p109Matrix) p109Matrix {
	b, _ := json.Marshal(m)
	var out p109Matrix
	_ = json.Unmarshal(b, &out)
	return out
}

func TestSpec109ParityMatrixMatchesSpec(t *testing.T) {
	spec := p109ParseSpecTable(t)
	require.Len(t, spec, 32, "the spec parity table has rows 1 to 30 including 8a and 22a")
	m := p109LoadMatrix(t)
	require.Equal(t, "parity-matrix/v1", m.Schema)
	require.Equal(t, "109", m.Spec)
	require.Empty(t, p109CompareToSpec(spec, m), "parity-matrix.json must have the spec table's rows and ticks")

	t.Run("a row missing from the matrix is caught", func(t *testing.T) {
		mut := p109Matrix{Schema: m.Schema, Spec: m.Spec, Rows: m.Rows[1:]}
		require.NotEmpty(t, p109CompareToSpec(spec, mut))
	})
	t.Run("a tick recorded as out is caught", func(t *testing.T) {
		mut := p109CloneMatrix(m)
		cell := mut.Rows[0].Cells["web"]
		cell.Status, cell.Reason = "out", "because"
		mut.Rows[0].Cells["web"] = cell
		require.NotEmpty(t, p109CompareToSpec(spec, mut))
	})
	t.Run("an out cell without a reason is caught", func(t *testing.T) {
		mut := p109CloneMatrix(m)
		cell := mut.Rows[0].Cells["mcp"] // row 1 MCP is a dash
		require.Equal(t, "out", cell.Status)
		cell.Reason = ""
		mut.Rows[0].Cells["mcp"] = cell
		require.NotEmpty(t, p109CompareToSpec(spec, mut))
	})
	t.Run("a capability text mismatch is caught", func(t *testing.T) {
		mut := p109CloneMatrix(m)
		mut.Rows[2].Capability += " (edited)"
		require.NotEmpty(t, p109CompareToSpec(spec, mut))
	})
	t.Run("a (108) cell recorded as a plain tick is caught", func(t *testing.T) {
		mut := p109CloneMatrix(m)
		last := len(mut.Rows) - 1
		require.Equal(t, "30", mut.Rows[last].Row)
		cell := mut.Rows[last].Cells["cli"]
		require.Equal(t, "spec108", cell.Status)
		cell.Status = "must"
		mut.Rows[last].Cells["cli"] = cell
		require.NotEmpty(t, p109CompareToSpec(spec, mut))
	})
}

// p109MatrixTestUnresolved checks one `tests` entry: the file exists and, when a
// #name is given, the function occurs in it. File-level refs are allowed.
func p109MatrixTestUnresolved(t testing.TB, ref string) string {
	t.Helper()
	kind, _, ok := strings.Cut(ref, ":")
	if !ok {
		return "malformed test ref"
	}
	switch kind {
	case "go", "vitest", "xctest", "pw":
		return p109ResolveTestRef(t, ref)
	}
	return "unknown test kind " + kind
}

func TestSpec109ParityMatrixResolves(t *testing.T) {
	m := p109LoadMatrix(t)
	var m108 p108Matrix
	p108ReadJSON(t, p108MatrixPath, &m108)
	m108Cells := map[string]p108Cell{}
	for _, r := range m108.Rows {
		for surface, cell := range r.Cells {
			m108Cells[fmt.Sprintf("%d/%s", r.Row, surface)] = cell
		}
	}

	ticked := 0
	for _, row := range m.Rows {
		for _, surface := range p108Surfaces {
			cell := row.Cells[surface]
			where := fmt.Sprintf("row %s %s", row.Row, surface)
			switch cell.Status {
			case "spec108":
				require.NotEmpty(t, cell.Ref, where+": a (108) cell names the 108 matrix row it resolves to")
				var n int
				_, err := fmt.Sscanf(cell.Ref, "108#%d", &n)
				require.NoError(t, err, where+": ref must be 108#<row>")
				target, ok := m108Cells[fmt.Sprintf("%d/%s", n, surface)]
				require.True(t, ok, where+": 108 matrix has no row %d", n)
				require.Equal(t, "must", target.Status, "%s: 108 row %d is not a ticked %s cell", where, n, surface)
				require.Empty(t, cell.Ids, where)
				continue
			case "must":
				ticked++
			default:
				require.Empty(t, cell.Ids, where+": only must cells name identifiers")
				require.Empty(t, cell.Tests, where+": only must cells name tests")
				continue
			}
			require.NotEmpty(t, cell.Ids, where+": a tick with no implementing identifier")
			require.NotEmpty(t, cell.Tests, where+": a tick with no test")
			for _, id := range cell.Ids {
				var fields []string
				if surface == "mcp" {
					fields = cell.Fields
				}
				if why := p108Unresolved(t, surface, id, fields); why != "" {
					t.Errorf("%s: %s: %s", where, id, why)
				}
			}
			for _, ref := range cell.Tests {
				if why := p109MatrixTestUnresolved(t, ref); why != "" {
					t.Errorf("%s: %s: %s", where, ref, why)
				}
			}
		}
	}
	require.Greater(t, ticked, 80, "the walk saw too few ticked cells: the reader is broken")

	t.Run("mutation self-check: an unresolvable identifier or test is reported", func(t *testing.T) {
		require.NotEmpty(t, p108Unresolved(t, "rest", "rest:GET /api/v1/nope", nil))
		require.NotEmpty(t, p108Unresolved(t, "rest", "rest:DELETE /api/v1/attention", nil), "a real path with a method it lacks")
		require.NotEmpty(t, p108Unresolved(t, "mcp", "mcp:quarantine_security.nope", nil))
		require.NotEmpty(t, p108Unresolved(t, "mcp", "mcp:search_servers", []string{"not_an_argument"}))
		require.NotEmpty(t, p108Unresolved(t, "web", "component:frontend/src/components/Nope.vue", nil))
		require.NotEmpty(t, p108Unresolved(t, "web", "symbol:frontend/src/utils/health.ts#noSuchSymbolAnywhere", nil))
		require.NotEmpty(t, p108Unresolved(t, "cli", "mcp:search_servers", nil), "a kind on the wrong surface")
		require.NotEmpty(t, p109MatrixTestUnresolved(t, "go:internal/httpapi/spec109_parity_matrix_test.go#TestNoSuchTest"))
		require.NotEmpty(t, p109MatrixTestUnresolved(t, "vitest:frontend/tests/unit/no-such.spec.ts"))
		require.Empty(t, p108Unresolved(t, "rest", "rest:GET /api/v1/attention", nil))
		require.Empty(t, p109MatrixTestUnresolved(t, "go:internal/httpapi/spec109_parity_matrix_test.go"))
	})
}
