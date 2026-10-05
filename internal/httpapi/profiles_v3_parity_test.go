package httpapi

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Spec 108-l (FR-051, FR-052, T121): the parity checks that read the checked-in
// goldens of specs/108-profiles-v3 and internal/profile/testdata/contract. Every
// helper here is prefixed p108 so Spec 109-m's p109 helpers never collide
// (decision L1).

// p108RepoRoot is the repository root, found from this package directory.
func p108RepoRoot(t testing.TB) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "go.mod"))
	require.NoError(t, err, "repo root not found from internal/httpapi")
	return root
}

func p108ReadJSON(t testing.TB, rel string, into any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p108RepoRoot(t), rel))
	require.NoError(t, err, "missing golden %s", rel)
	require.NoError(t, json.Unmarshal(b, into), rel)
}

func p108SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestProfilesV3LabelsCoverEnums (L6): labels.json carries exactly one word for
// every value of the label families, no more and no fewer. The Web and macOS
// suites then pin their helpers to these words.
func TestProfilesV3LabelsCoverEnums(t *testing.T) {
	var enums map[string][]string
	p108ReadJSON(t, "internal/profile/testdata/contract/enums.json", &enums)
	var labels map[string]json.RawMessage
	p108ReadJSON(t, "internal/profile/testdata/contract/labels.json", &labels)

	// credential_cta is keyed by the credential_state enum (the button a row
	// shows to fix its credential), so it is compared with that family.
	for _, family := range []string{"access_reason", "source", "explain_step", "credential_state", "credential_cta", "binding_mode", "unannotated", "max_tier"} {
		raw, ok := labels[family]
		require.True(t, ok, "labels.json has no %q family", family)
		var words map[string]string
		require.NoError(t, json.Unmarshal(raw, &words), family)
		enumFamily := family
		if family == "credential_cta" {
			enumFamily = "credential_state"
		}
		values, ok := enums[enumFamily]
		require.True(t, ok, "enums.json has no %q family", enumFamily)

		want := append([]string(nil), values...)
		sort.Strings(want)
		require.Equal(t, want, p108SortedKeys(words),
			"labels.json %q must have exactly one label per enums.json value (no missing key, no extra key)", family)
		for key, word := range words {
			if family == "source" && key == "none" {
				require.Empty(t, word, "source none carries no suffix")
				continue
			}
			if family == "credential_cta" && key == "client" {
				require.Empty(t, word, "a client credential needs no fix button")
				continue
			}
			require.NotEmpty(t, word, "%s.%s has an empty label", family, key)
		}
	}

	// Mutation self-check: dropping a key must be detected by the comparison.
	var words map[string]string
	require.NoError(t, json.Unmarshal(labels["access_reason"], &words))
	delete(words, "tool_approval")
	want := append([]string(nil), enums["access_reason"]...)
	sort.Strings(want)
	require.NotEqual(t, want, p108SortedKeys(words), "the coverage comparison cannot fail")
}

// ---------------------------------------------------------------------------
// Parity matrix (FR-051)
// ---------------------------------------------------------------------------

const p108MatrixPath = "specs/108-profiles-v3/parity-matrix.json"

var p108Surfaces = []string{"web", "macos", "cli", "mcp", "rest"}

type p108Cell struct {
	Status  string   `json:"status"`
	Reason  string   `json:"reason,omitempty"`
	Ids     []string `json:"ids"`
	Fields  []string `json:"fields,omitempty"`
	Pending string   `json:"pending,omitempty"`
	Tests   []string `json:"tests"`
}

type p108Row struct {
	Row        int                 `json:"row"`
	Capability string              `json:"capability"`
	Cells      map[string]p108Cell `json:"cells"`
}

type p108Matrix struct {
	Schema string    `json:"schema"`
	Spec   string    `json:"spec"`
	Rows   []p108Row `json:"rows"`
}

func p108LoadMatrix(t testing.TB) p108Matrix {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p108RepoRoot(t), p108MatrixPath))
	require.NoError(t, err, "parity-matrix.json missing (FR-051): %s", p108MatrixPath)
	var m p108Matrix
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

// p108SpecRow is one row of the spec.md parity table with its cells already
// reduced to a status (L3).
type p108SpecRow struct {
	Row        int
	Capability string
	Status     map[string]string
}

