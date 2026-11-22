package storage

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindingWriteSites parses one Go source and counts every direct write to a
// ProfilePin / ProfileMode field, keyed by "<enclosing func>|<shape>", where
// shape is "assign:<lhs>" for an assignment to a selector (e.g.
// "assign:tok.ProfilePin") and "literal:<Field>" for a composite-literal key.
func bindingWriteSites(t *testing.T, filename string, src []byte) map[string]int {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	require.NoError(t, err)
	isField := func(n string) bool { return n == "ProfilePin" || n == "ProfileMode" }
	out := map[string]int{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range x.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && isField(sel.Sel.Name) {
						recv := "?"
						if id, ok := sel.X.(*ast.Ident); ok {
							recv = id.Name
						}
						out[fn.Name.Name+"|assign:"+recv+"."+sel.Sel.Name]++
					}
				}
			case *ast.KeyValueExpr:
				if id, ok := x.Key.(*ast.Ident); ok && isField(id.Name) {
					out[fn.Name.Name+"|literal:"+id.Name]++
				}
			}
			return true
		})
	}
	return out
}

// allowedBindingWriteSites lists EVERY direct write to a client's pin/mode in
// this package's non-test code, by exact site (function, shape) and count, so a
// new mutation inside an allow-listed function fails too. A new writer must go
// through writeClientBindingTx (research R4).
var allowedBindingWriteSites = map[string]int{
	// THE writer of a client's pin/mode in both stores (API-key record, then
	// the OAuth entry).
	"writeClientBindingOptsTx|assign:tok.ProfilePin":  1, // tok.ProfilePin, tok.ProfileMode = pin, mode
	"writeClientBindingOptsTx|assign:tok.ProfileMode": 1,
	"writeClientBindingOptsTx|assign:b.ProfilePin":    1, // the OAuth entry
	"writeClientBindingOptsTx|assign:b.ProfileMode":   1,

	// Builds the brand-new client record literal; the OAuth entry is then
	// synced through writeClientBindingTx in the same transaction.
	"MintClientCredentialWith|literal:ProfileMode": 1,
	"MintClientCredentialWith|literal:ProfilePin":  1,

	// Assigns on a COPY only to validate the invariants and to return the
	// after-snapshot; the stored write is writeClientBindingOptsTx.
	"UpdateClientCredentialBinding|assign:updated.ProfilePin":  1,
	"UpdateClientCredentialBinding|assign:updated.ProfileMode": 1,

	// Sets the restored binding on the RETURNED record, which is already
	// stored; the stored restore is writeClientBindingTx.
	"forgetClientCredential|assign:tok.ProfilePin":  1,
	"forgetClientCredential|assign:tok.ProfileMode": 1,

	// Regular (non-client) agent tokens are rewritten directly by repin; client
	// records go through the writer.
	"repinProfile|assign:t.ProfilePin": 1,
	// The restore entry carries the OLD binding in these fields.
	"repinRestoreEntry|literal:ProfilePin":  1,
	"repinRestoreEntry|literal:ProfileMode": 1,

	// Restores a stored record's pin by hash, exactly as before Spec 132 (the
	// mode is never changed by a repin; browser-login entries are restored
	// compare-and-set through writeClientBindingTx).
	"RestorePins|assign:t.ProfilePin": 1,

	// The only creator of oauth_client_bindings entries (a client's first login).
	"CommitOAuthLogin|assign:nb.ProfilePin":  2, // new-client branch and existing-client branch
	"CommitOAuthLogin|assign:nb.ProfileMode": 2,
}

func TestClientBindingFieldsWrittenOnlyByAllowListedSites(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	got := map[string]int{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		for k, n := range bindingWriteSites(t, f, src) {
			got[k] += n
		}
	}
	assert.Equal(t, allowedBindingWriteSites, got,
		"a direct write to ProfilePin/ProfileMode appeared, disappeared or was duplicated: "+
			"route it through writeClientBindingTx, or justify the allow-list change")
}

// TestBindingWriteSiteChecker_DetectsBypass proves the checker sees a direct
// mutation added inside an allow-listed function and in a new function.
func TestBindingWriteSiteChecker_DetectsBypass(t *testing.T) {
	src := []byte(`package storage

func writeClientBindingOptsTx() {
	tok.ProfilePin, tok.ProfileMode = pin, mode
	tok.ProfilePin = "second write"
}

func sneaky() {
	rec.ProfileMode = "locked"
	_ = AgentToken{ProfilePin: "x"}
}
`)
	got := bindingWriteSites(t, "synthetic.go", src)
	assert.Equal(t, 2, got["writeClientBindingOptsTx|assign:tok.ProfilePin"], "the duplicate is counted")
	assert.Equal(t, 1, got["sneaky|assign:rec.ProfileMode"])
	assert.Equal(t, 1, got["sneaky|literal:ProfilePin"])
	assert.NotEqual(t, allowedBindingWriteSites["writeClientBindingOptsTx|assign:tok.ProfilePin"],
		got["writeClientBindingOptsTx|assign:tok.ProfilePin"], "exceeds the allow-list count")
	_, allowed := allowedBindingWriteSites["sneaky|assign:rec.ProfileMode"]
	assert.False(t, allowed)
}

// TestNoProductionCodeCreatesClientRecordsThroughCreateAgentToken: CreateAgentToken
// stores a whole record and would bypass the binding writer for a Kind=client
// record. Client records are minted only through MintClientCredential*, so no
// non-test caller of CreateAgentToken may mention KindClient or set a Kind.
func TestNoProductionCodeCreatesClientRecordsThroughCreateAgentToken(t *testing.T) {
	roots := []string{"..", filepath.Join("..", "..", "cmd")}
	var offenders []string
	calls := 0
	for _, root := range roots {
		require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(src), "CreateAgentToken(") {
				return err
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
			if err != nil {
				return nil
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || fn.Name.Name == "CreateAgentToken" {
					continue
				}
				callsHere, setsKind := false, false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					switch x := n.(type) {
					case *ast.CallExpr:
						if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "CreateAgentToken" {
							callsHere = true
						}
					case *ast.SelectorExpr:
						if x.Sel.Name == "KindClient" {
							setsKind = true
						}
					case *ast.KeyValueExpr:
						if id, ok := x.Key.(*ast.Ident); ok && id.Name == "Kind" {
							setsKind = true
						}
					case *ast.AssignStmt:
						for _, l := range x.Lhs {
							if sel, ok := l.(*ast.SelectorExpr); ok && sel.Sel.Name == "Kind" {
								setsKind = true
							}
						}
					}
					return true
				})
				if callsHere {
					calls++
					if setsKind {
						offenders = append(offenders, path+" "+fn.Name.Name)
					}
				}
			}
			return nil
		}))
	}
	sort.Strings(offenders)
	assert.Empty(t, offenders)
	assert.Positive(t, calls, "the walk must actually find the callers (guards against a vacuous pass)")
}
