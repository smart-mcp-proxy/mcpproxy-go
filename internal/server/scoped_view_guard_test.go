package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfinedAnonymousHandlersUseScopedView(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "mcp.go", nil, 0)
	require.NoError(t, err)
	guarded := map[string]bool{
		"handleUpstreamServers": false,
		"handleListUpstreams":   false,
		"handleTailLog":         false,
		"handleCallToolVariant": false,
	}
	adminChecks := map[string]bool{
		"IsAdmin":           true,
		"IsAdminOrAbsent":   true,
		"IsNonAdmin":        true,
		"IsAdministrator":   true,
		"AuthorizeServerOp": true,
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if _, ok := guarded[fn.Name.Name]; !ok {
			continue
		}
		guarded[fn.Name.Name] = true
		scopedViewVars := map[string]bool{}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, rhs := range assign.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok {
					continue
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "ScopedView" {
					continue
				}
				for _, lhs := range assign.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						scopedViewVars[id.Name] = true
					}
				}
			}
			return true
		})
		require.NotEmptyf(t, scopedViewVars, "%s must derive an auth context with auth.ScopedView", fn.Name.Name)
		adminCheckFound := false
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !adminChecks[selector.Sel.Name] {
				return true
			}
			adminCheckFound = true
			if len(call.Args) == 0 {
				t.Errorf("%s has an administrator check without an auth context", fn.Name.Name)
				return true
			}
			authArg, ok := call.Args[0].(*ast.Ident)
			if !ok || !scopedViewVars[authArg.Name] {
				t.Errorf("%s must pass an auth.ScopedView result to %s", fn.Name.Name, selector.Sel.Name)
			}
			return true
		})
		require.Truef(t, adminCheckFound, "%s must retain an administrator check", fn.Name.Name)
	}
	for name, found := range guarded {
		require.Truef(t, found, "missing guarded handler %s", name)
	}
}