var p108RowRe = regexp.MustCompile(`^\| (\d+) \| `)

// p108SpecStatus maps one spec cell to a matrix status.
func p108SpecStatus(surface, cell string) string {
	cell = strings.TrimSpace(cell)
	switch {
	case strings.HasPrefix(cell, "✓"):
		return "must"
	case cell == "—" || strings.HasPrefix(cell, "— "):
		return "out"
	case strings.HasPrefix(cell, "n/a"):
		return "na"
	case surface == "rest" && strings.HasPrefix(cell, "deprecated"):
		return "deprecated"
	case surface == "rest" && strings.Contains(cell, "`"):
		return "must" // the REST column carries no tick: a backticked route is a must
	}
	return "unrecognised:" + cell
}

func p108ParseSpecTable(t testing.TB) []p108SpecRow {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p108RepoRoot(t), "specs/108-profiles-v3/spec.md"))
	require.NoError(t, err)
	text := string(b)
	start := strings.Index(text, "### Parity matrix")
	end := strings.Index(text, "### Terminology")
	require.True(t, start >= 0 && end > start, "spec.md has no Parity matrix section")
	var rows []p108SpecRow
	for _, line := range strings.Split(text[start:end], "\n") {
		m := p108RowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// Cells never contain " | " (inner pipes of a command are written
		// `a|b` without spaces), so the table splits on it.
		cols := strings.SplitN(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "| "), "|"), " | ", 8)
		require.Len(t, cols, 8, "row %s of the spec table does not have 8 columns", m[1])
		var n int
		_, err := fmt.Sscanf(cols[0], "%d", &n)
		require.NoError(t, err)
		row := p108SpecRow{Row: n, Capability: strings.Join(strings.Fields(cols[1]), " "), Status: map[string]string{}}
		for i, surface := range p108Surfaces {
			row.Status[surface] = p108SpecStatus(surface, cols[2+i])
		}
		rows = append(rows, row)
	}
	return rows
}

// p108CompareToSpec lists every way the matrix disagrees with the spec table:
// a missing or extra row, a different capability text, a different status, an
// `out` cell with no reason. It is a pure function so the mutation self-check
// can feed it a corrupted matrix.
func p108CompareToSpec(spec []p108SpecRow, m p108Matrix) []string {
	var problems []string
	byRow := map[int]p108Row{}
	for _, r := range m.Rows {
		byRow[r.Row] = r
	}
	seen := map[int]bool{}
	for _, s := range spec {
		seen[s.Row] = true
		r, ok := byRow[s.Row]
		if !ok {
			problems = append(problems, fmt.Sprintf("row %d is in the spec but not in parity-matrix.json", s.Row))
			continue
		}
		if strings.Join(strings.Fields(r.Capability), " ") != s.Capability {
			problems = append(problems, fmt.Sprintf("row %d capability differs: %q vs spec %q", s.Row, r.Capability, s.Capability))
		}
		for _, surface := range p108Surfaces {
			cell, ok := r.Cells[surface]
			if !ok {
				problems = append(problems, fmt.Sprintf("row %d has no %s cell", s.Row, surface))
				continue
			}
			if cell.Status != s.Status[surface] {
				problems = append(problems, fmt.Sprintf("row %d %s: spec says %s, matrix says %s", s.Row, surface, s.Status[surface], cell.Status))
			}
			if cell.Status == "out" && strings.TrimSpace(cell.Reason) == "" {
				problems = append(problems, fmt.Sprintf("row %d %s is out of scope but carries no reason", s.Row, surface))
			}
		}
	}
	for n := range byRow {
		if !seen[n] {
			problems = append(problems, fmt.Sprintf("row %d is in parity-matrix.json but not in the spec", n))
		}
	}
	sort.Strings(problems)
	return problems
}

