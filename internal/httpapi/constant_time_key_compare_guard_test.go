package httpapi

import (
	"fmt"
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

// Guard: the global admin API key must never be compared with Go's `==`/`!=`
// string operators on an authentication path.
//
// `==` on strings short-circuits at the first differing byte, so the time a
// request takes to be rejected leaks a prefix of the configured key to anyone
// who can measure it. Every admin-key comparison that decides access must go
// through auth.ConstantTimeEqual (crypto/subtle.ConstantTimeCompare on
// []byte), which the server-edition OIDC provider already uses for its nonce
// check.
//
// The walk uses go/parser directly (precedent:
// internal/config/latent_symbols_guard_test.go), so build tags are irrelevant
// and the same verdict holds under `-tags server`. Emptiness guards
// (`cfg.APIKey == ""` / `!= ""`) are NOT violations: they are exactly the
// checks that must survive the rewrite, because
// subtle.ConstantTimeCompare([]byte(""), []byte("")) returns 1 and an absent
// credential would otherwise match.
var constantTimeGuardFiles = []string{
	"internal/httpapi/server.go",
	"internal/server/server.go",
}

func TestAdminKeyNeverComparedWithStringEquality(t *testing.T) {
	root := constantTimeGuardRepoRoot(t)
	fset := token.NewFileSet()

	var violations []string
	for _, rel := range constantTimeGuardFiles {
		path := filepath.Join(root, filepath.FromSlash(rel))
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			bin, ok := n.(*ast.BinaryExpr)
			if !ok {
				return true
			}
			if bin.Op != token.EQL && bin.Op != token.NEQ {
				return true
			}
			if !isAPIKeySelector(bin.X) && !isAPIKeySelector(bin.Y) {
				return true
			}
			// Emptiness guards are required, not forbidden.
			if isEmptyStringLiteral(bin.X) || isEmptyStringLiteral(bin.Y) {
				return true
			}
			pos := fset.Position(bin.Pos())
			violations = append(violations, fmt.Sprintf("%s:%d: %s %s %s",
				rel, pos.Line, exprString(bin.X), bin.Op, exprString(bin.Y)))
			return true
		})
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("admin API key compared with string equality at %d site(s); use auth.ConstantTimeEqual instead:\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// isAPIKeySelector reports whether e is a selector expression whose final
// field is APIKey (cfg.APIKey, s.cfg.APIKey, ...).
func isAPIKeySelector(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel != nil && sel.Sel.Name == "APIKey"
}

func isEmptyStringLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	v, err := strconv.Unquote(lit.Value)
	return err == nil && v == ""
}

// exprString renders the simple expression shapes this guard reports.
func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	case *ast.BasicLit:
		return v.Value
	case *ast.CallExpr:
		return exprString(v.Fun) + "(...)"
	default:
		return fmt.Sprintf("%T", e)
	}
}

func constantTimeGuardRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod: %v", root, err)
	}
	return root
}
