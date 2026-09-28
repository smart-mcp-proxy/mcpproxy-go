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
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if _, ok := guarded[fn.Name.Name]; !ok {
			continue
		}
		guarded[fn.Name.Name] = true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "IsAdmin" {
				t.Errorf("%s must use auth.ScopedView before administrator checks", fn.Name.Name)
			}
			return true
		})
	}
	for name, found := range guarded {
		require.Truef(t, found, "missing guarded handler %s", name)
	}
}
