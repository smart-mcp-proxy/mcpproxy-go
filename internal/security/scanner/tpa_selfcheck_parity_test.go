package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Self-check parity guards (tasks B14, T341). The activation self-check is a
// port of cmd/scan-eval's CI gate (that package is `package main` and cannot be
// imported, Δ17). These tests make a drift between the two loud. They read
// cmd/scan-eval read-only and are tolerant to both gate.go shapes (HEAD's
// gateChecks() and Spec 101 US2's gateChecks(bundle) / builtinGateChecks()).
//
// Test names use the TestSelfCheck prefix so eval.yml's -run regex never
// selects them.

const (
	selfCheckCorpusRel = "bundled/selfcheck-corpus.json"
	selfCheckSourceRel = "../../../specs/065-evaluation-foundation/datasets/detect_corpus_v1.json"
	selfCheckGateRel   = "../../../cmd/scan-eval/gate.go"
	selfCheckMainRel   = "../../../cmd/scan-eval/main.go"

	// gateBreachExitCode is cmd/scan-eval's exitGateBreach.
	gateBreachExitCode = 6
)

// TestSelfCheckCorpusByteParity is guard (a): the embedded corpus is a byte copy
// of the CI corpus (after CRLF normalisation). Whichever of US2/US3 merges
// second refreshes the copy (merge-order gate).
func TestSelfCheckCorpusByteParity(t *testing.T) {
	src, err := os.ReadFile(selfCheckSourceRel)
	require.NoError(t, err)
	src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	got := bytes.ReplaceAll(embeddedSelfCheckCorpus, []byte("\r\n"), []byte("\n"))
	require.NotEmpty(t, got, "the embedded self-check corpus is empty; run: cp specs/065-evaluation-foundation/datasets/detect_corpus_v1.json internal/security/scanner/bundled/selfcheck-corpus.json")
	if !bytes.Equal(src, got) {
		t.Fatalf("internal/security/scanner/%s drifted from specs/065-evaluation-foundation/datasets/detect_corpus_v1.json.\n"+
			"Refresh the copy from the repo root:\n  cp specs/065-evaluation-foundation/datasets/detect_corpus_v1.json internal/security/scanner/%s",
			selfCheckCorpusRel, selfCheckCorpusRel)
	}
	onDisk, err := os.ReadFile(selfCheckCorpusRel)
	require.NoError(t, err)
	assert.Equal(t, onDisk, embeddedSelfCheckCorpus, "the go:embed must serve the file on disk")
}

// parseGoFile parses a source file from cmd/scan-eval.
func parseGoFile(t *testing.T, rel string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), rel, nil, 0)
	require.NoError(t, err)
	return f
}

// gateBuiltinCheckNames returns the `checks.X` composite-literal type names in
// the gate's check-list constructors (gateChecks, builtinGateChecks).
func gateBuiltinCheckNames(t *testing.T, f *ast.File) []string {
	t.Helper()
	var names []string
	seen := false
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil || (fn.Name.Name != "gateChecks" && fn.Name.Name != "builtinGateChecks") {
			continue
		}
		seen = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if sel, ok := cl.Type.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "checks" {
					names = append(names, sel.Sel.Name)
				}
			}
			return true
		})
	}
	require.True(t, seen, "gate.go has neither gateChecks nor builtinGateChecks: update the parity guard")
	sort.Strings(names)
	return names
}

// gateCategoryPairs returns the string pairs of gate.go's categoryCheck map.
func gateCategoryPairs(t *testing.T, f *ast.File) map[string]string {
	t.Helper()
	pairs := map[string]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if name.Name != "categoryCheck" || i >= len(vs.Values) {
					continue
				}
				cl, ok := vs.Values[i].(*ast.CompositeLit)
				require.True(t, ok, "categoryCheck is not a composite literal")
				for _, el := range cl.Elts {
					kv := el.(*ast.KeyValueExpr)
					k, err1 := strconv.Unquote(kv.Key.(*ast.BasicLit).Value)
					v, err2 := strconv.Unquote(kv.Value.(*ast.BasicLit).Value)
					require.NoError(t, errors.Join(err1, err2))
					pairs[k] = v
				}
			}
		}
	}
	require.NotEmpty(t, pairs, "gate.go categoryCheck map not found")
	return pairs
}

func selfCheckBuiltinNames() []string {
	var names []string
	for _, c := range selfCheckBuiltins() {
		names = append(names, reflect.TypeOf(c).Elem().Name())
	}
	sort.Strings(names)
	return names
}

