package config

// T004a item (1) — the go/ast structural test the FR-009a rollout gate
// requires (zcode round 2, "the override cannot ship"; a later zcode round
// found only items (2) (TestEnablePolicyForTest_PanicsOutsideATestBinary) and
// (3) (TestServeRejectsV3PolicyFieldsAtStartup) had landed in
// profiles_rollout_gate_test.go — this file is the missing third artifact).
//
// The gate lives in this package (profiles.go), not package profile — see the
// PolicyEnforcementReady doc comment there for why — so this test parses
// profiles.go's own AST rather than a separate internal/profile/rollout_gate.go.
// It proves, independently of what profiles_rollout_gate_test.go's behavioural
// assertions happen to observe, that:
//
//  1. PolicyEnforcementReady's body reads only policyEnforcementReadyBase,
//     policyEnforcementTestOverride and testing.Testing() — no os.Getenv/
//     os.LookupEnv, no flag, no config field, no other package-level
//     identifier — so no runtime input can open the gate.
//  2. policyEnforcementReadyBase is declared `const` (not `var`): no import
//     can flip it by assignment.
//  3. profiles.go carries no `//go:build` line: the gate's value does not
//     depend on build tags (a `-tags server` build sees the same false).
//  4. Walking every non-test .go file in the module, EnablePolicyForTest and
//     policyEnforcementTestOverride are referenced from no production file
//     other than profiles.go itself and the dedicated override-probe binary
//     (testdata/overrideprobe/main.go, T004a item (2)'s fixture, which exists
//     precisely to prove the override panics outside a test binary).
import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// rolloutGateAllowedIdents is the exact set of package-level identifiers
// PolicyEnforcementReady's body may reference, besides local syntax
// (testing.Testing is checked separately as a selector).
var rolloutGateAllowedIdents = map[string]bool{
	"policyEnforcementReadyBase":    true,
	"policyEnforcementTestOverride": true,
	"Load":                          true, // atomic.Bool.Load method selector
}

// rolloutGateOverrideRefExemptFiles are the only non-test .go files (relative
// to the module root, forward-slash) allowed to reference EnablePolicyForTest
// or policyEnforcementTestOverride. profiles.go declares them; the override
// probe calls EnablePolicyForTest by design (T004a item 2) to prove it panics
// there.
var rolloutGateOverrideRefExemptFiles = map[string]bool{
	"internal/config/profiles.go":                    true,
	"internal/config/testdata/overrideprobe/main.go": true,
}

func TestRolloutGateAST_PolicyEnforcementReadyReadsOnlyItsOwnInputs(t *testing.T) {
	root := latentGuardRepoRoot(t)
	path := filepath.Join(root, "internal", "config", "profiles.go")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.Contains(string(raw), "//go:build") {
		t.Errorf("%s carries a //go:build line; the FR-009a gate's value must not depend on build tags", latentRelPath(root, path))
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var (
		gateFunc    *ast.FuncDecl
		baseIsConst bool
		foundBase   bool
	)
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.Name == "PolicyEnforcementReady" {
				gateFunc = d
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, n := range vs.Names {
					if n.Name == "policyEnforcementReadyBase" {
						foundBase = true
						baseIsConst = d.Tok == token.CONST
					}
				}
			}
		}
	}

	if !foundBase {
		t.Fatal("policyEnforcementReadyBase declaration not found in profiles.go; the gate's compile-time flag moved or was renamed")
	}
	if !baseIsConst {
		t.Error("policyEnforcementReadyBase must be declared `const`, not `var` — a var lets any import in the package flip the gate by assignment")
	}

	if gateFunc == nil {
		t.Fatal("func PolicyEnforcementReady not found in profiles.go")
	}
	if gateFunc.Body == nil {
		t.Fatal("PolicyEnforcementReady has no body to inspect")
	}

	var violations []string
	ast.Inspect(gateFunc.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if ok {
			if pkgIdent, ok := sel.X.(*ast.Ident); ok {
				switch pkgIdent.Name {
				case "testing":
					if sel.Sel.Name != "Testing" {
						pos := fset.Position(sel.Pos())
						violations = append(violations, "testing."+sel.Sel.Name+" at line "+strconv.Itoa(pos.Line)+" (only testing.Testing() is allowed)")
					}
					return false // do not descend into "testing" ident below
				case "policyEnforcementTestOverride":
					if !rolloutGateAllowedIdents[sel.Sel.Name] {
						pos := fset.Position(sel.Pos())
						violations = append(violations, "policyEnforcementTestOverride."+sel.Sel.Name+" at line "+strconv.Itoa(pos.Line)+" (only .Load() is allowed)")
					}
					return false
				default:
					// Any other selector base (os.Getenv, os.LookupEnv, flag.*,
					// a config field, ...) is a forbidden input to the gate.
					pos := fset.Position(sel.Pos())
					violations = append(violations, pkgIdent.Name+"."+sel.Sel.Name+" at line "+strconv.Itoa(pos.Line))
					return false
				}
			}
			return true
		}
		ident, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		switch ident.Name {
		case "policyEnforcementReadyBase", "policyEnforcementTestOverride", "testing", "bool", "true", "false":
			return true
		}
		if rolloutGateAllowedIdents[ident.Name] {
			return true
		}
		pos := fset.Position(ident.Pos())
		violations = append(violations, "identifier "+ident.Name+" at line "+strconv.Itoa(pos.Line))
		return true
	})

	if len(violations) > 0 {
		t.Errorf("PolicyEnforcementReady reads input(s) beyond policyEnforcementReadyBase/policyEnforcementTestOverride/testing.Testing(): %v", violations)
	}
}

// TestRolloutGateAST_OverrideUnreachableFromProductionCode walks every
// non-test .go file in the module and fails if EnablePolicyForTest or
// policyEnforcementTestOverride is referenced from any file other than the
// two exempt ones (profiles.go and the dedicated override probe).
func TestRolloutGateAST_OverrideUnreachableFromProductionCode(t *testing.T) {
	root := latentGuardRepoRoot(t)
	fset := token.NewFileSet()

	var violations []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "frontend", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel := latentRelPath(root, path)

		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parse %s: %v", rel, perr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if ident.Name != "EnablePolicyForTest" && ident.Name != "policyEnforcementTestOverride" {
				return true
			}
			if rolloutGateOverrideRefExemptFiles[rel] {
				return true
			}
			pos := fset.Position(ident.Pos())
			violations = append(violations, ident.Name+" referenced at "+rel+":"+strconv.Itoa(pos.Line))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	if len(violations) > 0 {
		t.Errorf("FR-009a override reachable from production code outside profiles.go / the override probe (%d reference(s)): %v",
			len(violations), violations)
	}
}
