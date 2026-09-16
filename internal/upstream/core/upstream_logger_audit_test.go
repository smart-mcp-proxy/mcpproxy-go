package core

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

// Spec 105 FR-007, research D8 rule 1 (producer rule): the per-server log is
// the file `upstream_servers tail_log` serves, and its attribution reader
// keys on the `server=<raw>` field of every record. Child-controlled text
// (stderr lines, launcher output, docker output) must therefore only ever be
// a zap FIELD VALUE — zap escapes it inside the fields object — never the
// message, where the console encoder writes it unescaped and a crafted line
// could try to look like a record boundary. This test is the audit: every
// `upstreamLogger.{Info,Warn,Error,Debug}(` call in this package passes a
// constant string literal as its message. (T054a; expected green on HEAD —
// it pins the invariant the reader rule relies on.)
func TestUpstreamLoggerAudit_MessagesAreConstant(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var violations []string
	audited := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		require.NoError(t, err, name)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Info", "Warn", "Error", "Debug":
			default:
				return true
			}
			recv, ok := sel.X.(*ast.SelectorExpr)
			if !ok || recv.Sel.Name != "upstreamLogger" {
				return true
			}
			audited++
			if len(call.Args) == 0 {
				violations = append(violations, fset.Position(call.Pos()).String()+": no message argument")
				return true
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); !ok || lit.Kind != token.STRING {
				violations = append(violations, fset.Position(call.Pos()).String()+": message is not a string literal")
			}
			return true
		})
	}

	require.NotZero(t, audited, "the audit found no upstreamLogger call sites — the receiver name changed and the audit is vacuous")
	require.Empty(t, violations, "upstreamLogger calls with a non-constant message (child text must be a field value):\n%s",
		strings.Join(violations, "\n"))
}