func TestProfilesV3ParityMatrixMatchesSpec(t *testing.T) {
	spec := p108ParseSpecTable(t)
	require.Len(t, spec, 25, "the spec parity table has rows 1..25")
	m := p108LoadMatrix(t)
	require.Equal(t, "parity-matrix/v1", m.Schema)
	require.Equal(t, "108", m.Spec)
	require.Empty(t, p108CompareToSpec(spec, m), "parity-matrix.json must have the spec table's rows and ticks")

	// Mutation self-check: each corruption the plan names must be caught.
	t.Run("a row missing from the matrix is caught", func(t *testing.T) {
		mut := p108Matrix{Schema: m.Schema, Spec: m.Spec, Rows: m.Rows[1:]}
		require.NotEmpty(t, p108CompareToSpec(spec, mut))
	})
	t.Run("a tick recorded as out is caught", func(t *testing.T) {
		mut := p108CloneMatrix(m)
		cell := mut.Rows[0].Cells["web"]
		cell.Status, cell.Reason = "out", "because"
		mut.Rows[0].Cells["web"] = cell
		require.NotEmpty(t, p108CompareToSpec(spec, mut))
	})
	t.Run("an out cell without a reason is caught", func(t *testing.T) {
		mut := p108CloneMatrix(m)
		cell := mut.Rows[5].Cells["mcp"] // row 6 MCP is a dash
		require.Equal(t, "out", cell.Status)
		cell.Reason = ""
		mut.Rows[5].Cells["mcp"] = cell
		require.NotEmpty(t, p108CompareToSpec(spec, mut))
	})
	t.Run("a capability text mismatch is caught", func(t *testing.T) {
		mut := p108CloneMatrix(m)
		mut.Rows[2].Capability += " (edited)"
		require.NotEmpty(t, p108CompareToSpec(spec, mut))
	})
}

func p108CloneMatrix(m p108Matrix) p108Matrix {
	b, _ := json.Marshal(m)
	var out p108Matrix
	_ = json.Unmarshal(b, &out)
	return out
}

// ---------------------------------------------------------------------------
// Identifier resolution (L4)
// ---------------------------------------------------------------------------

// p108AllowedPrefixes lists which identifier kinds each surface may use. Every
// kind is resolved by exactly one suite: this package (rest, mcp, testid, a11y,
// component, symbol and the tests refs), the CLI helper-process test (cmd), the
// vitest (route) and the XCTest (nav, add, approute).
var p108AllowedPrefixes = map[string][]string{
	"web":   {"route", "testid", "component", "symbol"},
	"macos": {"a11y", "nav", "add", "approute", "symbol"},
	"cli":   {"cmd"},
	"mcp":   {"mcp"},
	"rest":  {"rest", "symbol"},
}

// p108PendingAllowlist is the expiring exception list: a `must` cell whose
// implementing id lands in another spec's PR may carry `pending`, and only the
// cells named here. Once the id resolves, delete the cell's `pending` and the
// entry (TestProfilesV3ParityMatrixResolves logs when one is stale).
var p108PendingAllowlist = map[string]string{"19/web": "109-l"}

type p108Corpus struct {
	root string
	once sync.Once
	web  string // every non-test source file under frontend/src
	swft string // every non-test Swift source under native/macos/MCPProxy/MCPProxy
}

var p108Sources p108Corpus

func (c *p108Corpus) load(t testing.TB) {
	c.once.Do(func() {
		c.root = p108RepoRoot(t)
		read := func(dir string, exts ...string) string {
			var sb strings.Builder
			_ = filepath.WalkDir(filepath.Join(c.root, dir), func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				for _, ext := range exts {
					if strings.HasSuffix(path, ext) && !strings.Contains(path, ".test.") && !strings.Contains(path, ".spec.") {
						if b, err := os.ReadFile(path); err == nil {
							sb.Write(b)
							sb.WriteByte('\n')
						}
					}
				}
				return nil
			})
			return sb.String()
		}
		c.web = read("frontend/src", ".vue", ".ts")
		c.swft = read("native/macos/MCPProxy/MCPProxy", ".swift")
	})
}

var (
	p108SwaggerOnce sync.Once
	p108Swagger     map[string]map[string]any
	p108MCPOnce     sync.Once
	p108MCP         map[string]struct {
		InputSchema struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		} `json:"inputSchema"`
	}
)

var p108BraceRe = regexp.MustCompile(`\{[^}]*\}`)

