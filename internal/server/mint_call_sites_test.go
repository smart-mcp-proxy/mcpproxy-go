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

// mintExemptFunctions are the only places allowed to read the clock for an
// identifier. Everything else must go through them.
var mintExemptFunctions = map[string]bool{"mintCorrelationIDAt": true}

// Correlation ids minted from the clock alone collide: two identical tool calls
// dispatched in the same nanosecond produce the same id, and the tray — which
// collapses activity rows by request id — reports them as one call with an
// undercounted ×N. Fixing the shared helper is not enough while call sites keep
// formatting their own, so this walks the package and fails on any that do.
//
// A future clock read that is genuinely not an identifier belongs in
// mintExemptFunctions with a note saying why.
func TestActivityIDsAreNeverMintedInline(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var offenders []string

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		require.NoErrorf(t, err, "parsing %s", name)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || mintExemptFunctions[fn.Name.Name] {
				continue
			}

			ast.Inspect(fn.Body, func(node ast.Node) bool {
				sel, ok := node.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "UnixNano" {
					return true
				}
				offenders = append(offenders,
					fset.Position(sel.Pos()).String()+" in "+fn.Name.Name)
				return true
			})
		}
	}

	require.Emptyf(t, offenders,
		"these mint an id from the clock directly and will collide; "+
			"route them through mintCorrelationID / mintCorrelationIDAt:\n%s",
		strings.Join(offenders, "\n"))
}
