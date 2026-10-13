package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 107 PR-D (T100).
//
// The audit line (contracts/audit-line.schema.json) needs the caller's
// request context to reach the two activity funnels that will carry audit
// emission: emitActivityPolicyDecision (every policy block/warning) and
// emitActivityToolCallCompleted (every dispatched call). Today both take a
// bare (serverName, toolName, ...) string tuple with no context.Context at
// all, so there is nowhere for the audit sink to hang request-scoped values
// (deadline, cancellation, future trace/correlation propagation) off of.
//
// This test is the behaviour-red guard for that change: it parses this
// package's own non-test source (not the string-matching approach — a
// gofmt-only reflow must not flip it) and asserts, for both funnels:
//
//  1. the func declaration's first parameter is named "ctx" with type
//     context.Context, and
//  2. every call site's first argument is the identifier "ctx" — i.e. the
//     request context already in scope at the call site, never a fresh
//     context.Background()/TODO() or (worse) one of the string args shifted
//     into position 0.
//
// It must fail red today: neither funnel has a context.Context parameter at
// all, so requirement (1) fails for both, and every existing call site's
// first argument is a string (serverName/logServer), so requirement (2)
// fails for every call site too. No production code is touched by this task.
var ctxFirstFuncs = map[string]bool{
	"emitActivityPolicyDecision":    true,
	"emitActivityToolCallCompleted": true,
}

// ctxFirstOffense records one place (a func decl or a call site) that does
// not yet pass context.Context as the first parameter/argument.
type ctxFirstOffense struct {
	pos    string
	detail string
}

func TestActivityFunnelsTakeCtxFirst(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var offenses []ctxFirstOffense
	foundDecl := map[string]bool{}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		require.NoErrorf(t, err, "parsing %s", name)

		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.FuncDecl:
				if !ctxFirstFuncs[n.Name.Name] {
					return true
				}
				foundDecl[n.Name.Name] = true
				if !firstParamIsCtx(n.Type) {
					offenses = append(offenses, ctxFirstOffense{
						pos:    fset.Position(n.Pos()).String(),
						detail: "func " + n.Name.Name + " does not take context.Context as its first parameter",
					})
				}
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok || !ctxFirstFuncs[sel.Sel.Name] {
					return true
				}
				if len(n.Args) == 0 {
					offenses = append(offenses, ctxFirstOffense{
						pos:    fset.Position(n.Pos()).String(),
						detail: "call to " + sel.Sel.Name + " has no arguments",
					})
					return true
				}
				id, ok := n.Args[0].(*ast.Ident)
				if !ok || id.Name != "ctx" {
					got := "<non-identifier>"
					if ok {
						got = id.Name
					} else if bl, ok := n.Args[0].(*ast.BasicLit); ok {
						got = bl.Value
					}
					offenses = append(offenses, ctxFirstOffense{
						pos:    fset.Position(n.Pos()).String(),
						detail: "call to " + sel.Sel.Name + " passes " + got + " as its first argument, not the request ctx",
					})
				}
			}
			return true
		})
	}

	for name := range ctxFirstFuncs {
		require.Truef(t, foundDecl[name], "did not find a func decl for %s in internal/server — has it moved or been renamed?", name)
	}

	var msg strings.Builder
	for _, o := range offenses {
		msg.WriteString(o.pos)
		msg.WriteString(": ")
		msg.WriteString(o.detail)
		msg.WriteString("\n")
	}

	require.Emptyf(t, offenses,
		"emitActivityPolicyDecision and emitActivityToolCallCompleted must take "+
			"context.Context as their first parameter, and every call site must "+
			"pass the request ctx already in scope (Spec 107 PR-D, audit line "+
			"needs request-scoped context to reach the sink):\n%s", msg.String())
}

// firstParamIsCtx reports whether ft's first parameter field is named "ctx"
// and typed context.Context (a *ast.SelectorExpr "context.Context" — this
// package always qualifies the import, never dot-imports it).
func firstParamIsCtx(ft *ast.FuncType) bool {
	if ft.Params == nil || len(ft.Params.List) == 0 {
		return false
	}
	first := ft.Params.List[0]
	if len(first.Names) == 0 || first.Names[0].Name != "ctx" {
		return false
	}
	sel, ok := first.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkgIdent.Name == "context" && sel.Sel.Name == "Context"
}