// p108Unresolved returns "" when the identifier resolves on its surface, else a
// description of why not. Kinds owned by another suite return "".
func p108Unresolved(t testing.TB, surface, id string, fields []string) string {
	t.Helper()
	p108Sources.load(t)
	kind, rest, ok := strings.Cut(id, ":")
	if !ok {
		return "malformed identifier (no kind)"
	}
	allowed := false
	for _, k := range p108AllowedPrefixes[surface] {
		allowed = allowed || k == kind
	}
	if !allowed {
		return fmt.Sprintf("kind %q is not allowed on %s", kind, surface)
	}
	root := p108RepoRoot(t)
	switch kind {
	case "route", "nav", "add", "approute", "cmd":
		return "" // resolved by the vitest, the XCTest and the CLI helper process
	case "rest":
		p108SwaggerOnce.Do(func() {
			b, err := os.ReadFile(filepath.Join(root, "oas/swagger.yaml"))
			require.NoError(t, err)
			var doc struct {
				Paths map[string]map[string]any `yaml:"paths"`
			}
			require.NoError(t, yaml.Unmarshal(b, &doc))
			p108Swagger = doc.Paths
		})
		method, path, ok := strings.Cut(rest, " ")
		if !ok {
			return "rest id must be `rest:<METHOD> <path>`"
		}
		norm := p108BraceRe.ReplaceAllString(path, "{}")
		for p, ops := range p108Swagger {
			if p108BraceRe.ReplaceAllString(p, "{}") != norm {
				continue
			}
			if _, has := ops[strings.ToLower(method)]; has {
				return ""
			}
			return fmt.Sprintf("swagger has %s but not method %s", p, method)
		}
		return fmt.Sprintf("%s is not in oas/swagger.yaml", path)
	case "mcp":
		p108MCPOnce.Do(func() {
			b, err := os.ReadFile(filepath.Join(root, "internal/server/testdata/toolslist_goldens/default_server.json"))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(b, &p108MCP))
		})
		tool, op, hasOp := strings.Cut(rest, ".")
		schema, ok := p108MCP[tool]
		if !ok {
			return fmt.Sprintf("tool %q is not in the default tool surface", tool)
		}
		if hasOp {
			found := false
			for _, v := range schema.InputSchema.Properties["operation"].Enum {
				found = found || v == op
			}
			if !found {
				return fmt.Sprintf("%s has no operation %q", tool, op)
			}
		}
		for _, f := range fields {
			if _, has := schema.InputSchema.Properties[f]; !has {
				return fmt.Sprintf("%s has no argument %q", tool, f)
			}
		}
		return ""
	case "testid":
		return p108FindID(p108Sources.web, rest, `data-test="`, "testid")
	case "a11y":
		return p108FindID(p108Sources.swft, rest, `"`, "a11y")
	case "component":
		if _, err := os.Stat(filepath.Join(root, rest)); err != nil {
			return fmt.Sprintf("file %s does not exist", rest)
		}
		return ""
	case "symbol":
		file, name, ok := strings.Cut(rest, "#")
		if !ok {
			return "symbol id must be `symbol:<path>#<name>`"
		}
		b, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			return fmt.Sprintf("file %s does not exist", file)
		}
		if !strings.Contains(string(b), name) {
			return fmt.Sprintf("%s does not mention %q", file, name)
		}
		return ""
	}
	return "unknown identifier kind " + kind
}

// p108FindID looks for a literal UI identifier in non-test source. An id that
// ends in `-<x>` stands for a template interpolation (Vue `${…}`, Swift `\(…)`),
// so its literal prefix must be followed by one (R2).
func p108FindID(corpus, id, open, kind string) string {
	if strings.HasSuffix(id, "-<x>") {
		prefix := strings.TrimSuffix(id, "<x>")
		interp := "${"
		quote := ""
		if kind == "a11y" {
			interp = `\(`
		}
		if kind == "testid" {
			quote = "`" // Vue binds interpolated ids as :data-test="`prefix-${id}`"
		}
		if strings.Contains(corpus, open+quote+prefix+interp) {
			return ""
		}
		return fmt.Sprintf("no source interpolates %q", prefix)
	}
	if strings.Contains(corpus, open+id+`"`) {
		return ""
	}
	return fmt.Sprintf("%q does not occur in non-test source", id)
}

