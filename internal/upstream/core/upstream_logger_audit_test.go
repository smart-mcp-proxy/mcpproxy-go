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
// zap level call — on ANY receiver — in the packages that write into the
// per-server file passes a constant string literal as its message. (T054a;
// expected green on HEAD — it pins the invariant the reader rule relies on.)
//
// Critique round 1, finding C1.3 / C2.12: the audit covers every receiver,
// not only `upstreamLogger`, because the per-server file is also written
// through `oauthLogger()`'s tee (client.go) from internal/oauth, through
// loggerWriter's `primary`/`fallback` here, and through any local alias a
// future edit introduces. The one allowed non-constant message is
// loggerWriter.writeLine (the launcher-pumped child line IS the message —
// D8 rule 1 amendment: safe under rule 2 plus the newline split
// loggerWriter.Write performs, pinned by TestLoggerWriter_SplitsMultiLineChunks).

// auditedLogPackages are the directories, relative to this package, whose
// production files write into the per-server log.
var auditedLogPackages = []string{".", "../launcher", "../../oauth"}

// auditAllowedNonConstant lists the call sites permitted to pass a
// non-constant message, as "<file>:<enclosing func>". Each entry is a
// reviewed exception, not child-controlled text:
//   - connection_launcher.go:writeLine — the launcher-pumped child line IS
//     the message (D8 rule 1 amendment; safe under rule 2 + the newline
//     split in loggerWriter.Write).
//   - connection_http.go:runAuthStrategies — "🔐 Trying "+transportLabel+…
//     where transportLabel is one of two string literals chosen by
//     connectHTTP/connectSSE, never data from the upstream.
var auditAllowedNonConstant = map[string]bool{
	"connection_launcher.go:writeLine":     true,
	"connection_http.go:runAuthStrategies": true,
}

var zapLevelMethods = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true,
	"DPanic": true, "Panic": true, "Fatal": true,
}

func TestUpstreamLoggerAudit_MessagesAreConstant(t *testing.T) {
	fset := token.NewFileSet()
	var violations []string
	audited := 0
	upstreamLoggerSites := 0

	for _, dir := range auditedLogPackages {
		entries, err := os.ReadDir(dir)
		require.NoError(t, err, dir)
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
			require.NoError(t, err, name)

			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || !zapLevelMethods[sel.Sel.Name] {
						return true
					}
					// zap.Error(err) is a field constructor, not a level call;
					// err.Error() takes no message.
					if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "zap" {
						return true
					}
					if len(call.Args) == 0 {
						return true
					}
					audited++
					if recv, ok := sel.X.(*ast.SelectorExpr); ok && recv.Sel.Name == "upstreamLogger" {
						upstreamLoggerSites++
					}
					if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						return true
					}
					if auditAllowedNonConstant[name+":"+fn.Name.Name] {
						return true
					}
					violations = append(violations, fset.Position(call.Pos()).String()+": message is not a string literal")
					return true
				})
			}
		}
	}

	require.NotZero(t, upstreamLoggerSites, "the audit found no upstreamLogger call sites — the receiver name changed and the audit is vacuous")
	require.Greater(t, audited, upstreamLoggerSites, "the audit must see receivers beyond upstreamLogger (oauth tee, loggerWriter)")
	require.Empty(t, violations, "zap level calls with a non-constant message (child text must be a field value):\n%s",
		strings.Join(violations, "\n"))
}
