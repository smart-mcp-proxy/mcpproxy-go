//go:build server

package api

// Spec 107 T068 (US1, FR-004, contracts/entitlement-predicate.md §1): an AST
// guard pinning that exactly ONE function reads the `.Shared` field of an
// admin server config on a tenant-facing door — the entitlement predicate.
//
// Today (pre-T075) that logic lives, whole, inside `entitledServerNames`
// (`user_handlers.go:808ff`); T075 splits it into the predicate core
// `entitledServerNamesFor(user, isAdmin)` and a thin wrapper
// `entitledServerNames(userID, isAdmin)` that performs one `GetUser` and
// calls the core, reading nothing itself. Both names are permitted here so
// the guard does not itself force the rename order — it fails today for the
// real reason (seven doors bypass the predicate), not because the predicate
// hasn't been renamed yet.
//
// Permitted second reader: `adminSharedProjection`, the administrator
// projection of the same tenant doors (FR-004 "administrator projection
// unchanged").
//
// Exempted by file name: admin_handlers.go — the administrator-only surface
// (the share toggle at :551-597 writing `found.Shared = req.Shared`, and the
// `/admin/servers` whole-config enrichment at :637 reading `sc.Shared`).
// `/admin/servers` is the whole-config surface, not a tenant door. The walk
// below is untyped (it matches any selector named `Shared`, not just one on
// `*config.ServerConfig`), so this file exemption also covers `req.Shared` on
// the share-toggle's request struct.
//
// Behaviour-red on HEAD: `user_handlers.go:345,488,529,603,654`,
// `user_activity.go:153` and `credential_handlers.go:352` all read `.Shared`
// directly, outside the predicate — seven tenant-door sites that T075 must
// collapse onto `entitledServerNamesFor`/`entitledServerNames`.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// permittedSharedReaders are the only function names allowed to read `.Shared`
// directly: the entitlement predicate (core + wrapper, see file comment) and
// the administrator projection helper.
var permittedSharedReaders = map[string]bool{
	"entitledServerNamesFor":         true,
	"entitledServerNamesForSnapshot": true,
	"entitledServerNames":            true,
	"adminSharedProjection":          true,
}

// sharedGuardExemptFiles are exempted by file name entirely.
var sharedGuardExemptFiles = map[string]bool{
	"admin_handlers.go": true,
}

type sharedGuardViolation struct {
	file string
	line int
	fn   string
}

// TestUserHandlersSharedGuard_OnlyThePredicateReadsShared walks the non-test
// AST of internal/serveredition/api and fails on any selector `.Shared`
// outside the entitlement predicate, `adminSharedProjection`, or
// admin_handlers.go.
func TestUserHandlersSharedGuard_OnlyThePredicateReadsShared(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob *.go in internal/serveredition/api: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("guard fixture: filepath.Glob found no source files — the test is not running against internal/serveredition/api")
	}

	fset := token.NewFileSet()
	var violations []sharedGuardViolation
	filesWalked := 0
	funcsWalked := 0

	for _, path := range paths {
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") {
			continue
		}
		if sharedGuardExemptFiles[base] {
			continue
		}
		filesWalked++

		src, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		astFile, parseErr := parser.ParseFile(fset, path, src, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}

		for _, decl := range astFile.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			funcsWalked++
			if permittedSharedReaders[fn.Name.Name] {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Shared" {
					return true
				}
				pos := fset.Position(sel.Pos())
				violations = append(violations, sharedGuardViolation{
					file: base,
					line: pos.Line,
					fn:   fn.Name.Name,
				})
				return true
			})
		}
	}

	// Counter fixtures: prove the walk actually touched the package instead
	// of vacuously passing on an empty glob or a skip-everything bug.
	if filesWalked == 0 {
		t.Fatal("guard fixture: every file in internal/serveredition/api was skipped (test/exempt) — the walk exercises nothing")
	}
	if funcsWalked == 0 {
		t.Fatal("guard fixture: no top-level func decls were walked — the AST walk is not inspecting function bodies")
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].file != violations[j].file {
			return violations[i].file < violations[j].file
		}
		return violations[i].line < violations[j].line
	})

	if len(violations) > 0 {
		var b strings.Builder
		b.WriteString("raw `.Shared` read outside the entitlement predicate (contracts/entitlement-predicate.md §1) " +
			"— collapse onto entitledServerNamesFor/entitledServerNames (Spec 107 T075):\n")
		for _, v := range violations {
			b.WriteString("  " + v.file + ":" + strconv.Itoa(v.line) + " in func " + v.fn + "\n")
		}
		t.Error(b.String())
	}
}