// p108TestUnresolved checks one `tests` entry: the file exists and, for go: and
// xctest:, the named function occurs in it.
func p108TestUnresolved(t testing.TB, ref string) string {
	t.Helper()
	kind, rest, ok := strings.Cut(ref, ":")
	if !ok {
		return "malformed test ref"
	}
	file, name, hasName := strings.Cut(rest, "#")
	b, err := os.ReadFile(filepath.Join(p108RepoRoot(t), file))
	if err != nil {
		return fmt.Sprintf("test file %s does not exist", file)
	}
	switch kind {
	case "go":
		if !hasName || !strings.Contains(string(b), "func "+name+"(") {
			return fmt.Sprintf("%s has no func %s", file, name)
		}
	case "xctest":
		if !hasName || !strings.Contains(string(b), "func "+name+"(") {
			return fmt.Sprintf("%s has no method %s", file, name)
		}
	case "vitest", "pw":
		if hasName {
			return "vitest/pw refs name a file only"
		}
	default:
		return "unknown test kind " + kind
	}
	return ""
}

func TestProfilesV3ParityMatrixResolves(t *testing.T) {
	m := p108LoadMatrix(t)
	pending := 0
	for _, row := range m.Rows {
		for _, surface := range p108Surfaces {
			cell := row.Cells[surface]
			where := fmt.Sprintf("row %d %s", row.Row, surface)
			if cell.Status != "must" && cell.Status != "deprecated" {
				require.Empty(t, cell.Ids, where+": only must/deprecated cells name identifiers")
				continue
			}
			require.NotEmpty(t, cell.Ids, where+": a tick with no implementing identifier")
			require.NotEmpty(t, cell.Tests, where+": a tick with no test")
			if cell.Pending != "" {
				pending++
				require.Equal(t, p108PendingAllowlist[fmt.Sprintf("%d/%s", row.Row, surface)], cell.Pending,
					"%s: pending is only for the allowlisted cells", where)
			}
			for _, id := range cell.Ids {
				// MCP fields apply to the tool of the id; CLI fields are checked by
				// the helper-process test.
				var fields []string
				if surface == "mcp" {
					fields = cell.Fields
				}
				if why := p108Unresolved(t, surface, id, fields); why != "" {
					if cell.Pending != "" {
						continue
					}
					t.Errorf("%s: %s: %s", where, id, why)
				} else if cell.Pending != "" {
					t.Logf("%s: %s now resolves: remove pending %q and its allowlist entry", where, id, cell.Pending)
				}
			}
			for _, ref := range cell.Tests {
				if why := p108TestUnresolved(t, ref); why != "" {
					t.Errorf("%s: %s: %s", where, ref, why)
				}
			}
		}
	}
	require.LessOrEqual(t, pending, len(p108PendingAllowlist))

	t.Run("regression rows 11, 20 and 24 name their implementations", func(t *testing.T) {
		has := func(row int, surface, substr string) bool {
			for _, r := range m.Rows {
				if r.Row != row {
					continue
				}
				for _, id := range r.Cells[surface].Ids {
					if strings.Contains(id, substr) {
						return true
					}
				}
			}
			return false
		}
		require.True(t, has(11, "macos", "clients-other-client") && has(11, "macos", "credential-once"), "row 11 (Other client)")
		require.True(t, has(11, "web", "clients-add-other") && has(11, "web", "credential-once"), "row 11 (Other client, Web)")
		require.True(t, has(20, "macos", "ScopeFilter"), "row 20 (deep links, macOS ScopeFilter)")
		require.True(t, has(20, "web", "useScopeQuery"), "row 20 (deep links, Web)")
		require.True(t, has(24, "macos", "token-profile-picker"), "row 24 (token profile, macOS)")
		require.True(t, has(24, "web", "token-legacy-scope"), "row 24 (legacy scope, Web)")
		require.True(t, has(24, "rest", "handleListTokens"), "row 24 (REST filter)")
	})

	t.Run("mutation self-check: an unresolvable identifier is reported", func(t *testing.T) {
		require.NotEmpty(t, p108Unresolved(t, "rest", "rest:GET /api/v1/nope", nil))
		require.NotEmpty(t, p108Unresolved(t, "rest", "rest:DELETE /api/v1/profiles", nil), "a real path with a method it lacks")
		require.NotEmpty(t, p108Unresolved(t, "macos", "a11y:nope-xyz", nil))
		require.NotEmpty(t, p108Unresolved(t, "macos", "a11y:nope-xyz-<x>", nil))
		require.NotEmpty(t, p108Unresolved(t, "web", "testid:nope-xyz", nil))
		require.NotEmpty(t, p108Unresolved(t, "mcp", "mcp:profiles.nope", nil))
		require.NotEmpty(t, p108Unresolved(t, "mcp", "mcp:profiles.list", []string{"not_an_argument"}))
		require.NotEmpty(t, p108Unresolved(t, "rest", "symbol:internal/httpapi/tokens.go#noSuchHandler", nil))
		require.NotEmpty(t, p108Unresolved(t, "cli", "mcp:profiles.list", nil), "a kind on the wrong surface")
		require.NotEmpty(t, p108TestUnresolved(t, "go:internal/httpapi/profiles_v3_parity_test.go#TestNoSuchTest"))
		require.NotEmpty(t, p108TestUnresolved(t, "vitest:frontend/tests/unit/no-such.spec.ts"))
		// and the controls resolve, so the failures above are not an artefact
		require.Empty(t, p108Unresolved(t, "rest", "rest:GET /api/v1/profiles/{name}/effective-tools", nil))
		require.Empty(t, p108Unresolved(t, "macos", "a11y:client-row-<x>", nil))
		require.Empty(t, p108Unresolved(t, "web", "testid:clients-page", nil))
	})
}