// TestSelfCheckGateASTParity is guard (b): the built-in check list, the
// category map, the HARD-tier flag rule and the thresholds are the gate's.
func TestSelfCheckGateASTParity(t *testing.T) {
	gate := parseGoFile(t, selfCheckGateRel)

	t.Run("built-in check list", func(t *testing.T) {
		want := gateBuiltinCheckNames(t, gate)
		require.NotEmpty(t, want)
		assert.Equal(t, want, selfCheckBuiltinNames(),
			"the self-check built-ins must match the CI gate's check list (cmd/scan-eval/gate.go)")
	})

	t.Run("category pairs", func(t *testing.T) {
		pairs := gateCategoryPairs(t, gate)
		for cat, id := range pairs {
			if id == selfCheckTPABundleCheckID {
				assert.True(t, strings.HasPrefix(cat, "tpa_"), "tpa.bundle may only be the check of tpa_* categories, got %q", cat)
			}
			assert.Equal(t, id, selfCheckCheckID(cat), "category %q must map to the same check as the gate", cat)
		}
		for cat, id := range selfCheckCategoryCheck {
			assert.Equal(t, id, pairs[cat], "self-check maps %q but the gate does not", cat)
		}
		assert.Equal(t, selfCheckTPABundleCheckID, selfCheckCheckID("tpa_anything_new"))
		assert.Equal(t, selfCheckTPABundleCheckID, selfCheckCheckID("tpa_"))
		assert.Empty(t, selfCheckCheckID("benign"))
		assert.Empty(t, selfCheckCheckID("hard_negative"))
		assert.Empty(t, selfCheckCheckID("xtpa_not_prefix"))
	})

	t.Run("flag rule is the dangerous tier on the entry's own tool", func(t *testing.T) {
		var body *ast.BlockStmt
		for _, d := range gate.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "scanEntryFlagged" {
				body = fn.Body
			}
		}
		require.NotNil(t, body, "gate.go scanEntryFlagged not found")
		var sawDangerous bool
		ast.Inspect(body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "ThreatLevelDangerous" {
				sawDangerous = true
			}
			return true
		})
		assert.True(t, sawDangerous, "the gate no longer flags on ThreatLevelDangerous: port the new rule to tpa_selfcheck.go")
	})

	t.Run("threshold defaults", func(t *testing.T) {
		defaults := map[string]float64{}
		for _, rel := range []string{selfCheckMainRel, selfCheckGateRel} {
			ast.Inspect(parseGoFile(t, rel), func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Float64" {
					return true
				}
				name, ok1 := call.Args[0].(*ast.BasicLit)
				val, ok2 := call.Args[1].(*ast.BasicLit)
				if !ok1 || !ok2 {
					return true
				}
				key, err := strconv.Unquote(name.Value)
				if err != nil {
					return true
				}
				f, err := strconv.ParseFloat(val.Value, 64)
				if err == nil {
					defaults[key] = f
				}
				return true
			})
		}
		assert.Equal(t, selfCheckMinRecall, defaults["min-recall"])
		assert.Equal(t, selfCheckMaxFP, defaults["max-fp"])
	})
}

// TestSelfCheckBaselineHardFlagParity is guard (c): the soft checks the
// production scan adds on top of the gate's built-ins never change a HARD flag
// on the corpus, so "the self-check passed" describes what the product ships.
func TestSelfCheckBaselineHardFlagParity(t *testing.T) {
	corpus := selfCheckCorpusBytes()
	require.NotEmpty(t, corpus)
	want, err := selfCheckEvaluate(corpus, baselineEngineChecks())
	require.NoError(t, err)
	got, err := selfCheckEvaluate(corpus, selfCheckBuiltins())
	require.NoError(t, err)
	require.NotEmpty(t, got.Flagged)
	assert.Equal(t, want.Flagged, got.Flagged,
		"per-entry hard flags differ between baselineEngineChecks() and the self-check built-ins")
}

// TestSelfCheckCorpusProvenance is guard (e), Spec 087 FR-021: every entry
// names its source and licence.
func TestSelfCheckCorpusProvenance(t *testing.T) {
	var doc struct {
		Entries []selfCheckTestEntry `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(selfCheckCorpusBytes(), &doc))
	require.NotEmpty(t, doc.Entries)
	for _, e := range doc.Entries {
		assert.NotEmpty(t, strings.TrimSpace(e.Provenance.Source), "entry %s has no provenance.source", e.ID)
		assert.NotEmpty(t, strings.TrimSpace(e.Provenance.License), "entry %s has no provenance.license", e.ID)
	}
}

// TestSelfCheckGateCLIDifferential is the optional guard (d): the real CI gate
// binary and the in-process self-check agree on the embedded default bundle
// over the embedded corpus. Skipped when the go tool is not on PATH.
func TestSelfCheckGateCLIDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("differential guard compiles cmd/scan-eval; skipped with -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not on PATH")
	}
	corpusPath, err := filepath.Abs(selfCheckCorpusRel)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "run", "./cmd/scan-eval", "--gate", "--corpus", corpusPath)
	cmd.Dir = filepath.Join("..", "..", "..")
	out, runErr := cmd.CombinedOutput()
	gatePassed := runErr == nil
	var exitErr *exec.ExitError
	if runErr != nil && (!errors.As(runErr, &exitErr) || exitErr.ExitCode() != gateBreachExitCode) {
		t.Skipf("could not run the gate (%v): %s", runErr, out)
	}

	check, _, err := loadEmbeddedBundleCheck()
	require.NoError(t, err)
	selfErr := activationSelfCheck(check)
	assert.Equal(t, gatePassed, selfErr == nil, "gate passed=%v but self-check err=%v\ngate output:\n%s", gatePassed, selfErr, out)
}