// ---------------------------------------------------------------------------
// SC-001 index (L7)
// ---------------------------------------------------------------------------

const p108AcceptancePath = "specs/108-profiles-v3/acceptance-checks.json"

var p108AcceptanceFuncRe = regexp.MustCompile(`(?m)^func (TestProfilesV3Acceptance_Check(\d)_\w+)\(`)

// TestProfilesV3AcceptanceIndexResolves: acceptance-checks.json names six checks,
// every test it lists exists (the same resolver as the parity matrix), a check's
// Go test carries the check number in its name, and no TestProfilesV3Acceptance_
// function in the repository is missing from the index.
func TestProfilesV3AcceptanceIndexResolves(t *testing.T) {
	var index struct {
		Checks []struct {
			Check int      `json:"check"`
			Title string   `json:"title"`
			Spec  []string `json:"spec"`
			Tests []string `json:"tests"`
		} `json:"checks"`
	}
	p108ReadJSON(t, p108AcceptancePath, &index)
	require.Len(t, index.Checks, 6, "SC-001 names six audit acceptance checks")

	indexed := map[string]bool{}
	for i, c := range index.Checks {
		require.Equal(t, i+1, c.Check)
		require.NotEmpty(t, c.Title)
		require.NotEmpty(t, c.Spec, "check %d names its user story scenarios", c.Check)
		require.NotEmpty(t, c.Tests, "check %d has no test", c.Check)
		for _, ref := range c.Tests {
			if why := p108TestUnresolved(t, ref); why != "" {
				t.Errorf("check %d: %s: %s", c.Check, ref, why)
			}
			indexed[ref[strings.Index(ref, "#")+1:]] = true
			if name := ref[strings.Index(ref, "#")+1:]; strings.HasPrefix(name, "TestProfilesV3Acceptance_Check") {
				require.True(t, strings.HasPrefix(name, fmt.Sprintf("TestProfilesV3Acceptance_Check%d_", c.Check)),
					"%s is indexed under check %d", name, c.Check)
			}
		}
	}

	// Every acceptance test in the repository is indexed (no orphan).
	root := p108RepoRoot(t)
	found := 0
	for _, dir := range []string{"internal", "cmd"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			for _, m := range p108AcceptanceFuncRe.FindAllStringSubmatch(string(b), -1) {
				found++
				if !indexed[m[1]] {
					t.Errorf("%s in %s is not listed in %s", m[1], strings.TrimPrefix(path, root+"/"), p108AcceptancePath)
				}
			}
			return nil
		})
	}
	require.GreaterOrEqual(t, found, 6, "the five named server tests plus the CLI check 2 test exist")

	t.Run("mutation: an unknown test ref is reported", func(t *testing.T) {
		require.NotEmpty(t, p108TestUnresolved(t, "go:internal/server/profiles_v3_acceptance_test.go#TestProfilesV3Acceptance_Check9_Nope"))
	})
}
